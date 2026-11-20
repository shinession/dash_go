package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSource serves a release from memory. The program of a release is the text "dash_go <version>", which is also
// what fakeChecker takes for the version the program says it is.
type fakeSource struct {
	mu        sync.Mutex
	manifest  Manifest
	program   []byte
	err       error
	manifests int
	opens     int
}

func (s *fakeSource) Address() string { return "fake://release/dash_go.json" }

func (s *fakeSource) Manifest(context.Context) (Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifests++
	return s.manifest, s.err
}

func (s *fakeSource) Open(context.Context, Manifest) (io.ReadCloser, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens++
	return io.NopCloser(bytes.NewReader(s.program)), int64(len(s.program)), nil
}

// publish makes the manifest name a version and the program the given text, with the size and hash that fit.
func (s *fakeSource) publish(version, program string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.program = []byte(program)
	s.manifest = Manifest{LatestVersion: version, SHA256: sum(s.program), PackageURL: "dash_go.exe", Size: int64(len(s.program))}
	s.err = nil
}

func (s *fakeSource) release(version string) { s.publish(version, "dash_go "+version) }

// corrupt makes the program differ from the manifest, with the same size.
func (s *fakeSource) corrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.program = bytes.ToUpper(s.program)
}

func (s *fakeSource) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *fakeSource) counts() (manifests, opens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifests, s.opens
}

type fakeChecker struct {
	mu        sync.Mutex
	configErr error
}

func (c *fakeChecker) Version(_ context.Context, program string) (string, error) {
	data, err := os.ReadFile(program)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(data), "dash_go "), nil
}

func (c *fakeChecker) Config(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.configErr
}

func (c *fakeChecker) setConfigErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configErr = err
}

// fakeService stands for the HTTP side of the service: somebody works with it (busy), or a number of requests that
// change something are served (running).
type fakeService struct {
	mu       sync.Mutex
	busy     string
	running  int
	draining bool
	drains   int
	resumes  int
}

func (d *fakeService) Busy() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.busy
}

func (d *fakeService) Drain() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.draining = true
	d.drains++
}

func (d *fakeService) Resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.draining = false
	d.resumes++
}

func (d *fakeService) Running() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

func (d *fakeService) setBusy(reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.busy = reason
}

func (d *fakeService) setRunning(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running = n
}

func (d *fakeService) state() (draining bool, drains, resumes int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.draining, d.drains, d.resumes
}

// clock is a time that only moves when a test says so.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// logCatcher keeps the log of an updater, so that a test can say what was told to the administrator.
type logCatcher struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCatcher) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logCatcher) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type rig struct {
	t     *testing.T
	exe   string
	src   *fakeSource
	chk   *fakeChecker
	svc   *fakeService
	clock *clock
	logs  *logCatcher
	u     *Updater
}

// newRig makes an updater for a program of version 0.1.2 that sees a release of the same version. The timers are
// long, so that a test makes the updater look by calling check itself, unless it tunes them.
func newRig(t *testing.T, tune ...func(*Options)) *rig {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "dash_go.exe")
	writeFile(t, exe, []byte("dash_go 0.1.2"))
	r := &rig{t: t, exe: exe, src: &fakeSource{}, chk: &fakeChecker{}, svc: &fakeService{}, clock: &clock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, logs: &logCatcher{}}
	r.src.release("0.1.2")
	o := Options{
		Source: r.src, Checker: r.chk, Drainer: r.svc, Current: "0.1.2", ExePath: exe,
		Interval: time.Hour, FirstCheck: time.Hour, Poll: 5 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now: r.clock.now,
	}
	for _, f := range tune {
		f(&o)
	}
	u, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	r.u = u
	return r
}

func (r *rig) staged() string { return StagedPath(r.exe) }

// poll does what the loop of Run does on every tick, for a test that drives the updater itself.
func (r *rig) poll() (*Update, bool) {
	if p := r.u.pending; p != nil && p.ready {
		return r.u.proceed(bg)
	}
	return nil, false
}

var bg = context.Background()

