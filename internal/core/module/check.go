package module

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"dash_go/internal/core/config"
)

// Probe tests one connection of the service. dash_go -check runs the probes of the shared connections (Env.Probes), of
// the sign-in and of every module that implements Checker, all at the same time.
type Probe struct {
	Name   string // what is tested, for example "PostgreSQL"
	Target string // where it points, written without credentials, for example "localhost:5432 database Auth"
	// Run does the test. note is a fact worth showing when it works, for example the version of the server.
	Run func(ctx context.Context) (note string, err error)
}

// Check is the outcome of a Probe.
type Check struct {
	Name   string
	Target string
	Note   string        // shown when the test worked
	Took   time.Duration // how long the test took, a new connection included
	Err    error         // nil when it worked
}

// Checker is implemented by a module that depends on more than the shared PostgreSQL and Redis, for example on a
// backend of its own. dash_go -check asks it for its probes once the module is mounted.
type Checker interface {
	Probes() []Probe
}

// RunProbes runs the probes at the same time, so that a service with several things down is not waited for one after
// the other, and returns the outcomes in the order of the probes. Every probe has to end when ctx ends.
func RunProbes(ctx context.Context, probes []Probe) []Check {
	checks := make([]Check, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			note, err := p.Run(ctx)
			checks[i] = Check{Name: p.Name, Target: p.Target, Note: note, Took: time.Since(start), Err: err}
		}()
	}
	wg.Wait()
	return checks
}

// Report writes one line per check and a summary to w, and returns how many checks failed.
func Report(w io.Writer, checks []Check) (failed int) {
	_, _ = fmt.Fprintln(w, "connection checks:")
	for _, c := range checks {
		if c.Err != nil {
			failed++
			_, _ = fmt.Fprintf(w, "  %-6s  %-20s %s: %s\n", "FAILED", c.Name, c.Target, oneLine(c.Err.Error()))
			continue
		}
		detail := fmt.Sprintf("%d ms", c.Took.Milliseconds())
		if c.Note != "" {
			detail += ", " + c.Note
		}
		_, _ = fmt.Fprintf(w, "  %-6s  %-20s %s: %s\n", "ok", c.Name, c.Target, detail)
	}
	switch {
	case len(checks) == 0:
		_, _ = fmt.Fprintln(w, "  nothing to test: no database, Redis or Dash API is in use")
	case failed == 0:
		_, _ = fmt.Fprintf(w, "all %d connection check(s) passed\n", len(checks))
	default:
		_, _ = fmt.Fprintf(w, "%d of %d connection check(s) FAILED\n", failed, len(checks))
	}
	return failed
}

// oneLine puts an error text on one line of a reasonable length.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// Probes returns the tests of the shared connections that were opened: PostgreSQL and Redis. A connection that
// nothing asked for is left out, so a binary whose modules need neither is not held to them.
func (e *Env) Probes() []Probe {
	var probes []Probe
	e.pgMu.Lock()
	dbNames := make([]string, 0, len(e.pgPools))
	for name, pool := range e.pgPools {
		if pool != nil {
			dbNames = append(dbNames, name)
		}
	}
	sort.Slice(dbNames, func(i, j int) bool {
		if dbNames[i] == "Auth" {
			return true
		}
		if dbNames[j] == "Auth" {
			return false
		}
		return dbNames[i] < dbNames[j]
	})
	pools := make([]*pgxpool.Pool, len(dbNames))
	for i, name := range dbNames {
		pools[i] = e.pgPools[name]
	}
	e.pgMu.Unlock()

	for i, name := range dbNames {
		probeName := "PostgreSQL"
		if name != "Auth" {
			probeName = fmt.Sprintf("PostgreSQL (%s)", name)
		}
		probes = append(probes, postgresProbe(probeName, pools[i]))
	}
	if e.rd != nil {
		probes = append(probes, e.redisProbe())
	}
	return probes
}

// postgresProbe opens a new connection with the settings of the pool, not a connection of the pool: the time is what
// the first request after a restart pays, and the address is the server that really answered, which is not
// necessarily the one the settings name (AGENTS.md, 14.5).
func postgresProbe(probeName string, pool *pgxpool.Pool) Probe {
	cc := pool.Config().ConnConfig // a copy
	if cc.RuntimeParams == nil {
		cc.RuntimeParams = map[string]string{}
	}
	cc.RuntimeParams["application_name"] = "dash_go_check" // tells the check from the service in the server's own views
	return Probe{
		Name:   probeName,
		Target: fmt.Sprintf("%s:%d database %s", cc.Host, cc.Port, cc.Database),
		Run: func(ctx context.Context) (string, error) {
			conn, err := pgx.ConnectConfig(ctx, cc)
			if err != nil {
				return "", err
			}
			defer func() { _ = conn.Close(context.Background()) }()
			var version string
			if err := conn.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
				return "", err
			}
			return fmt.Sprintf("connected to %s, server %s", conn.PgConn().Conn().RemoteAddr(), version), nil
		},
	}
}

// redisProbe pings Redis with a client of its own, built from the same settings as the client of the service, so the
// first command opens a new connection even when the client of the service has already been used.
func (e *Env) redisProbe() Probe {
	rc := e.Cfg.Dash.Redis
	return Probe{
		Name:   "Redis",
		Target: fmt.Sprintf("%s db %d", config.Addr(rc.Host, rc.Port), rc.DB),
		Run: func(ctx context.Context) (string, error) {
			opts := redisOptions(rc)
			opts.MaxRetries = -1 // say what went wrong at once instead of trying again
			c := redis.NewClient(opts)
			defer func() { _ = c.Close() }()
			return "", c.Ping(ctx).Err()
		},
	}
}
