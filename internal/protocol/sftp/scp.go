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

func parseSCPExec(command string) (mode, target string, err error) {
	args := strings.Fields(command)
	if len(args) == 0 || args[0] != "scp" {
		return "", "", errors.New("unsupported exec command")
	}
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "f") {
				mode = scpModeSource
			}
			if strings.Contains(arg, "t") {
				mode = scpModeSink
			}
			continue
		}
		target = arg
	}
	if mode == "" {
		return "", "", errors.New("scp mode is missing")
	}
	if target == "" {
		target = "."
	}
	return mode, target, nil
}

func (s *Server) runSCP(ch ssh.Channel, fsys vfs.FileSystem, mode, target, username, remoteAddr string) error {
	switch mode {
	case scpModeSource:
		return s.runSCPSource(ch, fsys, normalizeSCPPath(target), username, remoteAddr)
	case scpModeSink:
		return s.runSCPSink(ch, fsys, normalizeSCPPath(target), username, remoteAddr)
	default:
		return fmt.Errorf("unknown scp mode: %s", mode)
	}
}

func (s *Server) runSCPSource(ch ssh.Channel, fsys vfs.FileSystem, filePath, username, remoteAddr string) error {
	br := bufio.NewReader(ch)
	if err := readSCPAck(br); err != nil {
		return err
	}

	f, err := fsys.Open(filePath, os.O_RDONLY, 0)
	if err != nil {
		_ = writeSCPError(ch, false, err.Error())
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		_ = writeSCPError(ch, false, err.Error())
		return err
	}
	if info.IsDir() {
		err := errors.New("directories are not supported in scp source mode")
		_ = writeSCPError(ch, false, err.Error())
		return err
	}

	if _, err := fmt.Fprintf(ch, "C%04o %d %s\n", info.Mode().Perm(), info.Size(), info.Name()); err != nil {
		return err
	}
	if err := readSCPAck(br); err != nil {
		return err
	}
	transferID := ""
	if s.xfer != nil {
		transferID = s.xfer.Start(username, "scp", filePath, transfer.DirectionDownload, info.Size())
	}
	n, err := io.CopyN(ch, f, info.Size())
	if err != nil {
		if s.xfer != nil && transferID != "" {
			s.xfer.AddBytes(transferID, n)
			s.xfer.End(transferID, transfer.StatusFailed, err.Error())
		}
		return err
	}
	if _, err := ch.Write([]byte{0}); err != nil {
		if s.xfer != nil && transferID != "" {
			s.xfer.AddBytes(transferID, n)
			s.xfer.End(transferID, transfer.StatusFailed, err.Error())
		}
		return err
	}
	if err := readSCPAck(br); err != nil {
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

	s.emitAudit(audit.EventFileRead, username, "scp", filePath, remoteAddr, "ok", "scp download")
	return nil
}

func (s *Server) runSCPSink(ch ssh.Channel, fsys vfs.FileSystem, target, username, remoteAddr string) error {
	br := bufio.NewReader(ch)
	if _, err := ch.Write([]byte{0}); err != nil {
		return err
	}

	for {
		header, err := br.ReadString('\n')
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
			if _, err := ch.Write([]byte{0}); err != nil {
				return err
			}
			continue
		case 'C':
			modeBits, size, filename, parseErr := parseSCPFileHeader(header)
			if parseErr != nil {
				_ = writeSCPError(ch, true, parseErr.Error())
				return parseErr
			}
			dst := resolveSCPSinkPath(fsys, target, filename)
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
			_ = f.Close()
			trailer, err := br.ReadByte()
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
			if _, err := ch.Write([]byte{0}); err != nil {
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
			s.emitAudit(audit.EventFileWrite, username, "scp", dst, remoteAddr, "ok", "scp upload")
		case 'E':
			if _, err := ch.Write([]byte{0}); err != nil {
				return err
			}
			return nil
		case 1, 2:
			return errors.New(strings.TrimSpace(header[1:]))
		default:
			err := fmt.Errorf("unsupported scp command: %q", header)
			_ = writeSCPError(ch, true, err.Error())
			return err
		}
	}
}

func parseSCPFileHeader(header string) (mode uint32, size int64, name string, err error) {
	if len(header) < 2 || header[0] != 'C' {
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
	return uint32(m), sz, name, nil
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

func readSCPAck(br *bufio.Reader) error {
	b, err := br.ReadByte()
	if err != nil {
		return err
	}
	switch b {
	case 0:
		return nil
	case 1, 2:
		msg, _ := br.ReadString('\n')
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
