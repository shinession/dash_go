package hcscan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"dash_go/internal/core/i18n"
	"dash_go/internal/version"
)

var catalogBlock = regexp.MustCompile(`(?s)<script type="application/json" id="dg-i18n">(.*?)</script>`)

// doLang sends a request of the signed-in person with the language cookie of the language switch.
func (h *httpEnv) doLang(method, path, body, lang string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(h.cookie)
	req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: lang})
	if method == http.MethodPost {
		req.Header.Set("X-Requested-With", "dash_go")
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func TestPageNeedsALoginAndCarriesTheTextsOfItsLanguage(t *testing.T) {
	h := newHTTPEnv(t)
	rec := h.do(http.MethodGet, httpPrefix+"/", "", false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dash_go/login?next=%2Fdash_go%2Fhc_scan%2F" {
		t.Fatalf("a visitor without a session goes to the login page: %d %q", rec.Code, rec.Header().Get("Location"))
	}

	en := h.do(http.MethodGet, httpPrefix+"/", "", true).Body.String()
	for _, want := range []string{`<html lang="en"`, `<span class="dg-app">HC Scan Assign</span>`, "Scan badge", `data-logout`, `class="dg-user" title="Lead Er (LD01A01)"`,
		`/dash_go/hc_scan/static/app.js?v=` + version.Version, `/dash_go/hc_scan/static/app.css?v=` + version.Version, `id="pagebar"`} {
		if !strings.Contains(en, want) {
			t.Errorf("the English page lacks %q", want)
		}
	}
	m := catalogBlock.FindStringSubmatch(en)
	if m == nil {
		t.Fatal("no catalog block")
	}
	var cat map[string]string
	if err := json.Unmarshal([]byte(m[1]), &cat); err != nil {
		t.Fatal(err)
	}
	for k := range cat {
		if !strings.HasPrefix(k, "ui.") && !strings.HasPrefix(k, "hc.ui.") {
			t.Errorf("%q must stay on the server: the page only needs ui. and hc.ui.", k)
		}
	}
	if cat["hc.ui.ready"] != "Ready. Scan the first badge." || cat["ui.network_error"] == "" {
		t.Errorf("the script needs its texts: %v", cat["hc.ui.ready"])
	}

	zh := h.doLang(http.MethodGet, httpPrefix+"/", "", "zh").Body.String()
	for _, want := range []string{`<html lang="zh"`, `<span class="dg-app">HC 扫码分派</span>`, "扫描工牌", "清空草稿"} {
		if !strings.Contains(zh, want) {
			t.Errorf("the Chinese page lacks %q", want)
		}
	}
	if strings.Contains(zh, "Scan badge") {
		t.Error("nothing of the page may stay in English")
	}
}

// A person who may not work on any process is signed in and gets a page that says so. The list of processes is empty,
// never null: the script reads its length (in version 0.1.0 it was null and the page stayed blank).
func TestMeOfAPersonWithoutPermissionHasAnEmptyProcessList(t *testing.T) {
	h := newHTTPEnv(t)
	h.perms.procs = nil
	rec := h.do(http.MethodGet, httpPrefix+"/api/me", "", true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"processes":[]`) {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
	script, err := os.ReadFile("web/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "me.processes = list(me.processes)") {
		t.Error("the script must not trust the server to send a list")
	}
}

func TestAPIErrorsFollowTheLanguage(t *testing.T) {
	h := newHTTPEnv(t)
	h.perms.procs = nil
	board := httpPrefix + "/api/board?process=Winder&date=2026-11-19&shift=AM"
	if got := decode(t, h.doLang(http.MethodGet, board, "", "en"))["error"]; got != "You don't have permission to work on Winder manpower." {
		t.Errorf("en: %v", got)
	}
	if got := decode(t, h.doLang(http.MethodGet, board, "", "zh"))["error"]; got != "你没有操作 Winder 人力的权限。" {
		t.Errorf("zh: %v", got)
	}
	h.perms.procs = []string{"Winder"}
	rec := h.doLang(http.MethodPost, httpPrefix+"/api/scan", `{`+sessionBody+`,"input":"nonsense"}`, "zh")
	if got := decode(t, rec); rec.Code != 400 || got["code"] != "invalid_id" || got["error"] != "“nonsense”不是有效的工号" {
		t.Errorf("a failed scan in Chinese: %d %v", rec.Code, got)
	}
	rec = h.doLang(http.MethodPost, httpPrefix+"/api/assign", `{`+sessionBody+`,"ee":"AB12C01","machine_id":"Winder_520"}`, "zh")
	if got := decode(t, rec); rec.Code != 404 || got["error"] != "AB12C01 已不在草稿中。请重新扫描工牌。" {
		t.Errorf("%d %v", rec.Code, got)
	}
	rec = h.doLang(http.MethodPost, httpPrefix+"/api/scan", `{"process":"Winder","date":"nope","shift":"AM","input":"AB12C01"}`, "zh")
	if got := decode(t, rec); got["error"] != "日期格式应类似 2026-11-19。" {
		t.Errorf("%v", got)
	}
	if got := decode(t, h.doLang(http.MethodPost, httpPrefix+"/api/scan", `not json`, "en"))["error"]; got != "The request body is not valid JSON." {
		t.Errorf("%v", got)
	}
}

func TestInternalFailuresNameTheStepInTheLanguage(t *testing.T) {
	e := &Error{Status: 500, Code: "internal", Key: "hc.err.internal", Args: i18n.P{"step": stepKey("read draft")}}
	for lang, want := range map[i18n.Lang]string{
		i18n.EN: "The server could not read the draft. Try again; if it keeps failing, contact the administrator.",
		i18n.ZH: "服务器无法读取草稿。请重试；如果一直失败，请联系管理员。",
	} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req = req.WithContext(i18n.WithLang(req.Context(), lang))
		rec := httptest.NewRecorder()
		respond[any](rec, req, nil, e)
		if got := decode(t, rec)["error"]; got != want {
			t.Errorf("%s: %v", lang, got)
		}
		if rec.Code != 500 {
			t.Errorf("status %d", rec.Code)
		}
	}
}

// A draft stores messages, not sentences: what was scanned while the page was English reads Chinese after the switch.
func TestADraftReadsInTheLanguageOfWhoLooksAtIt(t *testing.T) {
	h := newHTTPEnv(t)
	h.do(http.MethodPost, httpPrefix+"/api/scan", `{`+sessionBody+`,"input":"AB12C03"}`, true) // no MTR record: unassigned
	h.do(http.MethodPost, httpPrefix+"/api/scan", `{`+sessionBody+`,"input":"AB12C05"}`, true) // a Slitter member on the Winder page
	board := httpPrefix + "/api/board?process=Winder&date=2026-11-19&shift=AM"
	read := func(lang string) (map[string]string, map[string][]string) {
		got := decode(t, h.doLang(http.MethodGet, board, "", lang))
		reason := map[string]string{}
		flags := map[string][]string{}
		for _, e := range got["entries"].([]any) {
			m := e.(map[string]any)
			ee := m["ee"].(string)
			reason[ee], _ = m["reason"].(string)
			for _, f := range m["flags"].([]any) {
				flags[ee] = append(flags[ee], f.(map[string]any)["text"].(string))
			}
		}
		return reason, flags
	}
	enReason, enFlags := read("en")
	zhReason, zhFlags := read("zh")
	if !strings.Contains(enReason["AB12C03"], "No MTR record (Lv1 or higher) on any Winder machine") || !strings.Contains(zhReason["AB12C03"], "没有 MTR 记录") {
		t.Errorf("reasons:\n en %q\n zh %q", enReason["AB12C03"], zhReason["AB12C03"])
	}
	if got := strings.Join(enFlags["AB12C05"], " | "); !strings.Contains(got, "member of Slitter, not Winder") {
		t.Errorf("en flags: %s", got)
	}
	if got := strings.Join(zhFlags["AB12C05"], " | "); !strings.Contains(got, "属于 Slitter，不是 Winder") || !strings.Contains(got, "2026-11-19") {
		t.Errorf("zh flags: %s", got)
	}
	// the flag of a line without machine says the same as its reason, so the page shows it once
	if len(enFlags["AB12C03"]) != 1 || enFlags["AB12C03"][0] != enReason["AB12C03"] || len(zhFlags["AB12C03"]) != 1 || zhFlags["AB12C03"][0] != zhReason["AB12C03"] {
		t.Errorf("flag and reason must agree in both languages: %v %v", enFlags["AB12C03"], zhFlags["AB12C03"])
	}

	// the draft itself holds no sentence
	stored, err := h.svc.Drafts.Entries(context.Background(), testKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(stored)
	for _, english := range []string{"No MTR record", "member of Slitter", "Not on the"} {
		if strings.Contains(string(raw), english) {
			t.Errorf("the stored draft must not hold the sentence %q:\n%s", english, raw)
		}
	}
	if f := stored["AB12C05"].Flags; len(f) == 0 || f[0].Key != "hc.flag.roster_other_process" || f[0].Args["member_process"] != "Slitter" || f[0].Args["process"] != "Winder" || f[0].Text != "" {
		t.Errorf("stored flags: %+v", f)
	}
}

// Drafts that version 0.1.0 left in Redis (48 hours) have English sentences and no messages. They still display.
func TestDraftsOfVersion010StillDisplay(t *testing.T) {
	h := newHTTPEnv(t)
	legacy := Entry{
		EE: "AB12C03", Name: "Carl Three", Process: "Winder", Source: SourceNone, Shift: "PA", Schedule: "W",
		Reason:    "No MTR record (Lv1 or higher) on any Winder machine. Assign by hand or update the training record.",
		Flags:     []Flag{{Code: FlagShiftMismatch, Severity: SeverityWarn, Text: "Scheduled PM (PM), but scanning in the AM shift"}},
		ScannedAt: testNow, ScannedBy: "Lead Er",
	}
	if err := h.svc.Drafts.PutEntry(context.Background(), testKey, legacy); err != nil {
		t.Fatal(err)
	}
	got := decode(t, h.doLang(http.MethodGet, httpPrefix+"/api/board?process=Winder&date=2026-11-19&shift=AM", "", "zh"))
	e := got["entries"].([]any)[0].(map[string]any)
	if e["reason"] != legacy.Reason {
		t.Errorf("the old sentence is shown as it is: %v", e["reason"])
	}
	var texts []string
	for _, f := range e["flags"].([]any) {
		texts = append(texts, f.(map[string]any)["text"].(string))
	}
	if strings.Join(texts, "|") != legacy.Flags[0].Text+"|"+legacy.Reason {
		t.Errorf("the old flag and the flag of the line without machine: %v", texts)
	}
}

// A leader whose row is full gets the reason with the numbers, in both languages.
func TestLeaderRowFullReasonIsTranslated(t *testing.T) {
	env := newTestEnv(t)
	for _, lang := range []i18n.Lang{i18n.EN, i18n.ZH} {
		ctx := i18n.WithLang(context.Background(), lang)
		for key, wantParts := range map[string][]string{
			"hc.reason.leader_direct_full":  {"2", "3"},
			"hc.reason.leader_history_full": {"Group Leader", "2026-11-18", "2", "3"},
		} {
			m := msg(key, "row", "Group Leader", "date", "2026-11-18", "used", "2", "budget", "3")
			text := say(ctx, m, "")
			if strings.Contains(text, "{") || strings.Contains(text, "hc.") {
				t.Errorf("%s %s: unfilled text %q", lang, key, text)
			}
			for _, part := range wantParts {
				if !strings.Contains(text, part) {
					t.Errorf("%s %s: %q lacks %q", lang, key, text, part)
				}
			}
		}
	}
	_ = env
}
