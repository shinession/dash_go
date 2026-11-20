package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// State is what the updater is doing.
type State string

const (
	// StateIdle: the running version is the latest one, or nothing has been checked yet.
	StateIdle State = "idle"
	// StateChecking: the manifest is being read.
	StateChecking State = "checking"
	// StateDownloading: the new program is being downloaded and examined.
	StateDownloading State = "downloading"
	// StateWaiting: a new program is ready. The service waits for a moment in which nobody is clicking or submitting,
	// then takes no more requests that change something, and restarts when the ones that run are through.
	StateWaiting State = "waiting"
	// StateApplying: the new program is being put in place.
	StateApplying State = "applying"
	// StateError: the last check, download or installation failed. The service runs on as it is.
	StateError State = "error"
)

// Status is what the updater says about itself.
type Status struct {
	Address   string
	Interval  time.Duration
	Current   string
	Latest    string
	State     State
	CheckedAt time.Time
	LastError string
	// Busy is why a release that is ready is not installed yet: what somebody is doing with the service. It is empty
	// when nothing is in the way.
	Busy string
	// Draining is true from the quiet moment on: requests that change something are refused, and Running is the
	// number of them that are still served.
	Draining bool
	Running  int
	// Since is when the release became ready.
	Since time.Time
}

// Drainer is the part of the service an update needs. The service is busy while somebody is working with it: a
// request that changes something (a click or a submit of a page) is served or was served a moment ago, or a sign-in
// is under way. Drain stops it from taking requests that change something, Resume takes them again, and Running is
// the number of such requests that are still served.
type Drainer interface {
	// Busy says why the service should not restart now, or "" when nobody is working with it.
	Busy() string
	Drain()
	Resume()
	Running() int
}

