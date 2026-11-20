package update

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// helperProgram is a program that answers like dash_go: the test program itself, told by its environment.
func helperProgram(t *testing.T, version string) string {
	t.Helper()
	t.Setenv(envHelper, "program")
	t.Setenv(envHelperVersion, version)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestExecCheckerReadsTheVersionOfAProgram(t *testing.T) {
	exe := helperProgram(t, "v0.2.0")
	got, err := ExecChecker{}.Version(context.Background(), exe)
	if err != nil || got != "0.2.0" {
		t.Fatalf("Version = %q, %v", got, err)
	}

	t.Setenv(envHelperRaw, "hello there world")
	if _, err := (ExecChecker{}).Version(context.Background(), exe); err == nil || !strings.Contains(err.Error(), "does not say what version") {
		t.Fatalf("a program that does not know the flag was accepted: %v", err)
	}

	t.Setenv(envHelperRaw, "other_program 0.2.0")
	if _, err := (ExecChecker{}).Version(context.Background(), exe); err == nil {
		t.Fatal("a program of another name was accepted")
	}

	if _, err := (ExecChecker{}).Version(context.Background(), filepath.Join(t.TempDir(), "nothing.exe")); err == nil {
		t.Fatal("a program that is not there was accepted")
	}
}

func TestExecCheckerSaysWhyTheSettingsAreNotAccepted(t *testing.T) {
	exe := helperProgram(t, "0.2.0")
	ctx := context.Background()
	if err := (ExecChecker{}).Config(ctx, exe); err != nil {
		t.Fatalf("Config: %v", err)
	}

	// The program logs to the same stream before it reports a problem: the reason is not the first line.
	t.Setenv(envHelperConfig, "bad")
	err := ExecChecker{}.Config(ctx, exe)
	if err == nil || !strings.Contains(err.Error(), "dash_api_url is not an address") || strings.Contains(err.Error(), "starting dash_go") {
		t.Fatalf("got %v", err)
	}

	t.Setenv(envHelperConfig, "plain")
	err = ExecChecker{}.Config(ctx, exe)
	if err == nil || !strings.Contains(err.Error(), "the login settings are wrong") {
		t.Fatalf("got %v", err)
	}

	t.Setenv(envHelperConfig, "flood")
	err = ExecChecker{}.Config(ctx, exe)
	if err == nil || len(err.Error()) > 600 {
		t.Fatalf("a program that prints without end was not held in: %v", err)
	}

	t.Setenv(envHelperConfig, "hang")
	started := time.Now()
	err = ExecChecker{Timeout: 300 * time.Millisecond}.Config(ctx, exe)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("got %v", err)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatal("a program that hangs was waited for")
	}
}

// The new program is checked with the flags the running service was started with, because they say where the
// configuration is: without them it would be checked against the settings of another folder.
func TestExecCheckerChecksWithTheFlagsOfTheRunningService(t *testing.T) {
	exe := helperProgram(t, "0.2.0")
	ctx := context.Background()
	flags := []string{"-config-dir", `D:\dash_go\config`, "-listen", ":8751"}
	t.Setenv(envHelperFlags, strings.Join(flags, " "))

	if err := (ExecChecker{Args: flags}).Config(ctx, exe); err != nil {
		t.Fatalf("Config with the flags: %v", err)
	}
	if err := (ExecChecker{}).Config(ctx, exe); err == nil || !strings.Contains(err.Error(), "the flags are") {
		t.Fatalf("Config without the flags: %v", err)
	}
	if got, err := (ExecChecker{Args: flags}).Version(ctx, exe); err != nil || got != "0.2.0" {
		t.Fatalf("the version is asked without flags: %q, %v", got, err)
	}

	// The slice of the caller is not changed.
	args := make([]string, 4, 10)
	copy(args, flags)
	if err := (ExecChecker{Args: args}).Config(ctx, exe); err != nil {
		t.Fatalf("Config: %v", err)
	}
	if full := args[:5]; full[4] != "" {
		t.Errorf("the flags of the caller were appended to: %q", full)
	}
}

func TestDecideTheRestart(t *testing.T) {
	for _, c := range []struct {
		mode       string
		supervised bool
		want       Restart
	}{
		{"supervise", false, RestartSupervise},
		{"exit", false, RestartExit},
		{"supervise", true, RestartExit}, // the supervisor waits for the exit code
		{"exit", true, RestartExit},
	} {
		if got := Decide(c.mode, c.supervised); got != c.want {
			t.Errorf("Decide(%q, %v) = %v, want %v", c.mode, c.supervised, got, c.want)
		}
	}
}

