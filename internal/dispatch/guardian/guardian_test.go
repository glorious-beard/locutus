package guardian

import (
	"context"
	"errors"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/dispatch/policy"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestWorkstream returns a minimal Workstream for guardian tests.
// Under DJ-121 the Guardian's prompt is anchored at workstream grain;
// the inline step preserves expected-files context the buildPrompt
// helper unions across steps.
func newTestWorkstream() spec.Workstream {
	return spec.Workstream{
		ID:             "ws-auth",
		StrategyDomain: "auth",
		Steps: []spec.PlanStep{
			{
				ID:            "step-1",
				Description:   "Implement auth middleware",
				ExpectedFiles: []string{"internal/auth/middleware.go"},
			},
		},
	}
}

func writeOptions() []policy.Option {
	return []policy.Option{
		{OptionID: "allow", Kind: policy.KindAllowOnce, Name: "Allow"},
		{OptionID: "deny", Kind: policy.KindRejectOnce, Name: "Reject"},
	}
}

func TestGuardian_AllowVerdictSelectsAllowOnce(t *testing.T) {
	llm := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: "ALLOW"},
	})
	g := &Guardian{LLM: llm, Def: agent.AgentDef{ID: "validator"}, Workstream: newTestWorkstream()}

	dec, err := g.Decide(context.Background(), policy.Request{
		ToolName: "Write",
		RawInput: map[string]any{"file_path": "internal/auth/middleware.go"},
		Options:  writeOptions(),
	})
	require.NoError(t, err)
	assert.Equal(t, "allow", dec.OptionID, "ALLOW verdict should select the allow_once option")
}

func TestGuardian_DenyVerdictSelectsRejectOnce(t *testing.T) {
	llm := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: "DENY: writing to a file outside this step's expected scope"},
	})
	g := &Guardian{LLM: llm, Def: agent.AgentDef{ID: "validator"}, Workstream: newTestWorkstream()}

	dec, err := g.Decide(context.Background(), policy.Request{
		ToolName: "Write",
		RawInput: map[string]any{"file_path": "/etc/passwd"},
		Options:  writeOptions(),
	})
	require.NoError(t, err)
	assert.Equal(t, "deny", dec.OptionID, "DENY: verdict should select the reject_once option")
}

func TestGuardian_UnparseableVerdictTreatedAsDeny(t *testing.T) {
	// Model returns prose without the expected ALLOW/DENY prefix. The
	// safe default is to deny.
	llm := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: "I'm not sure, perhaps you should check the docs?"},
	})
	g := &Guardian{LLM: llm, Def: agent.AgentDef{ID: "validator"}, Workstream: newTestWorkstream()}

	dec, err := g.Decide(context.Background(), policy.Request{
		ToolName: "Bash",
		RawInput: map[string]any{"command": "ls /"},
		Options:  writeOptions(),
	})
	require.NoError(t, err)
	assert.Equal(t, "deny", dec.OptionID, "unrecognised verdict should fall through to reject")
}

func TestGuardian_NoValidatorAgent_CancelsWithReject(t *testing.T) {
	// No Def — guardian falls back to deny-by-default without consulting the LLM.
	g := &Guardian{LLM: agent.NewMockExecutor(), Workstream: newTestWorkstream()}

	dec, err := g.Decide(context.Background(), policy.Request{
		ToolName: "Write",
		Options:  writeOptions(),
	})
	require.NoError(t, err)
	assert.Equal(t, "deny", dec.OptionID, "missing validator agent def should pick the reject option")
}

func TestGuardian_NilLLMReturnsError(t *testing.T) {
	g := &Guardian{Def: agent.AgentDef{ID: "validator"}, Workstream: newTestWorkstream()}
	_, err := g.Decide(context.Background(), policy.Request{
		ToolName: "Write",
		Options:  writeOptions(),
	})
	require.Error(t, err, "nil LLM should surface as an error (programmer error)")
}

func TestGuardian_LLMErrorPicksRejectAndPropagates(t *testing.T) {
	// LLM call fails after exhausting retries. Guardian should pick a
	// reject option AND return the error so the supervisor logs it.
	want := errors.New("LLM unreachable")
	llm := agent.NewMockExecutor(
		agent.MockResponse{Err: want},
		agent.MockResponse{Err: want}, // guardianRetry has MaxAttempts=2
	)
	g := &Guardian{LLM: llm, Def: agent.AgentDef{ID: "validator"}, Workstream: newTestWorkstream()}

	dec, err := g.Decide(context.Background(), policy.Request{
		ToolName: "Write",
		Options:  writeOptions(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM unreachable")
	assert.Equal(t, "deny", dec.OptionID, "LLM failure should still pick a deny option as a fallback Decision")
}

func TestGuardian_AllowVerdictWithNoAllowOptionCancels(t *testing.T) {
	// Edge case: agent offered only reject options but the validator says
	// ALLOW. allowOrCancel returns an empty Decision (cancel).
	llm := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: "ALLOW"},
	})
	g := &Guardian{LLM: llm, Def: agent.AgentDef{ID: "validator"}, Workstream: newTestWorkstream()}

	dec, err := g.Decide(context.Background(), policy.Request{
		ToolName: "Write",
		Options:  []policy.Option{{OptionID: "deny", Kind: policy.KindRejectOnce, Name: "Reject"}},
	})
	require.NoError(t, err)
	assert.Empty(t, dec.OptionID, "no allow option offered → cancel (empty OptionID)")
}
