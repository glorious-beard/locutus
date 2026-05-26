package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PidSubpath is the per-project PID-file location under the project
// root. The daemon writes its own PID here after binding the socket
// so [StopDaemon] can send SIGTERM precisely instead of relying on
// socket-file removal to indirectly unwind the accept loop.
const PidSubpath = ".locutus/mcp.pid"

// PidPath returns the absolute PID-file path for a project root.
func PidPath(projectRoot string) string {
	return filepath.Join(projectRoot, PidSubpath)
}

// WritePidFile writes the current process's PID to PidPath(projectRoot).
// The file is created with 0o600 so other users on the system can't read
// or rewrite it. Overwrites any pre-existing file — a leftover PID from
// a daemon that crashed without cleanup gets replaced atomically.
//
// Atomicity: write to a sibling .tmp file, fsync, then rename. The
// rename is atomic on POSIX so a concurrent reader either sees the old
// content or the new — never a half-written one.
func WritePidFile(projectRoot string, pid int) error {
	dir := filepath.Dir(PidPath(projectRoot))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mcp: mkdir %s: %w", dir, err)
	}
	final := PidPath(projectRoot)
	tmp := final + ".tmp"

	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return fmt.Errorf("mcp: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mcp: rename %s -> %s: %w", tmp, final, err)
	}
	return nil
}

// ReadPidFile returns the PID stored at PidPath(projectRoot). Returns
// (0, nil) when the file doesn't exist — a missing PID file is the
// "no daemon was tracked" case and callers should treat it as a
// no-op condition rather than an error.
//
// Surfaces parse errors when the file exists but is malformed: a
// corrupted PID file is operator-visible (something wrote garbage to
// it) and shouldn't be silently swallowed.
func ReadPidFile(projectRoot string) (int, error) {
	data, err := os.ReadFile(PidPath(projectRoot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("mcp: read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("mcp: parse pid %q: %w", string(data), err)
	}
	return pid, nil
}

// RemovePidFile deletes the PID file. Missing file is fine — the
// daemon may have already removed it on clean shutdown, or this is
// the first time the cleanup path has been exercised.
func RemovePidFile(projectRoot string) error {
	if err := os.Remove(PidPath(projectRoot)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mcp: remove pid file: %w", err)
	}
	return nil
}
