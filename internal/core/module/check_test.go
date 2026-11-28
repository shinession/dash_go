package module

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"dash_go/internal/core/config"
)

// closedPort returns a port on 127.0.0.1 that nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestRunProbesRunsThemTogetherKeepsTheirOrderAndTimesThem(t *testing.T) {
	const n = 3
	var all sync.WaitGroup
	all.Add(n)
	// Every probe waits until all of them have started: run one after the other, the first would wait until the
	// context ends and fail.
	meet := func(ctx context.Context) error {
		all.Done()
		done := make(chan struct{})
		go func() { all.Wait(); close(done) }()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	boom := errors.New("boom")
	probes := make([]Probe, n)
	for i := range probes {
		probes[i] = Probe{
			Name:   "probe " + strconv.Itoa(i),
			Target: "target " + strconv.Itoa(i),
			Run: func(ctx context.Context) (string, error) {
				if err := meet(ctx); err != nil {
					return "", err
				}
				time.Sleep(time.Duration(i) * 20 * time.Millisecond)
				if i == 1 {
					return "", boom
				}
				return "note " + strconv.Itoa(i), nil
			},
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	checks := RunProbes(ctx, probes)
	if len(checks) != n {
		t.Fatalf("%d outcomes for %d probes", len(checks), n)
	}
	for i, c := range checks {
		if c.Name != probes[i].Name || c.Target != probes[i].Target {
			t.Errorf("outcome %d is %q %q: the order of the probes was not kept", i, c.Name, c.Target)
		}
		if i == 1 {
			if !errors.Is(c.Err, boom) {
				t.Errorf("outcome 1 must carry the error of its probe: %v", c.Err)
			}
		} else if c.Err != nil || c.Note != "note "+strconv.Itoa(i) {
			t.Errorf("outcome %d: err=%v note=%q (did the probes run at the same time?)", i, c.Err, c.Note)
		}
		if c.Took < time.Duration(i)*20*time.Millisecond {
			t.Errorf("outcome %d took %v, less than its probe slept", i, c.Took)
		}
	}
}

func TestReportWritesOneLinePerCheckAndCountsTheFailures(t *testing.T) {
	checks := []Check{
		{Name: "PostgreSQL", Target: "db.example:5432 database Auth", Note: "server 16.4", Took: 91 * time.Millisecond},
		{Name: "Redis", Target: "localhost:6379 db 0", Took: 2 * time.Millisecond},
		{Name: "Dash API (sign-in)", Target: "http://api.example/api", Err: errors.New("first line\n\tsecond   line")},
	}
	var out bytes.Buffer
	if failed := Report(&out, checks); failed != 1 {
		t.Fatalf("failed = %d, want 1", failed)
	}
	want := []string{
		"connection checks:",
		"  ok      PostgreSQL           db.example:5432 database Auth: 91 ms, server 16.4",
		"  ok      Redis                localhost:6379 db 0: 2 ms",
		"  FAILED  Dash API (sign-in)   http://api.example/api: first line second line",
		"1 of 3 connection check(s) FAILED",
	}
	if got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n"); !slices.Equal(got, want) {
		t.Fatalf("report:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	out.Reset()
	if failed := Report(&out, checks[:2]); failed != 0 || !strings.HasSuffix(out.String(), "all 2 connection check(s) passed\n") {
		t.Errorf("failed=%d report:\n%s", failed, out.String())
	}
	out.Reset()
	if failed := Report(&out, nil); failed != 0 || !strings.Contains(out.String(), "nothing to test") {
		t.Errorf("failed=%d report:\n%s", failed, out.String())
	}
}

func TestProbesOnlyCoverTheConnectionsThatWereOpened(t *testing.T) {
	env := NewEnv(&config.Config{}, nil, nil)
	if p := env.Probes(); len(p) != 0 {
		t.Fatalf("nothing was opened, yet there are %d probe(s)", len(p))
	}
}

func TestRedisProbeUsesANewConnectionAndNeverShowsThePassword(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("s3cret-pw")
	port, err := strconv.Atoi(mr.Port())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Dash: config.Dash{Redis: config.Redis{Host: mr.Host(), Port: port, Password: "s3cret-pw"}}}
	env := NewEnv(cfg, nil, nil)
	if _, err := env.Redis(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)

	probes := env.Probes()
	if len(probes) != 1 || probes[0].Name != "Redis" {
		t.Fatalf("probes: %+v", probes)
	}
	if want := mr.Addr() + " db 0"; probes[0].Target != want {
		t.Errorf("target %q, want %q (and never the password)", probes[0].Target, want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c := RunProbes(ctx, probes)[0]; c.Err != nil {
		t.Fatalf("a Redis that answers must pass: %v", c.Err)
	}

	mr.Close()
	c := RunProbes(ctx, probes)[0]
	if c.Err == nil {
		t.Fatal("a Redis that is gone must fail")
	}
	if strings.Contains(c.Err.Error(), "s3cret-pw") {
		t.Errorf("the password is in the error: %v", c.Err)
	}
}

func TestRedisProbeFailsForAWrongPasswordWithoutShowingIt(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("the-right-one")
	port, _ := strconv.Atoi(mr.Port())
	cfg := &config.Config{Dash: config.Dash{Redis: config.Redis{Host: mr.Host(), Port: port, Password: "bad-pw-9f3a"}}}
	env := NewEnv(cfg, nil, nil)
	if _, err := env.Redis(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := RunProbes(ctx, env.Probes())[0]
	if c.Err == nil {
		t.Fatal("a wrong password must fail the check")
	}
	if strings.Contains(c.Err.Error(), "bad-pw-9f3a") || strings.Contains(c.Err.Error(), "the-right-one") {
		t.Errorf("a password is in the error: %v", c.Err)
	}
}

func TestPostgresProbeFailsWithoutAServerAndLeavesThePoolSettingsAlone(t *testing.T) {
	port := closedPort(t)
	pc := config.Postgres{Host: "127.0.0.1", Port: port, Username: "mes", Password: "p4ss-w0rd"}
	pcfg, err := pgxpool.ParseConfig(pgDSN(pc, "Auth"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	env := NewEnv(&config.Config{}, nil, nil)
	env.SetPG(pool)
	t.Cleanup(env.Close)

	probes := env.Probes()
	if len(probes) != 1 || probes[0].Name != "PostgreSQL" {
		t.Fatalf("probes: %+v", probes)
	}
	if want := fmt.Sprintf("127.0.0.1:%d database Auth", port); probes[0].Target != want {
		t.Errorf("target %q, want %q", probes[0].Target, want)
	}
	// the probe tells itself from the service in the views of the server, on its own copy of the settings
	if got := pool.Config().ConnConfig.RuntimeParams["application_name"]; got != "dash_go" {
		t.Errorf("the probe changed the settings of the pool: application_name %q", got)
	}
	c := RunProbes(ctx, probes)[0]
	if c.Err == nil {
		t.Fatal("a server that is not there must fail")
	}
	if strings.Contains(c.Err.Error(), "p4ss-w0rd") {
		t.Errorf("the password is in the error: %v", c.Err)
	}
}

func TestProbesOrderAuthBeforeAdditionalDatabases(t *testing.T) {
	port := closedPort(t)
	pc := config.Postgres{Host: "127.0.0.1", Port: port, Username: "mes", Password: "pw"}
	authCfg, err := pgxpool.ParseConfig(pgDSN(pc, "Auth"))
	if err != nil {
		t.Fatal(err)
	}
	logCfg, err := pgxpool.ParseConfig(pgDSN(pc, "Log"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	authPool, err := pgxpool.NewWithConfig(ctx, authCfg)
	if err != nil {
		t.Fatal(err)
	}
	logPool, err := pgxpool.NewWithConfig(ctx, logCfg)
	if err != nil {
		authPool.Close()
		t.Fatal(err)
	}
	env := NewEnv(&config.Config{}, nil, nil)
	env.SetPGDB("Log", logPool)
	env.SetPG(authPool)
	t.Cleanup(env.Close)

	probes := env.Probes()
	if len(probes) != 2 {
		t.Fatalf("got %d probes, want 2", len(probes))
	}
	if probes[0].Name != "PostgreSQL" || probes[0].Target != fmt.Sprintf("127.0.0.1:%d database Auth", port) {
		t.Errorf("probe[0] = %+v", probes[0])
	}
	if probes[1].Name != "PostgreSQL (Log)" || probes[1].Target != fmt.Sprintf("127.0.0.1:%d database Log", port) {
		t.Errorf("probe[1] = %+v", probes[1])
	}
}
