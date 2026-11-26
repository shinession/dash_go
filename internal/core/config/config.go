// Package config loads the settings of the dash_go service.
//
// Two layers of JSON files are read, with the same patch convention as the Python dash application:
//
//  1. The shared infrastructure settings of the Python app (config.json + patch_<hostname>.json in
//     DASH_GO_DASH_CONFIG_DIR): postgresql, redis, api, api_server, cdn_data_folder, HC_PROCESS_LIST, ...
//  2. The settings of this service (dash_go.json + dash_go_patch_<hostname>.json in DASH_GO_CONFIG_DIR),
//     which may override any top-level key of layer 1 and hold the per-module settings.
//
// A patch file replaces whole top-level keys of its base file (shallow merge), exactly like the Python loader.
// Secrets can also be injected by environment variables so that they never have to be written to a file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Environment variables understood by Load.
const (
	EnvConfigDir     = "DASH_GO_CONFIG_DIR"
	EnvDashConfigDir = "DASH_GO_DASH_CONFIG_DIR"
	EnvListen        = "DASH_GO_LISTEN"
	EnvPGPassword    = "DASH_GO_PG_PASSWORD"
	EnvRedisPassword = "DASH_GO_REDIS_PASSWORD"
	EnvAPISalt       = "DASH_GO_API_SALT"
	EnvAPISalt2      = "DASH_GO_API_SALT_2"
)

// DefaultPhotoURLTemplate is where the employee pictures are, below the site that serves dash_go (the /api location
// of the Python application). {ee_code} is replaced by the employee code.
const DefaultPhotoURLTemplate = "/api/static/hr/{ee_code}.webp"

// Options controls where Load looks for files. Zero values fall back to environment variables and defaults.
type Options struct {
	ConfigDir     string
	DashConfigDir string
	Host          string              // host name used for the patch file names; defaults to os.Hostname()
	Getenv        func(string) string // defaults to os.Getenv
}

// Postgres holds the connection settings of the PostgreSQL server.
type Postgres struct {
	Host     string
	Port     int
	Username string
	Password string
}

// Redis holds the connection settings of the Redis server.
type Redis struct {
	Host     string
	Port     int
	DB       int
	Password string
}

// Dash holds the infrastructure settings shared with the Python dash application.
type Dash struct {
	Postgres      Postgres
	Redis         Redis
	APISalt       string   // api.salt_3, the secret used by the Python /api/user endpoint
	APISalt2      string   // api.salt_2, the secret of the Python sign-on result endpoint (needed for the Windows sign-in only)
	APIServer     string   // api_server, host name of the Python API
	SSOServer     string   // sso_server, host of the Windows single sign-on service; empty switches that sign-in off
	CDNDataFolder string   // cdn_data_folder, second copy of the change log files
	HCProcessList []string // HC_PROCESS_LIST
	LeaveList     []string // keys of SHIFT_LEAVE_PATTERN from the third one on (same rule as the Python code)
}

