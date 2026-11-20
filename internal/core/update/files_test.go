package update

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// fileRelease is a release folder with the program of a manifest.
func fileRelease(t *testing.T, program []byte) (Source, Manifest) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dash_go.exe"), program)
	src, err := NewSource(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return src, Manifest{LatestVersion: "9.9.9", SHA256: sum(program), PackageURL: "dash_go.exe", Size: int64(len(program))}
}

func TestStagedAndPreviousPathsKeepTheFolderAndTheExtension(t *testing.T) {
	exe := filepath.Join("d:", "dg", "dash_go.exe")
	if got, want := StagedPath(exe), filepath.Join("d:", "dg", "dash_go.new.exe"); got != want {
		t.Errorf("StagedPath = %q, want %q", got, want)
	}
	if got, want := PreviousPath(exe, "v0.1.2"), filepath.Join("d:", "dg", "dash_go.old-0.1.2.exe"); got != want {
		t.Errorf("PreviousPath = %q, want %q", got, want)
	}
	if got := PreviousPath(exe, `1.0/..\x`); filepath.Dir(got) != filepath.Join("d:", "dg") {
		t.Errorf("a version made the path leave its folder: %q", got)
	}
	if got := StagedPath("dash_go"); got != "dash_go.new" {
		t.Errorf("StagedPath without an extension = %q", got)
	}
}

func TestDownloadKeepsOnlyAProgramThatIsTheOneOfTheManifest(t *testing.T) {
	program := []byte("the new program")
	ctx := context.Background()

	t.Run("matching", func(t *testing.T) {
		src, m := fileRelease(t, program)
		dest := filepath.Join(t.TempDir(), "dash_go.new.exe")
		if err := Download(ctx, src, m, dest); err != nil {
			t.Fatalf("Download: %v", err)
		}
		if got := readFile(t, dest); got != string(program) {
			t.Fatalf("got %q", got)
		}
		if exists(dest + ".part") {
			t.Error("the .part file was left")
		}
	})

	for name, tweak := range map[string]func(*Manifest){
		"another size": func(m *Manifest) { m.Size++ },
		"another hash": func(m *Manifest) { m.SHA256 = sum([]byte("something else")) },
	} {
		t.Run(name, func(t *testing.T) {
			src, m := fileRelease(t, program)
			tweak(&m)
			dest := filepath.Join(t.TempDir(), "dash_go.new.exe")
			if err := Download(ctx, src, m, dest); !errors.Is(err, ErrMismatch) {
				t.Fatalf("got %v, want an ErrMismatch", err)
			}
			if exists(dest) || exists(dest+".part") {
				t.Error("a program that does not match was kept")
			}
		})
	}

	t.Run("a stream that is longer than the manifest says", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush() // no Content-Length
			_, _ = io.WriteString(w, string(program)+" and more")
		}))
		defer srv.Close()
		src, _ := NewSource(srv.URL+"/dash_go.json", nil)
		m := Manifest{LatestVersion: "9.9.9", SHA256: sum(program), PackageURL: "/", Size: int64(len(program))}
		dest := filepath.Join(t.TempDir(), "dash_go.new.exe")
		if err := Download(ctx, src, m, dest); !errors.Is(err, ErrMismatch) {
			t.Fatalf("got %v, want an ErrMismatch", err)
		}
		if exists(dest) || exists(dest+".part") {
			t.Error("a program that does not match was kept")
		}
	})

	t.Run("a connection that breaks", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("only a part"))
			panic(http.ErrAbortHandler)
		}))
		defer srv.Close()
		src, _ := NewSource(srv.URL+"/dash_go.json", nil)
		m := Manifest{LatestVersion: "9.9.9", SHA256: sum(program), PackageURL: "/", Size: 100}
		dest := filepath.Join(t.TempDir(), "dash_go.new.exe")
		err := Download(ctx, src, m, dest)
		if err == nil || errors.Is(err, ErrMismatch) {
			t.Fatalf("got %v, want a read error that is not a mismatch", err)
		}
		if exists(dest) || exists(dest+".part") {
			t.Error("a part of the program was kept")
		}
	})

	t.Run("a program that cannot be opened", func(t *testing.T) {
		src, m := fileRelease(t, program)
		m.PackageURL = "missing.exe"
		if err := Download(ctx, src, m, filepath.Join(t.TempDir(), "x.exe")); err == nil || errors.Is(err, ErrMismatch) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestDownloadGivesUpOnAServerThatStopsSending(t *testing.T) {
	old := downloadIdle
	downloadIdle = 150 * time.Millisecond
	t.Cleanup(func() { downloadIdle = old })

	mux := http.NewServeMux()
	mux.HandleFunc("/dash_go.exe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("the start"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	src, err := NewSource(srv.URL+"/dash_go.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{LatestVersion: "0.1.3", SHA256: strings.Repeat("0", 64), PackageURL: "dash_go.exe", Size: 100}
	dest := filepath.Join(t.TempDir(), "dash_go.new.exe")

	started := time.Now()
	err = Download(context.Background(), src, m, dest)
	if err == nil || !strings.Contains(err.Error(), "nothing was received") {
		t.Fatalf("Download = %v, want the stop to be said", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("giving up took %s", took)
	}
	if exists(dest) || exists(dest+".part") {
		t.Error("a part of the program was left")
	}
}

func TestSwapPutsTheStagedProgramInPlaceAndKeepsTheOldOne(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dash_go.exe")
	staged := StagedPath(exe)
	writeFile(t, exe, []byte("old"))
	writeFile(t, staged, []byte("new"))
	// an older copy of the same version is replaced
	writeFile(t, PreviousPath(exe, "0.1.2"), []byte("stale"))

	previous, err := Swap(exe, staged, "0.1.2")
	if err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if previous != PreviousPath(exe, "0.1.2") {
		t.Errorf("previous = %q", previous)
	}
	if got := readFile(t, exe); got != "new" {
		t.Errorf("the program is %q", got)
	}
	if got := readFile(t, previous); got != "old" {
		t.Errorf("the kept program is %q", got)
	}
	if exists(staged) {
		t.Error("the staged file is still there")
	}
}

func TestSwapGivesTheRunningProgramBackWhenTheNewOneIsMissing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dash_go.exe")
	writeFile(t, exe, []byte("old"))

	if _, err := Swap(exe, StagedPath(exe), "0.1.2"); err == nil {
		t.Fatal("Swap worked without a staged program")
	}
	if got := readFile(t, exe); got != "old" {
		t.Errorf("the program is %q, want the old one back", got)
	}
	if exists(PreviousPath(exe, "0.1.2")) {
		t.Error("a kept copy was left")
	}
}

// A copy of the same version that is still in use cannot be replaced: the launcher that was started from it runs on.
// The copy stays, and the program that is replaced now is kept under a name of its own.
func TestSwapKeepsACopyOfTheSameVersionThatIsStillInUse(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to delete or replace a file that is open")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "dash_go.exe")
	staged := StagedPath(exe)
	writeFile(t, exe, []byte("old"))
	writeFile(t, staged, []byte("new"))
	inUse := PreviousPath(exe, "0.1.2")
	writeFile(t, inUse, []byte("launcher"))
	f, err := os.Open(inUse)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	previous, err := Swap(exe, staged, "0.1.2")
	if err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if previous == inUse || filepath.Dir(previous) != dir || !strings.HasPrefix(filepath.Base(previous), "dash_go.old-0.1.2-") {
		t.Errorf("the replaced program was kept as %q", previous)
	}
	if got := readFile(t, previous); got != "old" {
		t.Errorf("the kept program is %q", got)
	}
	if got := readFile(t, inUse); got != "launcher" {
		t.Errorf("the copy in use is %q", got)
	}
	if got := readFile(t, exe); got != "new" {
		t.Errorf("the program is %q", got)
	}
}

// The reason the whole design works: a program that runs can be renamed, and a new file can take its name.
func TestSwapReplacesAProgramThatIsRunning(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "dash_go.exe")
	writeFile(t, exe, image)

	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), envHelper+"=hold")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Wait()
	})
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || strings.TrimSpace(line) != "running" {
		t.Fatalf("the program did not start: %q, %v", line, err)
	}

	staged := StagedPath(exe)
	writeFile(t, staged, []byte("new"))
	previous, err := Swap(exe, staged, "0.1.2")
	if err != nil {
		t.Fatalf("Swap while the program runs: %v", err)
	}
	if got := readFile(t, exe); got != "new" {
		t.Errorf("the program is %d bytes, want the new one", len(got))
	}
	if !exists(previous) {
		t.Error("the running program was not kept")
	}
}

