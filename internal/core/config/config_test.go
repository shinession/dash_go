package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func noEnv(string) string { return "" }

func TestLoadMergesPythonConfigPatchAndGoConfig(t *testing.T) {
	py, goDir := t.TempDir(), t.TempDir()
	write(t, py, "config.json", `{
		"postgresql": {"host": "base-host", "port": 5432, "username": "u", "password": "base-pw"},
		"redis": {"host": "r1", "port": 6380, "db": 2, "password": "rp"},
		"api": {"salt_1": "a", "salt_3": "s3"},
		"api_server": "base-api", "cdn_data_folder": "\\\\cdn\\data",
		"HC_PROCESS_LIST": ["Slitter", "Winder"],
		"SHIFT_LEAVE_PATTERN": {"AM": 1, "PM": 1, "Delete Record": 0, "ANL - Annual Leave": 0, "MCL - Medical Leave": 0}
	}`)
	write(t, py, "patch_hostx.json", `{"postgresql": {"host": "patch-host", "username": "u2", "password": "patch-pw"}, "api_server": "patch-api"}`)
	write(t, goDir, "dash_go.json", `{"listen": ":9000", "base_path": "dash_go/", "modules": {"hc_scan": {"recent_days": 5}}}`)

	cfg, err := Load(Options{ConfigDir: goDir, DashConfigDir: py, Host: "HostX", Getenv: noEnv})
	if err != nil {
		t.Fatal(err)
	}
	// a patch replaces whole top-level keys (shallow merge), so the port falls back to the default 5432
	if cfg.Dash.Postgres.Host != "patch-host" || cfg.Dash.Postgres.Username != "u2" || cfg.Dash.Postgres.Password != "patch-pw" || cfg.Dash.Postgres.Port != 5432 {
		t.Errorf("postgres: %+v", cfg.Dash.Postgres)
	}
	if cfg.Dash.Redis.Port != 6380 || cfg.Dash.Redis.DB != 2 || cfg.Dash.APISalt != "s3" || cfg.Dash.APIServer != "patch-api" {
		t.Errorf("redis/api: %+v %q %q", cfg.Dash.Redis, cfg.Dash.APISalt, cfg.Dash.APIServer)
	}
	if cfg.Listen != ":9000" || cfg.BasePath != "/dash_go" || cfg.DashHome != "/ie/" || cfg.LogDir != "logs" {
		t.Errorf("service settings: %q %q %q %q", cfg.Listen, cfg.BasePath, cfg.DashHome, cfg.LogDir)
	}
	if !reflect.DeepEqual(cfg.Dash.HCProcessList, []string{"Slitter", "Winder"}) {
		t.Errorf("process list: %v", cfg.Dash.HCProcessList)
	}
	// keys 1 and 2 (AM, PM) are work, the rest are leave options in file order, "Delete Record" included
	if want := []string{"Delete Record", "ANL - Annual Leave", "MCL - Medical Leave"}; !reflect.DeepEqual(cfg.Dash.LeaveList, want) {
		t.Errorf("leave list: %v", cfg.Dash.LeaveList)
	}
	var set struct {
		RecentDays int    `json:"recent_days"`
		Keep       string `json:"keep"`
	}
	set.RecentDays, set.Keep = 7, "default"
	if err := cfg.Module("hc_scan", &set); err != nil || set.RecentDays != 5 || set.Keep != "default" {
		t.Errorf("module settings: %+v %v", set, err)
	}
	if err := cfg.Module("absent", &set); err != nil || set.RecentDays != 5 {
		t.Errorf("an absent module keeps the defaults: %+v %v", set, err)
	}
	if len(cfg.Sources) != 3 {
		t.Errorf("sources: %v", cfg.Sources)
	}
}

func TestEnvironmentOverridesSecretsAndPaths(t *testing.T) {
	py := t.TempDir()
	write(t, py, "config.json", `{"postgresql": {"host": "h", "username": "u", "password": "file-pw"}, "redis": {"host": "r", "password": "file-rp"}, "api": {"salt_3": "file-salt"}}`)
	env := map[string]string{EnvDashConfigDir: py, EnvConfigDir: t.TempDir(), EnvListen: ":1234", EnvPGPassword: "env-pw", EnvRedisPassword: "env-rp", EnvAPISalt: "env-salt"}
	cfg, err := Load(Options{Host: "x", Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":1234" || cfg.Dash.Postgres.Password != "env-pw" || cfg.Dash.Redis.Password != "env-rp" || cfg.Dash.APISalt != "env-salt" {
		t.Errorf("environment values must win: %+v %+v", cfg.Dash.Postgres, cfg.Dash.Redis)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(Options{ConfigDir: t.TempDir(), DashConfigDir: t.TempDir(), Host: "x", Getenv: noEnv}); err == nil {
		t.Error("a requested Dash config folder without config.json must fail")
	}
	bad := t.TempDir()
	write(t, bad, "dash_go.json", `{not json`)
	if _, err := Load(Options{ConfigDir: bad, Host: "x", Getenv: noEnv}); err == nil {
		t.Error("invalid JSON must fail")
	}
	// no files at all is fine: defaults apply
	cfg, err := Load(Options{ConfigDir: t.TempDir(), Host: "x", Getenv: noEnv})
	if err != nil || cfg.Listen != ":8750" || cfg.BasePath != "/dash_go" {
		t.Errorf("defaults: %+v %v", cfg, err)
	}
}

func TestNormalizeBasePath(t *testing.T) {
	for in, want := range map[string]string{"/dash_go": "/dash_go", "dash_go/": "/dash_go", " /a/b/ ": "/a/b", "/": "", "": ""} {
		if got := normalizeBasePath(in); got != want {
			t.Errorf("normalizeBasePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionHours(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv})
	if err != nil || cfg.SessionHours != 12 {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	write(t, dir, "dash_go.json", `{"session_hours": 8}`)
	if cfg, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv}); err != nil || cfg.SessionHours != 8 {
		t.Fatalf("configured: %+v %v", cfg, err)
	}
	for _, bad := range []string{`{"session_hours": 0}`, `{"session_hours": 1000}`, `{"session_hours": "x"}`} {
		write(t, dir, "dash_go.json", bad)
		if _, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv}); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}
