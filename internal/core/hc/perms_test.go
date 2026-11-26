package hc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/dashapi"
)

type stubProfileSource struct {
	prof  dashapi.Profile
	err   error
	calls int
}

func (s *stubProfileSource) HCProfile(_ context.Context, _ string) (dashapi.Profile, error) {
	s.calls++
	return s.prof, s.err
}

func TestDashPermissionsCachesAndBypassesWhenFresh(t *testing.T) {
	src := &stubProfileSource{prof: dashapi.Profile{Name: "Admin", EECode: "AD01", Type: "admin", Processes: []string{"Winder", "Slitter"}}}
	p := NewDashPermissions(src, time.Minute)
	now := time.Date(2026, 11, 27, 8, 0, 0, 0, time.Local)
	p.now = func() time.Time { return now }
	u := auth.User{EE: "AD01", Name: "Admin"}

	acc, err := p.Access(context.Background(), u, false)
	if err != nil || !acc.IsAdmin || !reflect.DeepEqual(acc.Processes, []string{"Winder", "Slitter"}) || src.calls != 1 {
		t.Fatalf("first call: %+v err=%v calls=%d", acc, err, src.calls)
	}
	// Mutating caller slice must not corrupt cache.
	acc.Processes[0] = "Mutated"

	// Second call within TTL hits cache.
	procs, err := p.Processes(context.Background(), u, false)
	if err != nil || !reflect.DeepEqual(procs, []string{"Winder", "Slitter"}) || src.calls != 1 {
		t.Fatalf("cached call: %v err=%v calls=%d", procs, err, src.calls)
	}

	// Fresh call bypasses cache and notices role revocation.
	src.prof = dashapi.Profile{Name: "Admin", EECode: "AD01", Type: "user", Processes: []string{"Winder"}}
	acc, err = p.Access(context.Background(), u, true)
	if err != nil || acc.IsAdmin || !reflect.DeepEqual(acc.Processes, []string{"Winder"}) || src.calls != 2 {
		t.Fatalf("fresh call: %+v err=%v calls=%d", acc, err, src.calls)
	}

	// After TTL expires, cache refreshes.
	now = now.Add(2 * time.Minute)
	src.err = dashapi.ErrNotAllowed
	acc, err = p.Access(context.Background(), u, false)
	if err != nil || acc.IsAdmin || len(acc.Processes) != 0 || src.calls != 3 {
		t.Fatalf("not allowed: %+v err=%v calls=%d", acc, err, src.calls)
	}

	// Transient error is returned and never cached.
	now = now.Add(2 * time.Minute)
	src.err = errors.New("network down")
	if _, err := p.Access(context.Background(), u, false); err == nil {
		t.Fatal("expected error")
	}
	src.err = nil
	src.prof = dashapi.Profile{Processes: []string{"Slitter"}}
	acc, err = p.Access(context.Background(), u, false)
	if err != nil || !reflect.DeepEqual(acc.Processes, []string{"Slitter"}) {
		t.Fatalf("recovery after error: %+v err=%v", acc, err)
	}
}

func TestOrderProcesses(t *testing.T) {
	got := OrderProcesses([]string{"Winder", "Unknown", "Slitter"}, []string{"Slitter", "Taping", "Winder"})
	if !reflect.DeepEqual(got, []string{"Slitter", "Winder"}) {
		t.Fatalf("ordered = %v", got)
	}
	empty := OrderProcesses(nil, []string{"Slitter"})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty must be non-nil empty slice: %#v", empty)
	}
	if !ContainsProcess(got, "Winder") || ContainsProcess(got, "Taping") {
		t.Fatalf("ContainsProcess unexpected for %v", got)
	}
}
