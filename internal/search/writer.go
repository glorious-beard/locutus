package search

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/blugelabs/bluge"
)

// writerRetrySchedule is the inter-attempt wait sequence used by
// OpenWriterWithRetry. Four attempts total, worst-case wait 1.75s.
// Picked to be short enough that interactive CLI verbs don't feel
// frozen, long enough to absorb the typical "another verb is mid-
// rebuild" contention window.
var writerRetrySchedule = []time.Duration{
	0,
	250 * time.Millisecond,
	500 * time.Millisecond,
	1000 * time.Millisecond,
}

// blugePIDFile names the lock-holder PID file Bluge writes alongside
// its segments. The literal lives in
// vendor/github.com/blugelabs/bluge/index/directory_fs.go as
// "bluge.pid"; mirrored here so the lock-busy diagnostic can read it
// without importing the index package's private internals.
const blugePIDFile = "bluge.pid"

// OpenWriterWithRetry opens a Bluge writer at the given index path,
// retrying on lock contention per writerRetrySchedule. On final
// failure the returned error names the PID of the lock holder when
// .locutus/spec_index/bluge.pid is readable — a paste-able diagnostic
// the operator can act on.
//
// Callers are responsible for closing the returned writer. The
// function is the single chokepoint every mutating verb uses (and
// MCP server's in-process mutators do the same); centralizing it
// here keeps the retry policy and the diagnostic uniform.
func OpenWriterWithRetry(indexPath string) (*bluge.Writer, error) {
	if indexPath == "" {
		return nil, fmt.Errorf("search: OpenWriterWithRetry requires a non-empty index path")
	}
	cfg := bluge.DefaultConfig(indexPath)

	var lastErr error
	for attempt, wait := range writerRetrySchedule {
		if wait > 0 {
			time.Sleep(wait)
		}
		w, err := bluge.OpenWriter(cfg)
		if err == nil {
			return w, nil
		}
		lastErr = err
		if !isLockBusy(err) {
			return nil, fmt.Errorf("search: open writer (attempt %d): %w", attempt+1, err)
		}
	}
	return nil, formatLockBusyError(indexPath, lastErr)
}

// isLockBusy reports whether err is the syscall-level "would block"
// error Bluge propagates when another process holds the exclusive
// flock. The standard fs and errors packages forward syscall errnos
// through Unwrap, so errors.Is on the EWOULDBLOCK / EAGAIN sentinels
// reliably matches both Bluge's wrapped form and the bare Flock
// return.
//
// EWOULDBLOCK and EAGAIN are the same numeric value on every Unix
// Locutus targets; we still test both so the intent reads cleanly to
// future maintainers.
func isLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

// formatLockBusyError shapes the final retry-exhausted error into a
// message the operator can act on: it names the PID of the holder
// (when readable) and suggests the two recovery paths — wait or kill.
//
// Falls back to a generic "lock held" message when the PID file is
// missing or unparseable; we never want to fabricate a PID, so a
// best-effort read with a clean fallback is the right shape.
func formatLockBusyError(indexPath string, lastErr error) error {
	pid := readHolderPID(indexPath)
	if pid > 0 {
		return fmt.Errorf("search: index lock held by another locutus process (PID %d). Wait for it to finish, or kill the holder if it's stuck. (underlying: %w)", pid, lastErr)
	}
	return fmt.Errorf("search: index lock held by another locutus process (PID unknown — %s missing or unreadable). Wait for it to finish, or remove %s if no locutus is running. (underlying: %w)",
		filepath.Join(indexPath, blugePIDFile), indexPath, lastErr)
}

// readHolderPID parses the integer PID stored in bluge.pid. Returns
// 0 on any error — the caller falls back to a generic message
// rather than guessing.
func readHolderPID(indexPath string) int {
	data, err := os.ReadFile(filepath.Join(indexPath, blugePIDFile))
	if err != nil {
		return 0
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(text, "%d", &pid); err != nil {
		return 0
	}
	return pid
}