func TestPruneOldKeepsTheNewestCopies(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dash_go.exe")
	writeFile(t, exe, []byte("current"))
	other := filepath.Join(dir, "dash_go.exe.update.json")
	writeFile(t, other, []byte("{}"))
	start := time.Now().Add(-time.Hour)
	versions := []string{"0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5"}
	for i, v := range versions {
		p := PreviousPath(exe, v)
		writeFile(t, p, []byte(v))
		at := start.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	pruneOld(exe, 3)
	for i, v := range versions {
		if got, want := exists(PreviousPath(exe, v)), i >= 2; got != want {
			t.Errorf("version %s kept = %v, want %v", v, got, want)
		}
	}
	if !exists(exe) || !exists(other) {
		t.Error("pruneOld removed something that is not an older copy")
	}
}

func TestMarkerIsReadOnce(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "dash_go.exe")
	if _, ok := TakeMarker(exe); ok {
		t.Fatal("a marker from nowhere")
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	want := Marker{From: "0.1.2", To: "0.1.3", SHA256: goodSHA, At: at, WaitedSeconds: 42}
	if err := WriteMarker(exe, want); err != nil {
		t.Fatal(err)
	}
	got, ok := TakeMarker(exe)
	if !ok || got.From != want.From || got.To != want.To || got.SHA256 != want.SHA256 || !got.At.Equal(at) || got.WaitedSeconds != 42 {
		t.Fatalf("got %+v, %v", got, ok)
	}
	if _, ok := TakeMarker(exe); ok {
		t.Error("the marker was read twice")
	}
	if exists(MarkerPath(exe)) || exists(MarkerPath(exe)+".tmp") {
		t.Error("a marker file was left")
	}

	writeFile(t, MarkerPath(exe), []byte("not json"))
	if _, ok := TakeMarker(exe); ok {
		t.Error("garbage was taken for a marker")
	}
	if exists(MarkerPath(exe)) {
		t.Error("the garbage was left")
	}
}
