package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"dash_go/internal/core/i18n"
)

// Gate follows the requests that change something, which are the clicks and the submits of the pages (every method
// but GET, HEAD and OPTIONS). The automatic update restarts the service, and it does so only when nobody works with it
// like that: Busy says whether somebody does, and Drain makes the service refuse new requests of that kind so that the
// ones that are served are the last before the restart. Requests that only read go through all the time.
type Gate struct {
	mu       sync.Mutex
	running  int
	lastEnd  time.Time
	draining bool
	now      func() time.Time
}

// NewGate makes a Gate that lets everything through.
func NewGate() *Gate { return &Gate{now: time.Now} }

// changesSomething reports whether a method asks the service to change something.
func changesSomething(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// Wrap makes the handler count its requests that change something, and refuse them while the gate drains: with a 503
// that says what is going on, in the language of the request, so the page can show it as it shows any other error.
func (g *Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !changesSomething(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if !g.enter() {
			w.Header().Set("Retry-After", "5")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": i18n.T(r.Context(), "err.updating", nil), "code": "updating"})
			return
		}
		defer g.leave()
		next.ServeHTTP(w, r)
	})
}

func (g *Gate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.draining {
		return false
	}
	g.running++
	return true
}

func (g *Gate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.running--
	g.lastEnd = g.now()
}

// Running is the number of requests that change something and are being served.
func (g *Gate) Running() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

// Drain makes the gate refuse requests that change something until Resume.
func (g *Gate) Drain() {
	g.mu.Lock()
	g.draining = true
	g.mu.Unlock()
}

// Resume lets requests that change something through again.
func (g *Gate) Resume() {
	g.mu.Lock()
	g.draining = false
	g.mu.Unlock()
}

// Busy says why nobody should restart the service now: a request that changes something is being served, or the last
// one ended less than quiet ago (somebody who submits one thing often submits the next a moment later). It is empty
// when the service has been quiet for that long.
func (g *Gate) Busy(quiet time.Duration) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running > 0 {
		return fmt.Sprintf("%d request(s) that change something are being served", g.running)
	}
	if !g.lastEnd.IsZero() {
		if ago := g.now().Sub(g.lastEnd); ago < quiet {
			return fmt.Sprintf("the last request that changed something ended %s ago", ago.Round(time.Second))
		}
	}
	return ""
}
