// Package server wires the HTTP layer: the common middleware and the health check.
package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"dash_go/internal/core/i18n"
)

// contentSecurityPolicy lets a page load scripts, styles and pictures only from the service itself (the pictures
// of the employees are below the same host, /api/static/hr/). No page needs a plug-in, a base tag or a form that
// posts somewhere else.
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; " +
	"base-uri 'none'; object-src 'none'; form-action 'self'; frame-ancestors 'self'"

// Handler wraps the mux with panic recovery, security headers, the language of the request and an access log. The
// query string is never logged because the sso link carries a login token in it.
func Handler(mux http.Handler, log *slog.Logger) http.Handler {
	mux = i18n.Middleware(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h := rec.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
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

// MountCommon registers {base}/healthz. The landing page {base}/ belongs to the shell.
func MountCommon(mux *http.ServeMux, base string) {
	mux.HandleFunc("GET "+base+"/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
}
