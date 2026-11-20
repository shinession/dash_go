package shell

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The colours of the two themes are variables of shell.css. This test reads them from the file and checks that every
// text colour is readable on every surface it is used on (WCAG 2.1: 4.5:1 for text, 3:1 for the logo), so a change
// of the palette cannot make a theme unreadable without a failing test.

var cssVar = regexp.MustCompile(`--([a-z0-9-]+):\s*(#[0-9a-fA-F]{3,6})\s*;`)

func paletteBlock(t *testing.T, css, selector string) map[string]string {
	t.Helper()
	i := strings.Index(css, "\n"+selector+" {")
	if i < 0 {
		t.Fatalf("shell.css has no block %q", selector)
	}
	rest := css[i+len(selector)+3:]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatalf("the block %q is not closed", selector)
	}
	out := map[string]string{}
	for _, m := range cssVar.FindAllStringSubmatch(rest[:end], -1) {
		out[m[1]] = m[2]
	}
	return out
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	var c [3]float64
	for i := range c {
		v, err := strconv.ParseUint(hex[2*i:2*i+2], 16, 8)
		if err != nil {
			t.Fatalf("bad colour %q", hex)
		}
		f := float64(v) / 255
		if f <= 0.03928 {
			c[i] = f / 12.92
		} else {
			c[i] = math.Pow((f+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestBothThemesAreReadable(t *testing.T) {
	raw, err := webFS.ReadFile("web/assets/shell.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	light := paletteBlock(t, css, ":root")
	dark := map[string]string{}
	for k, v := range light {
		dark[k] = v
	}
	for k, v := range paletteBlock(t, css, `:root[data-theme="dark"]`) {
		dark[k] = v
	}
	if dark["bg"] == light["bg"] || dark["text"] == light["text"] {
		t.Fatal("the dark theme must override the palette")
	}

	type pair struct {
		fg, bg string
		min    float64
		what   string
	}
	var pairs []pair
	text := func(fg string, bgs ...string) {
		for _, bg := range bgs {
			pairs = append(pairs, pair{fg, bg, 4.5, "text"})
		}
	}
	text("text", "bg", "panel", "panel-2", "field", "warn-bg", "toast-bg")
	text("muted", "bg", "panel", "panel-2")
	text("accent", "bg", "panel", "panel-2", "soft-surface")
	text("on-primary", "primary", "primary-hover")
	for _, tone := range []string{"green", "yellow", "red", "lv1", "lv2", "lv3"} {
		text(tone, "bg", "panel", "panel-2")
	}
	// the warnings sit on their own backgrounds
	text("yellow", "warn-bg", "warn-bg-strong", "warn-flag-bg")
	text("text", "warn-flag-bg")
	// the logo is a graphic: 3:1 on the surface it is on. It sits in the header and in the card of the login page, both
	// of which are the panel colour, never directly on the page background.
	for _, c := range []string{"logo-navy", "logo-teal"} {
		pairs = append(pairs, pair{c, "panel", 3, "logo"})
	}

	for name, p := range map[string]map[string]string{"light": light, "dark": dark} {
		// accent text is also used on the pale tint of the buttons (soft = accent at low alpha over the panel):
		// approximate that surface by the panel itself, which is the stricter case for the dark theme
		p["soft-surface"] = p["panel"]
		for _, pr := range pairs {
			fg, okF := p[pr.fg]
			bg, okB := p[pr.bg]
			if !okF || !okB {
				t.Errorf("%s: the palette has no --%s or --%s", name, pr.fg, pr.bg)
				continue
			}
			if got := contrast(t, fg, bg); got < pr.min {
				t.Errorf("%s theme: %s --%s (%s) on --%s (%s) has a contrast of %.2f:1, needs %.1f:1", name, pr.what, pr.fg, fg, pr.bg, bg, got, pr.min)
			}
		}
	}
}

func TestEveryColourVariableOfTheLightThemeHasADarkCounterpartOrIsTheSame(t *testing.T) {
	raw, _ := webFS.ReadFile("web/assets/shell.css")
	light := paletteBlock(t, string(raw), ":root")
	dark := paletteBlock(t, string(raw), `:root[data-theme="dark"]`)
	// a colour that the dark theme does not set would stay the light one on a dark page
	shared := map[string]bool{"on-primary": true, "logo-teal": true}
	var missing []string
	for k := range light {
		if _, ok := dark[k]; !ok && !shared[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the dark theme does not set: %s", fmt.Sprint(missing))
	}
}
