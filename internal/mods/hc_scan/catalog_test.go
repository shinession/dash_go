package hcscan

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

// These tests keep the catalog and the code in step: both languages have the same texts and placeholders, every key
// the Go code, the script and the page use exists, and the values the code passes are the placeholders of the text.
// A typo would otherwise show as "hc.flag.x" or "{machine}" on the screen of a shift leader.

func TestCatalogHasTheSameTextsInBothLanguages(t *testing.T) {
	if p := i18n.CheckParity(webFS, "i18n"); len(p) != 0 {
		t.Fatalf("the hc_scan catalog is inconsistent:\n%s", strings.Join(p, "\n"))
	}
}

// sourceCall is a call of msg, i18n.T or newError found in the Go code, with the names of the values it passes.
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

// paramNames reads the keys of an i18n.P{...} literal.
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
				switch fn.Name {
				case "msg": // msg("key", "name", value, ...)
					if len(c.Args) > 0 {
						if key, ok := stringLit(c.Args[0]); ok {
							sc := sourceCall{key: key, at: name}
							for i := 1; i < len(c.Args); i += 2 {
								if s, ok := stringLit(c.Args[i]); ok {
									sc.args = append(sc.args, s)
								}
							}
							calls = append(calls, sc)
						}
					}
				case "newError": // newError(status, "code", i18n.P{...})
					if len(c.Args) > 1 {
						if code, ok := stringLit(c.Args[1]); ok {
							sc := sourceCall{key: "hc.err." + code, at: name}
							if len(c.Args) > 2 {
								sc.args = paramNames(c.Args[2])
							}
							calls = append(calls, sc)
						}
					}
				case "newErrorKey": // newErrorKey(status, "code", "key", i18n.P{...})
					if len(c.Args) > 3 {
						if key, ok := stringLit(c.Args[2]); ok {
							calls = append(calls, sourceCall{key: key, args: paramNames(c.Args[3]), at: name})
						}
					}
				}
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok && x.Name == "i18n" && fn.Sel.Name == "T" && len(c.Args) > 1 { // i18n.T(ctx, "key", i18n.P{...})
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

// A few messages get values from two places: the roster reasons of directory.go get the process and the date in
// flags.go, and the leader reasons get the row and the numbers when the row is full.
var completedLater = map[string][]string{
	"hc.flag.roster_resigned":       {"process", "date"},
	"hc.flag.roster_no_process":     {"process", "date"},
	"hc.flag.roster_other_process":  {"process", "date"},
	"hc.flag.roster_direct_unknown": {"process", "date"},
	"hc.flag.roster_direct_type":    {"process", "date"},
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestEveryMessageTheGoCodeNamesExistsAndGetsItsValues(t *testing.T) {
	calls := findCalls(t)
	if len(calls) < 40 {
		t.Fatalf("only %d calls found: the test no longer sees the code", len(calls))
	}
	std := i18n.Std()
	for _, c := range calls {
		text, ok := std.Message(i18n.EN, c.key)
		if !ok {
			t.Errorf("%s: the key %q is not in the catalog", c.at, c.key)
			continue
		}
		need := i18n.Placeholders(text)
		have := append(sorted(c.args), completedLater[c.key]...)
		have = sorted(have)
		for _, h := range have {
			if !contains(need, h) {
				t.Errorf("%s: %q is given the value %q but its text has no {%s}: %q", c.at, c.key, h, h, text)
			}
		}
		for _, n := range need {
			if !contains(have, n) {
				t.Errorf("%s: %q needs {%s} but the code gives no value for it: %q", c.at, c.key, n, text)
			}
		}
	}
}

func TestEveryKeyInTheGoCodeIsInTheCatalog(t *testing.T) {
	key := regexp.MustCompile(`^hc\.[a-z_]+(\.[a-z_0-9]+)+$`)
	std := i18n.Std()
	seen := 0
	for name, f := range goSources(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := n.(*ast.BasicLit); ok {
				if v, ok := stringLit(s); ok && key.MatchString(v) {
					seen++
					if !std.Has(v) {
						t.Errorf("%s: the key %q is not in the catalog", name, v)
					}
				}
			}
			return true
		})
	}
	if seen < 40 {
		t.Fatalf("only %d keys found", seen)
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
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "internal" && len(c.Args) == 2 {
				if what, ok := stringLit(c.Args[0]); ok {
					steps++
					if !std.Has(stepKey(what)) {
						t.Errorf("%s: s.internal(%q) needs the text %q", name, what, stepKey(what))
					}
				}
			}
			return true
		})
	}
	if steps < 8 {
		t.Fatalf("only %d steps found", steps)
	}
}

// The leader reasons that have no room get a _full variant of the key at run time (see placeLeader).
func TestLeaderReasonsHaveFullVariants(t *testing.T) {
	std := i18n.Std()
	for _, k := range []string{"hc.reason.leader_direct", "hc.reason.leader_history"} {
		if !std.Has(k) || !std.Has(k+"_full") {
			t.Errorf("%s and %s_full must both exist", k, k)
		}
		full, _ := std.Message(i18n.EN, k+"_full")
		for _, want := range []string{"row", "used", "budget"} {
			if !contains(i18n.Placeholders(full), want) {
				t.Errorf("%s_full needs {%s}", k, want)
			}
		}
	}
}

var (
	scriptCall = regexp.MustCompile(`\bt\(\s*'([a-z_.0-9]+)'\s*(?:,\s*\{([^}]*)\})?`)
	pageCall   = regexp.MustCompile(`\{\{\.T "([a-z_.0-9]+)"`)
	paramKey   = regexp.MustCompile(`(\w+)\s*:`)
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
			t.Errorf("app.js: t(%q) gives %v but the text needs %v: %q", m[1], sorted(given), need, text)
		}
	}
	if found < 50 {
		t.Fatalf("only %d texts found in app.js", found)
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
		if strings.HasPrefix(k, "hc.ui.") && !strings.Contains(source, k) {
			t.Errorf("%s is not used by the page or its script: remove it from the catalog", k)
		}
	}
}

// The texts of the server (errors, warnings, reasons, messages, steps) are named by the Go code: as a string, as the
// code of newError (hc.err.<code>), as the step of s.internal, or as the _full variant of a leader reason.
func TestEveryTextOfTheServerIsUsed(t *testing.T) {
	used := map[string]bool{"hc.reason.leader_direct_full": true, "hc.reason.leader_history_full": true}
	key := regexp.MustCompile(`^hc\.[a-z_]+(\.[a-z_0-9]+)+$`)
	for _, c := range findCalls(t) {
		used[c.key] = true
	}
	for _, f := range goSources(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if v, ok := stringLit(x); ok && key.MatchString(v) {
					used[v] = true
				}
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "internal" && len(x.Args) == 2 {
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
		if strings.HasPrefix(k, "hc.") && !strings.HasPrefix(k, "hc.ui.") && !strings.HasPrefix(k, "hc.title") && !strings.HasPrefix(k, "hc.description") && !used[k] {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("these texts are used by no code, remove them from the catalog: %v", unused)
	}
}

func TestAClientTextNeverHoldsAServerOnlyMessage(t *testing.T) {
	// the page asks for the prefix hc.ui. only; the warnings, errors and reasons must not be in it
	for k := range i18n.Std().Subset(i18n.EN, pageSpec("/x").ClientPrefixes...) {
		if !strings.HasPrefix(k, "hc.ui.") && !strings.HasPrefix(k, "ui.") {
			t.Errorf("%s would be sent to the browser", k)
		}
	}
}
