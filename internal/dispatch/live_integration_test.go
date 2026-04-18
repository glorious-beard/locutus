package dispatch_test

// Opt-in end-to-end smoke test for the batch supervisor against a real
// Claude Code subprocess. Gated behind LOCUTUS_INTEGRATION_TEST=1 so it
// never runs during `go test ./...`.
//
// This validates the existing non-streaming pipeline:
//   Dispatcher.Dispatch → CreateWorktree → ClaudeCodeDriver.BuildCommand
//   → ProductionRunner → batch Supervise → driver.ParseOutput
//   → worktree.Commit → worktree.MergeToFeatureBranch
//
// Parts 1–6 (streaming supervision) are NOT exercised here — runAttempt
// has no CLI entry point yet. This test validates that the layer beneath
// our new streaming work actually works end-to-end before we stack more
// on top.
//
// Prereqs:
//   - `claude` in PATH (tested against 2.1.x)
//   - Authenticated (OAuth via Claude Max subscription)

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/dispatch/drivers"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/require"
)

// driverAdapter wraps drivers.ClaudeCodeDriver to bridge the duplicate
// DriverOutput types in the dispatch vs drivers packages. This adapter
// exists only in the integration test; consolidating those types is a
// separate refactor tracked for later cleanup.
type driverAdapter struct{ drivers.ClaudeCodeDriver }

func (d driverAdapter) ParseOutput(out []byte) (dispatch.DriverOutput, error) {
	o, err := d.ClaudeCodeDriver.ParseOutput(out)
	if err != nil {
		return dispatch.DriverOutput{}, err
	}
	return dispatch.DriverOutput{
		Success:   o.Success,
		Files:     o.Files,
		SessionID: o.SessionID,
		Output:    o.Output,
	}, nil
}

func TestClaudeCodeLive(t *testing.T) {
	if os.Getenv("LOCUTUS_INTEGRATION_TEST") != "1" {
		t.Skip("set LOCUTUS_INTEGRATION_TEST=1 to enable; this test invokes the real claude CLI")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatalf("claude CLI not found in PATH: %v", err)
	}

	repoDir := setupLiveRepo(t)
	plan := &spec.MasterPlan{
		ID: "live-test-plan",
		Workstreams: []spec.Workstream{{
			ID:      "hello",
			AgentID: "claude-code",
			Steps: []spec.PlanStep{{
				ID:            "step-1",
				Order:         1,
				StrategyID:    "direct",
				Description:   "Create a file called hello.txt in the current working directory. Its contents must be exactly the word 'pong' followed by a single newline, and nothing else. Do not create any other files. Do not modify any existing files.",
				ExpectedFiles: []string{"hello.txt"},
			}},
		}},
	}

	// Validator always passes — the goal of this test is pipeline plumbing,
	// not validation correctness. The file-presence assertion below is the
	// real acceptance gate.
	validator := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: "PASS"},
	})

	d := &dispatch.Dispatcher{
		LLM:               validator,
		Drivers:           map[string]dispatch.AgentDriver{"claude-code": driverAdapter{}},
		Runner:            dispatch.ProductionRunner,
		MaxRetriesPerStep: 1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	t.Logf("dispatching plan %q against %s", plan.ID, repoDir)
	results, err := d.Dispatch(ctx, plan, repoDir)
	require.NoError(t, err, "Dispatch returned error")
	require.Len(t, results, 1)

	r := results[0]
	t.Logf("workstream result: success=%v branch=%q err=%v steps=%d",
		r.Success, r.BranchName, r.Err, len(r.StepResults))
	for i, s := range r.StepResults {
		if s != nil {
			t.Logf("  step %d: success=%v attempts=%d escalation=%q files=%v",
				i+1, s.Success, s.Attempts, s.Escalation, s.Files)
		}
	}

	require.Truef(t, r.Success, "workstream failed: err=%v branch=%q", r.Err, r.BranchName)
	require.NotEmpty(t, r.BranchName, "feature branch should be set on success")

	contents, showErr := gitShow(repoDir, r.BranchName, "hello.txt")
	require.NoErrorf(t, showErr, "hello.txt should exist on feature branch %s", r.BranchName)
	require.NotEmpty(t, contents, "hello.txt should not be empty")
	t.Logf("hello.txt contents (%d bytes): %q", len(contents), contents)
	require.Truef(t,
		strings.Contains(strings.ToLower(contents), "pong"),
		"hello.txt should contain 'pong' (case-insensitive); got: %q", contents)
}

func setupLiveRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@locutus.local"},
		{"config", "user.name", "Locutus Live Test"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# locutus-live-test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return dir
}

func gitShow(dir, branch, path string) (string, error) {
	c := exec.Command("git", "show", branch+":"+path)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}