func TestTheRunningVersionIsLeftAlone(t *testing.T) {
	r := newRig(t)
	r.u.check(bg)
	st := r.u.Status()
	if st.State != StateIdle || st.Latest != "0.1.2" || st.Current != "0.1.2" || st.CheckedAt.IsZero() || st.LastError != "" || st.Draining {
		t.Fatalf("status = %+v", st)
	}
	if st.Address != "fake://release/dash_go.json" {
		t.Errorf("address = %q", st.Address)
	}
	if _, opens := r.src.counts(); opens != 0 {
		t.Error("the program was downloaded")
	}
	if _, drains, _ := r.svc.state(); drains != 0 {
		t.Error("the service was drained")
	}
}

// The service is not touched while a release is only ready: people work as they did until a quiet moment comes.
func TestADifferentVersionIsExaminedAndWaitsWithoutTouchingTheService(t *testing.T) {
	r := newRig(t)
	r.svc.setBusy("somebody is working")
	r.src.release("0.1.3")
	r.u.check(bg)

	st := r.u.Status()
	if st.State != StateWaiting || st.Latest != "0.1.3" || st.Since.IsZero() || st.Draining {
		t.Fatalf("status = %+v", st)
	}
	if got := readFile(t, r.staged()); got != "dash_go 0.1.3" {
		t.Errorf("the staged program is %q", got)
	}
	if got := readFile(t, r.exe); got != "dash_go 0.1.2" {
		t.Errorf("the running program changed: %q", got)
	}
	if _, drains, _ := r.svc.state(); drains != 0 {
		t.Errorf("the service was drained %d times before a quiet moment", drains)
	}
	if !strings.Contains(r.logs.String(), "the new version is ready") {
		t.Errorf("the administrator was not told:\n%s", r.logs)
	}

	r.u.check(bg) // the same release again changes nothing
	if _, opens := r.src.counts(); opens != 1 {
		t.Errorf("downloaded %d times", opens)
	}
}

func TestAReleaseWaitsForAQuietMomentThenDrainsAndRestartsWhenTheRequestsAreThrough(t *testing.T) {
	r := newRig(t, func(o *Options) { o.FirstCheck = time.Millisecond; o.Reminder = time.Minute })
	r.svc.setBusy("a request that changes something ran 3s ago")
	r.src.release("0.1.3")
	ctx, cancel := context.WithTimeout(bg, 20*time.Second)
	defer cancel()
	done := make(chan *Update, 1)
	go func() { done <- r.u.Run(ctx) }()

	waitFor(t, "the updater to wait", func() bool { return r.u.Status().State == StateWaiting && r.u.Status().Busy != "" })
	select {
	case <-done:
		t.Fatal("the service was restarted while somebody works with it")
	case <-time.After(100 * time.Millisecond):
	}
	if draining, drains, _ := r.svc.state(); draining || drains != 0 {
		t.Errorf("requests were refused while somebody works (draining=%v, drains=%d)", draining, drains)
	}
	if st := r.u.Status(); st.Busy != "a request that changes something ran 3s ago" {
		t.Errorf("busy = %q", st.Busy)
	}

	// The quiet moment comes, but a request that began just before it is still served: it is waited for.
	r.svc.setRunning(1)
	r.svc.setBusy("")
	waitFor(t, "the service to be drained", func() bool { d, _, _ := r.svc.state(); return d })
	select {
	case <-done:
		t.Fatal("the service was restarted while a request still runs")
	case <-time.After(100 * time.Millisecond):
	}
	if st := r.u.Status(); !st.Draining || st.Running != 1 || st.Busy != "" {
		t.Errorf("status = %+v", st)
	}

	r.svc.setRunning(0)
	var up *Update
	select {
	case up = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no restart after the last request ended")
	}
	if up == nil || up.From != "0.1.2" || up.To != "0.1.3" || up.SHA256 != sum([]byte("dash_go 0.1.3")) || up.Previous != PreviousPath(r.exe, "0.1.2") {
		t.Fatalf("update = %+v", up)
	}
	if got := readFile(t, r.exe); got != "dash_go 0.1.3" {
		t.Errorf("the program is %q", got)
	}
	if got := readFile(t, up.Previous); got != "dash_go 0.1.2" {
		t.Errorf("the kept program is %q", got)
	}
	if exists(r.staged()) {
		t.Error("the staged program is still there")
	}
	marker, ok := TakeMarker(r.exe)
	if !ok || marker.From != "0.1.2" || marker.To != "0.1.3" {
		t.Errorf("marker = %+v, %v", marker, ok)
	}
	if _, drains, resumes := r.svc.state(); drains != 1 || resumes != 0 {
		t.Errorf("drains = %d, resumes = %d", drains, resumes)
	}
}

