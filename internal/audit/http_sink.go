package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type HTTPSinkOptions struct {
	URL           string
	Method        string
	Headers       map[string]string
	BatchSize     int
	FlushInterval time.Duration
	RetryCount    int
	Client        *http.Client
}

type HTTPSink struct {
	url           string
	method        string
	headers       map[string]string
	batchSize     int
	flushInterval time.Duration
	retryCount    int
	client        *http.Client

	mu     sync.Mutex
	batch  []Event
	closed bool

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewHTTPSink(opts HTTPSinkOptions) (*HTTPSink, error) {
	rawURL := strings.TrimSpace(opts.URL)
	if rawURL == "" {
		return nil, errors.New("http audit sink url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("http audit sink url must be a valid http or https URL")
	}

	method := strings.ToUpper(strings.TrimSpace(opts.Method))
	if method == "" {
		method = http.MethodPost
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 1
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	sink := &HTTPSink{
		url:           rawURL,
		method:        method,
		headers:       cloneHeaders(opts.Headers),
		batchSize:     batchSize,
		flushInterval: opts.FlushInterval,
		retryCount:    maxInt(opts.RetryCount, 0),
		client:        client,
		stopCh:        make(chan struct{}),
	}
	if sink.flushInterval > 0 {
		sink.wg.Add(1)
		go sink.flushLoop()
	}
	return sink, nil
}

func (s *HTTPSink) Write(ctx context.Context, evt Event) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("http audit sink is closed")
	}
	s.batch = append(s.batch, evt)
	flushNow := len(s.batch) >= s.batchSize
	var batch []Event
	if flushNow {
		batch = s.takeBatchLocked()
	}
	s.mu.Unlock()

	if !flushNow {
		return nil
	}
	return s.sendBatch(ctx, batch)
}

func (s *HTTPSink) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.stopCh)
	batch := s.takeBatchLocked()
	s.mu.Unlock()

	s.wg.Wait()
	if len(batch) == 0 {
		return nil
	}
	return s.sendBatch(context.Background(), batch)
}

func (s *HTTPSink) flushLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.flushPending()
		case <-s.stopCh:
			return
		}
	}
}

func (s *HTTPSink) flushPending() {
	s.mu.Lock()
	batch := s.takeBatchLocked()
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	_ = s.sendBatch(context.Background(), batch)
}

func (s *HTTPSink) takeBatchLocked() []Event {
	if len(s.batch) == 0 {
		return nil
	}
	out := make([]Event, len(s.batch))
	copy(out, s.batch)
	s.batch = s.batch[:0]
	return out
}

func (s *HTTPSink) sendBatch(ctx context.Context, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}

	var payload any
	if len(batch) == 1 && s.batchSize <= 1 && s.flushInterval <= 0 {
		payload = batch[0]
	} else {
		payload = batch
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= s.retryCount; attempt++ {
		req, err := http.NewRequestWithContext(ctx, s.method, s.url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		for key, value := range s.headers {
			req.Header.Set(key, value)
		}

		resp, err := s.client.Do(req)
		if err == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil {
			_ = resp.Body.Close()
			lastErr = errors.New(resp.Status)
		} else {
			lastErr = err
		}
		if attempt < s.retryCount {
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
	}
	return lastErr
}

func cloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func maxInt(v, fallback int) int {
	if v < fallback {
		return fallback
	}
	return v
}
