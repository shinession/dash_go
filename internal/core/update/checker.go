package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Checker examines a downloaded program before it takes the place of the running one.
type Checker interface {
	// Version is the version the program says it is.
	Version(ctx context.Context, program string) (string, error)
	// Config reports whether the program accepts the settings of this service: the same environment and the same
	// configuration files, which it finds like the running program does.
	Config(ctx context.Context, program string) error
}

// ExecChecker runs the program with its -version and -check flags. The program is only run after its sha256 was
// found to be the one of the manifest.
type ExecChecker struct {
	// Args are the flags the running service was started with. The new program is checked with the same flags, so it
	// finds the same configuration the running one does.
	Args []string
	// Timeout limits each run; zero means 30 seconds. The first run of a new file can be slow while a virus scanner
	// looks at it.
	Timeout time.Duration
}

// Version runs "program -version", which prints "dash_go <version>" and then the modules.
func (c ExecChecker) Version(ctx context.Context, program string) (string, error) {
	out, err := c.run(ctx, program, "-version")
	if err != nil {
		return "", fmt.Errorf("run the new program: %w", err)
	}
	fields := strings.Fields(firstLine(out))
	if len(fields) != 2 || fields[0] != "dash_go" {
		return "", fmt.Errorf("the new program does not say what version it is (it printed %q)", firstLine(out))
	}
	return NormalizeVersion(fields[1]), nil
}

// Config runs "program <flags> -check", which loads the configuration and the modules and succeeds when the program
// accepts the settings. It starts no server.
func (c ExecChecker) Config(ctx context.Context, program string) error {
	out, err := c.run(ctx, program, append(append([]string(nil), c.Args...), "-check")...)
	if err == nil {
		return nil
	}
	// A program that did not answer in time has not said anything about the settings, whatever it logged before.
	reason := reasonOf(out)
	var slow *timeoutError
	if reason == "" || errors.As(err, &slow) {
		reason = err.Error()
	}
	return fmt.Errorf("the new program does not accept the settings of this service: %s", reason)
}

// timeoutError says that a program did not answer in time.
type timeoutError struct{ after time.Duration }

func (e *timeoutError) Error() string { return fmt.Sprintf("it did not answer within %s", e.after) }

func (c ExecChecker) run(ctx context.Context, program string, args ...string) (string, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = os.Environ()
	cmd.WaitDelay = 2 * time.Second
	out := &capped{max: 8 << 10}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out.String(), &timeoutError{timeout}
		}
		return out.String(), err
	}
	return out.String(), nil
}

// capped keeps the first bytes of what a program prints and drops the rest, so that a program that does not stop
// printing cannot fill the memory.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }

// firstLine is the first line of some output that has text.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return clipLine(line)
		}
	}
	return ""
}

// reasonOf is the line of the output of "-check" that says why it failed: the program writes its log to the same
// stream, so the first line is often a harmless one. It is a line of level ERROR or a configuration error; when there
// is none, the first line with text.
func reasonOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "level=ERROR") || strings.HasPrefix(line, "configuration error") {
			return clipLine(line)
		}
	}
	return firstLine(s)
}

func clipLine(line string) string {
	if len(line) > 300 {
		return line[:300] + "..."
	}
	return line
}
