package s3

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeObject struct {
	data    []byte
	modTime time.Time
}

type fakeS3Server struct {
	mu      sync.RWMutex
	objects map[string]map[string]fakeObject
}

func newFakeS3Server() *fakeS3Server {
	return &fakeS3Server{
		objects: make(map[string]map[string]fakeObject),
	}
}

func (f *fakeS3Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key := parseBucketAndKey(r.URL.Path)
	if bucket == "" {
		http.Error(w, "missing bucket", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		f.handleList(w, bucket, r.URL.Query())
		return
	}

	switch r.Method {
	case http.MethodPut:
		if copySource := r.Header.Get("x-amz-copy-source"); copySource != "" {
			f.handleCopy(w, bucket, key, copySource)
			return
		}
		payload, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.ensureBucket(bucket)
		f.objects[bucket][key] = fakeObject{data: payload, modTime: time.Now().UTC().Round(time.Second)}
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		f.mu.RLock()
		obj, ok := f.objects[bucket][key]
		f.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
		w.Header().Set("Last-Modified", obj.modTime.UTC().Format(http.TimeFormat))
		_, _ = w.Write(obj.data)
	case http.MethodHead:
		f.mu.RLock()
		obj, ok := f.objects[bucket][key]
		f.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
		w.Header().Set("Last-Modified", obj.modTime.UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		f.mu.Lock()
		if bucketObjects, ok := f.objects[bucket]; ok {
			delete(bucketObjects, key)
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}
}

func (f *fakeS3Server) handleCopy(w http.ResponseWriter, dstBucket, dstKey, copySource string) {
	copySource = strings.TrimPrefix(copySource, "/")
	parts := strings.SplitN(copySource, "/", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid copy source", http.StatusBadRequest)
		return
	}
	srcBucket := parts[0]
	srcKey, _ := url.PathUnescape(parts[1])

	f.mu.Lock()
	defer f.mu.Unlock()
	srcObjects, ok := f.objects[srcBucket]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	obj, ok := srcObjects[srcKey]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	f.ensureBucket(dstBucket)
	f.objects[dstBucket][dstKey] = fakeObject{
		data:    append([]byte(nil), obj.data...),
		modTime: time.Now().UTC().Round(time.Second),
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<CopyObjectResult/>"))
}

func (f *fakeS3Server) handleList(w http.ResponseWriter, bucket string, query url.Values) {
	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	maxKeys := 1000
	if raw := query.Get("max-keys"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			maxKeys = parsed
		}
	}

	f.mu.RLock()
	bucketObjects := f.objects[bucket]
	f.mu.RUnlock()

	keys := make([]string, 0, len(bucketObjects))
	for key := range bucketObjects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)

	type content struct {
		Key          string    `xml:"Key"`
		LastModified time.Time `xml:"LastModified"`
		Size         int64     `xml:"Size"`
	}
	type commonPrefix struct {
		Prefix string `xml:"Prefix"`
	}
	type result struct {
		XMLName        xml.Name       `xml:"ListBucketResult"`
		IsTruncated    bool           `xml:"IsTruncated"`
		CommonPrefixes []commonPrefix `xml:"CommonPrefixes,omitempty"`
		Contents       []content      `xml:"Contents,omitempty"`
	}

	response := result{}
	seenPrefixes := map[string]struct{}{}
	count := 0
	for _, key := range keys {
		if count >= maxKeys {
			response.IsTruncated = true
			break
		}
		trimmed := strings.TrimPrefix(key, prefix)
		if delimiter != "" {
			if idx := strings.Index(trimmed, delimiter); idx >= 0 {
				dirPrefix := prefix + trimmed[:idx+1]
				if _, ok := seenPrefixes[dirPrefix]; !ok {
					seenPrefixes[dirPrefix] = struct{}{}
					response.CommonPrefixes = append(response.CommonPrefixes, commonPrefix{Prefix: dirPrefix})
					count++
				}
				continue
			}
		}
		obj := bucketObjects[key]
		response.Contents = append(response.Contents, content{
			Key:          key,
			LastModified: obj.modTime,
			Size:         int64(len(obj.data)),
		})
		count++
	}

	raw, _ := xml.Marshal(response)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(raw)
}

func (f *fakeS3Server) ensureBucket(bucket string) {
	if _, ok := f.objects[bucket]; !ok {
		f.objects[bucket] = make(map[string]fakeObject)
	}
}

func parseBucketAndKey(requestPath string) (string, string) {
	hadTrailingSlash := strings.HasSuffix(requestPath, "/")
	requestPath = strings.TrimPrefix(path.Clean("/"+requestPath), "/")
	if hadTrailingSlash && requestPath != "" && !strings.HasSuffix(requestPath, "/") {
		requestPath += "/"
	}
	parts := strings.SplitN(requestPath, "/", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func TestBackendFileLifecycle(t *testing.T) {
	server := httptest.NewServer(newFakeS3Server())
	t.Cleanup(server.Close)

	backend, err := New(Options{
		Endpoint:     server.URL,
		Bucket:       "test-bucket",
		Prefix:       "users/demo",
		UsePathStyle: true,
		DisableSSL:   true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := backend.MkdirAll("/docs/reports", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	file, err := backend.Open("/docs/reports/q1.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Open(write) error = %v", err)
	}
	if _, err := file.Write([]byte("hello s3")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	info, err := backend.Stat("/docs/reports/q1.txt")
	if err != nil {
		t.Fatalf("Stat(file) error = %v", err)
	}
	if info.Size() != int64(len("hello s3")) {
		t.Fatalf("Stat(file).Size() = %d", info.Size())
	}

	reader, err := backend.Open("/docs/reports/q1.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open(read) error = %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "hello s3" {
		t.Fatalf("ReadAll() = %q", string(data))
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close(read) error = %v", err)
	}

	entries, err := backend.ReadDir("/docs")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "reports" || !entries[0].IsDir() {
		t.Fatalf("ReadDir(/docs) unexpected entries = %#v", entries)
	}

	if err := backend.Rename("/docs/reports/q1.txt", "/docs/reports/q1-final.txt"); err != nil {
		t.Fatalf("Rename(file) error = %v", err)
	}
	if _, err := backend.Stat("/docs/reports/q1.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(old) error = %v, want os.ErrNotExist", err)
	}

	reader, err = backend.Open("/docs/reports/q1-final.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open(renamed) error = %v", err)
	}
	data, err = io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(renamed) error = %v", err)
	}
	if string(data) != "hello s3" {
		t.Fatalf("ReadAll(renamed) = %q", string(data))
	}
	_ = reader.Close()

	if err := backend.Remove("/docs/reports/q1-final.txt"); err != nil {
		t.Fatalf("Remove(file) error = %v", err)
	}
	if _, err := backend.Stat("/docs/reports/q1-final.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(removed) error = %v, want os.ErrNotExist", err)
	}
}

func TestBackendRenameDirAndRemoveAll(t *testing.T) {
	server := httptest.NewServer(newFakeS3Server())
	t.Cleanup(server.Close)

	backend, err := New(Options{
		Endpoint:     server.URL,
		Bucket:       "test-bucket",
		UsePathStyle: true,
		DisableSSL:   true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	writeFile := func(name, content string) {
		t.Helper()
		file, err := backend.Open(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("Open(%s) error = %v", name, err)
		}
		if _, err := io.Copy(file, bytes.NewBufferString(content)); err != nil {
			t.Fatalf("Copy(%s) error = %v", name, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close(%s) error = %v", name, err)
		}
	}

	if err := backend.MkdirAll("/alpha/nested", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeFile("/alpha/a.txt", "a")
	writeFile("/alpha/nested/b.txt", "b")

	if err := backend.Rename("/alpha", "/beta"); err != nil {
		t.Fatalf("Rename(dir) error = %v", err)
	}
	if _, err := backend.Stat("/alpha"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(old dir) error = %v, want os.ErrNotExist", err)
	}
	if _, err := backend.Stat("/beta/nested/b.txt"); err != nil {
		t.Fatalf("Stat(new dir file) error = %v", err)
	}

	if err := backend.RemoveAll("/beta"); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if _, err := backend.Stat("/beta"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(removed dir) error = %v, want os.ErrNotExist", err)
	}
}
