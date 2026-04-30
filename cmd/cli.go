package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/alecthomas/kong"
)

// CLI is the root command struct for the locutus CLI.
type CLI struct {
	JSON    bool             `help:"Output results as JSON (silences progress UI)." short:"j"`
	Plain   bool             `help:"Disable rich console output (no spinners or colors)."`
	Verbose int              `help:"Increase log verbosity. -v=info, -vv=debug." short:"v" type:"counter"`
	Debug   bool             `help:"Set log level to debug (alias for -vv)."`
	Version kong.VersionFlag `help:"Print version and exit." short:"V"`

	// Canonical 8-verb set.
	Init       InitCmd       `cmd:"" help:"Initialize a new spec-driven project."`
	Update     UpdateCmd     `cmd:"" help:"Self-update to the latest release."`
	Import     ImportCmd     `cmd:"" help:"Admit a new feature or bug from an issue."`
	Refine     RefineCmd     `cmd:"" help:"Council-driven deliberation on any spec node."`
	Assimilate AssimilateCmd `cmd:"" help:"Infer or update spec from an existing codebase."`
	Adopt      AdoptCmd      `cmd:"" help:"Bring code into alignment with spec (the reconcile loop)."`
	Status     StatusCmd     `cmd:"" help:"Show spec summary: state, drift, validation errors."`
	History    HistoryCmd    `cmd:"" help:"Query the past-tense record of spec changes."`

	Mcp McpCmd `cmd:"" help:"Start the MCP server."`

	// Invoked by Claude Code as an MCP subprocess; end users don't run this
	// directly. Hidden to keep it out of --help.
	McpPermBridge McpPermBridgeCmd `cmd:"mcp-perm-bridge" hidden:"" help:"Internal: permission-prompt-tool bridge for streaming supervision."`

	// mcpMode is set programmatically by McpCmd.Run before any work
	// starts so RenderMode reflects "this process is serving MCP."
	// Not a CLI flag — invocation is the signal.
	mcpMode bool
}

// EnvKeyLogLevel is the universal log-level override read by both CLI
// and MCP modes. Flags win; env var only consulted when no verbosity
// flag is set. Accepts case-insensitive debug|info|warn|warning|error.
const EnvKeyLogLevel = "LOCUTUS_LOG_LEVEL"

// AfterApply configures logging based on flags and LOCUTUS_LOG_LEVEL,
// and stashes the parsed CLI struct globally so package helpers can
// query the render mode without taking it on every signature.
//
// Default level is WARN — INFO and DEBUG are opt-in via -v / -vv /
// --debug. The env var is honored only when no flag is set; flag wins
// is the conventional behavior and matches user expectation when they
// type a flag explicitly to override an environment default.
func (c *CLI) AfterApply() error {
	level := resolveLogLevel(c.Verbose, c.Debug, os.Getenv(EnvKeyLogLevel))
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	globalCLI = c
	return nil
}

// resolveLogLevel implements the precedence: --debug or -vv → DEBUG;
// -v → INFO; otherwise env var if set; otherwise WARN. Garbage env
// values fall back to WARN with a stderr warning so a typo in
// LOCUTUS_LOG_LEVEL doesn't silently produce default behavior.
func resolveLogLevel(verbose int, debug bool, envValue string) slog.Level {
	if debug || verbose >= 2 {
		return slog.LevelDebug
	}
	if verbose == 1 {
		return slog.LevelInfo
	}
	if envValue != "" {
		if lvl, ok := parseLogLevel(envValue); ok {
			return lvl
		}
		fmt.Fprintf(os.Stderr,
			"locutus: invalid %s=%q (expected debug|info|warn|error); using default warn\n",
			EnvKeyLogLevel, envValue)
	}
	return slog.LevelWarn
}

func parseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return 0, false
}

// RenderMode reports the output mode for the current invocation. Used
// by callers to pick a sink for council progress events.
type RenderMode int

const (
	// RenderModeRich means an interactive CLI run — spinners on stderr.
	RenderModeRich RenderMode = iota
	// RenderModePlain means CLI mode with --plain set — structured
	// log lines on stderr instead of pterm UI.
	RenderModePlain
	// RenderModeMCP means we're serving MCP — progress notifications
	// over the protocol; nothing on stdout/stderr.
	RenderModeMCP
	// RenderModeSilent means --json was passed — only the JSON result
	// goes on stdout; progress is suppressed entirely.
	RenderModeSilent
)

// RenderMode returns the active output mode.
func (c *CLI) RenderMode() RenderMode {
	switch {
	case c.JSON:
		return RenderModeSilent
	case c.mcpMode:
		return RenderModeMCP
	case c.Plain:
		return RenderModePlain
	default:
		return RenderModeRich
	}
}
