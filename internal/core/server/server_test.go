package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerSetsHeadersRecoversAndHidesQueryStrings(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dash_go/auth/sso", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("secret internal detail") })
	h := Handler(mux, log)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dash_go/auth/sso?t=SECRET-LOGIN-TOKEN&next=/x", nil))
	if rec.Code != 200 || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("headers: %d %v", rec.Code, rec.Header())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("scripts must only come from the service itself: %s", csp)
	}
	if strings.Contains(logs.String(), "SECRET-LOGIN-TOKEN") || !strings.Contains(logs.String(), "/dash_go/auth/sso") {
		t.Fatalf("the access log has the path but never the query string (it carries the login token):\n%s", logs.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret internal detail") {
		t.Fatalf("a panic gives a plain 500 without details: %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "secret internal detail") {
		t.Fatal("the panic must be in the log")
	}
}
