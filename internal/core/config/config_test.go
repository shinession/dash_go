package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestLoginAndPhotoDefaults(t *testing.T) {
	cfg, err := Load(Options{ConfigDir: t.TempDir(), Host: "x", Getenv: noEnv})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhotoURLTemplate != "/api/static/hr/{ee_code}.webp" {
		t.Errorf("photo: %q", cfg.PhotoURLTemplate)
	}
	if !cfg.Login.Password || !cfg.Login.SSORequireSameIP {
		t.Errorf("password sign-in and the address check are on by default: %+v", cfg.Login)
	}
	want := []string{"127.0.0.1/32", "::1/128"}
	var got []string
	for _, p := range cfg.Login.TrustedProxies {
		got = append(got, p.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("only the loopback proxies are trusted by default: %v", got)
	}
	if cfg.Dash.SSOServer != "" {
		t.Errorf("the Windows sign-in is off without sso_server: %q", cfg.Dash.SSOServer)
	}
}

func TestLoginAndSSOSettings(t *testing.T) {
	py, goDir := t.TempDir(), t.TempDir()
	write(t, py, "config.json", `{"api": {"salt_2": "file-two", "salt_3": "s3"}, "sso_server": " prod_eng.co.murata.local "}`)
	write(t, goDir, "dash_go.json", `{
		"photo_url_template": "/pics/{ee_code}.jpg",
		"login_password": false, "sso_require_same_ip": false,
		"trusted_proxies": ["10.1.0.0/16", "10.2.3.4", "::ffff:10.9.9.9", "fd00::/8"]
	}`)
	cfg, err := Load(Options{ConfigDir: goDir, DashConfigDir: py, Host: "x", Getenv: noEnv})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhotoURLTemplate != "/pics/{ee_code}.jpg" || cfg.Login.Password || cfg.Login.SSORequireSameIP {
		t.Errorf("%q %+v", cfg.PhotoURLTemplate, cfg.Login)
	}
	if cfg.Dash.SSOServer != "prod_eng.co.murata.local" || cfg.Dash.APISalt2 != "file-two" {
		t.Errorf("sso: %q %q", cfg.Dash.SSOServer, cfg.Dash.APISalt2)
	}
	var got []string
	for _, p := range cfg.Login.TrustedProxies {
		got = append(got, p.String())
	}
	if want := []string{"10.1.0.0/16", "10.2.3.4/32", "10.9.9.9/32", "fd00::/8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("proxies: %v", got)
	}

	// an explicit empty list means no proxy is trusted
	write(t, goDir, "dash_go.json", `{"trusted_proxies": []}`)
	cfg, err = Load(Options{ConfigDir: goDir, DashConfigDir: py, Host: "x", Getenv: noEnv})
	if err != nil || len(cfg.Login.TrustedProxies) != 0 {
		t.Errorf("an empty list trusts nobody: %v %v", cfg.Login.TrustedProxies, err)
	}

	// the secret can come from the environment, so it need not be written to a file
	env := map[string]string{EnvAPISalt2: "env-two"}
	cfg, err = Load(Options{ConfigDir: goDir, DashConfigDir: py, Host: "x", Getenv: func(k string) string { return env[k] }})
	if err != nil || cfg.Dash.APISalt2 != "env-two" {
		t.Errorf("environment must win: %q %v", cfg.Dash.APISalt2, err)
	}
}

func TestDashAPIURL(t *testing.T) {
	py, goDir := t.TempDir(), t.TempDir()
	write(t, py, "config.json", `{"api_server": "api.example.local"}`)
	cfg, err := Load(Options{ConfigDir: goDir, DashConfigDir: py, Host: "x", Getenv: noEnv})
	if err != nil || cfg.DashAPIURL != "http://api.example.local/api" || cfg.DashAPISource != "api_server" {
		t.Errorf("derived from api_server: %q from %q %v", cfg.DashAPIURL, cfg.DashAPISource, err)
	}
	// the patch file of a host replaces api_server, and dash_go.json replaces both
	write(t, py, "patch_x.json", `{"api_server": "production.example.local"}`)
	cfg, err = Load(Options{ConfigDir: goDir, DashConfigDir: py, Host: "x", Getenv: noEnv})
	if err != nil || cfg.DashAPIURL != "http://production.example.local/api" || cfg.DashAPISource != "api_server" {
		t.Errorf("patched api_server: %q from %q %v", cfg.DashAPIURL, cfg.DashAPISource, err)
	}
	write(t, goDir, "dash_go.json", `{"dash_api_url": " http://127.0.0.1:9999/api "}`)
	cfg, err = Load(Options{ConfigDir: goDir, DashConfigDir: py, Host: "x", Getenv: noEnv})
	if err != nil || cfg.DashAPIURL != "http://127.0.0.1:9999/api" || cfg.DashAPISource != "dash_api_url" {
		t.Errorf("explicit: %q from %q %v", cfg.DashAPIURL, cfg.DashAPISource, err)
	}
	cfg, err = Load(Options{ConfigDir: t.TempDir(), Host: "x", Getenv: noEnv})
	if err != nil || cfg.DashAPIURL != "" || cfg.DashAPISource != "" {
		t.Errorf("none: %q from %q %v", cfg.DashAPIURL, cfg.DashAPISource, err)
	}
}

func TestLoginSettingsErrors(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{
		`{"login_password": "yes"}`,
		`{"sso_require_same_ip": 1}`,
		`{"trusted_proxies": "10.0.0.1"}`,
		`{"trusted_proxies": ["not-an-address"]}`,
		`{"trusted_proxies": ["10.0.0.0/99"]}`,
	} {
		write(t, dir, "dash_go.json", bad)
		if _, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv}); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestUpdateIsOffByDefaultAndTakesItsSettingsFromTheFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Update.URL != "" || cfg.Update.Interval != 10*time.Minute || cfg.Update.Restart != "supervise" {
		t.Errorf("a service does not update itself unless it is told where the release is: %+v", cfg.Update)
	}

	folder := filepath.Join(t.TempDir(), "release")
	for _, address := range []string{"http://localhost/api/static/cd/dash_go.json", "https://host/rel/", folder} {
		write(t, dir, "dash_go.json", `{"update_url": `+strconv.Quote(" "+address+" ")+`, "update_interval_minutes": 30, "update_restart": " EXIT "}`)
		cfg, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv})
		if err != nil {
			t.Fatalf("%s: %v", address, err)
		}
		if cfg.Update.URL != address || cfg.Update.Interval != 30*time.Minute || cfg.Update.Restart != "exit" {
			t.Errorf("%s: %+v", address, cfg.Update)
		}
	}

	// a patch file of a host turns it on for that host only
	write(t, dir, "dash_go.json", `{"update_interval_minutes": 5}`)
	write(t, dir, "dash_go_patch_s2.json", `{"update_url": "http://localhost/api/static/cd/dash_go.json"}`)
	cfg, err = Load(Options{ConfigDir: dir, Host: "s2", Getenv: noEnv})
	if err != nil || cfg.Update.URL == "" || cfg.Update.Interval != 5*time.Minute {
		t.Errorf("the host that has the patch: %+v %v", cfg.Update, err)
	}
	cfg, err = Load(Options{ConfigDir: dir, Host: "dev", Getenv: noEnv})
	if err != nil || cfg.Update.URL != "" {
		t.Errorf("another host: %+v %v", cfg.Update, err)
	}
}

