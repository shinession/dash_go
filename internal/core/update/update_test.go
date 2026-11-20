package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	envHelper        = "DASH_GO_UPDATE_HELPER"
	envHelperVersion = "DASH_GO_UPDATE_HELPER_VERSION"
	envHelperRaw     = "DASH_GO_UPDATE_HELPER_RAW"
	envHelperConfig  = "DASH_GO_UPDATE_HELPER_CONFIG"
	envHelperExit    = "DASH_GO_UPDATE_HELPER_EXIT"
	envHelperDir     = "DASH_GO_UPDATE_HELPER_DIR"
	envHelperFlags   = "DASH_GO_UPDATE_HELPER_FLAGS"
)

// TestMain lets the test program play the programs the updater runs: one that holds still until its input is closed,
// one that answers the flags of dash_go that the updater uses, and a supervisor with the program that stops when the
// supervisor is gone.
func TestMain(m *testing.M) {
	switch os.Getenv(envHelper) {
	case "":
		os.Exit(m.Run())
	case "hold":
		fmt.Println("running")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "launcher":
		exe, _ := os.Executable()
		_ = os.Setenv(envHelper, "watched")
		os.Exit(Supervise(exe, nil, nil, slog.New(slog.DiscardHandler)))
	case "watched":
		dir := os.Getenv(envHelperDir)
		_ = os.WriteFile(filepath.Join(dir, "started"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		StopWithLauncher(os.Stdin, func() {
			_ = os.WriteFile(filepath.Join(dir, "stopped"), nil, 0o600)
		}, slog.New(slog.DiscardHandler))
		os.Exit(0)
	default:
		os.Exit(fakeProgram(os.Args[1:]))
	}
}

func fakeProgram(args []string) int {
	if len(args) == 0 {
		return 2
	}
	// The flags of the running service come first; the command the updater runs is the last argument.
	command, flags := args[len(args)-1], strings.Join(args[:len(args)-1], " ")
	if command == "-check" {
		if want, ok := os.LookupEnv(envHelperFlags); ok && flags != want {
			fmt.Fprintf(os.Stderr, "configuration error: the flags are %q, want %q\n", flags, want)
			return 1
		}
	} else if flags != "" {
		return 2
	}
	switch command {
	case "-version":
		if raw := os.Getenv(envHelperRaw); raw != "" {
			fmt.Println(raw)
			return 0
		}
		fmt.Println("dash_go " + os.Getenv(envHelperVersion))
		fmt.Println("modules: hc_scan " + os.Getenv(envHelperVersion))
		return 0
	case "-check":
		// The real program logs to the same stream before it reports a problem.
		fmt.Fprintln(os.Stderr, `time=2026-01-02T03:04:05.000Z level=INFO msg="starting dash_go"`)
		switch os.Getenv(envHelperConfig) {
		case "bad":
			fmt.Fprintln(os.Stderr, `time=2026-01-02T03:04:05.000Z level=ERROR msg="configuration error" error="dash_api_url is not an address"`)
			return 1
		case "plain":
			fmt.Fprintln(os.Stderr, "configuration error: the login settings are wrong")
			return 1
		case "hang":
			time.Sleep(time.Minute)
		case "flood":
			line := strings.Repeat("x", 100)
			for i := 0; i < 1<<16; i++ {
				fmt.Fprintln(os.Stderr, line)
			}
			return 1
		}
		return 0
	case "exit":
		n, _ := strconv.Atoi(os.Getenv(envHelperExit))
		return n
	}
	return 2
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// waitFor waits until cond holds; the conditions it is used for are reached within milliseconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
