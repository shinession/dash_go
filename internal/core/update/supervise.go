package update

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const (
	// ExitRestart is the exit code of a service that has put a new program in place and wants to be started again,
	// by the service manager (update_restart "exit") or by Supervise.
	ExitRestart = 75
	// EnvSupervised is set by Supervise in the environment of the program it starts; its value is the process id of
	// the supervisor. The standard input of such a program is a pipe that only the supervisor holds (see
	// StopWithLauncher).
	EnvSupervised = "DASH_GO_SUPERVISED_BY"
)

// Supervised reports whether this process was started by Supervise.
func Supervised() bool { return os.Getenv(EnvSupervised) != "" }

// Restart is what the process does once the service has stopped and a new program is in place.
type Restart int

const (
	// RestartExit exits with ExitRestart: a service manager (or the supervisor) starts the program again.
	RestartExit Restart = iota
	// RestartSupervise keeps this process alive as a small launcher that starts the new program and waits for it.
	RestartSupervise
)

// Decide chooses what to do after an update. mode is the setting update_restart: "exit" or "supervise". A program
// that was started by Supervise always exits, because the supervisor is waiting for that exit code.
func Decide(mode string, supervised bool) Restart {
	if supervised || mode == "exit" {
		return RestartExit
	}
	return RestartSupervise
}

// RunFunc starts the program once, waits for it and says how it ended.
type RunFunc func(exe string, args, env []string) (exitCode int, err error)

// restartPause is the time between the end of a program that asks to be started again and its start.
var restartPause = 500 * time.Millisecond

// Supervise runs the program at exe with args for as long as it exits with ExitRestart, and returns the exit code of
// the run that ended otherwise. It is what keeps the service alive without a service manager: the process that was
// started by hand, or by a scheduled task, stays and starts the new program, so a console window or a task keeps
// seeing one running program, Ctrl+C still stops everything, and the new program is not cut off when a task that
// started the old one is over. The program that is started runs the same way as before, with the same environment,
// folder and console.
func Supervise(exe string, args []string, run RunFunc, log *slog.Logger) int {
	if run == nil {
		run = ExecRun
	}
	// Ctrl+C reaches every process of the console. The program that is started stops itself, and then this process
	// ends with its exit code; this one must not die first.
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer func() {
		signal.Stop(sig)
		close(sig)
	}()
	go func() {
		for range sig {
		}
	}()
	env := append(os.Environ(), EnvSupervised+"="+strconv.Itoa(os.Getpid()))
	for {
		code, err := run(exe, args, env)
		if err != nil {
			log.Error("the new program could not be started", "error", err)
			return 1
		}
		if code != ExitRestart {
			return code
		}
		log.Info("the program asks to be started again")
		time.Sleep(restartPause)
	}
}

// StopWithLauncher calls stop when the supervisor of this program is gone. ExecRun gives the program a standard input
// that only the supervisor holds and never writes to, so reading it ends when the supervisor ends, however that
// happens: a task that the scheduler ends is killed at once, with no chance to tell this program. Without it the
// program would run on with nobody to start the next one, and keep the port that the next start of the service needs.
// It returns when the input has ended (stop is called then) or cannot be read (it is not).
func StopWithLauncher(in io.Reader, stop func(), log *slog.Logger) {
	if _, err := io.Copy(io.Discard, in); err != nil {
		log.Warn("the launcher that started this program cannot be watched, so this program does not stop when the launcher ends", "error", err)
		return
	}
	log.Warn("the launcher that started this program is gone, so this program stops")
	stop()
}

// ExecRun is the RunFunc that starts the program as a child process with the console of this one, and with the pipe
// that StopWithLauncher reads as its standard input.
func ExecRun(exe string, args, env []string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = env
	lifeline, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	defer func() { _ = lifeline.Close() }()
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	}
	return 0, err
}
