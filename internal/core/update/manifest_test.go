package update

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const goodSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func manifestJSON(version, hash, packageURL string, size int64) string {
	return fmt.Sprintf(`{"latest_version":%q,"sha256":%q,"package_url":%q,"size":%d}`, version, hash, packageURL, size)
}

func TestParseManifestNormalizesWhatItAccepts(t *testing.T) {
	body := `{"latest_version":" v0.1.3 ","sha256":"` + strings.ToUpper(goodSHA) + `","package_url":" dash_go.exe ","size":1234,"built_by":"a later script"}`
	m, err := ParseManifest([]byte("\xef\xbb\xbf" + body)) // a byte order mark, and a field this version does not know
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	want := Manifest{LatestVersion: "0.1.3", SHA256: goodSHA, PackageURL: "dash_go.exe", Size: 1234}
	if m != want {
		t.Fatalf("got %+v, want %+v", m, want)
	}
}

func TestParseManifestRejectsWhatCannotBeTrusted(t *testing.T) {
	cases := map[string]string{
		"empty":               ``,
		"not json":            `nope`,
		"an array":            `[]`,
		"size as text":        `{"latest_version":"0.1.3","sha256":"` + goodSHA + `","package_url":"a","size":"12"}`,
		"no version":          manifestJSON("", goodSHA, "a", 1),
		"version of a word":   manifestJSON("latest", goodSHA, "a", 1),
		"version with a path": manifestJSON("1/../2", goodSHA, "a", 1),
		"version too long":    manifestJSON("1."+strings.Repeat("0", 40), goodSHA, "a", 1),
		"hash too short":      manifestJSON("0.1.3", goodSHA[:63], "a", 1),
		"hash not hex":        manifestJSON("0.1.3", strings.Repeat("g", 64), "a", 1),
		"no address":          manifestJSON("0.1.3", goodSHA, "", 1),
		"address with a code": manifestJSON("0.1.3", goodSHA, "a\u0001b", 1),
		"address too long":    manifestJSON("0.1.3", goodSHA, strings.Repeat("a", 3000), 1),
		"size zero":           manifestJSON("0.1.3", goodSHA, "a", 0),
		"size negative":       manifestJSON("0.1.3", goodSHA, "a", -5),
		"size beyond limit":   manifestJSON("0.1.3", goodSHA, "a", MaxPackageBytes+1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(body)); !errors.Is(err, ErrManifest) {
				t.Fatalf("got %v, want an ErrManifest", err)
			}
		})
	}
}

func TestVersionsCompareWithoutThePrefix(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"0.1.3", "v0.1.3", true},
		{" V0.1.3 ", "0.1.3", true},
		{"0.1.3", "0.1.2", false},
		{"0.1.3", "0.1.3-rc1", false},
		{"v", "v", true},
	} {
		if got := SameVersion(c.a, c.b); got != c.same {
			t.Errorf("SameVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.same)
		}
	}
	if got := NormalizeVersion("version"); got != "version" {
		t.Errorf("a word that starts with v lost its first letter: %q", got)
	}
}
