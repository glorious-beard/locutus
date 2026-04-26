package cmd

import (
	"errors"
	"strconv"
)

// ExitCode is an error returned by command handlers to signal that the CLI
// should terminate with a specific numeric status. main.go translates it via
// os.Exit after Kong's lifecycle returns; the indirection preserves Kong's
// AfterRun hooks and any caller defers, and lets shared RunX entry points
// (used by both the CLI and the MCP handler) report outcome through the
// standard error channel rather than a process termination the MCP server
// could not observe.
type ExitCode int

func (e ExitCode) Error() string {
	return "exit " + strconv.Itoa(int(e))
}

// IsExitCode reports whether err is (or wraps) an ExitCode and returns the
// numeric code. Returns (0, false) for nil and unrelated errors.
func IsExitCode(err error) (int, bool) {
	var ec ExitCode
	if errors.As(err, &ec) {
		return int(ec), true
	}
	return 0, false
}
