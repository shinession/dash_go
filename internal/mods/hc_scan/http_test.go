package hcscan

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
	"dash_go/internal/core/shell"
	"dash_go/internal/version"
)

const httpPrefix = "/dash_go/hc_scan"

type httpEnv struct {
	*testEnv
	mux    http.Handler
	cookie *http.Cookie
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	env := newTestEnv(t)
	signer, err := auth.NewSigner("http-test-secret")
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
	session, err := signer.Sign(auth.Claims{K: auth.KindSession, EE: testUser.EE, Name: testUser.Name, Iat: testNow.Unix(), Exp: testNow.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return &httpEnv{testEnv: env, mux: i18n.Middleware(mux), cookie: &http.Cookie{Name: "dash_go_session", Value: session}}
}

func (h *httpEnv) do(method, path, body string, withCookie bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if withCookie {
		req.AddCookie(h.cookie)
	}
	if method == http.MethodPost {
		req.Header.Set("X-Requested-With", "dash_go")
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return m
}

const sessionBody = `"process":"Winder","date":"2026-11-19","shift":"AM"`

func TestPageAndStaticFilesAreServed(t *testing.T) {
	h := newHTTPEnv(t)
	for path, want := range map[string]string{httpPrefix + "/": "<title>HC Scan Assign · CIM EDGE</title>", httpPrefix + "/static/app.js": "use strict", httpPrefix + "/static/app.css": ".pagebar"} {
		rec := h.do(http.MethodGet, path, "", true)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d, missing %q", path, rec.Code, want)
		}
	}
	if rec := h.do(http.MethodGet, httpPrefix, "", false); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != httpPrefix+"/" {
		t.Errorf("the bare path must redirect to the page: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAPIRequiresLogin(t *testing.T) {
	h := newHTTPEnv(t)
	for _, c := range []struct{ method, path string }{{"GET", "/api/me"}, {"GET", "/api/board?process=Winder&date=2026-11-19&shift=AM"}, {"GET", "/api/mtr?process=Winder&ee=AB12C01"}, {"POST", "/api/scan"}, {"POST", "/api/confirm"}} {
		if rec := h.do(c.method, httpPrefix+c.path, "{}", false); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", c.method, c.path, rec.Code)
		}
	}
}

func TestScanFlowOverHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	me := decode(t, h.do(http.MethodGet, httpPrefix+"/api/me", "", true))
	if me["version"] != version.Version || me["config"].(map[string]any)["production_date"] != "2026-11-19" {
		t.Fatalf("me: %v", me)
	}

	rec := h.do(http.MethodPost, httpPrefix+"/api/scan", `{`+sessionBody+`,"input":"ab12c01"}`, true)
	res := decode(t, rec)
	if rec.Code != 200 || res["status"] != "assigned" || res["entry"].(map[string]any)["machine_id"] != "Winder_515" {
		t.Fatalf("scan: %d %v", rec.Code, res)
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/scan", `{`+sessionBody+`,"input":"nonsense"}`, true); rec.Code != 400 || decode(t, rec)["code"] != "invalid_id" {
		t.Errorf("invalid id: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/scan", `{`+sessionBody+`,"input":"ZZ99Z99"}`, true); rec.Code != 404 {
		t.Errorf("unknown id: %d", rec.Code)
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/scan", `{"process":"Winder","date":"nope","shift":"AM","input":"AB12C01"}`, true); rec.Code != 400 {
		t.Errorf("bad date: %d", rec.Code)
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/scan", `not json`, true); rec.Code != 400 {
		t.Errorf("bad body: %d", rec.Code)
	}

	board := decode(t, h.do(http.MethodGet, httpPrefix+"/api/board?process=Winder&date=2026-11-19&shift=AM", "", true))
	if board["totals"].(map[string]any)["pending"].(float64) != 1 || len(board["entries"].([]any)) != 1 {
		t.Fatalf("board: %v", board["totals"])
	}

	if rec := h.do(http.MethodPost, httpPrefix+"/api/assign", `{`+sessionBody+`,"ee":"AB12C01","machine_id":"Winder_520"}`, true); rec.Code != 200 {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/assign", `{`+sessionBody+`,"ee":"AB12C01","machine_ids":["Winder_515","Winder_520"]}`, true); rec.Code != 200 {
		t.Fatalf("assign many: %d %s", rec.Code, rec.Body.String())
	} else {
		ev := decode(t, rec)
		ids := ev["machine_ids"].([]any)
		if len(ids) != 2 || ids[0] != "Winder_515" || ids[1] != "Winder_520" {
			t.Fatalf("assign many response: %v", ev)
		}
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/assign", `{`+sessionBody+`,"ee":"AB12C01","machine_ids":[]}`, true); rec.Code != 200 {
		t.Fatalf("assign clear: %d %s", rec.Code, rec.Body.String())
	} else if ev := decode(t, rec); len(ev["machine_ids"].([]any)) != 0 || ev["source"] != SourceNone {
		t.Fatalf("assign clear response: %v", ev)
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/remove", `{`+sessionBody+`,"ee":"AB12C01"}`, true); rec.Code != 200 {
		t.Fatalf("remove: %d", rec.Code)
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/reset", `{`+sessionBody+`}`, true); rec.Code != 200 {
		t.Fatalf("reset: %d", rec.Code)
	}

	// Direct target scan over HTTP
	rec = h.do(http.MethodPost, httpPrefix+"/api/scan", `{`+sessionBody+`,"input":"AB12C01","target_machine_id":"Winder_520"}`, true)
	if res = decode(t, rec); rec.Code != 200 || res["status"] != "assigned" || res["entry"].(map[string]any)["machine_id"] != "Winder_520" {
		t.Fatalf("target scan: %d %v", rec.Code, res)
	}

	// Employee all-MTR endpoint over HTTP
	mtrRec := h.do(http.MethodGet, httpPrefix+"/api/mtr?process=Winder&date=2026-11-19&ee=AB12C01", "", true)
	mtrBody := decode(t, mtrRec)
	if mtrRec.Code != 200 || mtrBody["ee"] != "AB12C01" || len(mtrBody["groups"].([]any)) != 1 {
		t.Fatalf("employee mtr: %d %v", mtrRec.Code, mtrBody)
	}
	if bad := h.do(http.MethodGet, httpPrefix+"/api/mtr?process=Winder&date=nope&ee=AB12C01", "", true); bad.Code != 400 {
		t.Errorf("bad date on /api/mtr: %d", bad.Code)
	}
}

func TestConfirmOverHTTPAsksForAcknowledgement(t *testing.T) {
	h := newHTTPEnv(t)
	h.repo.mtr.Levels["AB12C06"] = map[string]int{"C520": 2}
	h.do(http.MethodPost, httpPrefix+"/api/scan", `{`+sessionBody+`,"input":"AB12C06"}`, true)

	rec := h.do(http.MethodPost, httpPrefix+"/api/confirm", `{`+sessionBody+`}`, true)
	body := decode(t, rec)
	if rec.Code != http.StatusConflict || body["code"] != "needs_ack" || len(body["needs_ack"].([]any)) != 1 || len(h.repo.written) != 0 {
		t.Fatalf("without ack: %d %v written=%d", rec.Code, body, len(h.repo.written))
	}
	rec = h.do(http.MethodPost, httpPrefix+"/api/confirm", `{`+sessionBody+`,"ack":true}`, true)
	body = decode(t, rec)
	if rec.Code != 200 || len(body["written"].([]any)) != 1 || len(h.repo.written) != 1 {
		t.Fatalf("with ack: %d %v", rec.Code, body)
	}
}

func TestErrorsDoNotLeakInternals(t *testing.T) {
	h := newHTTPEnv(t)
	h.perms.procs = nil
	rec := h.do(http.MethodGet, httpPrefix+"/api/board?process=Winder&date=2026-11-19&shift=AM", "", true)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "permission") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Cache-Control"); ct != "no-store" {
		t.Errorf("API answers must not be cached: %q", ct)
	}
}

func TestHCScanPageHasDocsGuideAndTourSteps(t *testing.T) {
	h := newHTTPEnv(t)
	en := h.do(http.MethodGet, httpPrefix+"/", "", true).Body.String()
	for _, want := range []string{
		`href="/dash_go/hc_scan/help" target="_blank" rel="noopener" data-docs-link`,
		`data-tour-start`,
		`/dash_go/_shell/tour.js?v=` + version.Version,
		`data-tour-screen="hc_scan.picker"`, `data-tour-screen="hc_scan.board"`,
		`data-tour="hc.tour.picker"`, `data-tour="hc.tour.controls"`, `data-tour="hc.tour.counters"`,
		`data-tour="hc.tour.scan"`, `data-tour="hc.tour.result"`, `data-tour="hc.tour.actions"`,
		`data-tour="hc.tour.entries"`, `data-tour="hc.tour.machines"`, `data-tour="hc.tour.missing"`, `data-tour="hc.tour.candidates"`,
		`/dash_go/_shell/icons.svg#scanner`, `id="btn-other-shift"`, `id="btn-off-day"`, `id="btn-other-process"`, `id="candidate-dialog"`, `id="mtr-dialog"`, `id="target-banner"`,
		`data-tour="tour.back"`, `data-tour="tour.help"`,
	} {
		if !strings.Contains(en, want) {
			t.Errorf("hc_scan page lacks %q", want)
		}
	}
	if strings.Index(en, "tour.js") > strings.Index(en, "hc_scan/static/app.js") {
		t.Error("tour.js must load before hc_scan/static/app.js")
	}
	iLang := strings.Index(en, `class="dg-lang"`)
	iHelp := strings.Index(en, `class="dg-helptools"`)
	iUser := strings.Index(en, `class="dg-userbox"`)
	if !(iLang >= 0 && iHelp > iLang && iUser > iHelp) {
		t.Errorf("expected order dg-lang (%d) < dg-helptools (%d) < dg-userbox (%d)", iLang, iHelp, iUser)
	}
	// Not assigned yet appears above candidate buttons, which appear above Machines in the right column (since 0.1.7 / 0.1.9).
	iMissing := strings.Index(en, `id="missing"`)
	iCand := strings.Index(en, `id="candidate-bar"`)
	iMachines := strings.Index(en, `id="machines"`)
	if !(iMissing >= 0 && iCand > iMissing && iMachines > iCand) {
		t.Errorf("expected order id=missing (%d) < id=candidate-bar (%d) < id=machines (%d)", iMissing, iCand, iMachines)
	}
	zh := h.doLang(http.MethodGet, httpPrefix+"/", "", "zh").Body.String()
	for _, want := range []string{`data-tour-title="选择工序"`, `data-tour-title="扫描工牌"`, `data-tour-title="机台卡片"`} {
		if !strings.Contains(zh, want) {
			t.Errorf("Chinese hc_scan page lacks %q", want)
		}
	}
}

func TestHCScanHelpPageHasBothLanguagesAndMatchesSections(t *testing.T) {
	h := newHTTPEnv(t)
	// Accessible without signing in, as well as when signed in.
	recEN := h.do(http.MethodGet, httpPrefix+"/help", "", false)
	if recEN.Code != http.StatusOK {
		t.Fatalf("GET %s/help = %d", httpPrefix, recEN.Code)
	}
	en := recEN.Body.String()
	zh := h.doLang(http.MethodGet, httpPrefix+"/help", "", "zh").Body.String()
	if en == zh {
		t.Fatal("Chinese hc_scan help page must differ from English")
	}
	for _, want := range []string{
		`<title>HC Scan Assign — User guide · CIM EDGE</title>`,
		`href="/dash_go/hc_scan/"`, `href="/dash_go/help"`,
		`data-tour="tour.help_toc"`, `data-tour="tour.help_body"`,
		`data-docs-link`, `data-tour-start`,
	} {
		if !strings.Contains(en, want) {
			t.Errorf("English hc_scan help page lacks %q", want)
		}
	}
	for _, want := range []string{
		`<title>HC 扫码分派 — 使用手册 · CIM EDGE</title>`,
		`href="/dash_go/hc_scan/"`, `href="/dash_go/help"`,
		`data-tour-title="目录"`, `data-tour-title="手册正文"`,
	} {
		if !strings.Contains(zh, want) {
			t.Errorf("Chinese hc_scan help page lacks %q", want)
		}
	}
	for _, id := range []string{`id="overview"`, `id="workflow"`, `id="board"`, `id="rules"`, `id="warnings"`, `id="limits"`, `id="interface"`} {
		if !strings.Contains(en, id) || !strings.Contains(zh, id) {
			t.Errorf("hc_scan help page missing section %s in one language", id)
		}
	}
	if strings.Contains(en, "data-tour-auto") {
		t.Error("hc_scan help page must not auto-start tour")
	}
}
