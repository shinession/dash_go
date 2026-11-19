// Package logx builds the slog logger of the service: a daily rotating file plus the console.
package logx

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type dailyFile struct {
	mu   sync.Mutex
	dir  string
	day  string
	file *os.File
}

func (d *dailyFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := time.Now().Format("20060102")
	if d.file == nil || d.day != day {
		if d.file != nil {
			_ = d.file.Close()
			d.file = nil
		}
		if err := os.MkdirAll(d.dir, 0o755); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(filepath.Join(d.dir, fmt.Sprintf("dash_go-%s.log", day)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		d.file, d.day = f, day
	}
	return d.file.Write(p)
}

// New returns a logger writing to dir/dash_go-YYYYMMDD.log and to stderr. An empty dir logs to stderr only.
func New(dir string) *slog.Logger {
	var w io.Writer = os.Stderr
	if dir != "" {
		w = io.MultiWriter(os.Stderr, &dailyFile{dir: dir})
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
