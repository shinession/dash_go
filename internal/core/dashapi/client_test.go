package dashapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignatureMatchesPython(t *testing.T) {
	// hashlib.md5(f"unit-test-secret_26111907".encode()).hexdigest() computed in Python
	got := Signature("unit-test-secret", time.Date(2026, 11, 19, 7, 59, 0, 0, time.Local))
	if got != "4e90a08816394badabb3a35738d2da30" {
		t.Fatalf("signature = %s", got)
	}
}

func TestHCProfileSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/user/hc_scan" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != Signature("salt", time.Now()) {
			t.Errorf("wrong signature header")
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["func"] != "hc_user_processes" || body["user"] != "AB12C34" {
			t.Errorf("unexpected body %v", body)
		}
		_, _ = w.Write([]byte(`{"Response":"ok","name":"Test User","ee_code":"AB12C34","processes":["Winder","Slitter"]}`))
	}))
	defer srv.Close()
	p, err := New(srv.URL+"/api", "salt", time.Second).HCProfile(context.Background(), "AB12C34")
	if err != nil || p.Name != "Test User" || len(p.Processes) != 2 || p.IsAdmin() {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestHCProfileTypeAndIsAdmin(t *testing.T) {
	cases := []struct {
		body      string
		wantType  string
		wantAdmin bool
	}{
		{`{"Response":"ok","name":"Admin User","ee_code":"AD01","type":"admin","processes":["Winder"]}`, "admin", true},
		{`{"Response":"ok","name":"Admin Case","ee_code":"AD02","type":" Admin ","processes":["Winder"]}`, " Admin ", true},
		{`{"Response":"ok","name":"Normal User","ee_code":"US01","type":"user","processes":["Winder"]}`, "user", false},
		{`{"Response":"ok","name":"Legacy Backend","ee_code":"LG01","processes":["Winder"]}`, "", false},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(tc.body))
		}))
		p, err := New(srv.URL+"/api", "salt", time.Second).HCProfile(context.Background(), "X")
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if p.Type != tc.wantType || p.IsAdmin() != tc.wantAdmin {
			t.Errorf("%s: got Type=%q IsAdmin=%v, want Type=%q IsAdmin=%v", tc.body, p.Type, p.IsAdmin(), tc.wantType, tc.wantAdmin)
		}
	}
}

func TestHCProfileErrors(t *testing.T) {
	status := atomic.Int32{}
	calls := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"Response":"no"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "salt", time.Second)

	status.Store(202)
	if _, err := c.HCProfile(context.Background(), "x"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("202: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("a definite answer is not retried, calls=%d", calls.Load())
	}

	calls.Store(0)
	status.Store(401)
	if _, err := c.HCProfile(context.Background(), "x"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("401: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("a rejected signature is retried once (hour change), calls=%d", calls.Load())
	}

	calls.Store(0)
	status.Store(500)
	if _, err := c.HCProfile(context.Background(), "x"); err == nil || calls.Load() != 1 {
		t.Errorf("500 must fail without retry: %v calls=%d", err, calls.Load())
	} else if !strings.Contains(err.Error(), srv.URL) {
		t.Errorf("the error must say which backend answered: %v", err)
	}
}

// /api/user answers 204 to a function it does not have: the backend is older than dash_go, or it is the wrong one
// (the api_server of a development machine's patch file points at production). That is not "try again in a moment".
func TestAFunctionTheBackendDoesNotKnowIsReportedWithTheBackendAddress(t *testing.T) {
	calls := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(srv.URL+"/api/", "salt", time.Second)

	_, err := c.HCProfile(context.Background(), "AB12C34")
	if !errors.Is(err, ErrFunctionUnknown) || !strings.Contains(err.Error(), srv.URL+"/api answered status 204") {
		t.Fatalf("hc_user_processes: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("an answer that will not change is not repeated, calls=%d", calls.Load())
	}
	if err := c.VerifyUser(context.Background(), "AB12C34", "pw"); !errors.Is(err, ErrFunctionUnknown) {
		t.Errorf("verify_user: %v", err)
	}
}

// Check is what dash_go -check asks the backend: a signed call that reads and changes nothing. A working backend
// answers 202 to it (hc_user_processes without a user); everything else is a problem somebody would meet.
func TestCheckTellsAWorkingBackendFromTheFailuresPeopleWouldMeet(t *testing.T) {
	status := atomic.Int32{}
	calls := atomic.Int32{}
	var lastBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/user/hc_scan" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != Signature("salt", time.Now()) {
			t.Errorf("the check must be signed like every other call")
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastBody.Store(body)
		w.WriteHeader(int(status.Load()))
	}))
	c := New(srv.URL+"/api", "salt", time.Second)
	ctx := context.Background()

	for _, ok := range []int32{http.StatusAccepted, http.StatusOK} {
		calls.Store(0)
		status.Store(ok)
		if err := c.Check(ctx); err != nil {
			t.Errorf("%d: a working backend must pass: %v", ok, err)
		}
		if calls.Load() != 1 {
			t.Errorf("%d: calls=%d, a definite answer is not repeated", ok, calls.Load())
		}
	}
	body, _ := lastBody.Load().(map[string]string)
	if _, asksForAUser := body["user"]; body["func"] != "hc_user_processes" || asksForAUser {
		t.Errorf("the check must ask hc_user_processes without a user, so that no account is looked up: %v", body)
	}

	calls.Store(0)
	status.Store(401)
	if err := c.Check(ctx); !errors.Is(err, ErrUnauthorized) || calls.Load() != 2 {
		t.Errorf("401: err=%v calls=%d (a rejected signature is tried once more for the hour change)", err, calls.Load())
	}

	calls.Store(0)
	status.Store(204)
	if err := c.Check(ctx); !errors.Is(err, ErrFunctionUnknown) || !strings.Contains(err.Error(), srv.URL+"/api answered status 204") || calls.Load() != 1 {
		t.Errorf("204: err=%v calls=%d", err, calls.Load())
	}

	calls.Store(0)
	status.Store(500)
	if err := c.Check(ctx); err == nil || !strings.Contains(err.Error(), "status 500") || calls.Load() != 1 {
		t.Errorf("500: err=%v calls=%d", err, calls.Load())
	}

	srv.Close()
	if err := c.Check(ctx); err == nil || !strings.Contains(err.Error(), "dash api request failed") {
		t.Errorf("a backend that cannot be reached: %v", err)
	}
}

func TestRootIsWrittenWithoutCredentials(t *testing.T) {
	if got := New("http://user:secret@api.example.local/api/", "salt", time.Second).Root(); got != "http://api.example.local/api" {
		t.Errorf("%q", got)
	}
	if got := New("http://user:secret@api.example.local:bad/api", "salt", time.Second).Root(); strings.Contains(got, "secret") {
		t.Errorf("an address that cannot be read must not be written as it is: %q", got)
	}
}
