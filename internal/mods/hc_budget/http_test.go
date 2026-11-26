package hcbudget

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
	"dash_go/internal/core/shell"
	"dash_go/internal/version"
)

const httpPrefix = "/dash_go/hc_budget"

var catalogBlock = regexp.MustCompile(`(?s)<script type="application/json" id="dg-i18n">(.*?)</script>`)

type httpEnv struct {
	*testEnv
	mux         http.Handler
	adminCookie *http.Cookie
	stdCookie   *http.Cookie
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	env := newTestEnv(t)
	signer, err := auth.NewSigner("http-budget-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(signer, auth.NewMemoryNonces(), auth.Settings{BasePath: "/dash_go", Now: func() time.Time { return testNow }})
	sh, err := shell.New(shell.Options{Base: "/dash_go", Version: version.Version, PhotoURLTemplate: "/api/static/hr/{ee_code}.webp", Auth: authSvc})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	sh.Mount(mux)
	if err := env.svc.Routes(mux, httpPrefix, authSvc.Require, sh); err != nil {
		t.Fatal(err)
	}
	sign := func(u auth.User) *http.Cookie {
		tok, err := signer.Sign(auth.Claims{K: auth.KindSession, EE: u.EE, Name: u.Name, Iat: testNow.Unix(), Exp: testNow.Add(time.Hour).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: "dash_go_session", Value: tok}
	}
	return &httpEnv{
		testEnv:     env,
		mux:         i18n.Middleware(mux),
		adminCookie: sign(adminUser),
		stdCookie:   sign(stdUser),
	}
}

func (h *httpEnv) do(method, path, body string, cookie *http.Cookie, lang string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if lang != "" {
		req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: lang})
	}
	if method == http.MethodPost {
		req.Header.Set("X-Requested-With", "dash_go")
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return m
}

func TestPageRedirectAuthAndAssets(t *testing.T) {
	h := newHTTPEnv(t)

	// Bare path redirects to trailing slash.
	if rec := h.do(http.MethodGet, httpPrefix, "", nil, ""); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != httpPrefix+"/" {
		t.Fatalf("bare redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Page requires login.
	if rec := h.do(http.MethodGet, httpPrefix+"/", "", nil, ""); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/dash_go/login") {
		t.Fatalf("unauthenticated page: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// API requires login.
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/me"},
		{"GET", "/api/table?process=Winder"},
		{"POST", "/api/save"},
	} {
		if rec := h.do(tc.method, httpPrefix+tc.path, "{}", nil, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without cookie = %d", tc.method, tc.path, rec.Code)
		}
	}

	// Page and static files serve cleanly with budget icon, tour steps, and EN/ZH localization.
	en := h.do(http.MethodGet, httpPrefix+"/", "", h.adminCookie, "en").Body.String()
	for _, want := range []string{
		`<title>HC Budget Setting · CIM EDGE</title>`,
		`/dash_go/_shell/icons.svg#budget`,
		`href="/dash_go/hc_budget/help" target="_blank" rel="noopener" data-docs-link`,
		`data-tour-screen="hc_budget.picker"`, `data-tour-screen="hc_budget.table"`,
		`data-tour="hb.tour.picker"`, `data-tour="hb.tour.controls"`, `data-tour="hb.tour.add"`,
		`data-tour="hb.tour.counters"`, `data-tour="hb.tour.actions"`, `data-tour="hb.tour.table"`, `data-tour="hb.tour.reorder"`,
		`/dash_go/hc_budget/static/app.css?v=` + version.Version,
		`/dash_go/hc_budget/static/app.js?v=` + version.Version,
	} {
		if !strings.Contains(en, want) {
			t.Errorf("English page lacks %q", want)
		}
	}
	m := catalogBlock.FindStringSubmatch(en)
	if m == nil {
		t.Fatal("missing #dg-i18n block")
	}
	var cat map[string]string
	if err := json.Unmarshal([]byte(m[1]), &cat); err != nil {
		t.Fatal(err)
	}
	for k := range cat {
		if !strings.HasPrefix(k, "ui.") && !strings.HasPrefix(k, "hb.ui.") {
			t.Errorf("server-only key %q leaked into #dg-i18n", k)
		}
	}

	zh := h.do(http.MethodGet, httpPrefix+"/", "", h.adminCookie, "zh").Body.String()
	for _, want := range []string{`<html lang="zh"`, `<title>HC 预算设置 · CIM EDGE</title>`, `data-tour-title="选择工序"`, `data-tour-title="显示顺序（管理员）"`} {
		if !strings.Contains(zh, want) {
			t.Errorf("Chinese page lacks %q", want)
		}
	}

	for path, want := range map[string]string{httpPrefix + "/static/app.js": "use strict", httpPrefix + "/static/app.css": ".budget-table"} {
		rec := h.do(http.MethodGet, path, "", h.adminCookie, "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d missing %q", path, rec.Code, want)
		}
	}
}

func TestHelpPagePublicAndBilingual(t *testing.T) {
	h := newHTTPEnv(t)
	recEN := h.do(http.MethodGet, httpPrefix+"/help", "", nil, "en")
	if recEN.Code != 200 {
		t.Fatalf("GET help = %d", recEN.Code)
	}
	en := recEN.Body.String()
	zh := h.do(http.MethodGet, httpPrefix+"/help", "", nil, "zh").Body.String()
	if en == zh {
		t.Fatal("Chinese help page must differ from English")
	}
	for _, id := range []string{`id="overview"`, `id="permissions"`, `id="editing"`, `id="admin"`, `id="confirm"`, `id="interface"`} {
		if !strings.Contains(en, id) || !strings.Contains(zh, id) {
			t.Errorf("help page missing section %s", id)
		}
	}
	if !strings.Contains(en, `<title>HC Budget Setting — User guide · CIM EDGE</title>`) || !strings.Contains(zh, `<title>HC 预算设置 — 使用手册 · CIM EDGE</title>`) {
		t.Error("help page titles mismatch")
	}
	if strings.Contains(en, "data-tour-auto") {
		t.Error("help page must not auto-start tour")
	}
}

func TestBudgetAPIFlowAndLocalizedErrors(t *testing.T) {
	h := newHTTPEnv(t)

	// Admin me & table.
	me := decodeJSON(t, h.do(http.MethodGet, httpPrefix+"/api/me", "", h.adminCookie, "en"))
	uMap, _ := me["user"].(map[string]any)
	if me["is_admin"] != true || me["version"] != version.Version || uMap["ee"] != adminUser.EE || uMap["name"] != adminUser.Name {
		t.Fatalf("admin me: %v", me)
	}
	rec := h.do(http.MethodGet, httpPrefix+"/api/table?process=Winder&include_hidden=1", "", h.adminCookie, "en")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("table: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	tbl := decodeJSON(t, rec)
	if len(tbl["rows"].([]any)) != 4 {
		t.Fatalf("expected 4 rows with include_hidden=1, got %v", tbl["rows"])
	}

	// Admin saves new row + reorder over HTTP.
	rec = h.do(http.MethodPost, httpPrefix+"/api/save", `{
		"process":"Winder",
		"creates":[{"station_code":535,"cost_center":"2085","process_code":10,"type":"Routine","machine":"Winder 535","category":"Main","budget_head_count":"2"}],
		"updates":[],
		"order":["Winder_535","Winder_515","Winder_520","Winder_901","Winder_999"]
	}`, h.adminCookie, "en")
	if rec.Code != 200 {
		t.Fatalf("admin save: %d %s", rec.Code, rec.Body.String())
	}
	saved := decodeJSON(t, rec)
	if saved["inserted"].(float64) != 1 || saved["updated"].(float64) != 4 {
		t.Fatalf("saved result: %v", saved)
	}

	// Non-admin attempt to add returns localized 403 in Chinese.
	rec = h.do(http.MethodPost, httpPrefix+"/api/save", `{"process":"Winder","creates":[{"station_code":536,"type":"Routine","machine":"No"}]}`, h.stdCookie, "zh")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("std create status = %d %s", rec.Code, rec.Body.String())
	}
	errBody := decodeJSON(t, rec)
	if errBody["code"] != "forbidden_admin" || !strings.Contains(errBody["error"].(string), "仅管理员") {
		t.Fatalf("localized 403: %v", errBody)
	}

	// Invalid JSON returns 400.
	if rec := h.do(http.MethodPost, httpPrefix+"/api/save", `not json`, h.adminCookie, "en"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
}
