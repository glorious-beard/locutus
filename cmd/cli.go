package cmd

import (
	"log/slog"
	"os"
)

// CLI is the root command struct for the locutus CLI.
type CLI struct {
	JSON    bool `help:"Output results as JSON." short:"j"`
	Verbose bool `help:"Enable verbose (debug) logging." short:"v"`

	Version  VersionCmd  `cmd:"" help:"Print version information."`
	Init     InitCmd     `cmd:"" help:"Initialize a new spec-driven project."`
	Check    CheckCmd    `cmd:"" help:"Validate strategy prerequisites."`
	Status   StatusCmd   `cmd:"" help:"Show spec summary."`
	Update   UpdateCmd   `cmd:"" help:"Self-update to the latest release."`
	Diff     DiffCmd     `cmd:"" help:"Preview blast radius of a spec change."`
	Regen    RegenCmd    `cmd:"" help:"Regenerate stale modules."`
	Revisit  RevisitCmd  `cmd:"" help:"Update a decision or strategy."`
	Triage   TriageCmd   `cmd:"" help:"Evaluate an issue against GOALS.md."`
	Import   ImportCmd   `cmd:"" help:"Create a feature or bug from an issue."`
	Analyze  AnalyzeCmd  `cmd:"" help:"Analyze an existing codebase (assimilation)."`
	Mcp      McpCmd      `cmd:"" help:"Start the MCP server."`
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
