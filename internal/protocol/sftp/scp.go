package sftp

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
	"golang.org/x/crypto/ssh"
)

const (
	scpModeSource = "source"
	scpModeSink   = "sink"
)

func parseExecPayload(payload []byte) (string, error) {
	if len(payload) < 4 {
		return "", errors.New("invalid exec payload")
	}
	n := int(binary.BigEndian.Uint32(payload[:4]))
	if n < 0 || len(payload) < 4+n {
		return "", errors.New("invalid exec payload length")
	}
	return string(payload[4 : 4+n]), nil
}

// scpRequest is a parsed "scp -t|-f [-p] target" exec command.
type scpRequest struct {
	mode      string
	target    string
	preserve  bool // -p: times travel as a "T" record before each file
	recursive bool // -r: directories travel as "D" ... "E" records
}

func parseSCPExec(command string) (scpRequest, error) {
	var req scpRequest
	args, err := splitShellWords(command)
	if err != nil {
		return req, err
	}
	if len(args) == 0 || args[0] != "scp" {
		return req, errors.New("unsupported exec command")
	}
	optionsDone := false
	var targets []string
	for _, arg := range args[1:] {
		if !optionsDone && arg == "--" {
			optionsDone = true
			continue
		}
		if !optionsDone && strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "f") {
				req.mode = scpModeSource
			}
			if strings.Contains(arg, "t") {
				req.mode = scpModeSink
			}
			if strings.Contains(arg, "p") {
				req.preserve = true
			}
			if strings.Contains(arg, "r") {
				req.recursive = true
			}
			continue
		}
		targets = append(targets, arg)
	}
	if req.mode == "" {
		return req, errors.New("scp mode is missing")
	}
	switch len(targets) {
	case 0:
		req.target = "."
	case 1:
		req.target = targets[0]
	default:
		// Matches OpenSSH: an unquoted path with spaces reaches the remote
		// side as several words, which must not be silently truncated.
		return req, errors.New("ambiguous target")
	}
	return req, nil
}

// splitShellWords splits an exec request the way the remote POSIX shell would
// for the quoting forms scp clients emit: OpenSSH and libssh2/curl wrap paths
// in single quotes (an embedded quote is closed, backslash-escaped and
// reopened), and some clients use backslash escapes or double quotes. No
// expansion is performed.
func splitShellWords(s string) ([]string, error) {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		inQuote rune
	)
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case inQuote == '\'':
			if r == '\'' {
				inQuote = 0
			} else {
				cur.WriteRune(r)
			}
		case inQuote == '"':
			switch {
			case r == '"':
				inQuote = 0
			case r == '\\' && i+1 < len(runes) && strings.ContainsRune("$`\"\\\n", runes[i+1]):
				i++
				cur.WriteRune(runes[i])
			default:
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			inQuote = r
			inWord = true
		case r == '\\':
			if i+1 < len(runes) {
				i++
				cur.WriteRune(runes[i])
			}
			inWord = true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inQuote != 0 {
		return nil, errors.New("unterminated quote in exec command")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// touchReader renews the control-connection idle deadline whenever the SCP
// flow reads from the channel, so long transfers are not killed by the
// deadline set at connection accept.
type touchReader struct {
	r     io.Reader
	touch func()
}

func (t touchReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.touch()
	}
	return n, err
}

// touchWriter renews the deadline on channel writes (source-mode activity).
type touchWriter struct {
	w     io.Writer
	touch func()
}

func (t touchWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		t.touch()
	}
	return n, err
}

func (s *Server) runSCP(ch ssh.Channel, fsys vfs.FileSystem, req scpRequest, username, remoteAddr string, touch func()) error {
	switch req.mode {
	case scpModeSource:
		return s.runSCPSource(ch, fsys, normalizeSCPPath(req.target), req, username, remoteAddr, touch)
	case scpModeSink:
		return s.runSCPSink(ch, fsys, normalizeSCPPath(req.target), req.recursive, username, remoteAddr, touch)
	default:
		return fmt.Errorf("unknown scp mode: %s", req.mode)
	}
}

// scpSource streams files (and, with -r, directory trees) to an scp sink.
type scpSource struct {
	s          *Server
	ch         ssh.Channel
	br         *bufio.Reader
	fsys       vfs.FileSystem
	preserve   bool
	username   string
	remoteAddr string
	touch      func()
}