func TestUpdateSettingsErrors(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{
		`{"update_url": 5}`,
		`{"update_url": "relative/path.json"}`,
		`{"update_url": "ftp://host/dash_go.json"}`,
		`{"update_url": "http://user:secret@host/rel/"}`,
		`{"update_url": "http://host/rel/?key=1"}`,
		`{"update_url": "http://host/rel/#top"}`,
		`{"update_url": "http:///rel/"}`,
		`{"update_interval_minutes": 0}`,
		`{"update_interval_minutes": 1441}`,
		`{"update_interval_minutes": 1.5}`,
		`{"update_interval_minutes": "ten"}`,
		`{"update_restart": "reboot"}`,
		`{"update_restart": 1}`,
	} {
		write(t, dir, "dash_go.json", bad)
		_, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv})
		if err == nil {
			t.Errorf("%s must be refused", bad)
			continue
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("the error shows the password: %v", err)
		}
	}
}

func TestSessionHours(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv})
	if err != nil || cfg.SessionHours != 24 {
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

func TestDashDataDirTopLevel(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv})
	if err != nil || cfg.DashDataDir != "" {
		t.Fatalf("default: %q %v", cfg.DashDataDir, err)
	}
	write(t, dir, "dash_go.json", `{"dash_data_dir": " C:\\dash\\data "}`)
	cfg, err = Load(Options{ConfigDir: dir, Host: "x", Getenv: noEnv})
	if err != nil || cfg.DashDataDir != `C:\dash\data` {
		t.Fatalf("configured: %q %v", cfg.DashDataDir, err)
	}
}