// Login holds the settings of the sign-in of dash_go itself (the login page).
type Login struct {
	// Password lets the login page ask for an account and a password (login_password, default true).
	Password bool
	// SSORequireSameIP makes the Windows sign-in refuse a result that the sign-on service recorded for another
	// address than the one that started the sign-in (sso_require_same_ip, default true). It is what stops a sign-in
	// being forced on a victim, so it only goes off for a proxy that cannot pass the address of the browser.
	SSORequireSameIP bool
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header is believed (trusted_proxies, default the
	// loopback addresses: the nginx on the same computer).
	TrustedProxies []netip.Prefix
}

// Update holds the settings of the automatic update (internal/core/update, docs/auto_update.md).
type Update struct {
	// URL is where the release manifest (dash_go.json) is: an http(s) address, or the absolute path of the file or of
	// its folder (update_url). Empty means the service does not update itself, which is the default.
	URL string
	// Interval is the time between two looks at the manifest (update_interval_minutes, default 10).
	Interval time.Duration
	// Restart is how the service starts the new program: "supervise" keeps the process as a launcher, "exit" ends it
	// with the code 75 for a service manager (update_restart, default supervise).
	Restart string
}

// Config is the effective configuration of the service.
type Config struct {
	Listen   string
	BasePath string // URL prefix the service is mounted on, without trailing slash, e.g. /dash_go
	LogDir   string
	DashHome string // URL of the Dash application, shown when a page is opened without a login
	// SessionHours is how long a login stays valid (the session cookie of the sso hand-over).
	SessionHours int
	// PhotoURLTemplate is where the employee pictures are; {ee_code} stands for the employee code.
	PhotoURLTemplate string
	// DashAPIURL is the root of the Python API including /api (dash_api_url, default http://<api_server>/api); empty
	// when neither is set. The login and the permission checks ask it.
	DashAPIURL string
	// DashAPISource is the key DashAPIURL was taken from, "dash_api_url" or "api_server": the log says it, because the
	// api_server of a development machine's patch file may point at the production server.
	DashAPISource string
	// DashDataDir is the data folder of the Python dash app (top-level dash_data_dir), where change log files such as
	// DailyAttnData.log and MQPDHeadCount.log live. A module may still override it under modules.<name>.dash_data_dir.
	DashDataDir string
	Login       Login
	Update        Update
	Dash          Dash
	Sources       []string // configuration files that were loaded (paths only)

	modules map[string]json.RawMessage
}

// Load reads the configuration. A missing Go service file is fine (defaults apply); a missing Python config
// folder is only an error when it was requested explicitly.
func Load(opts Options) (*Config, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	host := strings.ToLower(opts.Host)
	if host == "" {
		h, err := os.Hostname()
		if err == nil {
			host = strings.ToLower(h)
		}
	}
	configDir := firstNonEmpty(opts.ConfigDir, getenv(EnvConfigDir), "config")
	var sources []string

	goCfg, err := loadLayered(configDir, "dash_go", "dash_go_patch_"+host, true, &sources)
	if err != nil {
		return nil, err
	}
	dashDir := firstNonEmpty(opts.DashConfigDir, getenv(EnvDashConfigDir), rawString(goCfg["dash_config_dir"]))
	pyCfg, err := loadLayered(dashDir, "config", "patch_"+host, false, &sources)
	if err != nil {
		return nil, err
	}
	merged := map[string]json.RawMessage{}
	for k, v := range pyCfg {
		merged[k] = v
	}
	for k, v := range goCfg {
		merged[k] = v
	}

	cfg := &Config{
		Listen:           firstNonEmpty(getenv(EnvListen), rawString(merged["listen"]), ":8750"),
		BasePath:         normalizeBasePath(firstNonEmpty(rawString(merged["base_path"]), "/dash_go")),
		LogDir:           firstNonEmpty(rawString(merged["log_dir"]), "logs"),
		DashHome:         firstNonEmpty(rawString(merged["dash_home"]), "/ie/"),
		DashDataDir:      strings.TrimSpace(rawString(merged["dash_data_dir"])),
		SessionHours:     12,
		PhotoURLTemplate: firstNonEmpty(rawString(merged["photo_url_template"]), DefaultPhotoURLTemplate),
		Login:            Login{Password: true, SSORequireSameIP: true},
		Update:           Update{Interval: 10 * time.Minute, Restart: "supervise"},
		Sources:          sources,
		modules:          map[string]json.RawMessage{},
	}
	if err := loadLogin(merged, &cfg.Login); err != nil {
		return nil, err
	}
	if err := loadUpdate(merged, &cfg.Update); err != nil {
		return nil, err
	}
	if raw, ok := merged["session_hours"]; ok {
		var h int
		if err := json.Unmarshal(raw, &h); err != nil || h <= 0 || h > 168 {
			return nil, fmt.Errorf("config key session_hours must be a whole number of hours between 1 and 168")
		}
		cfg.SessionHours = h
	}
	if raw, ok := merged["modules"]; ok {
		if err := json.Unmarshal(raw, &cfg.modules); err != nil {
			return nil, fmt.Errorf("config key modules must be an object: %w", err)
		}
	}

	var pg struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeKey(merged, "postgresql", &pg); err != nil {
		return nil, err
	}
	var rd struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		DB       int    `json:"db"`
		Password string `json:"password"`
	}
	if err := decodeKey(merged, "redis", &rd); err != nil {
		return nil, err
	}
	var api struct {
		Salt2 string `json:"salt_2"`
		Salt3 string `json:"salt_3"`
	}
	if err := decodeKey(merged, "api", &api); err != nil {
		return nil, err
	}
	cfg.Dash = Dash{
		Postgres:      Postgres{Host: pg.Host, Port: pg.Port, Username: pg.Username, Password: firstNonEmpty(getenv(EnvPGPassword), pg.Password)},
		Redis:         Redis{Host: rd.Host, Port: rd.Port, DB: rd.DB, Password: firstNonEmpty(getenv(EnvRedisPassword), rd.Password)},
		APISalt:       firstNonEmpty(getenv(EnvAPISalt), api.Salt3),
		APISalt2:      firstNonEmpty(getenv(EnvAPISalt2), api.Salt2),
		APIServer:     rawString(merged["api_server"]),
		SSOServer:     strings.TrimSpace(rawString(merged["sso_server"])),
		CDNDataFolder: rawString(merged["cdn_data_folder"]),
	}
	if u := strings.TrimSpace(rawString(merged["dash_api_url"])); u != "" {
		cfg.DashAPIURL, cfg.DashAPISource = u, "dash_api_url"
	} else if cfg.Dash.APIServer != "" {
		cfg.DashAPIURL, cfg.DashAPISource = "http://"+cfg.Dash.APIServer+"/api", "api_server"
	}
	if cfg.Dash.Postgres.Port == 0 {
		cfg.Dash.Postgres.Port = 5432
	}
	if cfg.Dash.Redis.Port == 0 {
		cfg.Dash.Redis.Port = 6379
	}
	if err := decodeKey(merged, "HC_PROCESS_LIST", &cfg.Dash.HCProcessList); err != nil {
		return nil, err
	}
	if raw, ok := merged["SHIFT_LEAVE_PATTERN"]; ok {
		keys, err := objectKeys(raw)
		if err != nil {
			return nil, fmt.Errorf("config key SHIFT_LEAVE_PATTERN: %w", err)
		}
		if len(keys) > 2 {
			cfg.Dash.LeaveList = keys[2:] // keys 1 and 2 are AM and PM, the rest are the leave options
		}
	}
	return cfg, nil
}

