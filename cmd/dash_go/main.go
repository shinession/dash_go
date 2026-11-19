// Command dash_go is the Go side of the dash application. It hosts the modules that were compiled in (see
// internal/core/module and scripts/build.ps1).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
	"dash_go/internal/core/logx"
	"dash_go/internal/core/module"
	"dash_go/internal/core/server"
	"dash_go/internal/version"
)

func main() { os.Exit(run()) }

func run() int {
	var (
		configDir     = flag.String("config-dir", "", "folder of dash_go.json (default: "+config.EnvConfigDir+" or ./config)")
		dashConfigDir = flag.String("dash-config-dir", "", "config folder of the Python dash app (default: "+config.EnvDashConfigDir+")")
		listen        = flag.String("listen", "", "listen address, for example :8750 (default: "+config.EnvListen+" or the config file)")
		showVersion   = flag.Bool("version", false, "print the version and the compiled-in modules, then exit")
		check         = flag.Bool("check", false, "load the configuration and the modules, print the result and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("dash_go %s\nmodules: %s\n", version.Version, strings.Join(module.Names(), ", "))
		return 0
	}
	cfg, err := config.Load(config.Options{ConfigDir: *configDir, DashConfigDir: *dashConfigDir})
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	log := logx.New(cfg.LogDir)

	signer, err := auth.NewSigner(cfg.Dash.APISalt)
	if err != nil {
		log.Error("cannot start", "error", err)
		return 2
	}
	env := module.NewEnv(cfg, log, nil)
	defer env.Close()
	var nonces auth.NonceStore = auth.NewMemoryNonces()
	if cfg.Dash.Redis.Host != "" {
		if rdb, err := env.Redis(); err == nil {
			nonces = auth.NewRedisNonces(rdb)
		}
	}
	env.Auth = auth.NewService(signer, nonces, auth.Settings{BasePath: cfg.BasePath, Log: log, DashHome: cfg.DashHome, SessionTTL: time.Duration(cfg.SessionHours) * time.Hour})

	names := module.Names()
	if len(names) == 0 {
		log.Error("cannot start: no module is compiled into this binary")
		return 2
	}
	mux := http.NewServeMux()
	env.Auth.Mount(mux)
	var mods []module.Module
	for _, name := range names {
		m, err := module.New(name)
		if err == nil {
			err = m.Mount(env, mux)
		}
		if err != nil {
			log.Error("cannot start module", "module", name, "error", err)
			return 2
		}
		defer m.Close()
		mods = append(mods, m)
		log.Info("module ready", "module", name, "version", m.Info().Version)
	}
	server.MountCommon(mux, cfg.BasePath, mods)

	if *check {
		fmt.Printf("configuration ok: %d module(s) ready (%s), base path %q\n", len(mods), strings.Join(names, ", "), cfg.BasePath)
		return 0
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.Handler(mux, log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("dash_go started", "version", version.Version, "listen", cfg.Listen, "base_path", cfg.BasePath, "modules", strings.Join(names, ","), "config_files", len(cfg.Sources))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "error", err)
		return 1
	}
	log.Info("dash_go stopped")
	return 0
}
