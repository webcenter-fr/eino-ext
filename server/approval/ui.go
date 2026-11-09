package approval

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:ui/dist
var uiFS embed.FS

func (s *Server) spaHandler() http.Handler {
	dist, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "UI unavailable", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce, err := randomNonce()
		if err != nil {
			http.Error(w, "UI unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self'; "+
				"img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; "+
				"base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		requestPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requestPath == "" || requestPath == "." {
			requestPath = "index.html"
		}
		if requestPath == "index.html" {
			s.serveIndex(w, dist, nonce)
			return
		}
		if _, err := fs.Stat(dist, requestPath); err != nil {
			s.serveIndex(w, dist, nonce)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, dist fs.FS, nonce string) {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "UI unavailable", http.StatusInternalServerError)
		return
	}
	html := strings.ReplaceAll(string(data), "<script", `<script nonce="`+nonce+`"`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, &http.Request{}, "index.html", time.Time{}, strings.NewReader(html))
}

func randomNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b), nil
}
