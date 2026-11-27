//go:build devserver

package hcbudget

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
	"dash_go/internal/core/hc"
	"dash_go/internal/core/server"
	"dash_go/internal/core/shell"
	"dash_go/internal/version"
)

// TestDevServer starts an in-memory hc_budget + landing page server for local browser testing and screenshots
// without touching any database:
//
//	go test -tags devserver -run TestDevServer -timeout 0 ./internal/mods/hc_budget
//
// Open http://127.0.0.1:18751/dev/login-admin (Admin) or /dev/login-user (standard user).
func TestDevServer(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	signer, err := auth.NewSigner("dev-budget-secret")
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(signer, auth.NewMemoryNonces(), auth.Settings{BasePath: "/dash_go", Log: log})

	repo := newMemRepo(
		Row{ID: "Winder_515", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeRoutine, StationCode: intPtr(515), Machine: "Winder 515", BudgetHC: 2, Category: "Main Line", DisplaySeq: intPtr(1), Hidden: false, UpdateAt: "2026-11-25 08:15:00", UpdateUser: "Alice Admin"},
		Row{ID: "Winder_520", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeRoutine, StationCode: intPtr(520), Machine: "Winder 520", BudgetHC: 1.5, Category: "Main Line", DisplaySeq: intPtr(2), Hidden: false, UpdateAt: "2026-11-25 08:15:00", UpdateUser: "Alice Admin"},
		Row{ID: "Winder_525", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeRoutine, StationCode: intPtr(525), Machine: "Winder 525", BudgetHC: 1, Category: "Main Line", DisplaySeq: intPtr(3), Hidden: false, UpdateAt: "2026-11-25 08:15:00", UpdateUser: "Alice Admin"},
		Row{ID: "Winder_901", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeNonRoutine, StationCode: intPtr(901), Machine: "Group Leader", BudgetHC: 1, Category: "Support", DisplaySeq: intPtr(4), Hidden: false, UpdateAt: "2026-11-25 08:15:00", UpdateUser: "Alice Admin"},
		Row{ID: "Winder_999", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeRoutine, StationCode: intPtr(999), Machine: "Retired Winder", BudgetHC: 0, Category: "Legacy", DisplaySeq: intPtr(5), Hidden: true, UpdateAt: "2026-10-01 09:00:00", UpdateUser: "Alice Admin"},
		Row{ID: "Slitter_105", Process: "Slitter", CostCenter: "2086", ProcessCode: intPtr(20), Type: TypeRoutine, StationCode: intPtr(105), Machine: "Slitter 105", BudgetHC: 1, Category: "Cutting", DisplaySeq: intPtr(1), Hidden: false, UpdateAt: "2026-11-25 08:15:00", UpdateUser: "Alice Admin"},
	)
	repo.admins[adminUser.EE] = true
	perms := &stubPerms{byEE: map[string]hc.Access{
		adminUser.EE: {Processes: []string{"Slitter", "Winder"}, IsAdmin: true},
		stdUser.EE:   {Processes: []string{"Winder"}, IsAdmin: false},
	}}
	svc, err := NewService(Deps{
		Repo:      repo,
		ChangeLog: NewFileChangeLog(t.TempDir()),
		Perms:     perms,
		Processes: []string{"Slitter", "Winder"},
		Log:       log,
	})
	if err != nil {
		t.Fatal(err)
	}
	sh, err := shell.New(shell.Options{
		Base:             "/dash_go",
		Version:          version.Version,
		PhotoURLTemplate: config.DefaultPhotoURLTemplate,
		DashHome:         "/ie/",
		Auth:             authSvc,
		Log:              log,
		Modules: []shell.ModuleLink{
			{Name: "hc_scan", Title: "HC Scan Assign", Icon: "scanner", Description: "Scan employee badges to assign machines and activities by MTR level", Href: "/dash_go/hc_scan/", HelpHref: "/dash_go/hc_scan/help"},
			{Name: "hc_budget", Title: "HC Budget Setting", TitleKey: "hb.title", Icon: "budget", Description: "Maintain machine and activity head count budgets, visibility, and display order", DescriptionKey: "hb.description", Href: "/dash_go/hc_budget/", HelpHref: "/dash_go/hc_budget/help"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	authSvc.Mount(mux)
	sh.Mount(mux)
	if err := svc.Routes(mux, "/dash_go/hc_budget", authSvc.Require, sh); err != nil {
		t.Fatal(err)
	}
	loginAs := func(u auth.User) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			now := time.Now()
			next := r.URL.Query().Get("next")
			if next == "" {
				next = "/dash_go/hc_budget/"
			}
			tok, _ := signer.Sign(auth.Claims{K: auth.KindSSO, EE: u.EE, Name: u.Name, Iat: now.Unix(), Exp: now.Add(time.Minute).Unix(), N: auth.NewNonce()})
			http.Redirect(w, r, "/dash_go/auth/sso?t="+tok+"&next="+next, http.StatusSeeOther)
		}
	}
	mux.HandleFunc("GET /dev/login-admin", loginAs(adminUser))
	mux.HandleFunc("GET /dev/login-user", loginAs(stdUser))

	port := envOr("DEV_PORT", "18751")
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: server.Handler(mux, log)}
	go func() { _ = srv.ListenAndServe() }()
	t.Logf("hc_budget dev server: http://127.0.0.1:%s/dev/login-admin", port)
	secs, _ := strconv.Atoi(envOr("DEV_SECONDS", "60"))
	time.Sleep(time.Duration(secs) * time.Second)
	_ = srv.Shutdown(ctx)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
