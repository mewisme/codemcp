package telegram

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed mini-app-dist/*
var miniAppWebAssets embed.FS

var miniAppInlineScriptHash = mustMiniAppInlineScriptHash()

func miniAppContentSecurityPolicy() string {
	return "default-src 'self'; script-src 'self' https://telegram.org 'sha256-" + miniAppInlineScriptHash + "'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; base-uri 'none'"
}

func mustMiniAppInlineScriptHash() string {
	data, err := miniAppWebAssets.ReadFile("mini-app-dist/mini-app.html")
	if err != nil {
		panic("read embedded Mini App shell: " + err.Error())
	}
	start := strings.Index(string(data), `<script type="module"`)
	if start < 0 {
		panic("embedded Mini App shell has no module script")
	}
	bodyStart := strings.Index(string(data[start:]), ">")
	if bodyStart < 0 {
		panic("embedded Mini App module script is malformed")
	}
	bodyStart += start + 1
	end := strings.Index(string(data[bodyStart:]), "</script>")
	if end < 0 {
		panic("embedded Mini App module script has no closing tag")
	}
	digest := sha256.Sum256(data[bodyStart : bodyStart+end])
	return base64.StdEncoding.EncodeToString(digest[:])
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
	clean := strings.TrimPrefix(path.Clean(strings.TrimPrefix(r.URL.Path, "/mini-app")), "/")
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
