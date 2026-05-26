package cmd

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveLogLevel(t *testing.T) {
	cases := []struct {
		name    string
		verbose int
		debug   bool
		env     string
		want    slog.Level
	}{
		{"default is warn", 0, false, "", slog.LevelWarn},
		{"-v lifts to info", 1, false, "", slog.LevelInfo},
		{"-vv lifts to debug", 2, false, "", slog.LevelDebug},
		{"--debug lifts to debug", 0, true, "", slog.LevelDebug},
		{"env debug honored when no flag", 0, false, "debug", slog.LevelDebug},
		{"env info honored when no flag", 0, false, "INFO", slog.LevelInfo},
		{"env warning honored", 0, false, "warning", slog.LevelWarn},
		{"flag overrides env (verbose=1 over env=warn)", 1, false, "warn", slog.LevelInfo},
		{"flag overrides env (debug over env=info)", 0, true, "info", slog.LevelDebug},
		{"garbage env falls back to warn", 0, false, "garbage", slog.LevelWarn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveLogLevel(c.verbose, c.debug, c.env)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestRenderModePrecedence(t *testing.T) {
	// JSON dominates everything else.
	cli := &CLI{JSON: true, Plain: true, mcpMode: true}
	assert.Equal(t, RenderModeSilent, cli.RenderMode(),
		"--json should win over plain and mcp")

	// MCP wins over plain when JSON is off.
	cli = &CLI{JSON: false, Plain: true, mcpMode: true}
	assert.Equal(t, RenderModeMCP, cli.RenderMode(),
		"mcp mode should win over --plain when no --json")

	// Plain wins when MCP and JSON are both off.
	cli = &CLI{Plain: true}
	assert.Equal(t, RenderModePlain, cli.RenderMode())

	// Default is rich.
	cli = &CLI{}
	assert.Equal(t, RenderModeRich, cli.RenderMode())
}
