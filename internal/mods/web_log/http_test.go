package weblog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
	"dash_go/internal/core/shell"
)

type httpFixture struct {
	t      *testing.T
	handler http.Handler
	auth   *auth.Service
	repo   *fakeLogRepo
	admin  *fakeAdmin
	live   *fakeLive
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local)
	svc, repo, admin, live := newTestService(t, sampleRows(), NewMemoryCache(func() time.Time { return now }), now)

	signer, err := auth.NewSigner("weblog-http-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.NewService(signer, auth.NewMemoryNonces(), auth.Settings{BasePath: "/dash_go", Log: log})
	sh, err := shell.New(shell.Options{Base: "/dash_go", Version: "0.2.5", Auth: authSvc, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	sh.Mount(mux)
	if err := svc.Routes(mux, "/dash_go/web_log", authSvc.Require, authSvc.Authenticate, sh); err != nil {
		t.Fatal(err)
	}
	return &httpFixture{t: t, handler: i18n.Middleware(mux), auth: authSvc, repo: repo, admin: admin, live: live}
}

func (f *httpFixture) cookie(ee, name string) *http.Cookie {
	sid, err := f.auth.IssueSession(context.Background(), auth.User{EE: ee, Name: name})
	if err != nil {
		f.t.Fatal(err)
	}
	return &http.Cookie{Name: "dash_go_session", Value: sid}
}

func (f *httpFixture) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestPublicPagesAndStaticAssets(t *testing.T) {
	f := newHTTPFixture(t)

	// Bare prefix redirects to trailing slash.
	if rec := f.get("/dash_go/web_log"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/dash_go/web_log/" {
		t.Fatalf("redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	// Main page is publicly accessible without login.
	rec := f.get("/dash_go/web_log/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dash_go/web_log/ = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`/dash_go/_shell/icons.svg#weblog`,
		`/dash_go/web_log/static/vendor/echarts.common.min.js`,
		`/dash_go/web_log/static/app.js`,
		`/dash_go/web_log/static/app.css`,
		`id="live-section" class="wl-card live-card hidden"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index page lacks %q", want)
		}
	}

	// Embedded offline ECharts asset is served.
	ec := f.get("/dash_go/web_log/static/vendor/echarts.common.min.js")
	if ec.Code != http.StatusOK || ec.Body.Len() < 100000 {
		t.Fatalf("echarts asset: code=%d size=%d", ec.Code, ec.Body.Len())
	}

	// Bilingual help page renders EN and ZH cleanly.
	enHelp := f.get("/dash_go/web_log/help")
	zhHelp := f.get("/dash_go/web_log/help", &http.Cookie{Name: i18n.CookieName, Value: "zh"})
	if enHelp.Code != 200 || zhHelp.Code != 200 || !strings.Contains(enHelp.Body.String(), "Web Log — User guide") || !strings.Contains(zhHelp.Body.String(), "Web 日志 — 使用手册") {
		t.Fatalf("help pages failed: en=%d zh=%d", enHelp.Code, zhHelp.Code)
	}
}

func TestMetaAndLiveAdminGating(t *testing.T) {
	f := newHTTPFixture(t)

	// Anonymous /api/meta -> 200, is_admin=false, user=nil.
	rec := f.get("/dash_go/web_log/api/meta")
	if rec.Code != http.StatusOK {
		t.Fatalf("anon meta = %d", rec.Code)
	}
	var meta MetaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.IsAdmin || meta.User != nil {
		t.Errorf("anon meta must not be admin: %+v", meta)
	}
	if len(meta.Hosts) != 3 || len(meta.Apps) != 4 {
		t.Errorf("unexpected meta hosts/apps: hosts=%v apps=%v", meta.Hosts, meta.Apps)
	}

	// Regular user /api/meta -> is_admin=false; /api/live -> 403.
	userCk := f.cookie("US01", "Normal User")
	rec = f.get("/dash_go/web_log/api/meta", userCk)
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.IsAdmin || meta.User == nil || meta.User.EE != "US01" {
		t.Errorf("user meta = %+v", meta)
	}
	if rec := f.get("/dash_go/web_log/api/live", userCk); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin /api/live = %d, want 403", rec.Code)
	}

	// Anonymous /api/live -> 401.
	if rec := f.get("/dash_go/web_log/api/live"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon /api/live = %d, want 401", rec.Code)
	}

	// Admin user /api/meta -> is_admin=true; /api/live -> 200 with entries.
	adminCk := f.cookie("AD01", "Admin User")
	rec = f.get("/dash_go/web_log/api/meta", adminCk)
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if !meta.IsAdmin {
		t.Errorf("admin meta must report is_admin=true: %+v", meta)
	}
	liveRec := f.get("/dash_go/web_log/api/live?limit=200", adminCk)
	if liveRec.Code != http.StatusOK {
		t.Fatalf("admin /api/live = %d %s", liveRec.Code, liveRec.Body.String())
	}
	var live LiveLogResponse
	if err := json.Unmarshal(liveRec.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if live.Total != 42 || len(live.Entries) != 1 || live.Limit != 200 {
		t.Errorf("live response = %+v", live)
	}

	// Admin DB error -> 500 on /api/live (fail closed).
	f.admin.err = errors.New("pg down")
	if rec := f.get("/dash_go/web_log/api/live", adminCk); rec.Code != http.StatusInternalServerError {
		t.Fatalf("db error /api/live = %d, want 500", rec.Code)
	}
}

func TestTrendHTTPParamsAndLocalizedErrors(t *testing.T) {
	f := newHTTPFixture(t)

	rec := f.get("/dash_go/web_log/api/trend?mode=week&from=2026-11-16&to=2026-11-29&group_by=host&host=prod_eng,sgmescimai&exclude_idle=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("trend = %d %s", rec.Code, rec.Body.String())
	}
	var trend TrendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &trend); err != nil {
		t.Fatal(err)
	}
	if trend.EffectiveGroupBy != "host" || !trend.ExcludeIdle || len(trend.Hosts) != 2 {
		t.Errorf("trend echo = %+v", trend)
	}
	// prod_eng (100 non-idle) + sgmescimai (200 + 300) = 600.
	if trend.Summary.TotalActions != 600 {
		t.Errorf("total actions = %d, want 600", trend.Summary.TotalActions)
	}

	// Localized Chinese validation error.
	bad := f.get("/dash_go/web_log/api/trend?from=2026-11-28&to=2026-11-01", &http.Cookie{Name: i18n.CookieName, Value: "zh"})
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "开始日期不能晚于结束日期") {
		t.Errorf("zh validation error = %d %s", bad.Code, bad.Body.String())
	}
}
