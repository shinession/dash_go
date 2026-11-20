// Package update lets the service replace itself with a newer build.
//
// A release is a folder with the program and a small manifest, dash_go.json, that names the latest version, the
// program and its sha256 and size. The service reads the manifest from a configured address (an http(s) address or a
// path), downloads the program when the version differs from the running one, checks it, waits until nobody is
// clicking or submitting, puts it in place of the running program and starts it. The design is the one of the
// TimeVault project (its ADR 0014); docs/auto_update.md says what is different here.
package update

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ManifestFile is the name of the manifest in a release folder.
const ManifestFile = "dash_go.json"

// Limits that keep a wrong address from filling the disk or the memory of the service.
const (
	MaxManifestBytes = 64 << 10
	MaxPackageBytes  = 512 << 20
)

// ErrManifest means the manifest cannot be used. The text says what is wrong with it.
var ErrManifest = errors.New("update: invalid manifest")

// Manifest says which version is the latest and where its program is.
type Manifest struct {
	// LatestVersion is the version of the program, as `dash_go -version` prints it (a leading v is ignored).
	LatestVersion string `json:"latest_version"`
	// SHA256 is the hash of the program file, in hexadecimal.
	SHA256 string `json:"sha256"`
	// PackageURL is where the program is: an http(s) address or a path, relative to the manifest when it is not
	// absolute.
	PackageURL string `json:"package_url"`
	// Size is the size of the program file in bytes.
	Size int64 `json:"size"`
}

var versionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z._+-]{0,31}$`)

// NormalizeVersion trims a version and drops a leading v, so "v0.1.3" and "0.1.3" are the same version.
func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && v[1] >= '0' && v[1] <= '9' {
		v = v[1:]
	}
	return v
}

// SameVersion reports whether two version texts name the same version.
func SameVersion(a, b string) bool {
	return strings.EqualFold(NormalizeVersion(a), NormalizeVersion(b))
}

// ParseManifest reads and checks a manifest. A UTF-8 byte order mark is accepted (some editors write one) and fields
// that are not known are ignored, so a later version of the release script can add some.
func ParseManifest(data []byte) (Manifest, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: not JSON of the expected shape (%s)", ErrManifest, shortError(err))
	}
	m.LatestVersion = NormalizeVersion(m.LatestVersion)
	m.SHA256 = strings.ToLower(strings.TrimSpace(m.SHA256))
	m.PackageURL = strings.TrimSpace(m.PackageURL)
	switch {
	case !versionPattern.MatchString(m.LatestVersion):
		return Manifest{}, fmt.Errorf("%w: latest_version is missing or not a version such as 0.1.3", ErrManifest)
	case len(m.SHA256) != 64 || !isHex(m.SHA256):
		return Manifest{}, fmt.Errorf("%w: sha256 must be 64 hexadecimal characters", ErrManifest)
	case m.PackageURL == "" || len(m.PackageURL) > 2048 || hasControl(m.PackageURL):
		return Manifest{}, fmt.Errorf("%w: package_url is missing or not an address", ErrManifest)
	case m.Size < 1 || m.Size > MaxPackageBytes:
		return Manifest{}, fmt.Errorf("%w: size must be between 1 and %d bytes", ErrManifest, MaxPackageBytes)
	}
	return m, nil
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// shortError keeps the text of a decoding error to one short line.
func shortError(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}