func (s *Server) runSCPSource(ch ssh.Channel, fsys vfs.FileSystem, target string, req scpRequest, username, remoteAddr string, touch func()) error {
	src := &scpSource{
		s:          s,
		ch:         ch,
		br:         bufio.NewReader(touchReader{r: ch, touch: touch}),
		fsys:       fsys,
		preserve:   req.preserve,
		username:   username,
		remoteAddr: remoteAddr,
		touch:      touch,
	}
	if err := readSCPAck(src.br); err != nil {
		return err
	}
	info, err := fsys.Stat(target)
	if err != nil {
		_ = writeSCPError(ch, false, err.Error())
		return err
	}
	if info.IsDir() {
		if !req.recursive {
			err := fmt.Errorf("%s: not a regular file (use -r)", target)
			_ = writeSCPError(ch, false, err.Error())
			return err
		}
		return src.sendDir(target, info)
	}
	// libssh2 (curl, PHP ssh2, ...) closes the channel after receiving a
	// single file instead of sending the final ack; every byte has been
	// delivered at that point, so EOF there is a completed download.
	return src.sendFile(target, true)
}

func (src *scpSource) sendTimes(info os.FileInfo) error {
	if !src.preserve {
		return nil
	}
	// libssh2 (curl) requests -p and rejects a "C" record that is not
	// preceded by the "T" times record.
	mtime := info.ModTime().Unix()
	if _, err := fmt.Fprintf(src.ch, "T%d 0 %d 0\n", mtime, mtime); err != nil {
		return err
	}
	return readSCPAck(src.br)
}

func (src *scpSource) sendDir(dirPath string, info os.FileInfo) error {
	if err := src.sendTimes(info); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(src.ch, "D%04o 0 %s\n", info.Mode().Perm(), path.Base(dirPath)); err != nil {
		return err
	}
	if err := readSCPAck(src.br); err != nil {
		return err
	}
	entries, err := src.fsys.ReadDir(dirPath)
	if err != nil {
		_ = writeSCPError(src.ch, false, err.Error())
		return err
	}
	for _, entry := range entries {
		child := path.Join(dirPath, entry.Name())
		switch {
		case entry.IsDir():
			childInfo, err := src.fsys.Stat(child)
			if err != nil {
				_ = writeSCPError(src.ch, false, err.Error())
				continue
			}
			if err := src.sendDir(child, childInfo); err != nil {
				return err
			}
		case entry.Type().IsRegular():
			if err := src.sendFile(child, false); err != nil {
				return err
			}
		}
	}
	if _, err := src.ch.Write([]byte("E\n")); err != nil {
		return err
	}
	return readSCPAck(src.br)
}

func (src *scpSource) sendFile(filePath string, eofIsSuccess bool) error {
	s := src.s
	f, err := src.fsys.Open(filePath, os.O_RDONLY, 0)
	if err != nil {
		_ = writeSCPError(src.ch, false, err.Error())
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		_ = writeSCPError(src.ch, false, err.Error())
		return err
	}
	if err := src.sendTimes(info); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(src.ch, "C%04o %d %s\n", info.Mode().Perm(), info.Size(), info.Name()); err != nil {
		return err
	}
	if err := readSCPAck(src.br); err != nil {
		return err
	}
	transferID := ""
	if s.xfer != nil {
		transferID = s.xfer.Start(src.username, "scp", filePath, transfer.DirectionDownload, info.Size())
	}
	fail := func(n int64, err error) error {
		if s.xfer != nil && transferID != "" {
			s.xfer.AddBytes(transferID, n)
			s.xfer.End(transferID, transfer.StatusFailed, err.Error())
		}
		return err
	}
	n, err := io.CopyN(touchWriter{w: src.ch, touch: src.touch}, f, info.Size())
	if err != nil {
		return fail(n, err)
	}
	if _, err := src.ch.Write([]byte{0}); err != nil {
		return fail(n, err)
	}
	if err := readSCPAck(src.br); err != nil && !(eofIsSuccess && errors.Is(err, io.EOF)) {
		return fail(n, err)
	}
	if s.xfer != nil && transferID != "" {
		s.xfer.AddBytes(transferID, n)
		s.xfer.End(transferID, transfer.StatusCompleted, "")
	}
	s.emitAudit(audit.EventFileRead, src.username, "scp", filePath, src.remoteAddr, "ok", "scp download")
	return nil
}

