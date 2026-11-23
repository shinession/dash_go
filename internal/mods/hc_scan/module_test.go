package hcscan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"dash_go/internal/core/dashapi"
)

// dash_go -check asks every module for the backends it depends on. hc_scan depends on the Dash API of the permission
// check, which may be another one than the sign-in asks (modules.hc_scan.dash_api_url).
func TestModuleProbesTheDashAPIItAsks(t *testing.T) {
	m := &Module{}
	if p := m.Probes(); len(p) != 0 {
		t.Fatalf("a module that is not mounted has nothing to test: %+v", p)
	}

	var status atomic.Int32
	status.Store(http.StatusAccepted)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer srv.Close()
	m.api = dashapi.New(srv.URL+"/api", "salt", time.Second)

	probes := m.Probes()
	if len(probes) != 1 || probes[0].Name != "Dash API (hc_scan)" || probes[0].Target != srv.URL+"/api" {
		t.Fatalf("probes: %+v", probes)
	}
	if _, err := probes[0].Run(context.Background()); err != nil {
		t.Errorf("a backend that has the function must pass: %v", err)
	}
	status.Store(http.StatusNoContent) // an older backend: it does not know hc_user_processes
	if _, err := probes[0].Run(context.Background()); err == nil {
		t.Error("a backend without hc_user_processes must fail")
	}
}
