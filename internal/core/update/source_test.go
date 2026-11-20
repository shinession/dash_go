package update

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestNewSourceAddresses(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, address, want string
		bad                 bool
	}{
		{name: "http file", address: "http://h/rel/dash_go.json", want: "http://h/rel/dash_go.json"},
		{name: "http folder", address: "http://h/rel/", want: "http://h/rel/dash_go.json"},
		{name: "http root", address: "http://h", want: "http://h/dash_go.json"},
		{name: "https with a password", address: "https://user:secret@h/x.json", bad: true},
		{name: "http without a host", address: "http:///x", bad: true},
		{name: "relative path", address: `rel` + string(filepath.Separator) + `dash_go.json`, bad: true},
		{name: "an existing folder", address: dir, want: filepath.Join(dir, "dash_go.json")},
		{name: "a folder with a separator", address: dir + string(filepath.Separator), want: filepath.Join(dir, "dash_go.json")},
		{name: "a file", address: filepath.Join(dir, "other.json"), want: filepath.Join(dir, "other.json")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, err := NewSource(c.address, nil)
			if c.bad {
				if err == nil {
					t.Fatalf("NewSource(%q) was accepted", c.address)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("the error shows the password: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewSource(%q): %v", c.address, err)
			}
			if src.Address() != c.want {
				t.Fatalf("Address() = %q, want %q", src.Address(), c.want)
			}
		})
	}
}