// Options configures an Updater.
type Options struct {
	Source  Source
	Checker Checker
	Drainer Drainer
	// Current is the version that runs; ExePath is the program that runs.
	Current string
	ExePath string
	// Interval is the time between two looks at the manifest.
	Interval time.Duration
	// FirstCheck is how long after Run starts the first look is made; zero means 10 seconds.
	FirstCheck time.Duration
	// Poll is how often a release that is ready looks whether the service is quiet; zero means 2 seconds.
	Poll time.Duration
	// Reminder is how often the log says that a release still waits for a quiet moment; zero means 5 minutes.
	Reminder time.Duration
	// DrainTimeout is how long requests that change something are refused while the ones that were running are waited
	// for. A request that does not end in that time is not waited for any longer: the requests are taken again and the
	// update waits for the next quiet moment. Zero means 1 minute.
	DrainTimeout time.Duration
	Log          *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Update is a new program that has been put in place of the running one and is to be started.
type Update struct {
	From, To string
	SHA256   string
	// Previous is where the program that ran until now was kept.
	Previous string
	// Waited is how long the release waited for a quiet moment and for the running requests.
	Waited time.Duration
}

// pending is the release the updater is working towards.
type pending struct {
	m    Manifest
	path string
	// staged: the program is on disk with the size and the hash of the manifest.
	staged bool
	// ready: it was examined. draining: the quiet moment came and the service takes no more requests that change
	// something.
	ready, draining bool
	// bad is why this release is not tried again until the manifest changes: the program is not the version the
	// manifest says, or it could not be put in place.
	bad string
	// since is when the release became ready; drainedAt is when the requests that change something were stopped.
	since, drainedAt time.Time
	// announced: the download of this release has been logged once.
	announced bool
}

// Updater watches a release manifest and replaces the running program when the version in it differs.
type Updater struct {
	o Options

	mu          sync.Mutex
	st          Status
	ready       bool
	lastFailure string

	// pending, failed and busyLoggedAt belong to the goroutine of Run.
	pending      *pending
	failed       bool
	busyLoggedAt time.Time
}

// New makes an Updater; call Run to start it.
func New(o Options) (*Updater, error) {
	switch {
	case o.Source == nil || o.Drainer == nil:
		return nil, errors.New("update: a source and the service to drain are needed")
	case o.ExePath == "" || NormalizeVersion(o.Current) == "":
		return nil, errors.New("update: the program and its version are needed")
	}
	if o.Interval <= 0 {
		o.Interval = 10 * time.Minute
	}
	if o.FirstCheck <= 0 {
		o.FirstCheck = 10 * time.Second
	}
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}
	if o.Reminder <= 0 {
		o.Reminder = 5 * time.Minute
	}
	if o.DrainTimeout <= 0 {
		o.DrainTimeout = time.Minute
	}
	if o.Checker == nil {
		o.Checker = ExecChecker{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.Current = NormalizeVersion(o.Current)
	u := &Updater{o: o}
	u.st = Status{Address: o.Source.Address(), Interval: o.Interval, Current: o.Current, State: StateIdle}
	return u, nil
}

// Status describes what the updater is doing.
func (u *Updater) Status() Status {
	u.mu.Lock()
	st := u.st
	u.mu.Unlock()
	if st.Draining {
		st.Running = u.o.Drainer.Running()
	}
	return st
}

// Run looks at the manifest now and then until ctx ends or a new program has been put in place, which it returns:
// the caller stops the service and starts the new program. While a new program is ready the service goes on as
// usual until it is quiet; from then on no request that changes something is taken, so the running ones are the last.
func (u *Updater) Run(ctx context.Context) *Update {
	u.removeLeftovers()
	timer := time.NewTimer(u.o.FirstCheck)
	defer timer.Stop()
	poll := time.NewTicker(u.o.Poll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			// Once requests are refused the update is under way; a manifest that answers slowly must not prolong that.
			if p := u.pending; p == nil || !p.draining {
				u.check(ctx)
			}
			timer.Reset(u.o.Interval)
		case <-poll.C:
			if p := u.pending; p != nil && p.ready {
				if update, ok := u.proceed(ctx); ok {
					return update
				}
			}
		}
	}
}

// removeLeftovers deletes what an earlier run of the service left of a download.
func (u *Updater) removeLeftovers() {
	staged := StagedPath(u.o.ExePath)
	_ = os.Remove(staged)
	_ = os.Remove(staged + ".part")
}

func (u *Updater) setState(s State) {
	u.mu.Lock()
	u.st.State = s
	u.mu.Unlock()
}

// fail records why a step did not work. The service goes on as it is; the step is tried again at the next look. The
// same text is only logged once until a look goes through without a failure, so a server that stays unreachable
// does not fill the log.
func (u *Updater) fail(ctx context.Context, step string, err error) {
	if ctx.Err() != nil {
		return
	}
	u.failed = true
	msg := err.Error()
	u.mu.Lock()
	u.st.LastError = msg
	u.st.State = StateError
	if u.ready {
		u.st.State = StateWaiting
	}
	repeat := msg == u.lastFailure
	u.lastFailure = msg
	u.mu.Unlock()
	if repeat {
		u.o.Log.Debug("automatic update: still failing", "step", step, "error", msg)
		return
	}
	u.o.Log.Warn("automatic update: a step failed; the service runs on as it is", "step", step, "error", msg)
}

// check reads the manifest and acts on what it says.
func (u *Updater) check(ctx context.Context) {
	u.failed = false
	defer func() {
		if !u.failed {
			u.mu.Lock()
			u.lastFailure = ""
			u.mu.Unlock()
		}
	}()
	u.mu.Lock()
	if !u.ready {
		u.st.State = StateChecking
	}
	u.mu.Unlock()
	m, err := u.o.Source.Manifest(ctx)
	if err != nil {
		u.fail(ctx, "check", err)
		return
	}
	u.mu.Lock()
	u.st.Latest = m.LatestVersion
	u.st.CheckedAt = u.o.Now()
	u.st.LastError = ""
	u.mu.Unlock()

	if SameVersion(m.LatestVersion, u.o.Current) {
		u.cancel()
		u.mu.Lock()
		u.st.State, u.st.LastError = StateIdle, ""
		u.mu.Unlock()
		return
	}
	if u.pending != nil && u.pending.m != m {
		u.dropPending()
	}
	if u.pending == nil {
		u.pending = &pending{m: m, path: StagedPath(u.o.ExePath)}
	}
	p := u.pending
	switch {
	case p.bad != "":
		u.fail(ctx, "program", errors.New(p.bad))
	case p.ready:
		u.setState(StateWaiting)
	default:
		u.stage(ctx, p)
	}
}

// stage downloads and examines the program of a release; when it is fine the release is ready.
func (u *Updater) stage(ctx context.Context, p *pending) {
	if p.staged {
		// A virus scanner may have taken the program away after it was downloaded.
		if st, err := os.Stat(p.path); err != nil || st.Size() != p.m.Size {
			p.staged = false
		}
	}
	if !p.staged {
		u.setState(StateDownloading)
		if p.announced {
			u.o.Log.Debug("automatic update: downloading the released version again", "released", p.m.LatestVersion)
		} else {
			p.announced = true
			u.o.Log.Info("automatic update: a different version is released, downloading it", "running", u.o.Current, "released", p.m.LatestVersion, "size", p.m.Size)
		}
		if err := Download(ctx, u.o.Source, p.m, p.path); err != nil {
			u.fail(ctx, "download", err)
			return
		}
		p.staged = true
	}
	version, err := u.o.Checker.Version(ctx, p.path)
	if err != nil {
		u.fail(ctx, "program", err)
		return
	}
	if !SameVersion(version, p.m.LatestVersion) {
		p.bad = fmt.Sprintf("the program of the release says it is version %s, but the manifest says %s", version, p.m.LatestVersion)
		p.staged = false
		_ = os.Remove(p.path)
		u.fail(ctx, "program", errors.New(p.bad))
		return
	}
	if err := u.o.Checker.Config(ctx, p.path); err != nil {
		u.fail(ctx, "program", err)
		return
	}
	p.ready, p.since = true, u.o.Now()
	u.busyLoggedAt = time.Time{}
	u.mu.Lock()
	u.ready = true
	u.st.State, u.st.LastError, u.st.Since = StateWaiting, "", p.since
	u.mu.Unlock()
	u.o.Log.Info("automatic update: the new version is ready; it is installed as soon as nobody is clicking or submitting",
		"running", u.o.Current, "released", p.m.LatestVersion)
}

// proceed moves a release that is ready towards the restart. It waits for a moment in which nobody is working with
// the service (the service goes on as usual until then), then stops taking requests that change something, and
// returns the update once the ones that run are through.
func (u *Updater) proceed(ctx context.Context) (*Update, bool) {
	p := u.pending
	if !p.draining {
		if reason := u.o.Drainer.Busy(); reason != "" {
			u.waitingFor(p, reason)
			return nil, false
		}
		// A request may start between the look and the drain: it is one of the running ones, and is waited for.
		u.o.Drainer.Drain()
		p.draining, p.drainedAt = true, u.o.Now()
		u.mu.Lock()
		u.st.Draining, u.st.Busy = true, ""
		u.mu.Unlock()
		u.o.Log.Info("automatic update: nobody is working with the service; requests that change something are not taken any more, and the service restarts when the running ones are through",
			"released", p.m.LatestVersion, "running_requests", u.o.Drainer.Running())
	}
	if running := u.o.Drainer.Running(); running > 0 {
		if u.o.Now().Sub(p.drainedAt) >= u.o.DrainTimeout {
			// One request that never ends must not keep the service from taking requests for good.
			u.o.Drainer.Resume()
			p.draining = false
			u.mu.Lock()
			u.st.Draining = false
			u.mu.Unlock()
			u.o.Log.Warn("automatic update: requests that were running did not end in time; requests that change something are taken again, and the update waits for the next quiet moment",
				"released", p.m.LatestVersion, "running_requests", running, "waited", u.o.DrainTimeout.String())
		}
		return nil, false
	}
	return u.apply(ctx)
}

// waitingFor records what is in the way of the update, and says so in the log now and then.
func (u *Updater) waitingFor(p *pending, reason string) {
	u.mu.Lock()
	u.st.Busy = reason
	u.mu.Unlock()
	now := u.o.Now()
	if u.busyLoggedAt.IsZero() || now.Sub(u.busyLoggedAt) >= u.o.Reminder {
		u.busyLoggedAt = now
		u.o.Log.Info("automatic update: the new version waits for a quiet moment", "released", p.m.LatestVersion, "busy", reason)
	}
}

// dropPending forgets the release that was being worked on and deletes its program; when it had stopped the requests
// that change something, they are taken again.
func (u *Updater) dropPending() {
	p := u.pending
	u.pending = nil
	if p == nil {
		return
	}
	_ = os.Remove(p.path)
	_ = os.Remove(p.path + ".part")
	if p.draining {
		u.o.Drainer.Resume()
	}
	if p.ready {
		u.mu.Lock()
		u.ready = false
		u.st.Since = time.Time{}
		u.st.Draining, u.st.Busy = false, ""
		u.mu.Unlock()
	}
}

// cancel ends an update that the manifest no longer asks for, because it names the running version again.
func (u *Updater) cancel() {
	p := u.pending
	if p == nil {
		return
	}
	wasReady, wasDraining, version := p.ready, p.draining, p.m.LatestVersion
	u.dropPending()
	if wasReady {
		u.o.Log.Info("automatic update: the release is the running version again, so nothing is installed", "released", version, "had_stopped_requests", wasDraining)
	}
}

// apply puts the new program in place. Nothing that changes something runs any more, so this is the moment; when it
// does not work the requests are taken again and the service goes on as it is.
func (u *Updater) apply(ctx context.Context) (*Update, bool) {
	p := u.pending
	u.setState(StateApplying)
	previous, err := Swap(u.o.ExePath, p.path, u.o.Current)
	if err != nil {
		p.bad = "the new program could not be put in place: " + err.Error()
		p.ready, p.draining = false, false
		u.mu.Lock()
		u.ready = false
		u.st.Draining, u.st.Busy = false, ""
		u.mu.Unlock()
		u.o.Drainer.Resume()
		u.fail(ctx, "install", errors.New(p.bad))
		return nil, false
	}
	waited := u.o.Now().Sub(p.since)
	if err := WriteMarker(u.o.ExePath, Marker{From: u.o.Current, To: p.m.LatestVersion, SHA256: p.m.SHA256, At: u.o.Now(), WaitedSeconds: int64(waited.Seconds())}); err != nil {
		u.o.Log.Warn("automatic update: the note for the new program could not be written", "error", err)
	}
	u.o.Log.Info("automatic update: the new program is in place; the service restarts", "from", u.o.Current, "to", p.m.LatestVersion,
		"waited", waited.Round(time.Second).String(), "previous_program", previous)
	return &Update{From: u.o.Current, To: p.m.LatestVersion, SHA256: p.m.SHA256, Previous: previous, Waited: waited}, true
}
