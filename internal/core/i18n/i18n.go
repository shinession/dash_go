// Package i18n holds the message catalogs of dash_go and picks the language of a request.
//
// Every user visible text lives in a flat JSON file per language (catalog/en.json, catalog/zh.json for the core,
// i18n/en.json and i18n/zh.json inside a module). The same files feed the Go code (T) and the browser (Subset puts
// the keys of a page into the page itself), so there is one source of truth for both. A message may contain
// {name} placeholders; unlike %s they can be reordered, which Chinese needs. Messages that are only for the server
// (errors, warnings) use other key prefixes than the ones a page asks for, so they are never sent to the browser.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Lang is a supported language code.
type Lang string

// Supported languages. English is the fallback of every missing message.
const (
	EN Lang = "en"
	ZH Lang = "zh"
)

// Supported lists the languages in the order the language switch shows them.
func Supported() []Lang { return []Lang{EN, ZH} }

// Parse accepts the exact codes the language switch sets.
func Parse(s string) (Lang, bool) {
	switch Lang(strings.ToLower(strings.TrimSpace(s))) {
	case EN:
		return EN, true
	case ZH:
		return ZH, true
	}
	return EN, false
}

// P holds the values of the {name} placeholders of a message. Numbers are fine; format a fraction yourself.
type P map[string]any

//go:embed catalog/*.json
var coreFS embed.FS

// Catalog is a set of messages per language.
type Catalog struct {
	mu   sync.RWMutex
	msgs map[Lang]map[string]string
}

// NewCatalog returns an empty catalog.
func NewCatalog() *Catalog {
	c := &Catalog{msgs: map[Lang]map[string]string{}}
	for _, l := range Supported() {
		c.msgs[l] = map[string]string{}
	}
	return c
}

// AddFS reads <dir>/<lang>.json for every supported language. A key that is already known is an error: modules
// share one namespace, so every module prefixes its keys.
func (c *Catalog) AddFS(fsys fs.FS, dir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range Supported() {
		name := dir + "/" + string(l) + ".json"
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("i18n: read %s: %w", name, err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("i18n: %s is not a flat JSON object of texts: %w", name, err)
		}
		for k, v := range m {
			if _, dup := c.msgs[l][k]; dup {
				return fmt.Errorf("i18n: key %q of %s is defined twice", k, name)
			}
			c.msgs[l][k] = v
		}
	}
	return nil
}

// Message returns the text of key in the language, falling back to English.
func (c *Catalog) Message(l Lang, key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.msgs[l][key]; ok {
		return m, true
	}
	m, ok := c.msgs[EN][key]
	return m, ok
}

// Has reports whether the English catalog knows the key.
func (c *Catalog) Has(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.msgs[EN][key]
	return ok
}

// T translates key and fills the placeholders. An unknown key returns the key itself, so a mistake is visible
// instead of blank.
func (c *Catalog) T(l Lang, key string, p P) string {
	msg, ok := c.Message(l, key)
	if !ok {
		return key
	}
	return Fill(msg, p)
}

var placeholderRe = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)

// Fill replaces the {name} placeholders of msg. A placeholder without a value stays as it is.
func Fill(msg string, p P) string {
	if len(p) == 0 || !strings.Contains(msg, "{") {
		return msg
	}
	return placeholderRe.ReplaceAllStringFunc(msg, func(m string) string {
		if v, ok := p[m[1:len(m)-1]]; ok {
			return fmt.Sprint(v)
		}
		return m
	})
}

// Placeholders lists the distinct placeholder names of a message, sorted.
func Placeholders(msg string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(msg, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// Subset returns the messages of the language whose key starts with one of the prefixes, with English filled in
// for what the language lacks. It is what a page embeds for its script.
func (c *Catalog) Subset(l Lang, prefixes ...string) map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[string]string{}
	for k, v := range c.msgs[EN] {
		if hasPrefix(k, prefixes) {
			out[k] = v
		}
	}
	for k, v := range c.msgs[l] {
		if hasPrefix(k, prefixes) {
			out[k] = v
		}
	}
	return out
}

// Keys returns the keys of a language, sorted.
func (c *Catalog) Keys(l Lang) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]string, 0, len(c.msgs[l]))
	for k := range c.msgs[l] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func hasPrefix(key string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// std is the catalog of the running program: the core messages plus the messages of every module that was
// compiled in and registered itself.
var std = func() *Catalog {
	c := NewCatalog()
	if err := c.AddFS(coreFS, "catalog"); err != nil {
		panic(err) // the embedded files are part of the build; a test covers them
	}
	return c
}()

// Std returns the catalog of the running program.
func Std() *Catalog { return std }

// Register adds the catalogs of a module (<dir>/en.json, <dir>/zh.json inside fsys). Call it from init().
func Register(fsys fs.FS, dir string) {
	if err := std.AddFS(fsys, dir); err != nil {
		panic(err)
	}
}

// CheckParity reports the differences between the language files of <dir>: a key that one language lacks, a
// placeholder that one language lacks or adds, and an empty text. Every catalog has a test that expects none.
func CheckParity(fsys fs.FS, dir string) []string {
	files := map[Lang]map[string]string{}
	for _, l := range Supported() {
		name := dir + "/" + string(l) + ".json"
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return []string{fmt.Sprintf("read %s: %v", name, err)}
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			return []string{fmt.Sprintf("%s is not a flat JSON object of texts: %v", name, err)}
		}
		files[l] = m
	}
	var problems []string
	for key, en := range files[EN] {
		if strings.TrimSpace(en) == "" {
			problems = append(problems, fmt.Sprintf("en: key %q is empty", key))
		}
		for _, l := range Supported()[1:] {
			other, ok := files[l][key]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("%s: key %q is missing", l, key))
			case strings.TrimSpace(other) == "":
				problems = append(problems, fmt.Sprintf("%s: key %q is empty", l, key))
			case strings.Join(Placeholders(en), ",") != strings.Join(Placeholders(other), ","):
				problems = append(problems, fmt.Sprintf("%s: key %q has the placeholders %v, English has %v", l, key, Placeholders(other), Placeholders(en)))
			}
		}
	}
	for _, l := range Supported()[1:] {
		for key := range files[l] {
			if _, ok := files[EN][key]; !ok {
				problems = append(problems, fmt.Sprintf("%s: key %q is not in English", l, key))
			}
		}
	}
	sort.Strings(problems)
	return problems
}
