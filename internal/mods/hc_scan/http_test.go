package hcscan

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dash_go/internal/core/auth"
)

const httpPrefix = "/dash_go/hc_scan"

type httpEnv struct {
	*testEnv
	mux    *http.ServeMux
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
	mux := http.NewServeMux()
	if err := env.svc.Routes(mux, httpPrefix, authSvc.Require); err != nil {
		t.Fatal(err)
	}
	session, err := signer.Sign(auth.Claims{K: auth.KindSession, EE: testUser.EE, Name: testUser.Name, Iat: testNow.Unix(), Exp: testNow.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return &httpEnv{testEnv: env, mux: mux, cookie: &http.Cookie{Name: "dash_go_session", Value: session}}
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
	for path, want := range map[string]string{httpPrefix + "/": "<title>HC Scan Assign</title>", httpPrefix + "/static/app.js": "use strict", httpPrefix + "/static/app.css": "--bg"} {
		rec := h.do(http.MethodGet, path, "", false)
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
	for _, c := range []struct{ method, path string }{{"GET", "/api/me"}, {"GET", "/api/board?process=Winder&date=2026-11-19&shift=AM"}, {"POST", "/api/scan"}, {"POST", "/api/confirm"}} {
		if rec := h.do(c.method, httpPrefix+c.path, "{}", false); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", c.method, c.path, rec.Code)
		}
	}
}

func TestScanFlowOverHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	me := decode(t, h.do(http.MethodGet, httpPrefix+"/api/me", "", true))
	if me["version"] != "0.1.0" || me["config"].(map[string]any)["production_date"] != "2026-11-19" {
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
	if rec := h.do(http.MethodPost, httpPrefix+"/api/remove", `{`+sessionBody+`,"ee":"AB12C01"}`, true); rec.Code != 200 {
		t.Fatalf("remove: %d", rec.Code)
	}
	if rec := h.do(http.MethodPost, httpPrefix+"/api/reset", `{`+sessionBody+`}`, true); rec.Code != 200 {
		t.Fatalf("reset: %d", rec.Code)
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
