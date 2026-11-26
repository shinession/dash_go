// Package hc holds helpers shared by the Head Count modules (such as hc_scan and hc_budget):
// permission lookups via the Python Dash API and process list ordering.
package hc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/dashapi"
)

// ProfileSource is the part of the Dash API client the HC permission check needs.
type ProfileSource interface {
	HCProfile(ctx context.Context, user string) (dashapi.Profile, error)
}

// Access is the effective HC permission of one user: the processes they may work on and whether they are an admin.
type Access struct {
	Processes []string
	IsAdmin   bool
}

// Permissions tells which HC processes a user may work on and whether they hold admin privileges.
type Permissions interface {
	Access(ctx context.Context, u auth.User, fresh bool) (Access, error)
	Processes(ctx context.Context, u auth.User, fresh bool) ([]string, error)
}

// DashPermissions asks the Python backend which HC processes a user may work on and their user Type.
// Answers are cached for a short time (a revoked permission is noticed within a minute); a failed lookup is never
// answered from the cache, and save steps always bypass it with fresh=true.
type DashPermissions struct {
	src ProfileSource
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	cache map[string]permItem
}

type permItem struct {
	access Access
	at     time.Time
}

// NewDashPermissions returns the Permissions implementation backed by the Python API.
func NewDashPermissions(src ProfileSource, ttl time.Duration) *DashPermissions {
	return &DashPermissions{src: src, ttl: ttl, now: time.Now, cache: map[string]permItem{}}
}

// Access returns the permitted processes and admin flag of u.
func (p *DashPermissions) Access(ctx context.Context, u auth.User, fresh bool) (Access, error) {
	if !fresh {
		p.mu.Lock()
		it, ok := p.cache[u.EE]
		p.mu.Unlock()
		if ok && p.now().Sub(it.at) < p.ttl {
			return cloneAccess(it.access), nil
		}
	}
	prof, err := p.src.HCProfile(ctx, u.EE)
	if err != nil {
		if errors.Is(err, dashapi.ErrNotAllowed) {
			prof = dashapi.Profile{}
		} else {
			return Access{}, err
		}
	}
	acc := Access{
		Processes: append([]string(nil), prof.Processes...),
		IsAdmin:   prof.IsAdmin(),
	}
	p.mu.Lock()
	p.cache[u.EE] = permItem{access: acc, at: p.now()}
	p.mu.Unlock()
	return cloneAccess(acc), nil
}

// Processes returns the processes of u (convenience method matching the hc_scan Permissions interface).
func (p *DashPermissions) Processes(ctx context.Context, u auth.User, fresh bool) ([]string, error) {
	acc, err := p.Access(ctx, u, fresh)
	if err != nil {
		return nil, err
	}
	return acc.Processes, nil
}

func cloneAccess(a Access) Access {
	out := Access{IsAdmin: a.IsAdmin}
	if len(a.Processes) > 0 {
		out.Processes = append([]string(nil), a.Processes...)
	}
	return out
}

// OrderProcesses returns the intersection of allowed and configured (HC_PROCESS_LIST) in configured order.
// Always returns a non-nil slice so JSON encoding produces [] instead of null.
func OrderProcesses(allowed, configured []string) []string {
	set := make(map[string]bool, len(allowed))
	for _, p := range allowed {
		set[strings.TrimSpace(p)] = true
	}
	out := []string{}
	for _, p := range configured {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

// ContainsProcess reports whether target is present in list.
func ContainsProcess(list []string, target string) bool {
	for _, p := range list {
		if p == target {
			return true
		}
	}
	return false
}
