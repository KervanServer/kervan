package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type FileSink struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

func NewFileSink(path string) (*FileSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create audit directory for %s: %w", path, err)
	}
	// #nosec G304 -- audit sink path is configured by trusted operators.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit file %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &FileSink{file: f, enc: enc}, nil
}

func (s *FileSink) Write(_ context.Context, evt Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.enc.Encode(evt); err != nil {
		return fmt.Errorf("encode audit event to file sink: %w", err)
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
