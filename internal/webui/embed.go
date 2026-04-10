package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

//go:embed dist/*
var embedded embed.FS

func NewHandler() (http.Handler, error) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := cleanWebPath(r.URL.Path)
		if p == "/" {
			setIndexCacheHeaders(w)
			serveIndex(w, sub)
			return
		}

		relative := strings.TrimPrefix(p, "/")
		if _, err := fs.Stat(sub, relative); err == nil {
			if isImmutableAsset(relative) {
				setImmutableCacheHeaders(w)
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		setIndexCacheHeaders(w)
		serveIndex(w, sub)
	}), nil
}

func serveIndex(w http.ResponseWriter, f fs.FS) {
	raw, err := fs.ReadFile(f, "index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func cleanWebPath(p string) string {
	if p == "" {
		return "/"
	}
	clean := path.Clean("/" + p)
	if clean == "." {
		return "/"
	}
	return clean
}

func isImmutableAsset(name string) bool {
	base := filepath.Base(name)
	if !strings.Contains(base, "-") {
		return false
	}
	ext := filepath.Ext(base)
	return ext == ".js" || ext == ".css"
}

func setIndexCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
}

func setImmutableCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
}