// Module decodes the settings of one module into out. Fields of out that are absent from the file keep the
// values the caller pre-filled (the module defaults).
func (c *Config) Module(name string, out any) error {
	raw, ok := c.modules[name]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("config modules.%s: %w", name, err)
	}
	return nil
}

// loadLogin reads login_password, sso_require_same_ip and trusted_proxies. The defaults are in l already.
func loadLogin(merged map[string]json.RawMessage, l *Login) error {
	for key, dst := range map[string]*bool{"login_password": &l.Password, "sso_require_same_ip": &l.SSORequireSameIP} {
		if err := decodeKey(merged, key, dst); err != nil {
			return fmt.Errorf("%w (true or false)", err)
		}
	}
	var proxies []string
	if err := decodeKey(merged, "trusted_proxies", &proxies); err != nil {
		return fmt.Errorf("%w (a list of addresses or CIDR ranges)", err)
	}
	if _, set := merged["trusted_proxies"]; !set {
		proxies = []string{"127.0.0.1", "::1"}
	}
	for _, p := range proxies {
		prefix, err := parseProxy(p)
		if err != nil {
			return fmt.Errorf("config key trusted_proxies: %w", err)
		}
		l.TrustedProxies = append(l.TrustedProxies, prefix)
	}
	return nil
}

// loadUpdate reads update_url, update_interval_minutes and update_restart. The defaults are in u already.
func loadUpdate(merged map[string]json.RawMessage, u *Update) error {
	var address string
	if err := decodeKey(merged, "update_url", &address); err != nil {
		return fmt.Errorf("%w (an address or a path in quotes)", err)
	}
	address, err := parseUpdateURL(address)
	if err != nil {
		return fmt.Errorf("config key update_url %w", err)
	}
	u.URL = address

	minutes := int(u.Interval / time.Minute)
	if err := decodeKey(merged, "update_interval_minutes", &minutes); err != nil {
		return fmt.Errorf("%w (a whole number of minutes)", err)
	}
	if minutes < 1 || minutes > 24*60 {
		return errors.New("config key update_interval_minutes must be a whole number of minutes between 1 and 1440")
	}
	u.Interval = time.Duration(minutes) * time.Minute

	restart := u.Restart
	if err := decodeKey(merged, "update_restart", &restart); err != nil {
		return fmt.Errorf("%w (supervise or exit)", err)
	}
	switch restart = strings.ToLower(strings.TrimSpace(restart)); restart {
	case "supervise", "exit":
		u.Restart = restart
	default:
		return errors.New(`config key update_restart must be "supervise" or "exit"`)
	}
	return nil
}

// parseUpdateURL checks the address of the release manifest: an http(s) address with no user name, password, query or
// fragment (the address is written to the log), or the absolute path of the manifest or of its folder. The error text
// starts with a verb, so a caller can put the name of the setting in front of it.
func parseUpdateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if l := strings.ToLower(raw); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		u, err := url.Parse(raw)
		switch {
		case err != nil || u.Hostname() == "":
			return "", errors.New("is not a valid address; write it as http://host/path/")
		case u.User != nil:
			return "", errors.New("must not contain a user name or password")
		case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
			return "", errors.New("must not have a query or a fragment")
		}
		return raw, nil
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New(`must be an http(s) address or an absolute path, such as http://localhost/api/static/cd/dash_go.json or D:\dash_go_release`)
	}
	return raw, nil
}

// parseProxy reads an address (10.0.0.5, ::1) or a range (10.0.0.0/8). An IPv4 address that is mapped into IPv6
// counts as the IPv4 address.
func parseProxy(text string) (netip.Prefix, error) {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "/") {
		p, err := netip.ParsePrefix(text)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not an address range", text)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(text)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an address", text)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func loadLayered(dir, base, patch string, optional bool, sources *[]string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if dir == "" {
		return out, nil
	}
	for i, name := range []string{base, patch} {
		path := filepath.Join(dir, name+".json")
		b, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				if i == 0 && !optional {
					return nil, fmt.Errorf("configuration file not found: %s", path)
				}
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &m); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for k, v := range m {
			out[k] = v
		}
		*sources = append(*sources, path)
	}
	return out, nil
}

func decodeKey(m map[string]json.RawMessage, key string, out any) error {
	raw, ok := m[key]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("config key %s: %w", key, err)
	}
	return nil
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

// objectKeys returns the keys of a JSON object in file order (encoding/json maps lose the order).
func objectKeys(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var keys []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, errors.New("unexpected token")
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	}
	return keys, nil
}

func normalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	p = "/" + strings.Trim(p, "/")
	if p == "/" {
		return ""
	}
	return p
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Addr returns host:port for a Postgres or Redis setting.
func Addr(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}
