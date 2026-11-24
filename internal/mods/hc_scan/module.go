// Package hcscan is the "HC scan badge to auto assign machine/activity" module.
//
// A shift leader opens the page from the Dash menu (MQPD > Head Count), picks a process and scans the employee
// badges. Every scan assigns the person to a machine by the MTR proficiency records (at least Lv1, Lv3 before Lv2
// before Lv1, machines worked in the last days first within a level). The result is only a draft: the shift
// leader checks and changes it and presses Confirm, which writes t_daily_attn_data like the Area Assign page.
package hcscan

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"dash_go/internal/core/dashapi"
	"dash_go/internal/core/i18n"
	"dash_go/internal/core/module"
)

// Name is the module name used in the URL, the build script and the configuration.
const Name = "hc_scan"

// ModuleVersion is the version of this module.
const ModuleVersion = "0.1.8"

func init() {
	i18n.Register(webFS, "i18n") // the texts of the page, the warnings and the messages, in English and Chinese
	module.Register(Name, func() module.Module { return &Module{} })
}

// Module implements module.Module and module.Checker.
type Module struct {
	svc *Service
	api *dashapi.Client // the backend of the permission check, kept for dash_go -check
}

var _ module.Checker = (*Module)(nil)

// Info describes the module.
func (m *Module) Info() module.Info {
	return module.Info{
		Name:           Name,
		Title:          "HC Scan Assign",
		TitleKey:       "hc.title",
		Version:        ModuleVersion,
		Description:    "Scan employee badges to assign machines and activities by MTR level",
		DescriptionKey: "hc.description",
		HasHelp:        true,
	}
}

// Mount builds the service from the configuration and registers the routes.
func (m *Module) Mount(env *module.Env, mux *http.ServeMux) error {
	set := DefaultSettings()
	if err := env.Cfg.Module(Name, &set); err != nil {
		return err
	}
	if env.Shell == nil {
		return fmt.Errorf("%s: the page shell is missing", Name)
	}
	if set.PhotoURLTemplate == "" {
		set.PhotoURLTemplate = env.Cfg.PhotoURLTemplate
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := env.PG(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", Name, err)
	}
	if err := pool.Ping(ctx); err != nil {
		env.Log.Warn("hc_scan: the Auth database does not answer yet", "error", err)
	}

	var drafts DraftStore
	switch set.DraftStore {
	case "memory":
		env.Log.Warn("hc_scan: drafts are kept in the process memory and are lost at a restart (draft_store=memory)")
		drafts = NewMemoryDrafts()
	case "redis", "":
		rdb, err := env.Redis()
		if err != nil {
			return fmt.Errorf("%s: %w", Name, err)
		}
		drafts = NewRedisDrafts(rdb, time.Duration(set.DraftTTLHours)*time.Hour)
	default:
		return fmt.Errorf("%s: draft_store must be redis or memory, not %q", Name, set.DraftStore)
	}

	apiURL, apiFrom := set.DashAPIURL, "modules.hc_scan.dash_api_url"
	if apiURL == "" {
		apiURL, apiFrom = env.Cfg.DashAPIURL, env.Cfg.DashAPISource
	}
	if apiURL == "" {
		return fmt.Errorf("%s: set modules.hc_scan.dash_api_url, dash_api_url or api_server in the Dash config", Name)
	}
	api := dashapi.New(apiURL, env.Cfg.Dash.APISalt, 5*time.Second)
	m.api = api
	env.Log.Info("hc_scan: the Dash API of the permission check", "url", api.Root(), "from", apiFrom)
	perms := NewDashPermissions(api, time.Minute)

	svc, err := NewService(Deps{
		Repo:      NewPGRepository(pool),
		Drafts:    drafts,
		ChangeLog: NewFileChangeLog(set.DashDataDir, env.Cfg.Dash.CDNDataFolder),
		Perms:     perms,
		Settings:  set,
		Processes: env.Cfg.Dash.HCProcessList,
		LeaveList: env.Cfg.Dash.LeaveList,
		Log:       env.Log,
	})
	if err != nil {
		return err
	}
	if len(env.Cfg.Dash.HCProcessList) == 0 {
		return fmt.Errorf("%s: HC_PROCESS_LIST is empty in the Dash config", Name)
	}
	if set.DashDataDir == "" && env.Cfg.Dash.CDNDataFolder == "" {
		env.Log.Warn("hc_scan: neither modules.hc_scan.dash_data_dir nor cdn_data_folder is set; the Python pages will not be told about saved data")
	}
	m.svc = svc
	return svc.Routes(mux, env.Base+"/"+Name, env.Auth.Require, env.Shell)
}

// Probes tests what the module depends on besides the shared PostgreSQL and Redis: the Dash API that answers who may
// work on which process. dash_go -check runs it. A module that is not mounted has nothing to test.
func (m *Module) Probes() []module.Probe {
	api := m.api
	if api == nil {
		return nil
	}
	return []module.Probe{{
		Name:   "Dash API (hc_scan)",
		Target: api.Root(),
		Run:    func(ctx context.Context) (string, error) { return "", api.Check(ctx) },
	}}
}

// Close releases the module.
func (m *Module) Close() error { return nil }
