package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrMismatch means the program that was read is not the one the manifest describes: it has another size or hash.
// That is also what a release looks like while it is still being copied, so it is not final: it is tried again later.
var ErrMismatch = errors.New("update: the program is not the one the manifest describes")

// StagedPath is where a downloaded program waits. It is next to the running program, so that putting it in place is a
// rename within one folder.
func StagedPath(exe string) string { return withSuffix(exe, ".new") }

// PreviousPath is where the program of a version is kept when a newer one takes its place.
func PreviousPath(exe, version string) string { return withSuffix(exe, ".old-"+safeName(version)) }

func withSuffix(exe, suffix string) string {
	ext := filepath.Ext(exe)
	return strings.TrimSuffix(exe, ext) + suffix + ext
}

// safeName keeps what may be part of a file name on every system; a version is one of those already.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '_', r == '-', r == '+':
			return r
		}
		return '_'
	}, NormalizeVersion(s))
}

// downloadIdle is how long a download may go without receiving a byte before it is given up, so that a server or a
// proxy that stops answering cannot hold the updater for good.
var downloadIdle = 2 * time.Minute

// idleTimer pushes the deadline of a download back with every read that returns data.
type idleTimer struct {
	r io.Reader
	t *time.Timer
}

func (i idleTimer) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.t.Reset(downloadIdle)
	}
	return n, err
}

// Download reads the program of a manifest into dest. The file is only there when its size and its sha256 are the
// ones of the manifest: it is written to a file of its own name plus ".part" and renamed when it is right. A source
// that sends nothing for downloadIdle is given up on (for an http(s) source; a file that is read from a share that
// stops answering is left to the system).
func Download(ctx context.Context, src Source, m Manifest, dest string) error {
	reading, stop := context.WithCancel(ctx)
	defer stop()
	idle := time.AfterFunc(downloadIdle, stop)
	defer idle.Stop()
	rc, size, err := src.Open(reading, m)
	if err != nil {
		return err
	}
	defer rc.Close()
	if size >= 0 && size != m.Size {
		return fmt.Errorf("%w: it is %d bytes, the manifest says %d", ErrMismatch, size, m.Size)
	}
	part := dest + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("write the program: %w", err)
	}
	sum := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, sum), io.LimitReader(idleTimer{rc, idle}, m.Size+1))
	closeErr := f.Close()
	fail := func(err error) error {
		_ = os.Remove(part)
		return err
	}
	switch {
	case copyErr != nil:
		if ctx.Err() == nil && reading.Err() != nil {
			copyErr = fmt.Errorf("nothing was received for %s", downloadIdle)
		}
		return fail(fmt.Errorf("read the program: %w", copyErr))
	case closeErr != nil:
		return fail(fmt.Errorf("write the program: %w", closeErr))
	case n != m.Size:
		return fail(fmt.Errorf("%w: it is %d bytes or more, the manifest says %d", ErrMismatch, n, m.Size))
	case hex.EncodeToString(sum.Sum(nil)) != m.SHA256:
		return fail(fmt.Errorf("%w: its sha256 is not the one of the manifest", ErrMismatch))
	}
	if err := renameRetry(part, dest); err != nil {
		return fail(fmt.Errorf("write the program: %w", err))
	}
	return nil
}

// renameRetry renames a file. A virus scanner or the search indexer may hold a new file for a moment, which Windows
// reports as an error, so a failure is tried again a few times.
func renameRetry(from, to string) error {
	var err error
	for i := 0; i < 5; i++ {
		if err = os.Rename(from, to); err == nil || errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

// Swap puts the staged program where the running one is. The running program is renamed (Windows lets a running
// program be renamed but neither overwritten nor deleted), kept under PreviousPath for a manual way back, and the
// staged one takes its name. When that fails the running program gets its name back, so nothing changes.
func Swap(exe, staged, currentVersion string) (previous string, err error) {
	previous = PreviousPath(exe, currentVersion)
	if err := os.Remove(previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		// An older copy of this very version is still in use (the launcher that was started from it runs on): it
		// stays, and this one gets a name of its own.
		previous = PreviousPath(exe, currentVersion+"-"+time.Now().UTC().Format("20060102T150405"))
	}
	if err := renameRetry(exe, previous); err != nil {
		return "", fmt.Errorf("move the running program aside: %w", err)
	}
	if err := renameRetry(staged, exe); err != nil {
		if back := renameRetry(previous, exe); back != nil {
			return "", fmt.Errorf("put the new program in place: %v; and the running program could not be put back: %w", err, back)
		}
		return "", fmt.Errorf("put the new program in place: %w", err)
	}
	pruneOld(exe, 3)
	return previous, nil
}

// pruneOld deletes the oldest kept programs, keeping the newest keep of them. A program that is still running (the
// one that waits for the new one to end, see Supervise) cannot be deleted and stays.
func pruneOld(exe string, keep int) {
	dir, base := filepath.Split(exe)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext) + ".old-"
	entries, err := os.ReadDir(dirOrDot(dir))
	if err != nil {
		return
	}
	type kept struct {
		name string
		at   time.Time
	}
	var found []kept
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		if info, err := e.Info(); err == nil {
			found = append(found, kept{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].at.After(found[j].at) })
	for i := keep; i < len(found); i++ {
		_ = os.Remove(filepath.Join(dirOrDot(dir), found[i].name))
	}
}

func dirOrDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

// Marker is left next to the program by the service that puts a new one in place, so that the new one can say that it
// has taken over.
type Marker struct {
	From          string    `json:"from"`
	To            string    `json:"to"`
	SHA256        string    `json:"sha256"`
	At            time.Time `json:"at"`
	WaitedSeconds int64     `json:"waited_seconds"`
}

// MarkerPath is where the marker of the program at exe is.
func MarkerPath(exe string) string { return exe + ".update.json" }

// WriteMarker stores a marker, through a temporary file so that a half-written one is never read.
func WriteMarker(exe string, m Marker) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := MarkerPath(exe) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return renameRetry(tmp, MarkerPath(exe))
}

// TakeMarker reads the marker of the program at exe and removes it. ok is false when there is none (or it cannot be
// read: it only says that an update took place, nothing depends on it).
func TakeMarker(exe string) (m Marker, ok bool) {
	path := MarkerPath(exe)
	data, err := os.ReadFile(path)
	if err != nil {
		return Marker{}, false
	}
	_ = os.Remove(path)
	if json.Unmarshal(data, &m) != nil || m.To == "" {
		return Marker{}, false
	}
	return m, true
}
