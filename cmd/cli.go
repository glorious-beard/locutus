package cmd

import (
	"log/slog"
	"os"

	"github.com/alecthomas/kong"
)

// CLI is the root command struct for the locutus CLI.
type CLI struct {
	JSON    bool             `help:"Output results as JSON." short:"j"`
	Verbose bool             `help:"Enable verbose (debug) logging." short:"v"`
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
}

// AfterApply configures logging based on --verbose.
func (c *CLI) AfterApply() error {
	level := slog.LevelInfo
	if c.Verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	return nil
}