func TestTheReasonForWaitingIsLoggedOnceAndThenNowAndThen(t *testing.T) {
	r := newRig(t, func(o *Options) { o.Reminder = 5 * time.Minute })
	r.svc.setBusy("a sign-in is under way")
	r.src.release("0.1.3")
	r.u.check(bg)
	count := func() int { return strings.Count(r.logs.String(), "waits for a quiet moment") }

	r.poll()
	r.poll()
	r.clock.advance(time.Minute)
	r.poll()
	if count() != 1 || !strings.Contains(r.logs.String(), "a sign-in is under way") {
		t.Fatalf("the reason was logged %d times:\n%s", count(), r.logs)
	}
	r.clock.advance(5 * time.Minute)
	r.poll()
	if count() != 2 {
		t.Fatalf("no reminder after the reminder time (%d)", count())
	}
}

func TestRequestsThatDoNotEndAreNotWaitedForForEver(t *testing.T) {
	r := newRig(t, func(o *Options) { o.DrainTimeout = 30 * time.Second })
	r.src.release("0.1.3")
	r.u.check(bg)
	r.svc.setRunning(1)

	if up, ok := r.poll(); ok || up != nil {
		t.Fatalf("poll = %v, %v", up, ok)
	}
	if draining, drains, _ := r.svc.state(); !draining || drains != 1 {
		t.Fatalf("draining = %v after %d drains", draining, drains)
	}
	r.clock.advance(29 * time.Second)
	r.poll()
	if draining, _, resumes := r.svc.state(); !draining || resumes != 0 {
		t.Fatalf("the requests were taken again too early (draining=%v, resumes=%d)", draining, resumes)
	}

	r.clock.advance(2 * time.Second)
	r.poll()
	if draining, _, resumes := r.svc.state(); draining || resumes != 1 {
		t.Fatalf("draining = %v, resumes = %d, want the requests taken again", draining, resumes)
	}
	if st := r.u.Status(); st.Draining || st.State != StateWaiting {
		t.Errorf("status = %+v", st)
	}
	if !strings.Contains(r.logs.String(), "did not end in time") {
		t.Errorf("the administrator was not told:\n%s", r.logs)
	}

	// The request ends and the service is quiet: the update goes on, from the start of the wait for a quiet moment.
	r.svc.setRunning(0)
	up, ok := r.poll()
	if !ok || up == nil || up.To != "0.1.3" {
		t.Fatalf("poll = %v, %v", up, ok)
	}
	if _, drains, resumes := r.svc.state(); drains != 2 || resumes != 1 {
		t.Errorf("drains = %d, resumes = %d", drains, resumes)
	}
}

func TestTheReleaseGoingBackToTheRunningVersionCancelsTheUpdate(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.3")
	r.u.check(bg)
	r.svc.setRunning(1)
	r.poll() // the service is quiet: requests are refused, and the one that runs is waited for
	if draining, _, _ := r.svc.state(); !draining {
		t.Fatal("the service was not drained")
	}

	r.src.release("0.1.2")
	r.u.check(bg)
	if draining, _, resumes := r.svc.state(); draining || resumes != 1 {
		t.Errorf("draining = %v, resumes = %d", draining, resumes)
	}
	if exists(r.staged()) {
		t.Error("the staged program was kept")
	}
	if st := r.u.Status(); st.State != StateIdle || st.Draining || !st.Since.IsZero() {
		t.Errorf("status = %+v", st)
	}
	if !strings.Contains(r.logs.String(), "nothing is installed") {
		t.Errorf("the administrator was not told:\n%s", r.logs)
	}
}

func TestAnotherReleaseWhileWaitingReplacesTheStagedProgram(t *testing.T) {
	r := newRig(t)
	r.svc.setBusy("somebody is working")
	r.src.release("0.1.3")
	r.u.check(bg)
	r.src.release("0.1.4")
	r.u.check(bg)

	if got := readFile(t, r.staged()); got != "dash_go 0.1.4" {
		t.Errorf("the staged program is %q", got)
	}
	if st := r.u.Status(); st.State != StateWaiting || st.Latest != "0.1.4" {
		t.Errorf("status = %+v", st)
	}
	if _, opens := r.src.counts(); opens != 2 {
		t.Errorf("downloaded %d times, want twice", opens)
	}
}

