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

// rawOutput is the JSON shape returned by Claude Code and Codex.
type rawOutput struct {
	Result        string   `json:"result"`
	FilesModified []string `json:"files_modified"`
	SessionID     string   `json:"session_id"`
}

// ---------- ClaudeCodeDriver ----------

// ClaudeCodeDriver drives the Claude Code CLI.
type ClaudeCodeDriver struct{}

func (d ClaudeCodeDriver) BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd {
	cmd := exec.Command("claude", "-p", "--output-format", "json", step.Description)
	cmd.Path = "claude"
	cmd.Dir = workDir
	return cmd
}

func (d ClaudeCodeDriver) BuildRetryCommand(step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd {
	cmd := exec.Command("claude", "-p", "--output-format", "json", "--resume", sessionID, feedback)
	cmd.Path = "claude"
	cmd.Dir = workDir
	return cmd
}

func (d ClaudeCodeDriver) ParseOutput(output []byte) (DriverOutput, error) {
	var raw rawOutput
	if err := json.Unmarshal(output, &raw); err != nil {
		return DriverOutput{}, err
	}
	return DriverOutput{
		Success:   raw.Result == "success",
		Files:     raw.FilesModified,
		Output:    string(output),
		SessionID: raw.SessionID,
	}, nil
}

// ---------- CodexDriver ----------

// CodexDriver drives the OpenAI Codex CLI.
type CodexDriver struct{}

func (d CodexDriver) BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd {
	cmd := exec.Command("codex", "exec", step.Description)
	cmd.Path = "codex"
	cmd.Dir = workDir
	return cmd
}

func (d CodexDriver) BuildRetryCommand(step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd {
	cmd := exec.Command("codex", "exec", "--resume", sessionID, feedback)
	cmd.Path = "codex"
	cmd.Dir = workDir
	return cmd
}

func (d CodexDriver) ParseOutput(output []byte) (DriverOutput, error) {
	var raw rawOutput
	if err := json.Unmarshal(output, &raw); err != nil {
		return DriverOutput{}, err
	}
	return DriverOutput{
		Success:   raw.Result == "success",
		Files:     raw.FilesModified,
		Output:    string(output),
		SessionID: raw.SessionID,
	}, nil
}
