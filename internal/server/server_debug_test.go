package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildDebugMuxServesHealthAndPprof(t *testing.T) {
	mux := buildDebugMux(true)

	healthRec := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	mux.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected debug health 200, got %d", healthRec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(healthRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode debug health: %v", err)
	}
	if payload["pprof"] != true {
		t.Fatalf("expected pprof=true payload, got %#v", payload)
	}

	pprofRec := httptest.NewRecorder()
	pprofReq := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	mux.ServeHTTP(pprofRec, pprofReq)
	if pprofRec.Code != http.StatusOK {
		t.Fatalf("expected pprof index 200, got %d", pprofRec.Code)
	}
}

func TestBuildDebugMuxDisablesPprofRoutesWhenConfiguredOff(t *testing.T) {
	mux := buildDebugMux(false)

	pprofRec := httptest.NewRecorder()
	pprofReq := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	mux.ServeHTTP(pprofRec, pprofReq)
	if pprofRec.Code != http.StatusNotFound {
		t.Fatalf("expected pprof route to be disabled, got %d", pprofRec.Code)
	}
}
