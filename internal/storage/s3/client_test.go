package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListObjectsV2WithTokenRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") != "2" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(strings.Repeat("a", maxS3ListResponseBytes+1)))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Endpoint:     server.URL,
		Region:       "us-east-1",
		UsePathStyle: true,
		DisableSSL:   true,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.ListObjectsV2WithToken(context.Background(), "bucket", "", "", 10, "")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized list response error, got %v", err)
	}
}

func TestClientDoTruncatesErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", maxS3ErrorBodyBytes*4)))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Endpoint:     server.URL,
		Region:       "us-east-1",
		UsePathStyle: true,
		DisableSSL:   true,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.GetObject(context.Background(), "bucket", "file.txt")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500 Internal Server Error") {
		t.Fatalf("expected status in error, got %v", err)
	}
	if len(err.Error()) > maxS3ErrorBodyBytes+256 {
		t.Fatalf("expected truncated error body, got length %d", len(err.Error()))
	}
}
