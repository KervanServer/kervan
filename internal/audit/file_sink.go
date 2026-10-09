package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// FileSinkOptions configures a JSON-lines audit file.
type FileSinkOptions struct {
	Path string
	// ChainKey enables tamper-evident records (see chain.go).
	ChainKey []byte
	// MaxSize rotates the file once it would exceed this many bytes; 0
	// disables rotation.
	MaxSize int64
	// MaxBackups bounds the rotated files kept; 0 keeps all.
	MaxBackups int
	// Now is overridable for tests.
	Now func() time.Time
}

type FileSink struct {
	opts  FileSinkOptions
	mu    sync.Mutex
	file  *os.File
	size  int64
	chain *chain
}

// NewFileSink appends plain records to path, without rotation.
func NewFileSink(path string) (*FileSink, error) {
	return OpenFileSink(FileSinkOptions{Path: path})
}

// NewChainedFileSink appends tamper-evident records to path, without
// rotation.
func NewChainedFileSink(path string, key []byte) (*FileSink, error) {
	return OpenFileSink(FileSinkOptions{Path: path, ChainKey: key})
}

func OpenFileSink(opts FileSinkOptions) (*FileSink, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &FileSink{opts: opts}
	if opts.ChainKey != nil {
		if len(opts.ChainKey) < chainKeySize {
			return nil, errors.New("audit integrity key is too short")
		}
		c, err := resumeChain(opts.Path, opts.ChainKey)
		if err != nil {
			return nil, fmt.Errorf("resume audit chain in %s: %w", opts.Path, err)
		}
		s.chain = c
	}
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o750); err != nil {
		return nil, fmt.Errorf("create audit directory for %s: %w", opts.Path, err)
	}
	if err := s.open(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileSink) open() error {
	// #nosec G304 -- audit sink path is configured by trusted operators.
	f, err := os.OpenFile(s.opts.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit file %s: %w", s.opts.Path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	s.file, s.size = f, info.Size()
	return nil
}

func encodeEvent(evt Event) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(evt); err != nil {
		return nil, fmt.Errorf("encode audit event: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *FileSink) Write(_ context.Context, evt Event) error {
	line, err := encodeEvent(evt)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.MaxSize > 0 && s.size > 0 && s.size+int64(len(line))+128 > s.opts.MaxSize {
		if err := s.rotateLocked(); err != nil {
			return fmt.Errorf("rotate audit file: %w", err)
		}
	}
	return s.appendLocked(line)
}

// appendLocked writes one encoded event, sealing it when chaining is on.
func (s *FileSink) appendLocked(line []byte) error {
	var next chainState
	if s.chain != nil {
		sealed, st, err := s.chain.seal(line)
		if err != nil {
			return err
		}
		line, next = append(sealed, '\n'), st
	}
	// One write per record keeps lines whole under O_APPEND.
	n, err := s.file.Write(line)
	s.size += int64(n)
	if err != nil {
		return fmt.Errorf("write audit event to file sink: %w", err)
	}
	// Advance only after the record is written, so a failed write does not
	// leave the next record linked to one that never reached the file.
	if s.chain != nil {
		s.chain.commit(next)
	}
	return nil
}

// rotateLocked moves the current file aside, starts a new one and prunes
// backups beyond MaxBackups. The chain continues into the new file; each
// pruned file's last seq/mac is recorded in a sealed audit.pruned event so
// verification can tell retention from tampering.
func (s *FileSink) rotateLocked() error {
	if err := s.file.Close(); err != nil {
		return err
	}
	rotated := rotatedName(s.opts.Path, s.opts.Now())
	if err := os.Rename(s.opts.Path, rotated); err != nil {
		_ = s.open()
		return err
	}
	if err := s.open(); err != nil {
		return err
	}
	if s.opts.MaxBackups <= 0 {
		return nil
	}
	backups, err := RotatedFiles(s.opts.Path)
	if err != nil {
		return err
	}
	for len(backups) > s.opts.MaxBackups {
		oldest := backups[0]
		backups = backups[1:]
		evt := Event{
			Type:      EventAuditPruned,
			Timestamp: s.opts.Now().UTC(),
			Status:    "ok",
			Message:   "rotated audit file removed by retention",
			Meta:      map[string]string{"file": filepath.Base(oldest)},
		}
		if st, ok := lastChainState(oldest); ok {
			evt.Meta["last_seq"] = strconv.FormatUint(st.Seq, 10)
			evt.Meta["last_mac"] = st.MAC
		}
		line, err := encodeEvent(evt)
		if err != nil {
			return err
		}
		// Record the prune before deleting, so a crash in between leaves
		// an extra file rather than an unexplained gap.
		if err := s.appendLocked(line); err != nil {
			return err
		}
		if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	if err != nil {
		return fmt.Errorf("close audit file sink: %w", err)
	}
	return nil
}
