//go:build devserver

package weblog

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
	"dash_go/internal/core/server"
	"dash_go/internal/core/shell"
	"dash_go/internal/version"
)

// TestDevServer starts an in-memory web_log + landing page server for local browser testing and visual
// inspection without touching PostgreSQL or Redis:
//
//	go test -tags devserver -run TestDevServer -timeout 0 ./internal/mods/web_log
//
// Open:
//   - http://127.0.0.1:18752/dash_go/web_log/       (anonymous visitor: trend chart only)
//   - http://127.0.0.1:18752/dev/login-user         (regular user: trend chart only)
//   - http://127.0.0.1:18752/dev/login-admin        (admin: trend chart + live Redis web_log panel)
func TestDevServer(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	signer, err := auth.NewSigner("dev-weblog-secret")
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(signer, auth.NewMemoryNonces(), auth.Settings{BasePath: "/dash_go", Log: log})

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	rows := buildDemoRows(today)
	liveEntries := []LiveLogEntry{
		ParseLiveLogLine(fmt.Sprintf("10.135.12.24 | %s | IE: tab11 | 200 | info | sgmescimai", now.Add(-2*time.Second).Format("2006-01-02 15:04:05.123456"))),
		ParseLiveLogLine(fmt.Sprintf("10.135.18.91 | %s | QDB: tab3 | 200 | info | sgmescimemg", now.Add(-6*time.Second).Format("2006-01-02 15:04:05.654321"))),
		ParseLiveLogLine(fmt.Sprintf("10.135.10.42 | %s | API: post_data timeout | 502 | error | prod_eng", now.Add(-15*time.Second).Format("2006-01-02 15:04:05.111222"))),
		ParseLiveLogLine(fmt.Sprintf("10.135.14.88 | %s | QAQC: lcs_cell_collection | 200 | warning | sgmescimai", now.Add(-25*time.Second).Format("2006-01-02 15:04:05.987654"))),
		ParseLiveLogLine(fmt.Sprintf("10.135.22.15 | %s | TimeVault: explorer | 200 | info | sgmescimemg", now.Add(-40*time.Second).Format("2006-01-02 15:04:05.333444"))),
	}
	for i := range liveEntries {
		liveEntries[i].Seq = i + 1
	}

	adminUser := auth.User{EE: "AD01", Name: "Alice Admin"}
	stdUser := auth.User{EE: "US01", Name: "Bob Operator"}

	svc, err := NewService(Deps{
		Repo:     &fakeLogRepo{rows: rows},
		Admin:    &fakeAdmin{admins: map[string]bool{adminUser.EE: true}},
		Cache:    NewMemoryCache(nil),
		LiveLogs: &fakeLive{entries: liveEntries, total: 12480},
		Settings: DefaultSettings(),
		Log:      log,
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
			{Name: "web_log", Title: "Web Log", TitleKey: "wl.title", Icon: "weblog", Description: "Analyze application and server traffic trends", DescriptionKey: "wl.description", Href: "/dash_go/web_log/", HelpHref: "/dash_go/web_log/help"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	authSvc.Mount(mux)
	sh.Mount(mux)
	if err := svc.Routes(mux, "/dash_go/web_log", authSvc.Require, authSvc.Authenticate, sh); err != nil {
		t.Fatal(err)
	}

	loginAs := func(u auth.User) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			now := time.Now()
			tok, _ := signer.Sign(auth.Claims{K: auth.KindSSO, EE: u.EE, Name: u.Name, Iat: now.Unix(), Exp: now.Add(time.Minute).Unix(), N: auth.NewNonce()})
			http.Redirect(w, r, "/dash_go/auth/sso?t="+tok+"&next=%2Fdash_go%2Fweb_log%2F", http.StatusSeeOther)
		}
	}
	mux.HandleFunc("GET /dev/login-admin", loginAs(adminUser))
	mux.HandleFunc("GET /dev/login-user", loginAs(stdUser))

	port := envOr("DEV_PORT", "18752")
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: server.Handler(mux, log)}
	go func() { _ = srv.ListenAndServe() }()
	t.Logf("web_log dev server: http://127.0.0.1:%s/dash_go/web_log/ (or /dev/login-admin)", port)
	secs, _ := strconv.Atoi(envOr("DEV_SECONDS", "60"))
	time.Sleep(time.Duration(secs) * time.Second)
	_ = srv.Shutdown(ctx)
}

func buildDemoRows(today time.Time) []logRow {
	apps := []struct {
		app  string
		cats []string
		base int64
	}{
		{"IE", []string{"tab1", "tab9", "tab11", "plan", "idle"}, 600},
		{"QDB", []string{"tab1", "tab3", "tab5", "idle"}, 520},
		{"API", []string{"post_data", "home_page", "user_mgmt", "send_email"}, 440},
		{"Gitea", []string{"git", "code", "issues", "idle"}, 320},
		{"MER", []string{"tab1", "tab7", "tab8", "idle"}, 250},
		{"QAQC", []string{"cards", "lcs_cell", "idle"}, 210},
		{"DataBridge", []string{"upload", "jobs", "idle"}, 180},
		{"TimeVault", []string{"explorer", "backups", "idle"}, 140},
	}
	hosts := []string{"prod_eng", "sgmescimai", "sgmescimemg"}
	var rows []logRow
	for d := 0; d < 120; d++ {
		day := today.AddDate(0, 0, -d)
		ds := day.Format("2006-01-02")
		weekendFactor := int64(2)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			weekendFactor = 1
		}
		for ai, a := range apps {
			for ci, c := range a.cats {
				h := hosts[(d+ai+ci)%len(hosts)]
				if d > 80 && h == "sgmescimemg" {
					h = "" // simulate historical blank host normalized to prod_eng
				}
				ipCount := 2 + (ai+ci+d)%5
				for k := 0; k < ipCount; k++ {
					rows = append(rows, logRow{
						date:     ds,
						ip:       fmt.Sprintf("10.135.%d.%d/32", 10+(ai+k)%8, 20+(d+ci+k*7)%60),
						app:      a.app,
						category: c,
						host:     h,
						actions:  (a.base / int64(ci+1)) * weekendFactor / int64(ipCount),
					})
				}
			}
		}
	}
	return rows
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
