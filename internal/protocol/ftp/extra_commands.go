package ftp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kervanserver/kervan/internal/vfs"
)

// legacyAliases maps the RFC 775 X-commands still sent by some clients.
var legacyAliases = map[string]string{
	"XPWD": "PWD",
	"XCWD": "CWD",
	"XCUP": "CDUP",
	"XMKD": "MKD",
	"XRMD": "RMD",
}

// stripTelnet drops the Telnet IAC, "Interrupt Process" and "Data Mark"
// bytes (0xFF, 0xF4, 0xF2) that RFC 959 §4.1.3 clients send ahead of ABOR.
// The urgent byte arrives inline (see setOOBInline).
func stripTelnet(line string) string {
	i := 0
	for i < len(line) && (line[i] == 0xff || line[i] == 0xf4 || line[i] == 0xf2) {
		i++
	}
	return line[i:]
}

func isAbortCommand(line []byte) bool {
	cmd, _ := splitCommand(strings.TrimSpace(stripTelnet(string(line))))
	return cmd == "ABOR"
}

// watchAbort lets a client abort a running transfer. It watches the control
// connection for ABOR and closes the data connection when one arrives; any
// other command is left buffered for the command loop. stop ends the watch
// and reports whether ABOR was received (and consumed).
func watchAbort(conn net.Conn, reader *bufio.Reader, dc io.Closer) (stop func() bool) {
	var (
		mu      sync.Mutex
		stopped bool
		aborted atomic.Bool
		done    = make(chan struct{})
	)
	go func() {
		defer close(done)
		want := 1
		for {
			// The deadline is armed under mu so stop's "now" deadline can
			// never be overwritten by a later one.
			mu.Lock()
			if stopped {
				mu.Unlock()
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			mu.Unlock()

			_, err := reader.Peek(want)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				return // closed or line too long: the command loop handles it
			}
			buf, _ := reader.Peek(reader.Buffered())
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				want = len(buf) + 1 // wait for the rest of the line
				continue
			}
			if isAbortCommand(buf[:i]) {
				_, _ = reader.Discard(i + 1)
				aborted.Store(true)
				_ = dc.Close()
			}
			return
		}
	}()
	return func() bool {
		mu.Lock()
		stopped = true
		_ = conn.SetReadDeadline(time.Now())
		mu.Unlock()
		<-done
		return aborted.Load()
	}
}

// uniqueName picks an unused name for STOU in dir, based on hint when given.
func uniqueName(fsys vfs.FileSystem, dir, hint string) (string, error) {
	base := path.Base(strings.TrimSpace(hint))
	if base == "." || base == "/" || base == "" {
		base = "upload"
	}
	candidate := path.Join(dir, base)
	for i := 1; i <= 1000; i++ {
		if _, err := fsys.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
		candidate = path.Join(dir, fmt.Sprintf("%s.%d", base, i))
	}
	return "", errors.New("no unique name available")
}

// handleSite implements SITE CHMOD and SITE HELP.
func handleSite(conn net.Conn, state *connState, arg string) {
	sub, rest := splitCommand(arg)
	switch sub {
	case "CHMOD":
		modeStr, target := splitCommand(rest)
		mode, err := strconv.ParseUint(modeStr, 8, 32)
		if err != nil || mode > 0o7777 || strings.TrimSpace(target) == "" {
			writeReply(conn, 501, "Usage: SITE CHMOD <octal mode> <path>.")
			return
		}
		p := resolvePath(state.cwd, target)
		if err := state.fs.Chmod(p, os.FileMode(mode)); err != nil { // #nosec G115 -- bounded above
			writeReply(conn, 550, "SITE CHMOD failed.")
			return
		}
		writeReply(conn, 200, "SITE CHMOD command successful.")
	case "HELP", "":
		writeMultiline(conn, 214, []string{"The following SITE commands are recognized:", "CHMOD HELP", "End"})
	default:
		writeReply(conn, 504, "SITE command not implemented.")
	}
}

// handleMFMT implements MFMT (draft-somers-ftp-mfxx): set a file's
// modification time.
func handleMFMT(conn net.Conn, state *connState, arg string) {
	stamp, target := splitCommand(arg)
	mtime, err := time.Parse("20060102150405", stamp)
	if err != nil || strings.TrimSpace(target) == "" {
		writeReply(conn, 501, "Usage: MFMT <YYYYMMDDHHMMSS> <path>.")
		return
	}
	p := resolvePath(state.cwd, target)
	if err := state.fs.Chtimes(p, mtime, mtime); err != nil {
		writeReply(conn, 550, "Could not set modification time.")
		return
	}
	writeReply(conn, 213, fmt.Sprintf("Modify=%s; %s", mtime.UTC().Format("20060102150405"), target))
}

// handleStat implements STAT: server status without an argument, or a
// directory listing over the control connection with one.
func handleStat(conn net.Conn, state *connState, arg string) {
	if strings.TrimSpace(arg) == "" {
		lines := []string{"Kervan FTP server status:", "Connected from " + state.remoteAddr}
		if state.user != nil {
			lines = append(lines, "Logged in as "+state.user.Username)
		} else {
			lines = append(lines, "Not logged in")
		}
		if state.secureControl {
			lines = append(lines, "Control connection is encrypted")
		}
		lines = append(lines, "TYPE: BINARY, STRUcture: File, MODE: Stream", "End of status")
		writeMultiline(conn, 211, lines)
		return
	}
	if !isAuthed(conn, state) {
		return
	}
	var buf bytes.Buffer
	if err := writeListing(&buf, state.fs, resolvePath(state.cwd, arg), "LIST"); err != nil {
		writeReply(conn, 450, "Cannot list "+arg+".")
		return
	}
	_, _ = fmt.Fprintf(conn, "213-Status of %s:\r\n", arg)
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\r\n"), "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			// A leading space keeps a listing line from looking like a
			// reply terminator.
			_, _ = fmt.Fprintf(conn, " %s\r\n", line)
		}
	}
	_, _ = fmt.Fprintf(conn, "213 End of status\r\n")
}

var helpCommands = []string{
	"ABOR ALLO APPE AUTH CDUP CWD DELE EPRT EPSV FEAT HELP HOST LIST",
	"MDTM MFMT MKD MLSD MLST MODE NLST NOOP OPTS PASS PASV PBSZ PORT",
	"PROT PWD QUIT REST RETR RMD RNFR RNTO SITE SIZE STAT STOR STOU",
	"STRU SYST TYPE USER XCUP XCWD XMKD XPWD XRMD",
}

func handleHelp(conn net.Conn) {
	lines := append([]string{"The following commands are recognized:"}, helpCommands...)
	writeMultiline(conn, 214, append(lines, "Help OK."))
}
