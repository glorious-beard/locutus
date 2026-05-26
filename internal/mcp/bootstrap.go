package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// DaemonStartTimeout bounds how long EnsureDaemon waits for a forked
// `locutus mcp-daemon` process to start listening before giving up.
// Picked at 5s: enough for cold-start (Go binary load, SpecStore
// open, listener bind) on a loaded laptop; short enough that a
// genuinely broken daemon doesn't hang the calling CLI for long.
const DaemonStartTimeout = 5 * time.Second

// daemonPollInterval is the gap between socket-probe attempts during
// EnsureDaemon's wait loop.
const daemonPollInterval = 50 * time.Millisecond

// EnsureDaemon returns the socket path for the project's MCP daemon,
// starting one if none is responsive at the expected location.
//
// Discovery:
//  1. Compute the expected socket path (.locutus/mcp.sock under
//     projectRoot).
//  2. Probe the path with a short-timeout net.Dial. If it succeeds,
//     a live daemon is already there; return its path.
//  3. Otherwise, fork `<binary> mcp-daemon --project <root>` and
//     poll the path until either it becomes responsive or
//     DaemonStartTimeout elapses.
//
// binaryPath is the executable to fork — pass os.Executable() in
// production; tests pass their own test-binary path.
//
// Forked daemons are NOT tracked or reaped by EnsureDaemon. The
// daemon's lifecycle is "runs until explicitly stopped or until the
// OS reaps it" (Q2=A in the test-design checkpoint: no idle
// shutdown). A separate `locutus mcp stop` subcommand handles
// explicit teardown.
func EnsureDaemon(ctx context.Context, projectRoot, binaryPath string) (string, error) {
	sockPath := SocketPath(projectRoot)

	if probeDaemon(sockPath) {
		return sockPath, nil
	}

	if err := forkDaemon(binaryPath, projectRoot); err != nil {
		return "", fmt.Errorf("mcp: fork daemon: %w", err)
	}

	deadline := time.Now().Add(DaemonStartTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if probeDaemon(sockPath) {
			return sockPath, nil
		}
		time.Sleep(daemonPollInterval)
	}
	return "", fmt.Errorf("mcp: daemon did not start listening on %s within %s", sockPath, DaemonStartTimeout)
}

