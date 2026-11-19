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
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Environment variables understood by Load.
const (
	EnvConfigDir     = "DASH_GO_CONFIG_DIR"
	EnvDashConfigDir = "DASH_GO_DASH_CONFIG_DIR"
	EnvListen        = "DASH_GO_LISTEN"
	EnvPGPassword    = "DASH_GO_PG_PASSWORD"
	EnvRedisPassword = "DASH_GO_REDIS_PASSWORD"
	EnvAPISalt       = "DASH_GO_API_SALT"
)

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
	APIServer     string   // api_server, host name of the Python API
	CDNDataFolder string   // cdn_data_folder, second copy of the change log files
	HCProcessList []string // HC_PROCESS_LIST
	LeaveList     []string // keys of SHIFT_LEAVE_PATTERN from the third one on (same rule as the Python code)
}

// Config is the effective configuration of the service.
type Config struct {
	Listen   string
	BasePath string // URL prefix the service is mounted on, without trailing slash, e.g. /dash_go
	LogDir   string
	DashHome string // URL of the Dash application, shown when a page is opened without a login
	// SessionHours is how long a login stays valid (the session cookie of the sso hand-over).
	SessionHours int
	Dash         Dash
	Sources      []string // configuration files that were loaded (paths only)

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
		Listen:       firstNonEmpty(getenv(EnvListen), rawString(merged["listen"]), ":8750"),
		BasePath:     normalizeBasePath(firstNonEmpty(rawString(merged["base_path"]), "/dash_go")),
		LogDir:       firstNonEmpty(rawString(merged["log_dir"]), "logs"),
		DashHome:     firstNonEmpty(rawString(merged["dash_home"]), "/ie/"),
		SessionHours: 12,
		Sources:      sources,
		modules:      map[string]json.RawMessage{},
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
		Salt3 string `json:"salt_3"`
	}
	if err := decodeKey(merged, "api", &api); err != nil {
		return nil, err
	}
	cfg.Dash = Dash{
		Postgres:      Postgres{Host: pg.Host, Port: pg.Port, Username: pg.Username, Password: firstNonEmpty(getenv(EnvPGPassword), pg.Password)},
		Redis:         Redis{Host: rd.Host, Port: rd.Port, DB: rd.DB, Password: firstNonEmpty(getenv(EnvRedisPassword), rd.Password)},
		APISalt:       firstNonEmpty(getenv(EnvAPISalt), api.Salt3),
		APIServer:     rawString(merged["api_server"]),
		CDNDataFolder: rawString(merged["cdn_data_folder"]),
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
