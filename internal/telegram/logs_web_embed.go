package telegram

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed logs-dist/*
var logsWebAssets embed.FS

func logsWebHandler() http.Handler {
	static, err := fs.Sub(logsWebAssets, "logs-dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return logsWebStaticHandler{static: static, files: http.FileServer(http.FS(static))}
}

type logsWebStaticHandler struct {
	static fs.FS
	files  http.Handler
}

func (h logsWebStaticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(path.Clean(strings.TrimPrefix(r.URL.Path, "/logs")), "/")
	if clean == "" || clean == "." || clean == "logs.html" {
		h.serveHTML(w)
		return
	}
	if info, err := fs.Stat(h.static, clean); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	clone := r.Clone(r.Context())
	clone.URL.Path = "/" + clean
	h.files.ServeHTTP(w, clone)
}

func (h logsWebStaticHandler) serveHTML(w http.ResponseWriter) {
	data, err := fs.ReadFile(h.static, "logs.html")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(".html"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
