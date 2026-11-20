package i18n

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func catalogFS(en, zh string) fstest.MapFS {
	return fstest.MapFS{"c/en.json": {Data: []byte(en)}, "c/zh.json": {Data: []byte(zh)}}
}

func TestTranslatesAndFillsPlaceholders(t *testing.T) {
	c := NewCatalog()
	if err := c.AddFS(catalogFS(`{"a.hello":"Hello {name}, {n} new","a.only_en":"English only"}`, `{"a.hello":"{n} 条新消息，{name} 你好","a.only_en":"仅英文"}`), "c"); err != nil {
		t.Fatal(err)
	}
	if got := c.T(EN, "a.hello", P{"name": "Ann", "n": 3}); got != "Hello Ann, 3 new" {
		t.Errorf("en: %q", got)
	}
	if got := c.T(ZH, "a.hello", P{"name": "Ann", "n": 3}); got != "3 条新消息，Ann 你好" {
		t.Errorf("zh keeps its own word order: %q", got)
	}
	if got := c.T(ZH, "a.missing", nil); got != "a.missing" {
		t.Errorf("an unknown key shows the key, got %q", got)
	}
	if got := c.T(EN, "a.hello", P{"name": "Ann"}); got != "Hello Ann, {n} new" {
		t.Errorf("a placeholder without a value stays visible, got %q", got)
	}
}

func TestMissingChineseTextFallsBackToEnglish(t *testing.T) {
	c := NewCatalog()
	if err := c.AddFS(catalogFS(`{"a.x":"English","a.y":"Only English"}`, `{"a.x":"中文"}`), "c"); err != nil {
		t.Fatal(err)
	}
	if got := c.T(ZH, "a.y", nil); got != "Only English" {
		t.Errorf("got %q", got)
	}
	sub := c.Subset(ZH, "a.")
	if sub["a.x"] != "中文" || sub["a.y"] != "Only English" {
		t.Errorf("subset must hold the language with English filled in: %v", sub)
	}
}

func TestSubsetOnlyHoldsTheAskedPrefixes(t *testing.T) {
	c := NewCatalog()
	if err := c.AddFS(catalogFS(`{"ui.a":"A","err.b":"B","hc.ui.c":"C","hc.err.d":"D"}`, `{"ui.a":"甲","err.b":"乙","hc.ui.c":"丙","hc.err.d":"丁"}`), "c"); err != nil {
		t.Fatal(err)
	}
	got := c.Subset(ZH, "ui.", "hc.ui.")
	if len(got) != 2 || got["ui.a"] != "甲" || got["hc.ui.c"] != "丙" {
		t.Errorf("server-only messages must not be sent to the browser: %v", got)
	}
}

func TestDuplicateKeysAcrossSourcesAreRefused(t *testing.T) {
	c := NewCatalog()
	if err := c.AddFS(catalogFS(`{"a.x":"1"}`, `{"a.x":"1"}`), "c"); err != nil {
		t.Fatal(err)
	}
	err := c.AddFS(catalogFS(`{"a.x":"2"}`, `{"a.x":"2"}`), "c")
	if err == nil || !strings.Contains(err.Error(), "a.x") {
		t.Errorf("modules share one namespace, a duplicate must be an error: %v", err)
	}
}

func TestBadCatalogFilesAreRefused(t *testing.T) {
	c := NewCatalog()
	if err := c.AddFS(catalogFS(`{"a":"x"}`, `not json`), "c"); err == nil {
		t.Error("invalid JSON must be an error")
	}
	if err := c.AddFS(fstest.MapFS{"c/en.json": {Data: []byte(`{}`)}}, "c"); err == nil {
		t.Error("a missing language file must be an error")
	}
	if err := c.AddFS(catalogFS(`{"a":{"nested":"x"}}`, `{}`), "c"); err == nil {
		t.Error("only flat texts are allowed")
	}
}

func TestPlaceholders(t *testing.T) {
	got := Placeholders("{b} and {a} and {b} but not {Upper} or { x }")
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("got %v", got)
	}
}

func TestCheckParity(t *testing.T) {
	ok := catalogFS(`{"a":"Hi {name}","b":"x"}`, `{"a":"{name} 你好","b":"y"}`)
	if p := CheckParity(ok, "c"); len(p) != 0 {
		t.Errorf("a matching pair must pass: %v", p)
	}
	bad := catalogFS(`{"a":"Hi {name}","b":"x","c":"only en"}`, `{"a":"你好","b":" ","z":"only zh"}`)
	p := strings.Join(CheckParity(bad, "c"), "\n")
	for _, want := range []string{`key "c" is missing`, `key "a" has the placeholders`, `key "b" is empty`, `key "z" is not in English`} {
		if !strings.Contains(p, want) {
			t.Errorf("missing finding %q in:\n%s", want, p)
		}
	}
}

func TestCoreCatalogIsComplete(t *testing.T) {
	if p := CheckParity(coreFS, "catalog"); len(p) != 0 {
		t.Fatalf("the core catalog is inconsistent:\n%s", strings.Join(p, "\n"))
	}
	if !Std().Has("login.invalid") || !Std().Has("ui.sign_out") {
		t.Error("the program catalog must hold the core messages")
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"en": EN, "ZH": ZH, " zh ": ZH} {
		if got, ok := Parse(in); !ok || got != want {
			t.Errorf("Parse(%q) = %v, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "fr", "zh-CN", "english"} {
		if _, ok := Parse(in); ok {
			t.Errorf("Parse(%q) must refuse: the language switch only sets en or zh", in)
		}
	}
}

func TestFromRequest(t *testing.T) {
	req := func(accept, cookie string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if accept != "" {
			r.Header.Set("Accept-Language", accept)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}
		return r
	}
	cases := []struct {
		name   string
		accept string
		cookie string
		want   Lang
	}{
		{"nothing at all is English", "", "", EN},
		{"a Chinese browser", "zh-CN,zh;q=0.9,en;q=0.8", "", ZH},
		{"a Traditional Chinese browser", "zh-TW", "", ZH},
		{"an English browser", "en-US,en;q=0.9", "", EN},
		{"the first known language wins", "fr-FR,zh;q=0.8,en;q=0.5", "", ZH},
		{"an unknown language falls back to English", "de,fr", "", EN},
		{"the cookie beats the browser", "zh-CN", "en", EN},
		{"the cookie beats the browser (other way)", "en-US", "zh", ZH},
		{"a damaged cookie is ignored", "zh-CN", "klingon", ZH},
	}
	for _, c := range cases {
		if got := FromRequest(req(c.accept, c.cookie)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMiddlewareAndContext(t *testing.T) {
	var seen Lang
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = LangOf(r.Context())
		_, _ = w.Write([]byte(T(r.Context(), "login.invalid", nil)))
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "zh"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if seen != ZH || !strings.Contains(w.Body.String(), "不正确") {
		t.Errorf("lang %v, body %q", seen, w.Body.String())
	}
	if LangOf(context.Background()) != EN {
		t.Error("a context without a language is English")
	}
}

func TestCookieIsLimitedToThePathAndLivesAYear(t *testing.T) {
	c := Cookie(ZH, "/dash_go/", true)
	if c.Name != CookieName || c.Value != "zh" || c.Path != "/dash_go/" || !c.Secure || c.MaxAge != 365*24*3600 || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("%+v", c)
	}
}