// probeDaemon returns true if a connection to sockPath succeeds within
// a short timeout. Returns false on any error (path missing, dial
// refused, timeout). Doesn't speak MCP — the SDK handshake happens at
// session.Connect time after the bridge dials.
func probeDaemon(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// forkDaemon spawns the daemon subprocess and detaches from it. The
// daemon writes to its own stderr (visible if the operator runs the
// CLI with `2>&1 | tee` or equivalent); stdin/stdout are connected
// to /dev/null so the daemon doesn't compete with the bridge for
// terminal IO.
//
// We use exec.Command, not exec.CommandContext: the daemon's
// lifetime is decoupled from the bootstrapping process's context.
// Cancelling ctx must not kill the daemon (other clients may be
// connected). Operator-driven teardown is `locutus mcp stop` or
// SIGTERM.
func forkDaemon(binaryPath, projectRoot string) error {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()

	cmd := exec.Command(binaryPath, "mcp-daemon", "--project", projectRoot)
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	// Stderr stays attached to a file under .locutus/ so operators
	// have a place to read panics / connect errors. If we can't open
	// it, fall back to discarding rather than failing the fork.
	logPath := SocketPath(projectRoot) + ".log"
	if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		cmd.Stderr = logFile
		// Don't close logFile here — the child holds an inherited fd.
		// Closing it in the parent is fine because exec.Command's
		// startup duplicated the fd into the child's file table; we
		// only need to keep the parent's reference alive until Start
		// returns. After Start, the parent's fd is unused and the
		// child owns its copy.
		defer logFile.Close()
	} else {
		cmd.Stderr = devNull
	}

	// Detach: don't propagate signals from parent to child. Without
	// this, a SIGINT to the bootstrapping CLI would also kill the
	// daemon — leaving subsequent CLI invocations to re-fork it.
	cmd.SysProcAttr = detachSysProcAttr()

	if err := cmd.Start(); err != nil {
		return err
	}
	// Release the process handle so we don't accumulate zombies or
	// hold parent-child semantics across the daemon's lifetime.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

// stopWaitTimeout bounds how long StopDaemon waits for the daemon
// process to exit after SIGTERM before escalating to SIGKILL.
// Picked at 2s: comfortably longer than the accept-loop teardown
// path (close listener, close in-flight conns, RemoveAll cleanup)
// observed locally; short enough that an unresponsive daemon doesn't
// stall `update --reset` for the user.
const stopWaitTimeout = 2 * time.Second

// stopPollInterval is the gap between liveness checks during the
// SIGTERM wait loop.
const stopPollInterval = 25 * time.Millisecond

// StopDaemon stops the per-project daemon by reading the PID file
// written at boot, sending SIGTERM to that process, and waiting for
// it to exit. Falls back to SIGKILL after [stopWaitTimeout] if the
// process is still alive — a hung daemon is worse for the operator
// than a forcibly-killed one (the next CLI invocation re-forks a
// fresh daemon either way).
//
// Cleanup order:
//  1. SIGTERM the PID. Daemon's signal-aware ctx cancels, accept
//     loop closes the listener and in-flight conns, the deferred
//     RemovePidFile / socket close run.
//  2. Poll until the process is gone (waitForExit). Done.
//  3. If timeout elapses, SIGKILL and remove the PID + socket files
//     directly — the daemon's deferred cleanup didn't run.
//
// Returns nil for the no-op cases: no PID file, PID file points at
// a dead process, or the daemon has already exited cleanly. An
// invalid PID file is surfaced as an error so the operator can
// inspect (something is rewriting .locutus/mcp.pid).
func StopDaemon(projectRoot string) error {
	pid, err := ReadPidFile(projectRoot)
	if err != nil {
		return fmt.Errorf("mcp: stop: %w", err)
	}
	if pid <= 0 {
		// No PID file means no daemon we know how to address. If a
		// stale socket exists from an older daemon that exited
		// without cleanup, remove it so the next bridge attempt
		// doesn't dial a dead listener.
		return removeStaleSocket(projectRoot)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("mcp: find pid %d: %w", pid, err)
	}

	// Signal 0 probes whether the process exists without delivering
	// a signal. ESRCH means it's already gone — a stale PID file
	// from a daemon that crashed; clean up and return success.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		_ = RemovePidFile(projectRoot)
		return removeStaleSocket(projectRoot)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// SIGTERM failed for a reason other than "process gone."
		// Surface it so the operator can investigate; don't silently
		// fall back to socket removal.
		return fmt.Errorf("mcp: sigterm pid %d: %w", pid, err)
	}

	if waitForExit(proc, stopWaitTimeout) {
		// Daemon's deferred cleanup ran: PID file and socket are
		// already gone (or close to it). Belt-and-suspenders: clean
		// up any residue so the next CLI sees a fresh slate.
		_ = RemovePidFile(projectRoot)
		_ = removeStaleSocket(projectRoot)
		return nil
	}

	// Hung daemon. Escalate.
	if err := proc.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("mcp: sigkill pid %d: %w", pid, err)
	}
	// Best-effort wait so a subsequent fork doesn't race the kernel
	// finishing the kill. We don't surface a hung SIGKILL as an error
	// — at that point only the kernel can help us, and reporting
	// success-after-cleanup is friendlier than wedging the operator.
	_ = waitForExit(proc, stopWaitTimeout)
	_ = RemovePidFile(projectRoot)
	_ = removeStaleSocket(projectRoot)
	return nil
}

// waitForExit polls until proc no longer responds to signal 0 (the
// process-exists probe) or timeout elapses. Returns true if the
// process exited within the timeout.
//
// Signal 0 is the standard POSIX idiom for "is this PID alive?": it
// runs the kernel's permission + existence checks but doesn't queue
// a signal for delivery. ESRCH means the process is gone; nil means
// it's still alive (regardless of whether it's running, sleeping,
// or stopped).
func waitForExit(proc *os.Process, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return true
		}
		time.Sleep(stopPollInterval)
	}
	return proc.Signal(syscall.Signal(0)) != nil
}

// removeStaleSocket deletes the project's socket file when no daemon
// is bound to it. Used by [StopDaemon] in the "no PID file" /
// "PID-file process already dead" paths so the next bridge attempt
// doesn't dial a path that resolves but won't accept.
func removeStaleSocket(projectRoot string) error {
	sockPath := SocketPath(projectRoot)
	if err := os.Remove(sockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mcp: remove socket %s: %w", sockPath, err)
	}
	return nil
}