func (s *Server) runSCPSink(ch ssh.Channel, fsys vfs.FileSystem, target string, recursive bool, username, remoteAddr string, touch func()) error {
	br := bufio.NewReader(touchReader{r: ch, touch: touch})
	var pendingTimes *[2]time.Time // atime, mtime from a preceding "T" record
	// dirs is the stack of directories opened by "D" records; files and
	// subdirectories land in the innermost one. dirTimes holds each
	// directory's "T" times, applied when its "E" record closes it.
	var dirs []string
	var dirTimes []*[2]time.Time
	if _, err := ch.Write([]byte{0}); err != nil {
		return err
	}

	for {
		header, err := readSCPLE(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		header = strings.TrimSpace(header)
		if header == "" {
			continue
		}

		switch header[0] {
		case 0:
			continue
		case 'T':
			pendingTimes = parseSCPTimes(header)
			if _, err := ch.Write([]byte{0}); err != nil {
				return err
			}
			continue
		case 'D':
			if !recursive {
				err := errors.New("received directory without -r")
				_ = writeSCPError(ch, true, err.Error())
				return err
			}
			if len(dirs) >= maxSCPDirDepth {
				err := fmt.Errorf("directory nesting exceeds %d levels", maxSCPDirDepth)
				_ = writeSCPError(ch, true, err.Error())
				return err
			}
			modeBits, _, dirname, parseErr := parseSCPRecord(header, 'D')
			if parseErr != nil {
				_ = writeSCPError(ch, true, parseErr.Error())
				return parseErr
			}
			var dst string
			if len(dirs) == 0 {
				dst = resolveSCPSinkPath(fsys, target, dirname)
			} else {
				dst = path.Join(dirs[len(dirs)-1], dirname)
			}
			if err := fsys.Mkdir(dst, os.FileMode(modeBits)|0o700); err != nil {
				if info, statErr := fsys.Stat(dst); statErr != nil || !info.IsDir() {
					_ = writeSCPError(ch, true, err.Error())
					return err
				}
			}
			dirs = append(dirs, dst)
			dirTimes = append(dirTimes, pendingTimes)
			pendingTimes = nil
			if _, err := ch.Write([]byte{0}); err != nil {
				return err
			}
		case 'C':
			modeBits, size, filename, parseErr := parseSCPRecord(header, 'C')
			if parseErr != nil {
				_ = writeSCPError(ch, true, parseErr.Error())
				return parseErr
			}
			dst := resolveSCPSinkPath(fsys, target, filename)
			if len(dirs) > 0 {
				dst = path.Join(dirs[len(dirs)-1], filename)
			}
			f, openErr := fsys.Open(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(modeBits))
			if openErr != nil {
				_ = writeSCPError(ch, true, openErr.Error())
				return openErr
			}
			if _, err := ch.Write([]byte{0}); err != nil {
				_ = f.Close()
				return err
			}
			transferID := ""
			if s.xfer != nil {
				transferID = s.xfer.Start(username, "scp", dst, transfer.DirectionUpload, size)
			}
			n, err := io.CopyN(f, br, size)
			if err != nil {
				_ = f.Close()
				if s.xfer != nil && transferID != "" {
					s.xfer.AddBytes(transferID, n)
					s.xfer.End(transferID, transfer.StatusFailed, err.Error())
				}
				_ = writeSCPError(ch, true, err.Error())
				return err
			}
			if closeErr := f.Close(); closeErr != nil {
				if s.xfer != nil && transferID != "" {
					s.xfer.AddBytes(transferID, n)
					s.xfer.End(transferID, transfer.StatusFailed, closeErr.Error())
				}
				_ = writeSCPError(ch, true, closeErr.Error())
				return closeErr
			}
			trailer, err := br.ReadByte()
			peerClosed := errors.Is(err, io.EOF)
			if peerClosed {
				// libssh2 senders may close right after the payload without
				// the trailing status byte; the full size has arrived, so the
				// upload is complete.
				trailer, err = 0, nil
			}
			if err != nil {
				if s.xfer != nil && transferID != "" {
					s.xfer.AddBytes(transferID, n)
					s.xfer.End(transferID, transfer.StatusFailed, err.Error())
				}
				return err
			}
			if trailer != 0 {
				err = errors.New("invalid scp file trailer")
				if s.xfer != nil && transferID != "" {
					s.xfer.AddBytes(transferID, n)
					s.xfer.End(transferID, transfer.StatusFailed, err.Error())
				}
				_ = writeSCPError(ch, true, err.Error())
				return err
			}
			if _, err := ch.Write([]byte{0}); err != nil && !peerClosed {
				if s.xfer != nil && transferID != "" {
					s.xfer.AddBytes(transferID, n)
					s.xfer.End(transferID, transfer.StatusFailed, err.Error())
				}
				return err
			}
			if s.xfer != nil && transferID != "" {
				s.xfer.AddBytes(transferID, n)
				s.xfer.End(transferID, transfer.StatusCompleted, "")
			}
			if pendingTimes != nil {
				_ = fsys.Chtimes(dst, pendingTimes[0], pendingTimes[1])
				pendingTimes = nil
			}
			s.emitAudit(audit.EventFileWrite, username, "scp", dst, remoteAddr, "ok", "scp upload")
		case 'E':
			if len(dirs) == 0 {
				// No directory is open: treat a stray "E" as end of session.
				_, err := ch.Write([]byte{0})
				return err
			}
			if times := dirTimes[len(dirTimes)-1]; times != nil {
				_ = fsys.Chtimes(dirs[len(dirs)-1], times[0], times[1])
			}
			dirs, dirTimes = dirs[:len(dirs)-1], dirTimes[:len(dirTimes)-1]
			if _, err := ch.Write([]byte{0}); err != nil {
				return err
			}
		case 1, 2:
			return errors.New(strings.TrimSpace(header[1:]))
		default:
			err := fmt.Errorf("unsupported scp command: %q", header)
			_ = writeSCPError(ch, true, err.Error())
			return err
		}
	}
}

// parseSCPRecord parses a "C<mode> <size> <name>" file or
// "D<mode> 0 <name>" directory record.
func parseSCPRecord(header string, kind byte) (mode uint32, size int64, name string, err error) {
	if len(header) < 2 || header[0] != kind {
		return 0, 0, "", errors.New("invalid scp file header")
	}
	parts := strings.SplitN(header[1:], " ", 3)
	if len(parts) != 3 {
		return 0, 0, "", errors.New("invalid scp file header fields")
	}
	m, err := strconv.ParseUint(parts[0], 8, 32)
	if err != nil {
		return 0, 0, "", errors.New("invalid file mode")
	}
	sz, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || sz < 0 {
		return 0, 0, "", errors.New("invalid file size")
	}
	name = strings.TrimSpace(parts[2])
	if name == "" {
		return 0, 0, "", errors.New("empty file name")
	}
	if err := validateSCPFileName(name); err != nil {
		return 0, 0, "", err
	}
	return uint32(m), sz, name, nil
}

func validateSCPFileName(name string) error {
	switch trimmed := strings.TrimSpace(name); trimmed {
	case "", ".", "..":
		return errors.New("invalid file name")
	default:
		if strings.Contains(trimmed, "/") || strings.Contains(trimmed, `\`) {
			return errors.New("invalid file name")
		}
		return nil
	}
}

// parseSCPTimes parses "T<mtime> <usec> <atime> <usec>"; nil when malformed.
func parseSCPTimes(header string) *[2]time.Time {
	fields := strings.Fields(strings.TrimPrefix(header, "T"))
	if len(fields) != 4 {
		return nil
	}
	mtime, err1 := strconv.ParseInt(fields[0], 10, 64)
	atime, err2 := strconv.ParseInt(fields[2], 10, 64)
	if err1 != nil || err2 != nil || mtime < 0 || atime < 0 {
		return nil
	}
	return &[2]time.Time{time.Unix(atime, 0), time.Unix(mtime, 0)}
}

func resolveSCPSinkPath(fsys vfs.FileSystem, target, fileName string) string {
	target = normalizeSCPPath(target)
	if strings.HasSuffix(target, "/") {
		return path.Clean(path.Join(target, fileName))
	}
	info, err := fsys.Stat(target)
	if err == nil && info.IsDir() {
		return path.Clean(path.Join(target, fileName))
	}
	return target
}

func normalizeSCPPath(p string) string {
	if p == "" {
		return "/"
	}
	clean := path.Clean("/" + strings.TrimSpace(p))
	if clean == "." {
		return "/"
	}
	return clean
}

// maxSCPDirDepth bounds "D" record nesting so a client cannot grow the
// directory stack (and the created tree) without limit.
const maxSCPDirDepth = 128

// maxSCPLEBytes bounds a single SCP protocol line: legitimate headers and ack
// messages are a few kilobytes at most, and an uncapped ReadString would let
// an authenticated client grow the connection buffer without bound toward
// process OOM.
const maxSCPLEBytes = 64 << 10

// readSCPLE reads one '\n'-terminated line, capping total accumulation at
// maxSCPLEBytes. An over-cap line returns an error, which the SCP flows treat
// like any read error: the transfer fails and the channel closes.
func readSCPLE(br *bufio.Reader) (string, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if err == nil {
			buf = append(buf, chunk...)
			return string(buf), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			buf = append(buf, chunk...)
			if len(buf) > maxSCPLEBytes {
				return "", fmt.Errorf("scp line exceeds %d bytes", maxSCPLEBytes)
			}
			continue
		}
		if len(buf) > 0 {
			buf = append(buf, chunk...)
			return string(buf), err
		}
		return "", err
	}
}

func readSCPAck(br *bufio.Reader) error {
	b, err := br.ReadByte()
	if err != nil {
		return err
	}
	switch b {
	case 0:
		return nil
	case 1, 2:
		msg, err := readSCPLE(br)
		if err != nil {
			return err
		}
		return errors.New(strings.TrimSpace(msg))
	default:
		return fmt.Errorf("unexpected ack byte: %d", b)
	}
}

func writeSCPError(w io.Writer, fatal bool, msg string) error {
	code := byte(1)
	if fatal {
		code = 2
	}
	_, err := fmt.Fprintf(w, "%c%s\n", code, strings.TrimSpace(msg))
	return err
}
