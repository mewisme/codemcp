package telegram

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed mini-app-dist/*
var miniAppWebAssets embed.FS

func miniAppContentSecurityPolicy() string {
	return "default-src 'self'; script-src 'self' https://telegram.org; connect-src 'self' wss:; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; base-uri 'none'"
}

func miniAppWebHandler() http.Handler {
	static, err := fs.Sub(miniAppWebAssets, "mini-app-dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return miniAppWebStaticHandler{static: static, files: http.FileServer(http.FS(static))}
}

type miniAppWebStaticHandler struct {
	static fs.FS
	files  http.Handler
}

func (h miniAppWebStaticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if clean == "" || clean == "." || clean == "mini-app.html" {
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

func (h miniAppWebStaticHandler) serveHTML(w http.ResponseWriter) {
	data, err := fs.ReadFile(h.static, "mini-app.html")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(".html"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