func TestADownloadThatDoesNotMatchIsTriedAgain(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.3")
	r.src.corrupt() // a release that is still being copied
	r.u.check(bg)

	st := r.u.Status()
	if st.State != StateError || !strings.Contains(st.LastError, "not the one the manifest describes") {
		t.Fatalf("status = %+v", st)
	}
	if exists(r.staged()) || exists(r.staged()+".part") {
		t.Error("a program that does not match was kept")
	}
	if _, drains, _ := r.svc.state(); drains != 0 {
		t.Error("the service was drained")
	}

	r.src.release("0.1.3") // the copy is complete
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateWaiting || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestAProgramOfAnotherVersionThanTheManifestIsNotTriedAgainUntilTheManifestChanges(t *testing.T) {
	r := newRig(t)
	r.src.publish("0.1.3", "dash_go 0.1.4") // the manifest and the file agree, but the file is another version
	r.u.check(bg)

	st := r.u.Status()
	if st.State != StateError || !strings.Contains(st.LastError, "version 0.1.4") {
		t.Fatalf("status = %+v", st)
	}
	if exists(r.staged()) {
		t.Error("the program was kept")
	}
	_, opens := r.src.counts()
	r.u.check(bg)
	if _, again := r.src.counts(); again != opens {
		t.Error("the same release was downloaded again")
	}
	if st := r.u.Status(); st.State != StateError {
		t.Errorf("status = %+v", st)
	}
	if _, drains, _ := r.svc.state(); drains != 0 {
		t.Error("the service was drained")
	}

	r.src.release("0.1.4")
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateWaiting || st.Latest != "0.1.4" {
		t.Fatalf("status = %+v", st)
	}
}

func TestAProgramThatDoesNotAcceptTheSettingsIsNotInstalled(t *testing.T) {
	r := newRig(t)
	r.chk.setConfigErr(errors.New("the new program does not accept the settings of this service: no password"))
	r.src.release("0.1.3")
	r.u.check(bg)

	if st := r.u.Status(); st.State != StateError || !strings.Contains(st.LastError, "no password") {
		t.Fatalf("status = %+v", st)
	}
	if up, ok := r.poll(); ok || up != nil {
		t.Fatalf("a program that does not accept the settings was installed: %v", up)
	}
	if _, drains, _ := r.svc.state(); drains != 0 {
		t.Error("the service was drained")
	}

	r.chk.setConfigErr(nil) // the administrator fixed the settings
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateWaiting || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if _, opens := r.src.counts(); opens != 1 {
		t.Errorf("downloaded %d times, want once", opens)
	}
}

func TestAStagedProgramThatWasRemovedIsDownloadedAgain(t *testing.T) {
	r := newRig(t)
	r.chk.setConfigErr(errors.New("the new program does not accept the settings of this service: no password"))
	r.src.release("0.1.3")
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateError {
		t.Fatalf("status = %+v", st)
	}

	if err := os.Remove(r.staged()); err != nil { // a virus scanner took it away
		t.Fatal(err)
	}
	r.chk.setConfigErr(nil)
	r.u.check(bg)
	if st := r.u.Status(); st.State != StateWaiting || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if _, opens := r.src.counts(); opens != 2 {
		t.Errorf("downloaded %d times, want twice (the program was gone the second time)", opens)
	}
}

func TestAFailedInstallationTakesRequestsAgainAndIsNotRepeated(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.3")
	r.u.check(bg)
	if err := os.Remove(r.staged()); err != nil { // the new program cannot be moved any more
		t.Fatal(err)
	}

	// The service is quiet and nothing runs, so the installation is tried at once.
	if up, ok := r.poll(); ok || up != nil {
		t.Fatalf("poll = %v, %v", up, ok)
	}
	if got := readFile(t, r.exe); got != "dash_go 0.1.2" {
		t.Errorf("the program is %q, want the running one back", got)
	}
	if draining, drains, resumes := r.svc.state(); draining || drains != 1 || resumes != 1 {
		t.Errorf("draining = %v, drains = %d, resumes = %d", draining, drains, resumes)
	}
	if st := r.u.Status(); st.State != StateError || !strings.Contains(st.LastError, "put in place") || st.Draining {
		t.Fatalf("status = %+v", st)
	}

	_, opens := r.src.counts()
	r.u.check(bg)
	r.poll()
	if _, again := r.src.counts(); again != opens {
		t.Error("the release was downloaded again")
	}
	if _, drains, _ := r.svc.state(); drains != 1 {
		t.Errorf("the service was drained again (%d)", drains)
	}
	if n := strings.Count(r.logs.String(), "a step failed"); n != 1 {
		t.Errorf("the failure was logged %d times", n)
	}
}

func TestAManifestThatCannotBeReadWhileWaitingKeepsWaiting(t *testing.T) {
	r := newRig(t)
	r.svc.setBusy("somebody is working")
	r.src.release("0.1.3")
	r.u.check(bg)
	r.src.fail(errors.New("the server is down"))
	r.u.check(bg)

	st := r.u.Status()
	if st.State != StateWaiting || st.LastError != "the server is down" {
		t.Fatalf("status = %+v", st)
	}
	if _, _, resumes := r.svc.state(); resumes != 0 {
		t.Error("requests were taken again")
	}

	r.svc.setBusy("")
	if up, ok := r.poll(); !ok || up.To != "0.1.3" {
		t.Fatalf("poll = %v, %v", up, ok)
	}
}

func TestTheSameFailureIsLoggedOnceUntilALookGoesThrough(t *testing.T) {
	r := newRig(t)
	r.src.release("0.1.3")
	r.src.corrupt()
	for i := 0; i < 3; i++ {
		r.u.check(bg)
	}
	count := func() int { return strings.Count(r.logs.String(), "a step failed") }
	if count() != 1 {
		t.Fatalf("logged %d times:\n%s", count(), r.logs)
	}

	r.src.release("0.1.2") // a look without a failure
	r.u.check(bg)
	r.src.release("0.1.3")
	r.src.corrupt()
	r.u.check(bg)
	if count() != 2 {
		t.Fatalf("logged %d times:\n%s", count(), r.logs)
	}
}

func TestNoLookIsMadeWhileRequestsAreRefused(t *testing.T) {
	r := newRig(t, func(o *Options) { o.FirstCheck = time.Millisecond; o.Interval = 5 * time.Millisecond })
	r.src.release("0.1.3")
	r.svc.setRunning(1) // the service gets drained and then waits for this request
	ctx, cancel := context.WithCancel(bg)
	done := make(chan *Update, 1)
	go func() { done <- r.u.Run(ctx) }()

	waitFor(t, "the service to be drained", func() bool { d, _, _ := r.svc.state(); return d })
	time.Sleep(50 * time.Millisecond) // a few intervals pass
	before, _ := r.src.counts()
	time.Sleep(100 * time.Millisecond)
	if after, _ := r.src.counts(); after != before {
		t.Errorf("the manifest was read %d times while requests were refused", after-before)
	}
	cancel()
	<-done
}

func TestRunRemovesWhatAnEarlierRunLeftOfADownload(t *testing.T) {
	r := newRig(t)
	writeFile(t, r.staged(), []byte("left"))
	writeFile(t, r.staged()+".part", []byte("left"))
	ctx, cancel := context.WithCancel(bg)
	done := make(chan *Update, 1)
	go func() { done <- r.u.Run(ctx) }()
	waitFor(t, "the leftovers to be removed", func() bool {
		_, a := os.Stat(r.staged())
		_, b := os.Stat(r.staged() + ".part")
		return errors.Is(a, fs.ErrNotExist) && errors.Is(b, fs.ErrNotExist)
	})
	cancel()
	<-done
}

func TestNewNeedsWhatItWorksWith(t *testing.T) {
	good := Options{Source: &fakeSource{}, Drainer: &fakeService{}, Current: "0.1.2", ExePath: "dash_go.exe"}
	if _, err := New(good); err != nil {
		t.Fatalf("New: %v", err)
	}
	for name, tune := range map[string]func(*Options){
		"no source":  func(o *Options) { o.Source = nil },
		"no drainer": func(o *Options) { o.Drainer = nil },
		"no program": func(o *Options) { o.ExePath = "" },
		"no version": func(o *Options) { o.Current = " " },
	} {
		o := good
		tune(&o)
		if _, err := New(o); err == nil {
			t.Errorf("%s: New worked", name)
		}
	}
}
