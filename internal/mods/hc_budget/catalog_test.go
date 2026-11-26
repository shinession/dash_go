package hcbudget

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"dash_go/internal/core/i18n"
)

func TestCatalogHasTheSameTextsInBothLanguages(t *testing.T) {
	if p := i18n.CheckParity(webFS, "i18n"); len(p) != 0 {
		t.Fatalf("the hc_budget catalog is inconsistent:\n%s", strings.Join(p, "\n"))
	}
}

type sourceCall struct {
	key  string
	args []string
	at   string
}

func goSources(t *testing.T) map[string]*ast.File {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = parsed
	}
	return out
}

func stringLit(e ast.Expr) (string, bool) {
	l, ok := e.(*ast.BasicLit)
	if !ok || l.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(l.Value)
	return s, err == nil
}

func paramNames(e ast.Expr) []string {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var names []string
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if s, ok := stringLit(kv.Key); ok {
				names = append(names, s)
			}
		}
	}
	return names
}

func findCalls(t *testing.T) []sourceCall {
	t.Helper()
	var calls []sourceCall
	for name, f := range goSources(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := c.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "newError" && len(c.Args) > 1 {
					if code, ok := stringLit(c.Args[1]); ok {
						sc := sourceCall{key: "hb.err." + code, at: name}
						if len(c.Args) > 2 {
							sc.args = paramNames(c.Args[2])
						}
						calls = append(calls, sc)
					}
				}
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok && x.Name == "i18n" && fn.Sel.Name == "T" && len(c.Args) > 1 {
					if key, ok := stringLit(c.Args[1]); ok {
						sc := sourceCall{key: key, at: name}
						if len(c.Args) > 2 {
							sc.args = paramNames(c.Args[2])
						}
						calls = append(calls, sc)
					}
				}
			}
			return true
		})
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].key < calls[j].key })
	return calls
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func hasStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestEveryMessageTheGoCodeNamesExistsAndGetsItsValues(t *testing.T) {
	calls := findCalls(t)
	if len(calls) < 15 {
		t.Fatalf("only %d calls found", len(calls))
	}
	std := i18n.Std()
	for _, c := range calls {
		text, ok := std.Message(i18n.EN, c.key)
		if !ok {
			t.Errorf("%s: the key %q is not in the catalog", c.at, c.key)
			continue
		}
		need := i18n.Placeholders(text)
		have := sorted(c.args)
		for _, h := range have {
			if !hasStr(need, h) {
				t.Errorf("%s: %q is given %q but text has no {%s}: %q", c.at, c.key, h, h, text)
			}
		}
		for _, n := range need {
			if !hasStr(have, n) {
				t.Errorf("%s: %q needs {%s} but code passes none: %q", c.at, c.key, n, text)
			}
		}
	}
}

func TestEveryStepOfAFailedRequestHasAText(t *testing.T) {
	std := i18n.Std()
	steps := 0
	for name, f := range goSources(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "internal" && len(c.Args) == 1 {
				if what, ok := stringLit(c.Args[0]); ok {
					steps++
					if !std.Has(stepKey(what)) {
						t.Errorf("%s: s.internal(%q) needs %q", name, what, stepKey(what))
					}
				}
			}
			return true
		})
	}
	if steps < 1 {
		t.Fatalf("expected at least 1 s.internal call")
	}
}

var (
	scriptCall   = regexp.MustCompile(`\bt\(\s*'([a-z_.0-9]+)'\s*(?:,\s*\{([^}]*)\})?`)
	pageCall     = regexp.MustCompile(`\{\{\.T "([a-z_.0-9]+)"`)
	paramKey     = regexp.MustCompile(`(\w+)\s*:`)
	tourStepCall = regexp.MustCompile(`\{\{\.Tour "([a-z0-9_.]*[a-z0-9])"\}\}`)
)

func TestEveryTextTheScriptAndThePageUseExistsAndGetsItsValues(t *testing.T) {
	std := i18n.Std()
	js, err := os.ReadFile("web/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, m := range scriptCall.FindAllStringSubmatch(string(js), -1) {
		found++
		text, ok := std.Message(i18n.EN, m[1])
		if !ok {
			t.Errorf("app.js: the key %q is not in the catalog", m[1])
			continue
		}
		var given []string
		for _, p := range paramKey.FindAllStringSubmatch(m[2], -1) {
			given = append(given, p[1])
		}
		need := i18n.Placeholders(text)
		if strings.Join(sorted(given), ",") != strings.Join(need, ",") {
			t.Errorf("app.js: t(%q) gives %v but text needs %v: %q", m[1], sorted(given), need, text)
		}
	}
	if found < 25 {
		t.Fatalf("only %d t() calls found in app.js", found)
	}
	html, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range pageCall.FindAllStringSubmatch(string(html), -1) {
		if !std.Has(m[1]) {
			t.Errorf("index.html: the key %q is not in the catalog", m[1])
		}
	}
}

func TestEveryTextOfThePageIsUsed(t *testing.T) {
	js, _ := os.ReadFile("web/static/app.js")
	html, _ := os.ReadFile("web/index.html")
	source := string(js) + string(html)
	for _, k := range i18n.Std().Keys(i18n.EN) {
		if strings.HasPrefix(k, "hb.ui.") && !strings.Contains(source, k) {
			t.Errorf("%s is not used by the page or its script: remove it from the catalog", k)
		}
	}
}

func TestEveryTextOfTheServerIsUsed(t *testing.T) {
	used := map[string]bool{}
	keyRe := regexp.MustCompile(`^hb\.[a-z_]+(\.[a-z_0-9]+)+$`)
	for _, c := range findCalls(t) {
		used[c.key] = true
	}
	for _, f := range goSources(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if v, ok := stringLit(x); ok && keyRe.MatchString(v) {
					used[v] = true
				}
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "internal" && len(x.Args) == 1 {
					if what, ok := stringLit(x.Args[0]); ok {
						used[stepKey(what)] = true
					}
				}
			}
			return true
		})
	}
	var unused []string
	for _, k := range i18n.Std().Keys(i18n.EN) {
		if strings.HasPrefix(k, "hb.") && !strings.HasPrefix(k, "hb.ui.") && !strings.HasPrefix(k, "hb.tour.") && k != "hb.title" && k != "hb.description" && !used[k] {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("unused server keys in hc_budget catalog: %v", unused)
	}
}

func TestEveryTourStepOfThePageHasTextsAndNoTourTextIsUnused(t *testing.T) {
	std := i18n.Std()
	used := map[string]bool{}
	for _, file := range []string{"web/index.html", "web/help.html"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range tourStepCall.FindAllStringSubmatch(string(raw), -1) {
			for _, suffix := range []string{".title", ".text"} {
				k := m[1] + suffix
				used[k] = true
				if !std.Has(k) {
					t.Errorf("%s: tour step %q lacks catalog key %q", file, m[1], k)
				}
			}
		}
	}
	if len(used) < 12 {
		t.Fatalf("only %d tour keys found in hc_budget templates", len(used))
	}
	var unused []string
	for _, k := range std.Keys(i18n.EN) {
		if strings.HasPrefix(k, "hb.tour.") && !used[k] {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("unused hb.tour keys: %v", unused)
	}
}

func TestAClientTextNeverHoldsAServerOnlyMessage(t *testing.T) {
	for k := range i18n.Std().Subset(i18n.EN, pageSpec("/x").ClientPrefixes...) {
		if !strings.HasPrefix(k, "hb.ui.") && !strings.HasPrefix(k, "ui.") {
			t.Errorf("%s would be sent to the browser", k)
		}
	}
}
