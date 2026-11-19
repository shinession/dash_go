// Package server wires the HTTP layer: the module routes, the landing page and the common middleware.
package server

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"dash_go/internal/core/module"
	"dash_go/internal/version"
)

// Handler wraps the mux with panic recovery, security headers and an access log. The query string is never
// logged because the sso link carries a login token in it.
func Handler(mux http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h := rec.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'self'")
		defer func() {
			if p := recover(); p != nil {
				log.Error("panic in handler", "path", r.URL.Path, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
				if !rec.wrote {
					http.Error(rec, "internal error", http.StatusInternalServerError)
				}
			}
			if r.URL.Path != "" && !strings.HasSuffix(r.URL.Path, "/healthz") {
				log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", time.Since(start).Milliseconds())
			}
		}()
		mux.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status, s.wrote = code, true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// MountCommon registers {base}/healthz and the landing page {base}/ that lists the modules.
func MountCommon(mux *http.ServeMux, base string, mods []module.Module) {
	mux.HandleFunc("GET "+base+"/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET "+base+"/{$}", func(w http.ResponseWriter, _ *http.Request) {
		var b strings.Builder
		b.WriteString("<!doctype html><html lang=en><meta charset=utf-8><meta name=viewport content=\"width=device-width,initial-scale=1\"><title>dash_go</title>")
		b.WriteString("<body style=\"font-family:Segoe UI,sans-serif;background:#111;color:#eee;margin:0;padding:2rem\">")
		b.WriteString(fmt.Sprintf("<h1>dash_go <small style=\"color:#888;font-weight:400\">v%s</small></h1><ul style=\"line-height:2.2;font-size:1.2rem\">", html.EscapeString(version.Version)))
		for _, m := range mods {
			i := m.Info()
			b.WriteString(fmt.Sprintf("<li><a style=\"color:#7cc4ff\" href=\"%s/%s/\">%s</a> <small style=\"color:#888\">v%s - %s</small></li>",
				html.EscapeString(base), html.EscapeString(i.Name), html.EscapeString(i.Title), html.EscapeString(i.Version), html.EscapeString(i.Description)))
		}
		b.WriteString("</ul></body></html>")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
}
