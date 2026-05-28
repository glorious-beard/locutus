package mcp

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBootstrap_DiscoversLiveDaemon(t *testing.T) {
	// Stand up a daemon at the canonical project location, then call
	// EnsureDaemon with the same project root. It should probe the
	// socket, find it responsive, and return without forking.
	projectRoot := shortTempSocketDir(t)
	// The daemon's listener path must equal SocketPath(projectRoot)
	// for EnsureDaemon to find it. Substitute the test-temp socket
	// into that location by stashing it directly.
	sockPath := SocketPath(projectRoot)

	// startSocketDaemon uses its own temp dir; we need it to bind
	// the project's expected path. Build the daemon machinery
	// inline here so we control the socket path.
	store := bootstrapTestStore(t)
	listener, err := ListenSocket(sockPath)
	assert.NoError(t, err)
	server := NewSpecServer(store, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ServeOnSocket(ctx, listener, server)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	got, err := EnsureDaemon(context.Background(), projectRoot, "/nonexistent/binary")
	assert.NoError(t, err, "EnsureDaemon should not fork (binary path is bogus) when daemon already responds")
	assert.Equal(t, sockPath, got)
}

func TestBootstrap_ForkFailsWithBogusBinary(t *testing.T) {
	// No daemon running, fork target is a path that doesn't exist —
	// EnsureDaemon should surface the fork error.
	projectRoot := shortTempSocketDir(t)
	// Ensure the canonical socket path is absent.
	_ = removeIfExists(filepath.Join(projectRoot, ".locutus", "mcp.sock"))

	_, err := EnsureDaemon(context.Background(), projectRoot, "/definitely/not/a/real/binary/path")
	assert.Error(t, err, "EnsureDaemon should fail when fork target doesn't exist")
}

func TestStopDaemon_NoPidFile_RemovesStaleSocket(t *testing.T) {
	// In-process listener, no PID file written. StopDaemon falls
	// through to removeStaleSocket so the next bridge attempt doesn't
	// dial a path that resolves but won't accept (the listener goroutine
	// stays alive in the test, but the contract is "socket file gone
	// after stop"). The PID-file-driven SIGTERM path is exercised by
	// TestStopDaemon_LivePid_SendsSigterm.
	projectRoot := shortTempSocketDir(t)
	sockPath := SocketPath(projectRoot)
	store := bootstrapTestStore(t)
	listener, err := ListenSocket(sockPath)
	assert.NoError(t, err)
	server := NewSpecServer(store, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ServeOnSocket(ctx, listener, server)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	assert.True(t, probeDaemon(sockPath), "socket should be probable before stop")
	err = StopDaemon(projectRoot)
	assert.NoError(t, err)
	assert.False(t, fileExists(sockPath), "socket file should be removed by StopDaemon")
}

func TestStopDaemon_NoDaemonIsNoOp(t *testing.T) {
	projectRoot := shortTempSocketDir(t)
	err := StopDaemon(projectRoot)
	assert.NoError(t, err, "StopDaemon on a non-running daemon should be a no-op")
}

func TestStopDaemon_StalePidPointingAtDeadProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The dead-process probe uses signal(0) on a recycled PID,
		// which is a Unix-specific liveness check. Windows
		// OpenProcess on an arbitrary integer returns "parameter is
		// incorrect" even for legitimately-dead PIDs, so the probe
		// doesn't discriminate dead-vs-invalid. Different mechanism
		// needed for a Windows-portable StopDaemon.
		t.Skip("StopDaemon's dead-process probe is Unix-specific (signal(0))")
	}
	// Boot a child process and wait for it to exit so its PID is
	// reaped. Then write that (now-dead) PID into the PID file and
	// call StopDaemon — it should detect the dead process via the
	// signal-0 probe, remove the PID file, and return nil.
	projectRoot := shortTempSocketDir(t)
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("'true' binary not available in PATH")
	}
	cmd := exec.Command(truePath)
	assert.NoError(t, cmd.Run(), "true should exit cleanly")
	deadPid := cmd.Process.Pid

	assert.NoError(t, WritePidFile(projectRoot, deadPid))
	assert.NoError(t, StopDaemon(projectRoot))
	assert.False(t, fileExists(PidPath(projectRoot)), "PID file should be cleaned up when its process is already dead")
}

func TestStopDaemon_LivePid_SendsSigterm(t *testing.T) {
	if runtime.GOOS == "windows" {
		// SIGTERM does not exist on Windows. The Locutus daemon's
		// graceful-stop path is Unix-specific; making StopDaemon
		// Windows-portable means switching to TerminateProcess or
		// the named-event signaling pattern, which is real
		// engineering scope beyond keeping CI green.
		t.Skip("StopDaemon's graceful-stop path is SIGTERM-based (Unix-only)")
	}
	// Spawn a real child process (sleep 30) and record its PID. Then
	// call StopDaemon and verify the child receives SIGTERM and exits
	// within the wait window. sleep handles SIGTERM by exiting
	// immediately, which is exactly the daemon's signal.NotifyContext
	// behavior — close enough that this test exercises the same
	// kernel mechanism end-to-end without spinning up a full daemon.
	//
	// Note on zombie reaping: because the test process forks sleep,
	// SIGTERM + exit leaves a zombie entry until Wait() runs. We
	// reap concurrently in a goroutine so StopDaemon's signal-0
	// liveness probe doesn't lie. In production this isn't an
	// issue — the daemon is detached from its bootstrapping CLI
	// (Process.Release in forkDaemon), so no zombie can form from
	// the stopping process's perspective.
	projectRoot := shortTempSocketDir(t)

	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("'sleep' binary not available in PATH")
	}
	cmd := exec.Command(sleepPath, "30")
	assert.NoError(t, cmd.Start())
	pid := cmd.Process.Pid

	reaped := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(reaped)
	}()
	t.Cleanup(func() {
		// Belt-and-suspenders: if StopDaemon didn't kill it, kill it
		// here so the test process doesn't leak a 30-second sleep.
		_ = cmd.Process.Signal(syscall.SIGKILL)
		select {
		case <-reaped:
		case <-time.After(2 * time.Second):
		}
	})

	assert.NoError(t, WritePidFile(projectRoot, pid))
	start := time.Now()
	assert.NoError(t, StopDaemon(projectRoot))
	elapsed := time.Since(start)

	// Wait for the reaper goroutine to remove the zombie so signal(0)
	// reports the true kernel state.
	select {
	case <-reaped:
	case <-time.After(1 * time.Second):
		t.Fatal("child process should have been reaped by background goroutine")
	}

	// Process should be gone now.
	assert.Error(t, cmd.Process.Signal(syscall.Signal(0)), "child should be exited after StopDaemon")
	assert.Less(t, elapsed, stopWaitTimeout, "StopDaemon should return well before the kill-escalation deadline for a SIGTERM-responsive child")
	assert.False(t, fileExists(PidPath(projectRoot)), "PID file should be cleaned up")
}

func TestStopDaemon_CorruptPidFileIsError(t *testing.T) {
	// A malformed PID file is operator-visible: don't silently swallow.
	projectRoot := shortTempSocketDir(t)
	pidPath := PidPath(projectRoot)
	assert.NoError(t, writeFileMkdir(pidPath, []byte("not-a-number\n")))
	err := StopDaemon(projectRoot)
	assert.Error(t, err, "corrupt PID file should surface a parse error")
}