// releaseServer serves a release folder at /rel/ and a few answers that are not one.
func releaseServer(t *testing.T, manifest string, program []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rel/dash_go.json", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, manifest) })
	mux.HandleFunc("/rel/dash_go.exe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(program) })
	mux.HandleFunc("/other/pkg.bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(program) })
	mux.HandleFunc("/dash_go.json", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, manifest) })
	mux.HandleFunc("/dash_go.exe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(program) })
	// the application that serves the static files answers 202 for a missing file
	mux.HandleFunc("/accepted/dash_go.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, manifest)
	})
	mux.HandleFunc("/huge/dash_go.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte(" "), MaxManifestBytes+10))
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/moved/dash_go.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/rel/dash_go.json", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPSourceReadsTheManifestAndTheProgram(t *testing.T) {
	program := []byte("the program")
	m := manifestJSON("0.1.3", sum(program), "dash_go.exe", int64(len(program)))
	srv := releaseServer(t, m, program)
	ctx := context.Background()

	for _, address := range []string{srv.URL + "/rel/dash_go.json", srv.URL + "/rel/", srv.URL + "/moved/dash_go.json"} {
		src, err := NewSource(address, nil)
		if err != nil {
			t.Fatalf("NewSource(%q): %v", address, err)
		}
		got, err := src.Manifest(ctx)
		if err != nil || got.LatestVersion != "0.1.3" {
			t.Fatalf("Manifest(%q) = %+v, %v", address, got, err)
		}
	}

	src, _ := NewSource(srv.URL+"/rel/dash_go.json", nil)
	got, _ := src.Manifest(ctx)
	rc, size, err := src.Open(ctx, got)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(data, program) || size != int64(len(program)) {
		t.Fatalf("Open gave %q (%d bytes announced)", data, size)
	}

	// a manifest at the root of the server: the program is next to it
	root, _ := NewSource(srv.URL, nil)
	if rootManifest, err := root.Manifest(ctx); err != nil {
		t.Fatalf("Manifest of the server root: %v", err)
	} else if rc, _, err := root.Open(ctx, rootManifest); err != nil {
		t.Fatalf("Open at the server root: %v", err)
	} else {
		_ = rc.Close()
	}

	// a package address that is an absolute path of the same server
	abs := got
	abs.PackageURL = "/other/pkg.bin"
	rc, _, err = src.Open(ctx, abs)
	if err != nil {
		t.Fatalf("Open with an absolute path: %v", err)
	}
	_ = rc.Close()
}

func TestHTTPSourceRefusesWhatIsNotAManifestOrAProgram(t *testing.T) {
	program := []byte("the program")
	m := manifestJSON("0.1.3", sum(program), "dash_go.exe", int64(len(program)))
	srv := releaseServer(t, m, program)
	ctx := context.Background()

	for _, c := range []struct{ path, want string }{
		{"/missing/dash_go.json", "404"},
		{"/accepted/dash_go.json", "202"},
		{"/huge/dash_go.json", "longer than"},
		{"/loop", "redirects"},
	} {
		src, err := NewSource(srv.URL+c.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := src.Manifest(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Manifest(%s) = %v, want an error with %q", c.path, err, c.want)
		}
	}

	src, _ := NewSource(srv.URL+"/rel/dash_go.json", nil)
	good, _ := src.Manifest(ctx)
	for _, bad := range []string{"ftp://h/dash_go.exe", "file:///c:/windows/system32/cmd.exe", "http://user:pw@h/x", "/missing.exe"} {
		m := good
		m.PackageURL = bad
		if rc, _, err := src.Open(ctx, m); err == nil {
			_ = rc.Close()
			t.Errorf("Open accepted the package address %q", bad)
		}
	}
}

func TestFileSourceReadsAFolderAndFindsTheProgramNextToIt(t *testing.T) {
	dir := t.TempDir()
	program := []byte("the program")
	write := func(rel string, data []byte) string {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("dash_go.json", []byte(manifestJSON("0.1.3", sum(program), "dash_go.exe", int64(len(program)))))
	write("dash_go.exe", program)
	write("sub/pkg.exe", program)
	absolute := write("elsewhere/pkg.exe", program)
	ctx := context.Background()

	src, err := NewSource(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := src.Manifest(ctx)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	for name, packageURL := range map[string]string{"next to the manifest": "dash_go.exe", "in a subfolder": "sub/pkg.exe", "absolute": absolute} {
		m.PackageURL = packageURL
		rc, size, err := src.Open(ctx, m)
		if err != nil {
			t.Fatalf("Open (%s): %v", name, err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !bytes.Equal(data, program) || size != int64(len(program)) {
			t.Fatalf("Open (%s) gave %q, %d", name, data, size)
		}
	}

	for _, bad := range []string{"missing.exe", "sub"} {
		m.PackageURL = bad
		if rc, _, err := src.Open(ctx, m); err == nil {
			_ = rc.Close()
			t.Errorf("Open accepted %q", bad)
		}
	}

	empty, _ := NewSource(filepath.Join(dir, "none"+string(filepath.Separator)), nil)
	if _, err := empty.Manifest(ctx); err == nil {
		t.Error("a folder without a manifest was read")
	}
}

// A share that cannot be reached when the service starts is the folder of the release all the same once it can.
func TestFileSourceFindsAFolderThatAppearsAfterTheStart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "release") // no trailing separator, and not there yet
	src, err := NewSource(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := src.Manifest(ctx); err == nil {
		t.Fatal("a folder that is not there was read")
	}

	program := []byte("the program")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "dash_go.json"), []byte(manifestJSON("0.1.3", sum(program), "dash_go.exe", int64(len(program)))))
	writeFile(t, filepath.Join(dir, "dash_go.exe"), program)
	m, err := src.Manifest(ctx)
	if err != nil {
		t.Fatalf("Manifest once the folder is there: %v", err)
	}
	rc, _, err := src.Open(ctx, m)
	if err != nil {
		t.Fatalf("Open once the folder is there: %v", err)
	}
	_ = rc.Close()
}

// dashStatic stands for the static files of the Dash API, which is where the release is published: it answers 200 for
// a file that exists, 202 and a picture for anything else, and text/plain for the types it does not know. It records
// what was asked for.
func dashStatic(t *testing.T, files map[string][]byte) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		body, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write(bytes.Repeat([]byte{0xFF, 0xD8}, 5000)) // the "file not found" picture
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

// A package_url that is not an address of its own is looked for next to the manifest: with the manifest at
// /api/static/cd/dash_go.json, dash_go.exe is /api/static/cd/dash_go.exe.
func TestARelativePackageURLIsLookedForNextToTheManifest(t *testing.T) {
	program := []byte("the program")
	files := map[string][]byte{
		"/api/static/cd/dash_go.exe":        program,
		"/api/static/cd/sub/pkg.exe":        program,
		"/api/static/elsewhere/dash_go.exe": program,
	}
	m := manifestJSON("0.1.3", sum(program), "dash_go.exe", int64(len(program)))
	files["/api/static/cd/dash_go.json"] = []byte(m)
	srv, asked := dashStatic(t, files)
	ctx := context.Background()

	// The address of the manifest, or of the folder that holds it.
	for _, address := range []string{srv.URL + "/api/static/cd/dash_go.json", srv.URL + "/api/static/cd/"} {
		src, err := NewSource(address, nil)
		if err != nil {
			t.Fatalf("NewSource(%q): %v", address, err)
		}
		got, err := src.Manifest(ctx)
		if err != nil || got.LatestVersion != "0.1.3" || got.PackageURL != "dash_go.exe" {
			t.Fatalf("Manifest(%q) = %+v, %v", address, got, err)
		}
		for packageURL, wantPath := range map[string]string{
			"dash_go.exe":                          "/api/static/cd/dash_go.exe",
			"./dash_go.exe":                        "/api/static/cd/dash_go.exe",
			"sub/pkg.exe":                          "/api/static/cd/sub/pkg.exe",
			"../elsewhere/dash_go.exe":             "/api/static/elsewhere/dash_go.exe",
			"/api/static/cd/dash_go.exe":           "/api/static/cd/dash_go.exe",
			srv.URL + "/api/static/cd/dash_go.exe": "/api/static/cd/dash_go.exe",
		} {
			got.PackageURL = packageURL
			before := len(asked())
			rc, size, err := src.Open(ctx, got)
			if err != nil {
				t.Fatalf("Open(package_url %q): %v", packageURL, err)
			}
			data, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !bytes.Equal(data, program) || size != int64(len(program)) {
				t.Errorf("Open(package_url %q) gave %q (%d announced)", packageURL, data, size)
			}
			if now := asked(); len(now) != before+1 || now[before] != wantPath {
				t.Errorf("package_url %q asked for %v, want %s", packageURL, now[before:], wantPath)
			}
		}
	}

	// The whole path of an update: the program arrives in the staged file with the size and the hash of the manifest.
	src, _ := NewSource(srv.URL+"/api/static/cd/dash_go.json", nil)
	manifest, _ := src.Manifest(ctx)
	dest := filepath.Join(t.TempDir(), "dash_go.new.exe")
	if err := Download(ctx, src, manifest, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got := readFile(t, dest); got != string(program) {
		t.Errorf("the staged program is %q", got)
	}
}

// What the static files answer for a file that is not there must never be taken for the manifest or for the program.
func TestAMissingReleaseFileOfTheStaticServerIsAnErrorNotAProgram(t *testing.T) {
	program := []byte("the program")
	srv, _ := dashStatic(t, map[string][]byte{"/api/static/cd/dash_go.exe": program})
	ctx := context.Background()

	src, _ := NewSource(srv.URL+"/api/static/cd/dash_go.json", nil) // nothing is published yet
	if _, err := src.Manifest(ctx); err == nil || !strings.Contains(err.Error(), "202") {
		t.Errorf("a manifest that is not there: %v, want the 202 to be said", err)
	}

	m := Manifest{LatestVersion: "0.1.3", SHA256: sum(program), PackageURL: "gone.exe", Size: int64(len(program))}
	if rc, _, err := src.Open(ctx, m); err == nil {
		_ = rc.Close()
		t.Error("the answer for a program that is not there was opened as the program")
	}
	dest := filepath.Join(t.TempDir(), "dash_go.new.exe")
	if err := Download(ctx, src, m, dest); err == nil || exists(dest) || exists(dest+".part") {
		t.Errorf("Download of a program that is not there: %v", err)
	}
}

func TestFileSourceMayPointAtAnHTTPProgram(t *testing.T) {
	program := []byte("the program")
	srv := releaseServer(t, "{}", program)
	dir := t.TempDir()
	src, _ := NewSource(dir, nil)
	m := Manifest{LatestVersion: "0.1.3", SHA256: sum(program), PackageURL: srv.URL + "/rel/dash_go.exe", Size: int64(len(program))}
	rc, _, err := src.Open(context.Background(), m)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(data, program) {
		t.Fatalf("got %q", data)
	}
	m.PackageURL = "http://user:pw@" + strings.TrimPrefix(srv.URL, "http://") + "/rel/dash_go.exe"
	if rc, _, err := src.Open(context.Background(), m); err == nil {
		_ = rc.Close()
		t.Error("an address with a password was accepted")
	}
}
