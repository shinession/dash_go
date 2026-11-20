package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
	"dash_go/internal/core/server"
	"dash_go/internal/core/update"
	"dash_go/internal/version"
)

// quietFor is how long nobody must have clicked or submitted before the service restarts for an update: somebody who
// submits one thing often submits the next one a moment later.
const quietFor = 10 * time.Second

// updateGate is the service as the updater sees it. The service is busy while somebody works with it: a request that
// changes something is served or was served a moment ago, or a Windows sign-in is under way (it is only in memory, so
// a restart would lose it). Requests that only read do not count, so the pages that poll do not hold an update back.
type updateGate struct {
	gate *server.Gate
	auth *auth.Service
}

func (g updateGate) Busy() string {
	if reason := g.gate.Busy(quietFor); reason != "" {
		return reason
	}
	if n := g.auth.PendingSignIns(); n > 0 {
		return fmt.Sprintf("%d Windows sign-in(s) are under way", n)
	}
	return ""
}

func (g updateGate) Drain()       { g.gate.Drain() }
func (g updateGate) Resume()      { g.gate.Resume() }
func (g updateGate) Running() int { return g.gate.Running() }

// newUpdater makes the updater of the service from its settings. exe is the path the program was started from.
func newUpdater(cfg *config.Config, log *slog.Logger, exe string, gate updateGate) (*update.Updater, error) {
	src, err := update.NewSource(cfg.Update.URL, nil)
	if err != nil {
		return nil, err
	}
	return update.New(update.Options{
		Source:   src,
		Checker:  update.ExecChecker{Args: os.Args[1:]},
		Drainer:  gate,
		Current:  version.Version,
		ExePath:  exe,
		Interval: cfg.Update.Interval,
		Log:      log,
	})
}

// folderWritable reports why the folder of the program cannot take a new file. The updater puts the new program next
// to the running one, so it needs the right to write there.
func folderWritable(exe string) error {
	probe, err := os.CreateTemp(filepath.Dir(exe), ".dash_go-write-test-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

// describeUpdate says in a few words what the service does about updates, for -check and the start-up log.
func describeUpdate(u config.Update) string {
	if u.URL == "" {
		return "off"
	}
	return fmt.Sprintf("%s every %s, restart %s", u.URL, u.Interval, u.Restart)
}
