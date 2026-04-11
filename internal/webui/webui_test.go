package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNewHandlerServesIndexAndAssets(t *testing.T) {
	handler, err := NewHandler()
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	handler.ServeHTTP(rootRec, rootReq)

	if rootRec.Code != http.StatusOK {
		t.Fatalf("expected index status 200, got %d", rootRec.Code)
	}
	if got := rootRec.Header().Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
		t.Fatalf("unexpected index cache header: %q", got)
	}
	if !strings.Contains(rootRec.Body.String(), "<!doctype html") && !strings.Contains(strings.ToLower(rootRec.Body.String()), "<html") {
		t.Fatalf("expected html body for root request, got %q", rootRec.Body.String())
	}

	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		t.Fatalf("sub fs: %v", err)
	}
	matches, err := fs.Glob(sub, "assets/*.js")
	if err != nil || len(matches) == 0 {
		t.Fatalf("find embedded js asset: matches=%v err=%v", matches, err)
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/"+path.Clean(matches[0]), nil)
	assetRec := httptest.NewRecorder()
	handler.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("expected asset status 200, got %d", assetRec.Code)
	}
	if got := assetRec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected asset cache header: %q", got)
	}
}

func TestCleanWebPathAndImmutableAssetHelpers(t *testing.T) {
	if got := cleanWebPath(""); got != "/" {
		t.Fatalf("expected empty path to normalize to root, got %q", got)
	}
	if got := cleanWebPath("."); got != "/" {
		t.Fatalf("expected dot path to normalize to root, got %q", got)
	}
	if got := cleanWebPath("/../assets/app.js"); got != "/assets/app.js" {
		t.Fatalf("unexpected cleaned path: %q", got)
	}
	if !isImmutableAsset("assets/app-abc123.js") {
		t.Fatal("expected hashed js asset to be immutable")
	}
	if !isImmutableAsset("assets/styles-deadbeef.css") {
		t.Fatal("expected hashed css asset to be immutable")
	}
	if isImmutableAsset("assets/app.js") {
		t.Fatal("expected unhashed asset to be mutable")
	}
	if isImmutableAsset("assets/image-deadbeef.png") {
		t.Fatal("expected non-css/js asset to be mutable")
	}
}

func TestUnknownRouteFallsBackToIndex(t *testing.T) {
	handler, err := NewHandler()
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/missing/client/route", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected SPA fallback status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
		t.Fatalf("unexpected fallback cache header: %q", got)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "<html") {
		t.Fatalf("expected fallback html body, got %q", rec.Body.String())
	}
}

func TestServeIndexMissingReturnsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	serveIndex(rec, fstest.MapFS{})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected missing index to return 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "index.html not found") {
		t.Fatalf("expected not found body, got %q", rec.Body.String())
	}
}
