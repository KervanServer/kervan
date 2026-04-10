package audit

import (
	"context"
	"encoding/json"
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &FileSink{file: f, enc: enc}, nil
}

func (s *FileSink) Write(_ context.Context, evt Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enc.Encode(evt)
}

func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}
