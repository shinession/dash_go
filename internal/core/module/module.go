// Package module is the registry that makes dash_go modular.
//
// Every module lives in internal/mods/<name>, registers itself from init() and is pulled into the binary by one
// tiny file in cmd/dash_go (mod_<name>.go) that carries the build constraint "!nomod_<name>". scripts/build.ps1
// passes nomod_* tags for the modules that were not selected, so a package contains exactly the chosen modules.
package module

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
	"dash_go/internal/core/shell"
)

// Info describes a module in the landing page and in the logs. The Key fields are message keys of the catalog the
// module registered; the landing page shows the translated text and falls back to Title and Description.
type Info struct {
	Name           string
	Title          string
	TitleKey       string
	Version        string
	Description    string
	DescriptionKey string
	HasHelp        bool
}

// Module is implemented by every module.
type Module interface {
	Info() Info
	// Mount registers the routes of the module below env.Base + "/" + name.
	Mount(env *Env, mux *http.ServeMux) error
	// Close releases the resources of the module.
	Close() error
}

// Factory creates a module instance.
type Factory func() Module

var (
	mu        sync.Mutex
	factories = map[string]Factory{}
)

// Register adds a module factory. It is called from the init() of the module package.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[name]; dup {
		panic("module registered twice: " + name)
	}
	factories[name] = f
}

// Names returns the compiled-in module names, sorted.
func Names() []string {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(factories))
	for n := range factories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// New creates the module with the given name.
func New(name string) (Module, error) {
	mu.Lock()
	f, ok := factories[name]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("module %q is not compiled into this binary", name)
	}
	return f(), nil
}

// Env is the shared infrastructure the server gives to each module. The database and Redis connections are
// opened on first use, so a binary that only contains modules without a database does not need either.
type Env struct {
	Cfg   *config.Config
	Log   *slog.Logger
	Auth  *auth.Service
	Shell *shell.Shell // the frame of the pages: a module gives it the content of its page
	Base  string       // URL prefix without trailing slash, for example /dash_go

	pgOnce sync.Once
	pg     *pgxpool.Pool
	pgErr  error

	rdOnce sync.Once
	rd     *redis.Client
	rdErr  error
}

// NewEnv returns the environment of the server.
func NewEnv(cfg *config.Config, log *slog.Logger, a *auth.Service) *Env {
	return &Env{Cfg: cfg, Log: log, Auth: a, Base: cfg.BasePath}
}

// PG returns the pool of the Auth database.
func (e *Env) PG(ctx context.Context) (*pgxpool.Pool, error) {
	e.pgOnce.Do(func() {
		pc := e.Cfg.Dash.Postgres
		if pc.Host == "" || pc.Username == "" {
			e.pgErr = errors.New("postgresql is not configured (config key postgresql)")
			return
		}
		// the whole target goes into one connection string: setting Host and Port on a parsed empty configuration would
		// leave the fallback entries (localhost:5432, used when the first attempt fails) in place, and the service
		// could silently talk to another database
		pcfg, err := pgxpool.ParseConfig(pgDSN(pc, "Auth"))
		if err != nil {
			e.pgErr = fmt.Errorf("postgresql settings are not valid: %w", err)
			return
		}
		pcfg.MaxConns = 8
		pool, err := pgxpool.NewWithConfig(ctx, pcfg)
		if err != nil {
			e.pgErr = fmt.Errorf("open postgresql pool: %w", err)
			return
		}
		e.pg = pool
	})
	return e.pg, e.pgErr
}

// SetPG lets tests inject a pool.
func (e *Env) SetPG(p *pgxpool.Pool) {
	e.pgOnce.Do(func() {})
	e.pg, e.pgErr = p, nil
}

// Redis returns the Redis client.
func (e *Env) Redis() (*redis.Client, error) {
	e.rdOnce.Do(func() {
		rc := e.Cfg.Dash.Redis
		if rc.Host == "" {
			e.rdErr = errors.New("redis is not configured (config key redis)")
			return
		}
		e.rd = redis.NewClient(redisOptions(rc))
	})
	return e.rd, e.rdErr
}

// redisOptions are the settings of the Redis client of the service and of the client of its check. Every client gets
// options of its own: a client changes the options it is given, and go-redis does not accept options that another
// client has already prepared.
func redisOptions(rc config.Redis) *redis.Options {
	return &redis.Options{Addr: config.Addr(rc.Host, rc.Port), Password: rc.Password, DB: rc.DB}
}

// Close releases the shared connections.
func (e *Env) Close() {
	if e.pg != nil {
		e.pg.Close()
	}
	if e.rd != nil {
		_ = e.rd.Close()
	}
}

// pgDSN builds a keyword/value connection string with quoted values.
func pgDSN(pc config.Postgres, database string) string {
	quote := strings.NewReplacer("\\", "\\\\", "'", "\\'")
	q := func(v string) string { return "'" + quote.Replace(v) + "'" }
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s application_name=dash_go connect_timeout=10 statement_timeout=20000",
		q(pc.Host), pc.Port, q(pc.Username), q(pc.Password), q(database))
}
