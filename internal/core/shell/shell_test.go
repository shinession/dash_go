package shell

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
)

const testSecret = "shell-test-secret"

type stubDirectory struct{}

func (stubDirectory) Login(context.Context, string, string) (auth.Identity, error) {
	return auth.Identity{}, auth.ErrInvalidCredentials
}
func (stubDirectory) Identify(context.Context, string) (auth.Identity, error) {
	return auth.Identity{}, auth.ErrInvalidCredentials
}

type stubSSO struct{}

func (stubSSO) LoginURL(token string) string { return "http://sso.example/?token=" + token }
func (stubSSO) Result(context.Context, string) (auth.SSOResult, error) {
	return auth.SSOResult{}, auth.ErrSSOPending
}

type fixture struct {
	t      *testing.T
	sh     *Shell
	mux    http.Handler
	auth   *auth.Service
	signer *auth.Signer
}

func newFixture(t *testing.T, password, sso bool) *fixture {
	t.Helper()
	signer, err := auth.NewSigner(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	set := auth.Settings{BasePath: "/dash_go", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if password || sso {
		set.Directory = stubDirectory{}
	}
	set.PasswordLogin = password
	if sso {
		set.SSO = stubSSO{}
	}
	svc := auth.NewService(signer, auth.NewMemoryNonces(), set)
	sh, err := New(Options{
		Base: "/dash_go", Version: "9.9.9", PhotoURLTemplate: "/api/static/hr/{ee_code}.webp", Auth: svc,
		Modules: []ModuleLink{{Name: "demo", TitleKey: "ui.landing_title", Icon: "scanner", Description: "A demo module", Href: "/dash_go/demo/", HelpHref: "/dash_go/demo/help"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	sh.Mount(mux)
	return &fixture{t: t, sh: sh, mux: i18n.Middleware(mux), auth: svc, signer: signer}
}

func (f *fixture) cookie(ee, name string) *http.Cookie {
	sid, err := f.auth.IssueSession(context.Background(), auth.User{EE: ee, Name: name})
	if err != nil {
		f.t.Fatal(err)
	}
	return &http.Cookie{Name: "dash_go_session", Value: sid}
}

func (f *fixture) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

var catalogBlock = regexp.MustCompile(`(?s)<script type="application/json" id="dg-i18n">(.*?)</script>`)

func pageCatalog(t *testing.T, body string) map[string]string {
	t.Helper()
	m := catalogBlock.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("the page has no catalog block:\n%s", body)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(m[1]), &out); err != nil {
		t.Fatalf("the catalog block is not JSON: %v\n%s", err, m[1])
	}
	return out
}

func TestLandingPageForAVisitorWhoIsNotSignedIn(t *testing.T) {
	f := newFixture(t, true, true)
	rec := f.get("/dash_go/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	for _, want := range []string{
		`<html lang="en"`, `class="dg-logo"`, `aria-label="CIM EDGE"`, `href="/dash_go/"`, // the logo leads home
		`data-theme-toggle`, `class="dg-lang"`, `lang="en" href="/dash_go/lang?set=en&amp;next=%2Fdash_go%2F"`,
		`href="/dash_go/login?next=%2Fdash_go%2F"`, "Sign in",
		`<a class="dg-module" href="/dash_go/demo/"><span class="dg-module-icon">`, `/dash_go/_shell/icons.svg#scanner`, `<strong>dash_go</strong><span>A demo module</span>`, "Version 9.9.9",
		`/dash_go/_shell/theme-init.js?v=9.9.9`, `/dash_go/_shell/shell.css?v=9.9.9`, `/dash_go/_shell/shell.js?v=9.9.9`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "data-logout") || strings.Contains(body, `class="dg-user"`) {
		t.Error("nobody is signed in: no user box, no sign-out button")
	}
	// the theme script has to be in the head, before the stylesheet, and must not be deferred: it prevents the flash
	if i, j := strings.Index(body, "theme-init.js"), strings.Index(body, "shell.css"); i < 0 || j < 0 || i > j || strings.Contains(body[i-40:i+60], "defer") {
		t.Error("theme-init.js must run before the first paint")
	}
}

func TestHeaderShowsTheSignedInUserAndTheSignOutButton(t *testing.T) {
	f := newFixture(t, true, true)
	body := f.get("/dash_go/", f.cookie("AB12C34", "Yan Shi")).Body.String()
	for _, want := range []string{
		`class="dg-user" title="Yan Shi (AB12C34)"`, `data-photo-for="AB12C34"`, `data-name="Yan Shi"`, `>YS</span>`, `<span class="dg-user-name">Yan Shi</span>`,
		`data-logout`, `#logout`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the header lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "dg-btn-small") {
		t.Error("a signed-in person has no sign-in button")
	}
}

func TestHeaderEscapesTheNameOfTheUser(t *testing.T) {
	f := newFixture(t, true, true)
	body := f.get("/dash_go/", f.cookie("AB12C34", `<script>alert(1)</script> "x"`)).Body.String()
	if strings.Contains(body, "<script>alert(1)") {
		t.Errorf("a name is data, not markup:\n%s", body)
	}
	if !strings.Contains(body, html.EscapeString(`<script>alert(1)</script>`)[:20]) {
		t.Errorf("the name should still be shown, escaped:\n%s", body)
	}
}

func TestPagesFollowTheLanguageCookie(t *testing.T) {
	f := newFixture(t, true, true)
	zh := &http.Cookie{Name: i18n.CookieName, Value: "zh"}
	body := f.get("/dash_go/", zh).Body.String()
	for _, want := range []string{`<html lang="zh"`, "登录", "模块", "版本 9.9.9", `class="dg-lang-link is-active" lang="zh"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the Chinese page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `class="dg-lang-link is-active" lang="en"`) {
		t.Error("only the language in use is active")
	}
	// the browser language decides until the person picks one
	req := httptest.NewRequest(http.MethodGet, "/dash_go/", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `<html lang="zh"`) {
		t.Error("a Chinese browser gets the Chinese page")
	}
}

func TestLanguageSwitchSetsTheCookieAndGoesBack(t *testing.T) {
	f := newFixture(t, true, true)
	rec := f.get("/dash_go/lang?set=zh&next=%2Fdash_go%2Fhc_scan%2F")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dash_go/hc_scan/" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "dash_go_lang" || cookies[0].Value != "zh" || cookies[0].Path != "/dash_go/" || cookies[0].MaxAge < 300*24*3600 {
		t.Fatalf("cookie: %+v", cookies)
	}
	// nothing but a page of the service can be the target, and only en and zh can be set
	for _, next := range []string{"https://evil.example/", "//evil.example/", "/other", "/dash_go/../x"} {
		rec := f.get("/dash_go/lang?set=en&next=" + next)
		if rec.Header().Get("Location") != "/dash_go/" {
			t.Errorf("next=%q went to %q", next, rec.Header().Get("Location"))
		}
	}
	rec = f.get("/dash_go/lang?set=klingon&next=%2Fdash_go%2F")
	if len(rec.Result().Cookies()) != 0 || rec.Code != http.StatusSeeOther {
		t.Errorf("an unknown language is ignored: %d %v", rec.Code, rec.Result().Cookies())
	}
}

func TestLanguageLinkComesBackToTheSamePageWithItsQuery(t *testing.T) {
	f := newFixture(t, true, true)
	body := f.get("/dash_go/login?next=%2Fdash_go%2Fhc_scan%2F&error=link_used").Body.String()
	if !strings.Contains(body, `href="/dash_go/lang?set=zh&amp;next=%2Fdash_go%2Flogin%3Fnext%3D%252Fdash_go%252Fhc_scan%252F%26error%3Dlink_used"`) {
		t.Errorf("the switch must lead back to this very page:\n%s", body)
	}
}

func TestLoginPageOffersTheEnabledWaysToSignIn(t *testing.T) {
	cases := []struct {
		name          string
		password, sso bool
		has, hasNot   []string
	}{
		{"both", true, true, []string{`id="login-form"`, `id="sso-box"`, "or", `data-next="/dash_go/"`}, []string{`class="dg-alert warn"`}},
		{"password only", true, false, []string{`id="login-form"`}, []string{`id="sso-box"`, `class="dg-alert warn"`}},
		{"windows only", false, true, []string{`id="sso-box"`}, []string{`id="login-form"`, `class="dg-alert warn"`}},
		{"neither", false, false, []string{`class="dg-alert warn">Signing in on this page is not set up on this server`}, []string{`id="login-form"`, `id="sso-box"`}},
	}
	for _, c := range cases {
		f := newFixture(t, c.password, c.sso)
		body := f.get("/dash_go/login").Body.String()
		for _, w := range c.has {
			if !strings.Contains(body, w) {
				t.Errorf("%s: the login page lacks %q", c.name, w)
			}
		}
		for _, w := range c.hasNot {
			if strings.Contains(body, w) {
				t.Errorf("%s: the login page must not contain %q", c.name, w)
			}
		}
		if strings.Contains(body, `class="dg-btn dg-btn-small"`) {
			t.Errorf("%s: the sign-in button of the header is not shown on the login page itself", c.name)
		}
	}
}

func TestLoginFormNeverPostsPasswordsToAnAddress(t *testing.T) {
	f := newFixture(t, true, false)
	body := f.get("/dash_go/login").Body.String()
	if !strings.Contains(body, `<form class="dg-form" id="login-form" method="post"`) || strings.Contains(body, `method="get"`) {
		t.Errorf("without the script the form must not put the password into the address:\n%s", body)
	}
	if !strings.Contains(body, "/dash_go/_shell/login.js") {
		t.Error("the login page needs its script")
	}
}

func TestLoginPageNotices(t *testing.T) {
	f := newFixture(t, true, true)
	cases := map[string]string{
		"?signed_out=1":       "You have been signed out.",
		"?expired=1":          "Your login has expired. Sign in again.",
		"?error=link_used":    "The login link from Dash was already used.",
		"?error=link_expired": "The login link from Dash has expired.",
		"?error=link_invalid": "The login link from Dash is not valid.",
		"?error=unavailable":  "The login service is temporarily unavailable.",
	}
	for q, want := range cases {
		if body := f.get("/dash_go/login" + q).Body.String(); !strings.Contains(body, want) {
			t.Errorf("%s: no %q", q, want)
		}
	}
	if body := f.get("/dash_go/login?error=<script>").Body.String(); strings.Contains(body, "<script>") && strings.Contains(body, "error=<script>") {
		t.Error("an unknown error code is ignored, never echoed")
	}
	zh := f.get("/dash_go/login?signed_out=1", &http.Cookie{Name: i18n.CookieName, Value: "zh"}).Body.String()
	if !strings.Contains(zh, "你已退出登录。") {
		t.Errorf("the notice follows the language:\n%s", zh)
	}
}

func TestLoginPageSendsASignedInPersonOnButLetsASignedOutPersonSeeTheNotice(t *testing.T) {
	f := newFixture(t, true, true)
	rec := f.get("/dash_go/login?next=%2Fdash_go%2Fhc_scan%2F", f.cookie("AB12C34", "N"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dash_go/hc_scan/" {
		t.Errorf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = f.get("/dash_go/login?next=https://evil.example/", f.cookie("AB12C34", "N"))
	if rec.Header().Get("Location") != "/dash_go/" {
		t.Errorf("next is never an open redirect: %q", rec.Header().Get("Location"))
	}
	if rec := f.get("/dash_go/login?signed_out=1", f.cookie("AB12C34", "N")); rec.Code != 200 {
		t.Errorf("right after signing out the page shows the notice: %d", rec.Code)
	}
}

func TestPageOnlySendsThePrefixesItAsksFor(t *testing.T) {
	f := newFixture(t, true, true)
	cat := pageCatalog(t, f.get("/dash_go/login").Body.String())
	for k := range cat {
		if !strings.HasPrefix(k, "ui.") && !strings.HasPrefix(k, "login.") {
			t.Errorf("the key %q must stay on the server", k)
		}
	}
	for _, want := range []string{"ui.sign_in", "login.sso_waiting", "login.submitting", "ui.network_error"} {
		if cat[want] == "" {
			t.Errorf("the script of the login page needs %q", want)
		}
	}
	landing := pageCatalog(t, f.get("/dash_go/").Body.String())
	for k := range landing {
		if strings.HasPrefix(k, "login.") || strings.HasPrefix(k, "err.") {
			t.Errorf("the landing page sends %q to the browser", k)
		}
	}
	zh := pageCatalog(t, f.get("/dash_go/login", &http.Cookie{Name: i18n.CookieName, Value: "zh"}).Body.String())
	if zh["login.submit"] != "登录" {
		t.Errorf("the catalog of the page is in the language of the page: %q", zh["login.submit"])
	}
}

func TestCatalogBlockCannotBreakOutOfItsScriptTag(t *testing.T) {
	f := newFixture(t, true, true)
	page, err := f.sh.NewPage(PageSpec{TitleKey: "ui.landing_title"}, fstest.MapFS{"p.html": {Data: []byte(`{{define "content"}}x{{end}}`)}}, "p.html")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	page.Render(rec, httptest.NewRequest(http.MethodGet, "/dash_go/x", nil), 200, nil)
	// every catalog text that contains markup characters is escaped inside the block
	if m := catalogBlock.FindStringSubmatch(rec.Body.String()); m == nil || strings.Contains(m[1], "</script") {
		t.Errorf("the block: %v", m)
	}
}

func TestModulePagesBringTheirOwnContentStylesAndScripts(t *testing.T) {
	f := newFixture(t, true, true)
	files := fstest.MapFS{"web/index.html": {Data: []byte(`{{define "content"}}<main id="mine">{{.T "login.submit"}} {{.Data}} {{.Base}}</main>{{end}}`)}}
	page, err := f.sh.NewPage(PageSpec{
		TitleKey: "ui.landing_title", AppKey: "ui.sign_out", AppIcon: "scanner", RequireLogin: true,
		Styles: []string{"/dash_go/demo/static/app.css"}, Scripts: []string{"/dash_go/demo/static/app.js"}, ClientPrefixes: []string{"login."},
	}, files, "web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	f.mux = i18n.Middleware(func() http.Handler {
		mux := http.NewServeMux()
		mux.Handle("GET /dash_go/demo/{$}", page.Handler(func(*http.Request) any { return "payload" }))
		return mux
	}())

	// a visitor without a session is sent to the login page and comes back afterwards
	rec := f.get("/dash_go/demo/")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dash_go/login?next=%2Fdash_go%2Fdemo%2F" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
	res := f.get("/dash_go/demo/", f.cookie("AB12C34", "Yan Shi"))
	body := res.Body.String()
	var sessionCookies int
	for _, ck := range res.Result().Cookies() {
		if ck.Name == "dash_go_session" {
			sessionCookies++
			if ck.MaxAge != 24*3600 {
				t.Errorf("expected refreshed 24h session cookie, got MaxAge=%d", ck.MaxAge)
			}
		}
	}
	if sessionCookies != 1 {
		t.Errorf("expected exactly 1 refreshed session cookie per page render, got %d", sessionCookies)
	}
	for _, want := range []string{
		`<main id="mine">Sign in payload /dash_go</main>`, `<span class="dg-app"><svg class="dg-icon"`, `/dash_go/_shell/icons.svg#scanner`, `Sign out</span>`, `<title>dash_go · CIM EDGE</title>`,
		`href="/dash_go/demo/static/app.css?v=9.9.9"`, `src="/dash_go/demo/static/app.js?v=9.9.9"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the module page lacks %q:\n%s", want, body)
		}
	}
	// the script of the module comes after the one of the shell, so it can use window.dashGo
	if strings.Index(body, "shell.js") > strings.Index(body, "demo/static/app.js") {
		t.Error("shell.js must come first")
	}
}

// The pages of a module can go back to the root of the server they are on (the page of Dash, whatever the address of
// the server is), so the link has no host. The landing page and the login page have nothing to go back from.
func TestModulePagesHaveABackButtonToTheRootOfTheServer(t *testing.T) {
	f := newFixture(t, true, true)
	page, err := f.sh.NewPage(PageSpec{TitleKey: "ui.landing_title", AppKey: "ui.landing_modules"}, fstest.MapFS{"p.html": {Data: []byte(`{{define "content"}}x{{end}}`)}}, "p.html")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	f.sh.Mount(mux)
	mux.Handle("GET /dash_go/demo/{$}", page.Handler(func(*http.Request) any { return nil }))
	f.mux = i18n.Middleware(mux)

	body := f.get("/dash_go/demo/").Body.String()
	for _, want := range []string{
		`<span class="dg-app">Modules</span>`,
		`<a class="dg-btn dg-btn-small dg-back" href="/" title="Back to the home page of this server">`,
		`/dash_go/_shell/icons.svg#back`, `Back</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the module page lacks %q:\n%s", want, body)
		}
	}
	if i, j := strings.Index(body, `class="dg-app"`), strings.Index(body, `dg-back`); i < 0 || j < i {
		t.Error("the button comes after the name of the module")
	}
	zh := f.get("/dash_go/demo/", &http.Cookie{Name: i18n.CookieName, Value: "zh"}).Body.String()
	if !strings.Contains(zh, `title="返回本服务器的首页"`) || !strings.Contains(zh, "返回</a>") {
		t.Errorf("the button is in the language of the page:\n%s", zh)
	}
	for _, path := range []string{"/dash_go/", "/dash_go/login"} {
		if strings.Contains(f.get(path).Body.String(), "dg-back") {
			t.Errorf("%s has a back button", path)
		}
	}
}

func TestNewPageNeedsAContentTemplate(t *testing.T) {
	f := newFixture(t, true, true)
	if _, err := f.sh.NewPage(PageSpec{}, fstest.MapFS{"x.html": {Data: []byte(`nothing`)}}, "x.html"); err == nil || !strings.Contains(err.Error(), "content") {
		t.Errorf("%v", err)
	}
	if _, err := f.sh.NewPage(PageSpec{}, fstest.MapFS{}, "missing.html"); err == nil {
		t.Error("a missing file must be an error")
	}
}

func TestStaticFilesOfTheShell(t *testing.T) {
	f := newFixture(t, true, true)
	for path, ct := range map[string]string{
		"/dash_go/_shell/shell.css":     "text/css",
		"/dash_go/_shell/shell.js":      "text/javascript",
		"/dash_go/_shell/tour.js":       "text/javascript",
		"/dash_go/_shell/theme-init.js": "text/javascript",
		"/dash_go/_shell/login.js":      "text/javascript",
		"/dash_go/_shell/icons.svg":     "image/svg+xml",
		"/dash_go/_shell/favicon.svg":   "image/svg+xml",
	} {
		rec := f.get(path)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), ct) || rec.Header().Get("Cache-Control") != "no-cache" || rec.Body.Len() == 0 {
			t.Errorf("%s: %d %q %q", path, rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
		}
	}
	if rec := f.get("/dash_go/_shell/nothing.js"); rec.Code != 404 {
		t.Errorf("%d", rec.Code)
	}
	if rec := f.get("/dash_go/_shell/../shell.go"); rec.Code == 200 {
		t.Error("only the files of web/assets are served")
	}
	icons := f.get("/dash_go/_shell/icons.svg").Body.String()
	for _, sym := range []string{`id="scanner"`, `id="budget"`, `id="weblog"`} {
		if !strings.Contains(icons, sym) {
			t.Errorf("icons.svg lacks %s", sym)
		}
	}
	js := f.get("/dash_go/_shell/shell.js").Body.String()
	if !strings.Contains(js, "picker:") || !strings.Contains(js, "'hc.process'") {
		t.Error("shell.js must expose the shared hc.process picker helper")
	}
}

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{"Yan Shi": "YS", "yan": "Y", "": "?", "  ": "?", "陈 小明": "陈小", "A B C": "AB", "élan vital": "ÉV"} {
		if got := initials(in); got != want {
			t.Errorf("initials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewRequiresTheAuthService(t *testing.T) {
	if _, err := New(Options{Base: "/dash_go"}); err == nil {
		t.Error("the shell shows the user, it needs the auth service")
	}
}

func TestHeaderPlacesDocsAndGuideButtonsBetweenLanguageAndUser(t *testing.T) {
	f := newFixture(t, true, true)

	// Landing page when signed in: Docs and Guide sit between language switch and user box.
	signedIn := f.get("/dash_go/", f.cookie("AB12C34", "Yan Shi")).Body.String()
	for _, want := range []string{
		`class="dg-helptools"`,
		`href="/dash_go/help" target="_blank" rel="noopener" data-docs-link`,
		`/dash_go/_shell/icons.svg#book`,
		`data-tour-start data-tour-auto="landing"`,
		`/dash_go/_shell/icons.svg#explore`,
		`/dash_go/_shell/tour.js?v=9.9.9`,
		`data-tour="tour.landing_modules"`,
		`data-tour="tour.theme"`, `data-tour="tour.lang"`, `data-tour="tour.help"`, `data-tour="tour.account"`,
	} {
		if !strings.Contains(signedIn, want) {
			t.Errorf("landing page lacks %q", want)
		}
	}
	iLang := strings.Index(signedIn, `class="dg-lang"`)
	iHelp := strings.Index(signedIn, `class="dg-helptools"`)
	iUser := strings.Index(signedIn, `class="dg-userbox"`)
	if !(iLang >= 0 && iHelp > iLang && iUser > iHelp) {
		t.Errorf("expected order dg-lang (%d) < dg-helptools (%d) < dg-userbox (%d)", iLang, iHelp, iUser)
	}
	if strings.Index(signedIn, "shell.js") > strings.Index(signedIn, "tour.js") {
		t.Error("shell.js must load before tour.js")
	}

	// Landing page when signed out: Docs and Guide sit between language switch and Sign in link.
	signedOut := f.get("/dash_go/").Body.String()
	iLang = strings.Index(signedOut, `class="dg-lang"`)
	iHelp = strings.Index(signedOut, `class="dg-helptools"`)
	iSignIn := strings.Index(signedOut, `href="/dash_go/login?next=%2Fdash_go%2F"`)
	if !(iLang >= 0 && iHelp > iLang && iSignIn > iHelp) {
		t.Errorf("expected order dg-lang (%d) < dg-helptools (%d) < sign-in (%d)", iLang, iHelp, iSignIn)
	}
	if !strings.Contains(signedOut, `data-tour="tour.signin"`) || strings.Contains(signedOut, `data-tour="tour.account"`) {
		t.Error("signed-out header should mark signin, not account")
	}

	// Chinese tooltips and tour texts follow the language cookie.
	zh := f.get("/dash_go/", &http.Cookie{Name: i18n.CookieName, Value: "zh"}).Body.String()
	for _, want := range []string{
		`title="文档：在新标签页中打开本页使用手册"`,
		`title="导览：逐步介绍本页面"`,
		`data-tour-title="文档与页面导览"`,
	} {
		if !strings.Contains(zh, want) {
			t.Errorf("Chinese landing page lacks %q", want)
		}
	}

	// Login page hides Docs, Guide, tour.js and tour markup.
	login := f.get("/dash_go/login").Body.String()
	for _, forbid := range []string{"dg-helptools", "data-docs-link", "data-tour-start", "tour.js", "data-tour="} {
		if strings.Contains(login, forbid) {
			t.Errorf("login page must not contain %q", forbid)
		}
	}
}

func TestCoreHelpPageHasBothLanguagesAndMatchesSections(t *testing.T) {
	f := newFixture(t, true, true)
	recEN := f.get("/dash_go/help")
	if recEN.Code != http.StatusOK {
		t.Fatalf("GET /dash_go/help = %d", recEN.Code)
	}
	en := recEN.Body.String()
	zh := f.get("/dash_go/help", &http.Cookie{Name: i18n.CookieName, Value: "zh"}).Body.String()
	if en == zh {
		t.Fatal("Chinese help page must differ from English")
	}
	for _, want := range []string{`<title>User guide · CIM EDGE</title>`, `href="/dash_go/demo/help"`, `data-tour="tour.help_toc"`, `data-tour="tour.help_body"`, `data-docs-link`, `data-tour-start`} {
		if !strings.Contains(en, want) {
			t.Errorf("English help page lacks %q", want)
		}
	}
	for _, want := range []string{`<title>使用手册 · CIM EDGE</title>`, `href="/dash_go/demo/help"`, `data-tour-title="目录"`, `data-tour-title="手册正文"`} {
		if !strings.Contains(zh, want) {
			t.Errorf("Chinese help page lacks %q", want)
		}
	}
	for _, id := range []string{`id="overview"`, `id="signin"`, `id="interface"`, `id="guide"`, `id="faq"`} {
		if !strings.Contains(en, id) || !strings.Contains(zh, id) {
			t.Errorf("help page missing section %s in one language", id)
		}
	}
	// Opening the documentation page must not auto-start the tour overlay.
	if strings.Contains(en, "data-tour-auto") {
		t.Error("help page should not auto-start tour on open")
	}
	// Server-only tour.* keys must stay out of #dg-i18n JSON.
	for k := range pageCatalog(t, en) {
		if strings.HasPrefix(k, "tour.") {
			t.Errorf("tour key %q must not be sent inside #dg-i18n", k)
		}
	}
}
