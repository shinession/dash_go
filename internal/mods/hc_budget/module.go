// Package hcbudget is the "HC Budget Setting" module.
//
// Authorized users pick a process (sharing the process picker and HeadCount permissions with hc_scan) and maintain
// machine and activity head count budgets in t_mqpd_hc_table. Administrators (Dash user Type = "admin") can also
// add new machines/activities, toggle hidden status, and reorder per-process display sequence.
package hcbudget

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"dash_go/internal/core/dashapi"
	"dash_go/internal/core/hc"
	"dash_go/internal/core/i18n"
	"dash_go/internal/core/module"
)

// Name is the module name used in the URL, the build script, and the configuration.
const Name = "hc_budget"

func init() {
	i18n.Register(webFS, "i18n")
	module.Register(Name, func() module.Module { return &Module{} })
}

// Module implements module.Module and module.Checker.
type Module struct {
	svc *Service
	api *dashapi.Client
}

var _ module.Checker = (*Module)(nil)

// Info describes the module.
func (m *Module) Info() module.Info {
	return module.Info{
		Name:           Name,
		Title:          "HC Budget Setting",
		TitleKey:       "hb.title",
		Icon:           "budget",
		Description:    "Maintain machine and activity head count budgets, visibility, and display order",
		DescriptionKey: "hb.description",
		HasHelp:        true,
	}
}

// Mount builds the service from configuration and registers the module routes.
func (m *Module) Mount(env *module.Env, mux *http.ServeMux) error {
	set := DefaultSettings()
	if err := env.Cfg.Module(Name, &set); err != nil {
		return err
	}
	if env.Shell == nil {
		return fmt.Errorf("%s: the page shell is missing", Name)
	}
	if set.DashDataDir == "" {
		set.DashDataDir = env.Cfg.DashDataDir
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := env.PG(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", Name, err)
	}
	if err := pool.Ping(ctx); err != nil {
		env.Log.Warn("hc_budget: the Auth database does not answer yet", "error", err)
	}

	apiURL, apiFrom := set.DashAPIURL, "modules.hc_budget.dash_api_url"
	if apiURL == "" {
		apiURL, apiFrom = env.Cfg.DashAPIURL, env.Cfg.DashAPISource
	}
	if apiURL == "" {
		return fmt.Errorf("%s: set modules.hc_budget.dash_api_url, dash_api_url or api_server in the Dash config", Name)
	}
	api := dashapi.New(apiURL, env.Cfg.Dash.APISalt, 5*time.Second)
	m.api = api
	env.Log.Info("hc_budget: the Dash API of the permission check", "url", api.Root(), "from", apiFrom)
	perms := hc.NewDashPermissions(api, time.Minute)

	svc, err := NewService(Deps{
		Repo:      NewPGRepository(pool),
		ChangeLog: NewFileChangeLog(set.DashDataDir, env.Cfg.Dash.CDNDataFolder),
		Perms:     perms,
		Processes: env.Cfg.Dash.HCProcessList,
		Log:       env.Log,
	})
	if err != nil {
		return err
	}
	if len(env.Cfg.Dash.HCProcessList) == 0 {
		return fmt.Errorf("%s: HC_PROCESS_LIST is empty in the Dash config", Name)
	}
	if set.DashDataDir == "" && env.Cfg.Dash.CDNDataFolder == "" {
		env.Log.Warn("hc_budget: neither dash_data_dir nor cdn_data_folder is set; the Python pages will not be told about saved budget changes")
	}
	m.svc = svc
	return svc.Routes(mux, env.Base+"/"+Name, env.Auth.Require, env.Shell)
}

// Probes checks the Dash API dependency for dash_go -check.
func (m *Module) Probes() []module.Probe {
	api := m.api
	if api == nil {
		return nil
	}
	return []module.Probe{{
		Name:   "Dash API (hc_budget)",
		Target: api.Root(),
		Run:    func(ctx context.Context) (string, error) { return "", api.Check(ctx) },
	}}
}

// Close releases the module.
func (m *Module) Close() error { return nil }
