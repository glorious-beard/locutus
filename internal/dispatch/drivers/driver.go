package drivers

import (
	"encoding/json"
	"os/exec"

	"github.com/chetan/locutus/internal/spec"
)

// DriverOutput is the normalized result returned by any agent driver.
type DriverOutput struct {
	Success   bool     `json:"success"`
	Files     []string `json:"files"`
	Output    string   `json:"output"`
	SessionID string   `json:"session_id"`
}

// AgentDriver is the interface every coding-agent driver must implement.
type AgentDriver interface {
	BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd
	BuildRetryCommand(step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd
	ParseOutput(output []byte) (DriverOutput, error)
}

// rawOutput captures the fields we expect from agent CLI JSON output.
// This is a superset — fields may be absent depending on the agent.
// Success is determined by the absence of an "error" field rather than
// a specific "result" value, since different agents use different schemas.
type rawOutput struct {
	Result        string   `json:"result"`
	Error         string   `json:"error"`
	FilesModified []string `json:"files_modified"`
	SessionID     string   `json:"session_id"`
}

// ---------- ClaudeCodeDriver ----------

// ClaudeCodeDriver drives the Claude Code CLI via `claude -p --output-format json`.
// The actual JSON schema from Claude Code may vary; ParseOutput is lenient
// and treats any parseable, non-error response as success. Modified files
// should be discovered via `git diff --name-only` in the worktree rather
// than relying solely on the parsed output.
type ClaudeCodeDriver struct{}

func (d ClaudeCodeDriver) BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd {
	// stream-json + --verbose + --include-partial-messages produce the
	// NDJSON event stream our parser consumes (see
	// internal/dispatch/drivers/claude_stream.go). --permission-mode
	// acceptEdits is the safe default for -p mode (no permission prompt
	// possible; acceptEdits allows file edit/write tools but not shell
	// or network). --no-session-persistence keeps one-shot supervised
	// runs out of the user's session store.
	cmd := exec.Command(
		"claude",
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", "acceptEdits",
		"--no-session-persistence",
		step.Description,
	)
	cmd.Dir = workDir
	return cmd
}

func (d ClaudeCodeDriver) BuildRetryCommand(step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd {
	cmd := exec.Command(
		"claude",
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", "acceptEdits",
		"--resume", sessionID,
		feedback,
	)
	cmd.Dir = workDir
	return cmd
}

func (d ClaudeCodeDriver) ParseOutput(output []byte) (DriverOutput, error) {
	var raw rawOutput
	if err := json.Unmarshal(output, &raw); err != nil {
		return DriverOutput{}, err
	}
	// Success if no error field and result is not explicitly "error".
	success := raw.Error == "" && raw.Result != "error"
	return DriverOutput{
		Success:   success,
		Files:     raw.FilesModified,
		Output:    string(output),
		SessionID: raw.SessionID,
	}, nil
}

// ---------- CodexDriver ----------

// CodexDriver drives the OpenAI Codex CLI via `codex exec`.
type CodexDriver struct{}

func (d CodexDriver) BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd {
	cmd := exec.Command("codex", "exec", step.Description)

	cmd.Dir = workDir
	return cmd
}

func (d CodexDriver) BuildRetryCommand(step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd {
	cmd := exec.Command("codex", "exec", "--resume", sessionID, feedback)

	cmd.Dir = workDir
	return cmd
}

func (d CodexDriver) ParseOutput(output []byte) (DriverOutput, error) {
	var raw rawOutput
	if err := json.Unmarshal(output, &raw); err != nil {
		return DriverOutput{}, err
	}
	success := raw.Error == "" && raw.Result != "error"
	return DriverOutput{
		Success:   success,
		Files:     raw.FilesModified,
		Output:    string(output),
		SessionID: raw.SessionID,
	}, nil
}
