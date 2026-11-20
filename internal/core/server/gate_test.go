package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dash_go/internal/core/i18n"
)

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func gateWith(now *time.Time) *Gate {
	g := NewGate()
	g.now = func() time.Time { return *now }
	return g
}

func TestGateCountsOnlyRequestsThatChangeSomething(t *testing.T) {
	g := NewGate()
	started := make(chan struct{})
	release := make(chan struct{})
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
		}
	}))

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/x", nil))
		if got := g.Busy(time.Hour); got != "" {
			t.Errorf("a %s request made the service busy: %s", method, got)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/slow", nil))
	}()
	<-started
	if g.Running() != 1 || !strings.Contains(g.Busy(0), "being served") {
		t.Errorf("running = %d, busy = %q", g.Running(), g.Busy(0))
	}
	close(release)
	<-done
	if g.Running() != 0 {
		t.Errorf("running = %d after the request ended", g.Running())
	}
}

func TestGateIsBusyUntilTheServiceHasBeenQuietForLongEnough(t *testing.T) {
	now := time.Unix(1700000000, 0)
	g := gateWith(&now)
	if got := g.Busy(10 * time.Second); got != "" {
		t.Fatalf("a service nobody used is busy: %s", got)
	}
	h := g.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/x", nil))
	}

	now = now.Add(4 * time.Second)
	if got := g.Busy(10 * time.Second); !strings.Contains(got, "ended 4s ago") {
		t.Errorf("busy = %q, want the age of the last request", got)
	}
	now = now.Add(6 * time.Second)
	if got := g.Busy(10 * time.Second); got != "" {
		t.Errorf("busy = %q after the quiet time", got)
	}
}

func TestGateRefusesWhatChangesSomethingWhileItDrainsAndKeepsReading(t *testing.T) {
	g := NewGate()
	called := 0
	h := Handler(g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++ })), testLogger())
	g.Drain()

	req := httptest.NewRequest(http.MethodPost, "/dash_go/hc_scan/api/scan", nil)
	req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "zh"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the answer is not JSON: %q", rec.Body.String())
	}
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || body["code"] != "updating" ||
		rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("%d %v %v", rec.Code, rec.Header(), body)
	}
	if !strings.Contains(body["error"], "更新") {
		t.Errorf("the message is not in the language of the request: %q", body["error"])
	}
	if called != 0 || g.Running() != 0 {
		t.Errorf("a refused request reached the handler (%d) or was counted (%d)", called, g.Running())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dash_go/hc_scan/api/board", nil))
	if rec.Code != 200 || called != 1 {
		t.Errorf("a request that only reads was refused: %d", rec.Code)
	}

	g.Resume()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/dash_go/hc_scan/api/scan", nil))
	if rec.Code != 200 || called != 2 {
		t.Errorf("requests were not taken again after Resume: %d", rec.Code)
	}
}

// A request that was already served when the gate began to drain is the one the update waits for.
func TestGateServesTheRequestsThatWereRunningWhenItBeganToDrain(t *testing.T) {
	g := NewGate()
	started := make(chan struct{})
	release := make(chan struct{})
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("done"))
	}))
	rec := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	}()
	<-started
	g.Drain()
	if g.Running() != 1 {
		t.Fatalf("running = %d", g.Running())
	}
	close(release)
	wg.Wait()
	if rec.Code != 200 || rec.Body.String() != "done" || g.Running() != 0 {
		t.Errorf("%d %q, running = %d", rec.Code, rec.Body.String(), g.Running())
	}
}
