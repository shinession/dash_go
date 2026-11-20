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
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
	"dash_go/internal/core/dashapi"
	"dash_go/internal/core/logx"
	"dash_go/internal/core/module"
	"dash_go/internal/core/server"
	"dash_go/internal/core/shell"
	"dash_go/internal/core/update"
	"dash_go/internal/version"
)

func main() { os.Exit(run()) }

func run() int {
	// The path of the program is taken before anything else: after an update the file has been renamed, and Windows
	// goes on reporting the path the process was started from, which is where the new program is.
	exe, exeErr := os.Executable()
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

	code, updated := serve(cfg, log, *check, exe, exeErr)
	if updated == nil {
		return code
	}
	return restart(cfg, log, updated, exe)
}

// restart starts the new program after an update. It runs once serve has returned, so everything the service opened is
// closed and the port is free.
func restart(cfg *config.Config, log *slog.Logger, up *update.Update, exe string) int {
	if update.Decide(cfg.Update.Restart, update.Supervised()) == update.RestartExit {
		log.Info("exiting so that the new version is started", "code", update.ExitRestart, "to", up.To)
		return update.ExitRestart
	}
	log.Info("starting the new version", "to", up.To)
	return update.Supervise(exe, os.Args[1:], nil, log)
}

// serve runs the service until it is stopped, or only checks it when checkOnly is set. When it stopped for an update
// it returns the update, which is already in place: the caller starts the new program.
func serve(cfg *config.Config, log *slog.Logger, checkOnly bool, exe string, exeErr error) (int, *update.Update) {
	signer, err := auth.NewSigner(cfg.Dash.APISalt)
	if err != nil {
		log.Error("cannot start", "error", err)
		return 2, nil
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
		return 2, nil
	}
	env.Auth = auth.NewService(signer, nonces, authSet)

	names := module.Names()
	if len(names) == 0 {
		log.Error("cannot start: no module is compiled into this binary")
		return 2, nil
	}
	var mods []module.Module
	var links []shell.ModuleLink
	for _, name := range names {
		m, err := module.New(name)
		if err != nil {
			log.Error("cannot start module", "module", name, "error", err)
			return 2, nil
		}
		defer m.Close()
		i := m.Info()
		mods = append(mods, m)
		links = append(links, shell.ModuleLink{Name: i.Name, Title: i.Title, TitleKey: i.TitleKey, Description: i.Description, DescriptionKey: i.DescriptionKey, Version: i.Version, Href: cfg.BasePath + "/" + i.Name + "/"})
	}
	env.Shell, err = shell.New(shell.Options{Base: cfg.BasePath, Version: version.Version, PhotoURLTemplate: cfg.PhotoURLTemplate, DashHome: cfg.DashHome, Auth: env.Auth, Modules: links, Log: log})
	if err != nil {
		log.Error("cannot start", "error", err)
		return 2, nil
	}

	mux := http.NewServeMux()
	env.Auth.Mount(mux)
	env.Shell.Mount(mux)
	for _, m := range mods {
		if err := m.Mount(env, mux); err != nil {
			log.Error("cannot start module", "module", m.Info().Name, "error", err)
			return 2, nil
		}
		log.Info("module ready", "module", m.Info().Name, "version", m.Info().Version)
	}
	server.MountCommon(mux, cfg.BasePath)

	if checkOnly {
		fmt.Printf("configuration ok: %d module(s) ready (%s), base path %q, password login %t, windows login %t, automatic update %s\n",
			len(mods), strings.Join(names, ", "), cfg.BasePath, env.Auth.PasswordEnabled(), env.Auth.SSOEnabled(), describeUpdate(cfg.Update))
		return 0, nil
	}
	if m, ok := update.TakeMarker(exe); ok {
		log.Info("the program was updated", "from", m.From, "to", m.To, "waited_seconds", m.WaitedSeconds, "updated_at", m.At.Format(time.RFC3339))
	}

	gate := server.NewGate()
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.Handler(gate.Wrap(mux), log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	// signalled ends with Ctrl+C; ctx also ends when the launcher of this program is gone or an update is in place.
	signalled, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	ctx, stop := context.WithCancel(signalled)
	defer stop()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if update.Supervised() {
		go update.StopWithLauncher(os.Stdin, stop, log)
	}
	var updated atomic.Pointer[update.Update]
	if cfg.Update.URL != "" {
		startUpdater(ctx, cfg, log, exe, exeErr, updateGate{gate: gate, auth: env.Auth}, func(up *update.Update) {
			updated.Store(up)
			stop()
		})
	}
	log.Info("dash_go started", "version", version.Version, "listen", cfg.Listen, "base_path", cfg.BasePath, "modules", strings.Join(names, ","), "config_files", len(cfg.Sources),
		"password_login", env.Auth.PasswordEnabled(), "windows_login", env.Auth.SSOEnabled(), "automatic_update", describeUpdate(cfg.Update))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "error", err)
		return 1, nil
	}
	<-stopped // the requests that were served when the service was asked to stop are through
	log.Info("dash_go stopped")
	if up := updated.Load(); up != nil && signalled.Err() == nil {
		return 0, up
	}
	return 0, nil
}

// startUpdater starts the automatic update. A problem with it is logged and the service runs without it: the service
// matters more than its way of updating. onUpdate is called once a new program has been put in place.
func startUpdater(ctx context.Context, cfg *config.Config, log *slog.Logger, exe string, exeErr error, gate updateGate, onUpdate func(*update.Update)) {
	if exeErr != nil {
		log.Error("automatic update is off: the path of this program is not known", "error", exeErr)
		return
	}
	if err := folderWritable(exe); err != nil {
		log.Warn("automatic update cannot put a new program in the folder of this one, so updates will fail until the folder can be written to", "folder", filepath.Dir(exe), "error", err)
	}
	u, err := newUpdater(cfg, log, exe, gate)
	if err != nil {
		log.Error("automatic update is off", "error", err)
		return
	}
	go func() {
		if up := u.Run(ctx); up != nil {
			onUpdate(up)
		}
	}()
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
	api := dashapi.New(cfg.DashAPIURL, cfg.Dash.APISalt, 5*time.Second)
	log.Info("the Dash API of the sign-in", "url", api.Root(), "from", cfg.DashAPISource)
	set.Directory = auth.NewDashDirectory(api)
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
