package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dash_go/internal/core/i18n"
)

func TestHandlerPutsTheLanguageOfTheRequestIntoItsContext(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(i18n.T(r.Context(), "login.invalid", nil)))
	})
	h := Handler(mux, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "zh"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "账号或密码不正确") {
		t.Errorf("the handlers below must be able to translate: %q", rec.Body.String())
	}
}

func TestContentSecurityPolicyForbidsWhatNoPageNeeds(t *testing.T) {
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "base-uri 'none'", "object-src 'none'", "form-action 'self'", "frame-ancestors 'self'"} {
		if !strings.Contains(contentSecurityPolicy, want) {
			t.Errorf("the policy lacks %q: %s", want, contentSecurityPolicy)
		}
	}
	if strings.Contains(contentSecurityPolicy, "script-src 'self' 'unsafe-inline'") || strings.Contains(contentSecurityPolicy, "unsafe-eval") {
		t.Errorf("scripts only come from files of the service: %s", contentSecurityPolicy)
	}
}

func TestMountCommonServesTheHealthCheckOnly(t *testing.T) {
	mux := http.NewServeMux()
	MountCommon(mux, "/dash_go")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dash_go/healthz", nil))
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dash_go/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("the landing page belongs to the shell now: %d", rec.Code)
	}
}

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
