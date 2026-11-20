// Command dash_go is the Go side of the dash application. It hosts the modules that were compiled in (see
// internal/core/module and scripts/build.ps1).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
	"dash_go/internal/core/dashapi"
	"dash_go/internal/core/logx"
	"dash_go/internal/core/module"
	"dash_go/internal/core/server"
	"dash_go/internal/core/shell"
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
	authSet, err := authSettings(cfg, log)
	if err != nil {
		log.Error("cannot start", "error", err)
		return 2
	}
	env.Auth = auth.NewService(signer, nonces, authSet)

	names := module.Names()
	if len(names) == 0 {
		log.Error("cannot start: no module is compiled into this binary")
		return 2
	}
	var mods []module.Module
	var links []shell.ModuleLink
	for _, name := range names {
		m, err := module.New(name)
		if err != nil {
			log.Error("cannot start module", "module", name, "error", err)
			return 2
		}
		defer m.Close()
		i := m.Info()
		mods = append(mods, m)
		links = append(links, shell.ModuleLink{Name: i.Name, Title: i.Title, TitleKey: i.TitleKey, Description: i.Description, DescriptionKey: i.DescriptionKey, Version: i.Version, Href: cfg.BasePath + "/" + i.Name + "/"})
	}
	env.Shell, err = shell.New(shell.Options{Base: cfg.BasePath, Version: version.Version, PhotoURLTemplate: cfg.PhotoURLTemplate, DashHome: cfg.DashHome, Auth: env.Auth, Modules: links, Log: log})
	if err != nil {
		log.Error("cannot start", "error", err)
		return 2
	}

	mux := http.NewServeMux()
	env.Auth.Mount(mux)
	env.Shell.Mount(mux)
	for _, m := range mods {
		if err := m.Mount(env, mux); err != nil {
			log.Error("cannot start module", "module", m.Info().Name, "error", err)
			return 2
		}
		log.Info("module ready", "module", m.Info().Name, "version", m.Info().Version)
	}
	server.MountCommon(mux, cfg.BasePath)

	if *check {
		fmt.Printf("configuration ok: %d module(s) ready (%s), base path %q, password login %t, windows login %t\n",
			len(mods), strings.Join(names, ", "), cfg.BasePath, env.Auth.PasswordEnabled(), env.Auth.SSOEnabled())
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
	log.Info("dash_go started", "version", version.Version, "listen", cfg.Listen, "base_path", cfg.BasePath, "modules", strings.Join(names, ","), "config_files", len(cfg.Sources),
		"password_login", env.Auth.PasswordEnabled(), "windows_login", env.Auth.SSOEnabled())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "error", err)
		return 1
	}
	log.Info("dash_go stopped")
	return 0
}

// authSettings builds the sign-in of dash_go itself from the configuration. The hand-over from Dash needs nothing
// but the shared secret; the login page needs the Python API (api_server or dash_api_url) to check accounts, and the
// Windows sign-in also the address of the sign-on service (sso_server) and the secret api.salt_2.
func authSettings(cfg *config.Config, log *slog.Logger) (auth.Settings, error) {
	set := auth.Settings{
		BasePath: cfg.BasePath, Log: log, SessionTTL: time.Duration(cfg.SessionHours) * time.Hour,
		SSORequireSameIP: cfg.Login.SSORequireSameIP, TrustedProxies: cfg.Login.TrustedProxies,
	}
	if cfg.DashAPIURL == "" {
		log.Warn("the login page is switched off: neither api_server nor dash_api_url is set, so accounts cannot be checked; people can still open the page from the Dash application")
		return set, nil
	}
	set.Directory = auth.NewDashDirectory(dashapi.New(cfg.DashAPIURL, cfg.Dash.APISalt, 5*time.Second))
	set.PasswordLogin = cfg.Login.Password
	if cfg.Dash.SSOServer != "" {
		sso, err := dashapi.NewSSO(cfg.Dash.SSOServer, cfg.Dash.APISalt2, 10*time.Second)
		if err != nil {
			return set, fmt.Errorf("the Windows sign-in (sso_server) is not set up correctly: %w", err)
		}
		set.SSO = auth.NewDashSSO(sso)
	}
	return set, nil
}
