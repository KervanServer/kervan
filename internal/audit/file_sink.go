package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type FileSink struct {
	mu    sync.Mutex
	file  *os.File
	chain *chain
}

func NewFileSink(path string) (*FileSink, error) {
	return openFileSink(path, nil)
}

// NewChainedFileSink writes tamper-evident records sealed with key (see
// chain.go), continuing the chain already present in the file.
func NewChainedFileSink(path string, key []byte) (*FileSink, error) {
	if len(key) < chainKeySize {
		return nil, errors.New("audit integrity key is too short")
	}
	c, err := resumeChain(path, key)
	if err != nil {
		return nil, fmt.Errorf("resume audit chain in %s: %w", path, err)
	}
	return openFileSink(path, c)
}

func openFileSink(path string, c *chain) (*FileSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create audit directory for %s: %w", path, err)
	}
	// #nosec G304 -- audit sink path is configured by trusted operators.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit file %s: %w", path, err)
	}
	return &FileSink{file: f, chain: c}, nil
}

func (s *FileSink) Write(_ context.Context, evt Event) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(evt); err != nil {
		return fmt.Errorf("encode audit event to file sink: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := buf.Bytes()
	var next chainState
	if s.chain != nil {
		sealed, st, err := s.chain.seal(line)
		if err != nil {
			return err
		}
		line, next = append(sealed, '\n'), st
	}
	// One write per record keeps lines whole under O_APPEND.
	if _, err := s.file.Write(line); err != nil {
		return fmt.Errorf("write audit event to file sink: %w", err)
	}
	// Advance only after the record is written, so a failed write does not
	// leave the next record linked to one that never reached the file.
	if s.chain != nil {
		s.chain.commit(next)
	}
	return nil
}

func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close audit file sink: %w", err)
	}
	return nil
}
