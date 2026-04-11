package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebSocketProtocolHelpers(t *testing.T) {
	protocols := parseWebSocketProtocols("kervan.v1, auth.header.payload.signature")

	if len(protocols) != 2 {
		t.Fatalf("expected 2 protocols, got %d", len(protocols))
	}
	if got := websocketAuthToken(protocols); got != "header.payload.signature" {
		t.Fatalf("unexpected auth token: %q", got)
	}
	if got := websocketProtocolHeader(protocols); got != "\r\nSec-WebSocket-Protocol: kervan.v1" {
		t.Fatalf("unexpected websocket protocol header: %q", got)
	}
}

func TestWebSocketOriginAllowed(t *testing.T) {
	srv := &Server{}

	req := httptest.NewRequest(http.MethodGet, "http://kervan.local/api/v1/ws", nil)
	req.Host = "kervan.local"
	req.Header.Set("Origin", "http://kervan.local")
	if !srv.webSocketOriginAllowed(req) {
		t.Fatal("expected same-origin websocket request to be allowed")
	}

	req = httptest.NewRequest(http.MethodGet, "http://api.internal/api/v1/ws", nil)
	req.Host = "api.internal"
	req.Header.Set("Origin", "https://console.example.com")
	srv.cfg.CORSOrigins = []string{"https://console.example.com"}
	if !srv.webSocketOriginAllowed(req) {
		t.Fatal("expected allowlisted websocket origin to be allowed")
	}

	req = httptest.NewRequest(http.MethodGet, "http://kervan.local/api/v1/ws", nil)
	req.Host = "kervan.local"
	req.Header.Set("Origin", "https://evil.example.com")
	srv.cfg.CORSOrigins = nil
	if srv.webSocketOriginAllowed(req) {
		t.Fatal("expected cross-origin websocket request to be rejected")
	}
}

func TestHandleWebSocketRejectsDisallowedOrigin(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "http://kervan.local/api/v1/ws", nil)
	req.Host = "kervan.local"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Origin", "https://evil.example.com")

	rec := httptest.NewRecorder()
	srv.handleWebSocket(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed websocket origin, got %d", rec.Code)
	}
	if got := rec.Body.String(); got == "" {
		t.Fatal("expected error body for disallowed websocket origin")
	}
}
