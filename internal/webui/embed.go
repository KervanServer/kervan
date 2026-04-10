package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dist/*
var embedded embed.FS

func NewHandler() (http.Handler, error) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := cleanWebPath(r.URL.Path)
		if p == "/" {
			serveIndex(w, sub)
			return
		}

		relative := strings.TrimPrefix(p, "/")
		if _, err := fs.Stat(sub, relative); err == nil {
			http.FileServer(http.FS(sub)).ServeHTTP(w, r)
			return
		}
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
