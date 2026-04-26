package drivers

import (
	"context"
	"os/exec"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

func TestClaudeCodeBuildCommand(t *testing.T) {
	driver := ClaudeCodeDriver{}
	step := spec.PlanStep{
		ID:          "step-1",
		Description: "Implement the auth middleware",
	}
	workDir := "/tmp/project"

	cmd := driver.BuildCommand(context.Background(), step, workDir)

	assert.IsType(t, &exec.Cmd{}, cmd)
	assert.Equal(t, workDir, cmd.Dir)

	// Streaming-mode flags the supervisor's NDJSON parser depends on.
	assert.Equal(t, "claude", cmd.Args[0])
	assert.Contains(t, cmd.Args, "-p")
	assert.Contains(t, cmd.Args, "--output-format")
	assert.Contains(t, cmd.Args, "stream-json")
	assert.Contains(t, cmd.Args, "--verbose")
	assert.Contains(t, cmd.Args, "--include-partial-messages")
	assert.Contains(t, cmd.Args, "--permission-mode")
	assert.Contains(t, cmd.Args, "acceptEdits")

	// Step description must appear in the arguments.
	found := false
	for _, arg := range cmd.Args {
		if arg == step.Description {
			found = true
			break
		}
	}
	assert.True(t, found, "step description should appear in command args")
}

func TestClaudeCodeBuildRetryCommand(t *testing.T) {
	driver := ClaudeCodeDriver{}
	step := spec.PlanStep{
		ID:          "step-1",
		Description: "Implement the auth middleware",
	}
	workDir := "/tmp/project"
	sessionID := "sess-abc-123"
	feedback := "The tests are failing because the handler is not exported"

	cmd := driver.BuildRetryCommand(context.Background(), step, workDir, sessionID, feedback)

	assert.IsType(t, &exec.Cmd{}, cmd)
	assert.Equal(t, workDir, cmd.Dir)
	assert.Equal(t, "claude", cmd.Args[0])
	assert.Contains(t, cmd.Args, "--resume")
	assert.Contains(t, cmd.Args, sessionID)
	assert.Contains(t, cmd.Args, "-p")

	// Feedback should appear in the args.
	found := false
	for _, arg := range cmd.Args {
		if arg == feedback {
			found = true
			break
		}
	}
	assert.True(t, found, "feedback should appear in retry command args")
}

func TestClaudeCodeParseOutput(t *testing.T) {
	driver := ClaudeCodeDriver{}
	rawJSON := []byte(`{"result": "success", "files_modified": ["cmd/main.go", "internal/auth.go"], "session_id": "sess-123"}`)

	out, err := driver.ParseOutput(rawJSON)

	assert.NoError(t, err)
	assert.True(t, out.Success)
	assert.Equal(t, []string{"cmd/main.go", "internal/auth.go"}, out.Files)
	assert.Equal(t, "sess-123", out.SessionID)
	assert.NotEmpty(t, out.Output)
}

func TestClaudeCodeParseOutputFailure(t *testing.T) {
	driver := ClaudeCodeDriver{}
	rawJSON := []byte(`{"result": "error", "files_modified": [], "session_id": "sess-456"}`)

	out, err := driver.ParseOutput(rawJSON)

	assert.NoError(t, err)
	assert.False(t, out.Success)
	assert.Empty(t, out.Files)
	assert.Equal(t, "sess-456", out.SessionID)
}

func TestClaudeCodeParseOutputInvalidJSON(t *testing.T) {
	driver := ClaudeCodeDriver{}
	rawJSON := []byte(`not valid json`)

	_, err := driver.ParseOutput(rawJSON)
	assert.Error(t, err)
}

func TestCodexBuildCommand(t *testing.T) {
	driver := CodexDriver{}
	step := spec.PlanStep{
		ID:          "step-2",
		Description: "Add database migration for users table",
	}
	workDir := "/tmp/project"

	cmd := driver.BuildCommand(context.Background(), step, workDir)

	assert.IsType(t, &exec.Cmd{}, cmd)
	assert.Equal(t, workDir, cmd.Dir)
	assert.Equal(t, "codex", cmd.Args[0])
	assert.Contains(t, cmd.Args, "codex")
	assert.Contains(t, cmd.Args, "exec")

	// Step description must appear in the arguments.
	found := false
	for _, arg := range cmd.Args {
		if arg == step.Description {
			found = true
			break
		}
	}
	assert.True(t, found, "step description should appear in codex command args")
}

func TestCodexParseOutput(t *testing.T) {
	driver := CodexDriver{}
	rawJSON := []byte(`{"result": "success", "files_modified": ["db/migrate/001_users.sql"], "session_id": "codex-789"}`)

	out, err := driver.ParseOutput(rawJSON)

	assert.NoError(t, err)
	assert.True(t, out.Success)
	assert.Equal(t, []string{"db/migrate/001_users.sql"}, out.Files)
	assert.Equal(t, "codex-789", out.SessionID)
	assert.NotEmpty(t, out.Output)
}

func TestCodexParseOutputInvalidJSON(t *testing.T) {
	driver := CodexDriver{}
	rawJSON := []byte(`{broken`)

	_, err := driver.ParseOutput(rawJSON)
	assert.Error(t, err)
}

// TestDriversUseCommandContext is a structural regression guard: every
// driver's command-building method must use exec.CommandContext, not
// exec.Command. Without this, a SIGINT during dispatch leaves the
// spawned claude/codex process running until it exits on its own — the
// gap surfaced in the 2026-04-25 resilience audit. exec.CommandContext
// sets Cmd.Cancel; exec.Command leaves it nil, so the field is the
// cheapest reliable signal that ctx is wired through.
func TestDriversUseCommandContext(t *testing.T) {
	step := spec.PlanStep{ID: "step-1", Description: "do thing"}
	workDir := "/tmp/project"
	cases := []struct {
		name string
		got  *exec.Cmd
	}{
		{"claude.BuildCommand", ClaudeCodeDriver{}.BuildCommand(context.Background(), step, workDir)},
		{"claude.BuildRetryCommand", ClaudeCodeDriver{}.BuildRetryCommand(context.Background(), step, workDir, "sess-1", "fb")},
		{"codex.BuildCommand", CodexDriver{}.BuildCommand(context.Background(), step, workDir)},
		{"codex.BuildRetryCommand", CodexDriver{}.BuildRetryCommand(context.Background(), step, workDir, "sess-1", "fb")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.NotNil(t, c.got.Cancel,
				"%s must use exec.CommandContext so ctx cancel kills the subprocess", c.name)
		})
	}
}
