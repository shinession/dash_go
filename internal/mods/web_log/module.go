// Package weblog is the "Web Log" module (Go migration of IE.py tab11).
//
// It visualizes web traffic aggregated in Log.t_log_daily by calendar day, ISO week, or calendar month,
// supporting date range selection, App / Tool (category) filtering, Server (host) filtering, an optional
// stacked-by-server view, Redis result caching, and an administrator-only real-time Redis web_log stream
// gated by Auth."T_USER_ALL"."Type" = 'admin'.
package weblog

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"dash_go/internal/core/i18n"
	"dash_go/internal/core/module"
)

// Name is the module name used in the URL, the build script, and the configuration.
const Name = "web_log"

func init() {
	i18n.Register(webFS, "i18n")
	module.Register(Name, func() module.Module { return &Module{} })
}

// Module implements module.Module.
type Module struct {
	svc *Service
}

// Info describes the module for the landing page and logs.
func (m *Module) Info() module.Info {
	return module.Info{
		Name:           Name,
		Title:          "Web Log",
		TitleKey:       "wl.title",
		Icon:           "weblog",
		Description:    "Analyze application and server traffic trends by day, week, or month with live admin logs",
		DescriptionKey: "wl.description",
		HasHelp:        true,
	}
}

// Mount builds the service from configuration and registers the module routes.
func (m *Module) Mount(env *module.Env, mux *http.ServeMux) error {
	set := DefaultSettings()
	if err := env.Cfg.Module(Name, &set); err != nil {
		return err
	}
	set = set.normalized()
	if env.Shell == nil {
		return fmt.Errorf("%s: the page shell is missing", Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authPool, err := env.PG(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", Name, err)
	}
	if err := authPool.Ping(ctx); err != nil {
		env.Log.Warn("web_log: the Auth database does not answer yet", "error", err)
	}

	logPool, err := env.PGDB(ctx, "Log")
	if err != nil {
		return fmt.Errorf("%s: %w", Name, err)
	}
	if err := logPool.Ping(ctx); err != nil {
		env.Log.Warn("web_log: the Log database does not answer yet", "error", err)
	}

	var (
		cache Cache
		live  LiveLogStore
	)
	if env.Cfg.Dash.Redis.Host != "" {
		rdb, err := env.Redis()
		if err != nil {
			return fmt.Errorf("%s: %w", Name, err)
		}
		cache = NewRedisCache(rdb)
		live = NewRedisLiveLog(rdb, set.LiveLogKey)
	} else {
		env.Log.Warn("web_log: redis is not configured; falling back to in-memory cache and disabling live Redis log stream")
		cache = NewMemoryCache(nil)
	}

	svc, err := NewService(Deps{
		Repo:     NewPGLogRepository(logPool),
		Admin:    NewPGAdminChecker(authPool),
		Cache:    cache,
		LiveLogs: live,
		Settings: set,
		Log:      env.Log,
	})
	if err != nil {
		return err
	}
	m.svc = svc
	return svc.Routes(mux, env.Base+"/"+Name, env.Auth.Require, env.Auth.Authenticate, env.Shell)
}

// Close releases the module. Shared PG and Redis pools are owned by module.Env.
func (m *Module) Close() error { return nil }