func TestSuperviseStartsAgainWhileTheProgramAsksForIt(t *testing.T) {
	old := restartPause
	restartPause = time.Millisecond
	t.Cleanup(func() { restartPause = old })

	var calls int
	var seenEnv []string
	run := func(exe string, args, env []string) (int, error) {
		calls++
		seenEnv = env
		if exe != "program" || len(args) != 1 || args[0] != "-listen=:1" {
			t.Errorf("run(%q, %q)", exe, args)
		}
		if calls < 3 {
			return ExitRestart, nil
		}
		return 7, nil
	}
	if code := Supervise("program", []string{"-listen=:1"}, run, slog.New(slog.DiscardHandler)); code != 7 || calls != 3 {
		t.Fatalf("code = %d after %d runs, want 7 after 3", code, calls)
	}
	want := EnvSupervised + "=" + strconv.Itoa(os.Getpid())
	found := false
	for _, kv := range seenEnv {
		found = found || kv == want
	}
	if !found {
		t.Errorf("the program was not told that it is supervised (%s)", want)
	}
}

func TestSuperviseReturnsTheCodeOfAProgramThatStops(t *testing.T) {
	calls := 0
	run := func(string, []string, []string) (int, error) { calls++; return 0, nil }
	if code := Supervise("program", nil, run, slog.New(slog.DiscardHandler)); code != 0 || calls != 1 {
		t.Fatalf("code = %d after %d runs", code, calls)
	}
}

func TestSuperviseGivesUpWhenTheProgramCannotBeStarted(t *testing.T) {
	run := func(string, []string, []string) (int, error) { return 0, os.ErrNotExist }
	if code := Supervise("program", nil, run, slog.New(slog.DiscardHandler)); code != 1 {
		t.Fatalf("code = %d", code)
	}
}

func TestExecRunReportsTheExitCode(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), envHelper+"=program", envHelperExit+"=0")
	if code, err := ExecRun(exe, []string{"exit"}, env); err != nil || code != 0 {
		t.Fatalf("ExecRun = %d, %v", code, err)
	}
	env = append(os.Environ(), envHelper+"=program", envHelperExit+"="+strconv.Itoa(ExitRestart))
	if code, err := ExecRun(exe, []string{"exit"}, env); err != nil || code != ExitRestart {
		t.Fatalf("ExecRun = %d, %v", code, err)
	}
	if _, err := ExecRun(filepath.Join(t.TempDir(), "nothing.exe"), nil, os.Environ()); err == nil {
		t.Fatal("a program that is not there was started")
	}
}

func TestStopWithLauncherStopsWhenTheInputEnds(t *testing.T) {
	r, w := io.Pipe()
	stopped := make(chan struct{})
	go StopWithLauncher(r, func() { close(stopped) }, slog.New(slog.DiscardHandler))
	select {
	case <-stopped:
		t.Fatal("stopped while the input was still open")
	case <-time.After(50 * time.Millisecond):
	}
	_ = w.Close()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop when the input ended")
	}
}

type brokenInput struct{}

func (brokenInput) Read([]byte) (int, error) { return 0, errors.New("the input is not there") }

func TestStopWithLauncherKeepsGoingWhenTheInputCannotBeRead(t *testing.T) {
	StopWithLauncher(brokenInput{}, func() { t.Error("stopped although nothing says the launcher is gone") }, slog.New(slog.DiscardHandler))
}

// A scheduled task that is ended kills the launcher at once. The program it started must not stay behind.
func TestTheProgramStopsWhenItsLauncherIsKilled(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	started, stopped := filepath.Join(dir, "started"), filepath.Join(dir, "stopped")

	launcher := exec.Command(exe)
	launcher.Env = append(os.Environ(), envHelper+"=launcher", envHelperDir+"="+dir)
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = launcher.Process.Kill()
		_ = launcher.Wait()
		if exists(stopped) {
			return
		}
		if b, err := os.ReadFile(started); err == nil {
			if pid, err := strconv.Atoi(string(b)); err == nil {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	})

	waitFor(t, "the program to start", func() bool { return exists(started) })
	time.Sleep(300 * time.Millisecond)
	if exists(stopped) {
		t.Fatal("the program stopped while its launcher was running")
	}
	if err := launcher.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the program to stop after its launcher was killed", func() bool { return exists(stopped) })
}
