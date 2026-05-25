package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
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

// StopDaemon connects to a running daemon's socket and signals it to
// shut down. The signaling mechanism is "close the socket file and
// SIGTERM the listening process," which is more reliable than
// trying to invent an MCP-level shutdown verb. The daemon's accept
// loop closes when the socket file vanishes or when SIGTERM cancels
// its context.
//
// Returns nil if the daemon was already gone (the socket path doesn't
// exist or doesn't accept connections). Returns an error only if the
// signaling itself failed.
//
// Currently this is a placeholder implementation: actual signal
// dispatch requires knowing the daemon's PID, which we haven't
// captured anywhere yet. Phase 1 ships this as "remove the socket
// file; the daemon's accept loop will error and exit on next
// connection." A future revision should write a PID file alongside
// the socket and signal it properly.
func StopDaemon(projectRoot string) error {
	sockPath := SocketPath(projectRoot)
	if !probeDaemon(sockPath) {
		return nil
	}
	if err := os.Remove(sockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mcp: remove socket %s: %w", sockPath, err)
	}
	return nil
}
