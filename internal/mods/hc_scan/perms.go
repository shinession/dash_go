package hcscan

import (
	"context"
	"errors"
	"sync"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/dashapi"
)

// ProfileSource is the part of the Dash API client the permission check needs.
type ProfileSource interface {
	HCProfile(ctx context.Context, user string) (dashapi.Profile, error)
}

// dashPerms asks the Python backend which HC processes a user may work on. Answers are cached for a short time
// (a revoked permission is noticed within a minute); a failed lookup is never answered from the cache, and the
// save step always bypasses it.
type dashPerms struct {
	src ProfileSource
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	cache map[string]permItem
}

type permItem struct {
	procs []string
	at    time.Time
}

// NewDashPermissions returns the Permissions implementation backed by the Python API.
func NewDashPermissions(src ProfileSource, ttl time.Duration) Permissions {
	return &dashPerms{src: src, ttl: ttl, now: time.Now, cache: map[string]permItem{}}
}

func (p *dashPerms) Processes(ctx context.Context, u auth.User, fresh bool) ([]string, error) {
	if !fresh {
		p.mu.Lock()
		it, ok := p.cache[u.EE]
		p.mu.Unlock()
		if ok && p.now().Sub(it.at) < p.ttl {
			return it.procs, nil
		}
	}
	prof, err := p.src.HCProfile(ctx, u.EE)
	if err != nil {
		if errors.Is(err, dashapi.ErrNotAllowed) {
			prof = dashapi.Profile{}
		} else {
			return nil, err
		}
	}
	p.mu.Lock()
	p.cache[u.EE] = permItem{procs: prof.Processes, at: p.now()}
	p.mu.Unlock()
	return prof.Processes, nil
}
