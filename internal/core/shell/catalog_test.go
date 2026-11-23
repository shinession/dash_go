package shell

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"dash_go/internal/core/i18n"
)

// Every text of the core catalog (ui., login., err.) must exist where the code, the templates and the scripts ask for
// it, and no text may be left in the catalog that nothing asks for.

var (
	coreKey    = regexp.MustCompile(`"((?:ui|login|err)\.[a-z0-9_.]*[a-z0-9])"`)
	templateT  = regexp.MustCompile(`\.T "((?:ui|login|err)\.[a-z0-9_.]*[a-z0-9])"`)
	tourCall   = regexp.MustCompile(`\.Tour "((?:tour)\.[a-z0-9_.]*[a-z0-9])"`)
	scriptT    = regexp.MustCompile(`\bt\('((?:ui|login|err)\.[a-z0-9_.]*[a-z0-9])'`)
	ssoStatus  = []string{"popup", "no_user", "denied", "mismatch", "expired", "busy", "timeout", "cancelled", "error", "unavailable", "waiting"}
	scriptFile = "web/assets/login.js"
)

// readAll reads the files that match the patterns, under slash separated names.
func readAll(t *testing.T, patterns ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range patterns {
		files, err := filepath.Glob(p)
		if err != nil || len(files) == 0 {
			t.Fatalf("no files for %q", p)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			out[filepath.ToSlash(f)] = string(b)
		}
	}
	return out
}

func TestCoreCatalogKeysAreUsedAndExist(t *testing.T) {
	std := i18n.Std()
	// the core, and the modules (which use ui.ok, ui.cancel and the session texts of the shell)
	sources := readAll(t,
		"shell.go", "web/templates/*.html", "web/assets/*.js",
		"../auth/*.go", "../server/*.go", "../i18n/lang.go",
		"../../mods/*/web/static/*.js", "../../mods/*/web/*.html",
	)
	used := map[string]bool{}
	for file, src := range sources {
		for _, re := range []*regexp.Regexp{coreKey, templateT, scriptT} {
			for _, m := range re.FindAllStringSubmatch(src, -1) {
				used[m[1]] = true
				if !std.Has(m[1]) {
					t.Errorf("%s: the key %q is not in the catalog", file, m[1])
				}
			}
		}
	}
	// login.js builds the key of a status of the Windows sign-in: d.t('login.sso_' + key)
	if !strings.Contains(sources[scriptFile], "'login.sso_' + key") {
		t.Error("login.js no longer builds the key of the status: update this test")
	}
	for _, status := range ssoStatus {
		used["login.sso_"+status] = true
		if !std.Has("login.sso_" + status) {
			t.Errorf("the Windows sign-in can end with %q: login.sso_%s is missing", status, status)
		}
	}

	var unused []string
	for _, k := range std.Keys(i18n.EN) {
		if (strings.HasPrefix(k, "ui.") || strings.HasPrefix(k, "login.") || strings.HasPrefix(k, "err.")) && !used[k] {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("these texts of the core catalog are used by nothing: %v", unused)
	}
}

// The script of the login page asks for login.* and ui.* only; both are sent with the page.
func TestLoginPageSendsEveryTextItsScriptUses(t *testing.T) {
	f := newFixture(t, true, true)
	cat := pageCatalog(t, f.get("/dash_go/login").Body.String())
	login := readAll(t, scriptFile)[scriptFile]
	asked := regexp.MustCompile(`\bd\.t\('((?:ui|login)\.[a-z0-9_.]*[a-z0-9])'`).FindAllStringSubmatch(login, -1)
	if len(asked) < 3 {
		t.Fatalf("only %d texts found in login.js", len(asked))
	}
	for _, m := range asked {
		if cat[m[1]] == "" {
			t.Errorf("login.js uses %q but the login page does not send it", m[1])
		}
	}
	for _, status := range ssoStatus {
		if cat["login.sso_"+status] == "" {
			t.Errorf("the login page must send login.sso_%s", status)
		}
	}
}

func TestCoreTourCatalogKeysAreUsedAndExist(t *testing.T) {
	std := i18n.Std()
	templates := readAll(t, "web/templates/*.html")
	used := map[string]bool{}
	for file, src := range templates {
		for _, m := range tourCall.FindAllStringSubmatch(src, -1) {
			for _, suffix := range []string{".title", ".text"} {
				k := m[1] + suffix
				used[k] = true
				if !std.Has(k) {
					t.Errorf("%s: tour step %q lacks catalog key %q", file, m[1], k)
				}
			}
		}
	}
	if len(used) < 10 {
		t.Fatalf("only %d tour keys found in shell templates", len(used))
	}
	var unused []string
	for _, k := range std.Keys(i18n.EN) {
		if strings.HasPrefix(k, "tour.") && !used[k] {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("these tour texts of the core catalog are used by nothing: %v", unused)
	}
}

func TestTourScriptUsesOnlyKeysSentToTheBrowser(t *testing.T) {
	f := newFixture(t, true, true)
	cat := pageCatalog(t, f.get("/dash_go/").Body.String())
	src := readAll(t, "web/assets/tour.js")["web/assets/tour.js"]
	asked := scriptT.FindAllStringSubmatch(src, -1)
	if len(asked) < 5 {
		t.Fatalf("only %d texts found in tour.js", len(asked))
	}
	for _, m := range asked {
		if cat[m[1]] == "" {
			t.Errorf("tour.js uses %q but the page does not send it", m[1])
		}
	}
}
