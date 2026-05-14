package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderWorkstreamPlan_FullShape(t *testing.T) {
	ws := spec.Workstream{
		ID:             "ws-auth",
		StrategyDomain: "auth",
		AgentID:        "claude-code",
		Steps: []spec.PlanStep{
			{
				Order:         1,
				ID:            "step-1",
				ApproachID:    "strat-jwt",
				Description:   "Add JWT verification middleware.",
				ExpectedFiles: []string{"internal/auth/middleware.go", "internal/auth/middleware_test.go"},
				Assertions: []spec.Assertion{
					{Kind: spec.AssertionKindTestPass, Target: "./internal/auth/..."},
				},
			},
			{
				Order:         2,
				ID:            "step-2",
				ApproachID:    "strat-jwt",
				Description:   "Wire middleware into request pipeline.",
				ExpectedFiles: []string{"internal/api/router.go"},
			},
		},
		Assertions: []spec.Assertion{
			{Kind: spec.AssertionKindCompiles, Message: "whole project builds"},
		},
	}

	body := renderWorkstreamPlan(ws)

	// Header info — workstream id, strategy, agent assignment.
	assert.Contains(t, body, "# Workstream ws-auth")
	assert.Contains(t, body, "**Strategy domain:** auth")
	assert.Contains(t, body, "**Assigned agent:** claude-code")

	// Approaches section lists each unique ApproachID once.
	assert.Contains(t, body, "## Approaches in scope")
	assert.Equal(t, 1, strings.Count(body, "- strat-jwt"),
		"strat-jwt appears on two steps but should be deduplicated in the approaches list")

	// Expected files section lists each unique path, sorted.
	expectedFilesIdx := strings.Index(body, "## Expected files")
	require.NotEqual(t, -1, expectedFilesIdx)
	filesBlock := body[expectedFilesIdx:]
	apiIdx := strings.Index(filesBlock, "internal/api/router.go")
	authIdx := strings.Index(filesBlock, "internal/auth/middleware.go")
	require.True(t, apiIdx != -1 && authIdx != -1)
	assert.Less(t, apiIdx, authIdx, "expected files should be sorted alphabetically")

	// Suggested decomposition section preserves step order + descriptions.
	assert.Contains(t, body, "## Suggested decomposition")
	assert.Contains(t, body, "### Step 1: step-1")
	assert.Contains(t, body, "### Step 2: step-2")
	assert.Contains(t, body, "Add JWT verification middleware.")

	// Workstream-level acceptance criteria.
	assert.Contains(t, body, "## Acceptance criteria (workstream-level)")
	assert.Contains(t, body, "compiles")
	assert.Contains(t, body, "whole project builds")

	// Checklist instruction is always present — it's the load-bearing
	// DJ-121 piece.
	assert.Contains(t, body, "## Your checklist")
	assert.Contains(t, body, "_locutus/checklist.md")
}

func TestRenderWorkstreamPlan_MinimalShape(t *testing.T) {
	// A workstream with no steps, no assertions, no agent assigned still
	// produces a well-formed plan. The checklist section anchors the
	// agent regardless of how thin the spec input was.
	ws := spec.Workstream{
		ID:             "ws-spike",
		StrategyDomain: "exploration",
	}
	body := renderWorkstreamPlan(ws)

	assert.Contains(t, body, "# Workstream ws-spike")
	assert.Contains(t, body, "**Strategy domain:** exploration")
	assert.NotContains(t, body, "**Assigned agent:**", "AgentID is empty; assignment line should be omitted")
	assert.NotContains(t, body, "## Approaches in scope", "no steps → no approaches section")
	assert.NotContains(t, body, "## Expected files", "no steps → no expected files section")
	assert.NotContains(t, body, "## Suggested decomposition", "no steps → no decomposition section")
	assert.NotContains(t, body, "## Acceptance criteria", "no assertions → no criteria section")
	assert.Contains(t, body, "## Your checklist", "checklist section is always present")
}

func TestWriteWorkstreamPlan_CreatesFileAtExpectedPath(t *testing.T) {
	dir := t.TempDir()
	ws := spec.Workstream{
		ID:             "ws-test",
		StrategyDomain: "test",
	}

	require.NoError(t, WriteWorkstreamPlan(dir, ws))

	planPath := filepath.Join(dir, LocutusWorktreeDir, "plan.md")
	body, err := os.ReadFile(planPath)
	require.NoError(t, err, "plan.md should exist at the conventional path")
	assert.Contains(t, string(body), "# Workstream ws-test")
}

func TestWriteWorkstreamPlan_EmptyWorktreeDirIsError(t *testing.T) {
	err := WriteWorkstreamPlan("", spec.Workstream{ID: "ws"})
	require.Error(t, err, "empty worktreeDir must error rather than silently writing into the cwd")
}
