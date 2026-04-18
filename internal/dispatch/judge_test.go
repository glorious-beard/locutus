package dispatch

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCapturingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return logger, &buf
}

func TestMonitorCycle_MissingAgent_LogsOnceAndReturnsFalse(t *testing.T) {
	logger, buf := newCapturingLogger()
	sup := &Supervisor{
		cfg: SupervisorConfig{
			AgentDefs: map[string]agent.AgentDef{}, // no monitor agent
			Logger:    logger,
		},
	}
	ctx := context.Background()

	v1, err := sup.monitorCycle(ctx, newTestStep(), nil)
	require.NoError(t, err)
	require.NotNil(t, v1)
	assert.False(t, v1.IsCycle, "missing monitor must return false, not error")

	v2, err := sup.monitorCycle(ctx, newTestStep(), nil)
	require.NoError(t, err)
	assert.False(t, v2.IsCycle)

	count := strings.Count(buf.String(), "monitor agent not configured")
	assert.Equal(t, 1, count, "INFO log fires exactly once per supervisor")
}

func TestMonitorCycle_ParsesVerdict(t *testing.T) {
	mock := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{
			Content: `{"is_cycle":true,"confidence":0.85,"pattern":"file_thrashing","reasoning":"same file edited then reverted"}`,
		},
	})
	sup := &Supervisor{cfg: SupervisorConfig{
		FastLLM:   mock,
		AgentDefs: map[string]agent.AgentDef{"monitor": {ID: "monitor", SystemPrompt: "detect cycles"}},
	}}

	v, err := sup.monitorCycle(context.Background(), newTestStep(), []AgentEvent{{Kind: EventText}})
	require.NoError(t, err)
	require.NotNil(t, v)
	assert.True(t, v.IsCycle)
	assert.InDelta(t, 0.85, v.Confidence, 0.001)
	assert.Equal(t, "file_thrashing", v.Pattern)
	assert.Contains(t, v.Reasoning, "same file")
}

func TestMonitorCycle_MalformedJSON_ReturnsError(t *testing.T) {
	mock := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: "not json at all"},
	})
	sup := &Supervisor{cfg: SupervisorConfig{
		FastLLM:   mock,
		AgentDefs: map[string]agent.AgentDef{"monitor": {ID: "monitor"}},
	}}

	v, err := sup.monitorCycle(context.Background(), newTestStep(), []AgentEvent{{Kind: EventText}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse monitor verdict")
	assert.Nil(t, v, "no verdict returned on parse failure")
}

func TestMonitorCycle_UsesFastLLMNotStrong(t *testing.T) {
	strong := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: "unused strong-tier response"},
	})
	fast := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: `{"is_cycle":false,"confidence":0.1,"reasoning":"healthy"}`},
	})
	sup := &Supervisor{cfg: SupervisorConfig{
		LLM:       strong,
		FastLLM:   fast,
		AgentDefs: map[string]agent.AgentDef{"monitor": {ID: "monitor"}},
	}}

	_, err := sup.monitorCycle(context.Background(), newTestStep(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, fast.CallCount(), "fast-tier LLM must be invoked exactly once")
	assert.Equal(t, 0, strong.CallCount(), "strong-tier LLM must NOT be invoked for monitoring")
}

func TestMonitorCycle_FastLLMNil_ReturnsError(t *testing.T) {
	// Defensive: a configured monitor agent with no FastLLM is a config bug;
	// surface it clearly rather than silently routing to the strong tier.
	sup := &Supervisor{cfg: SupervisorConfig{
		FastLLM:   nil,
		AgentDefs: map[string]agent.AgentDef{"monitor": {ID: "monitor"}},
	}}

	_, err := sup.monitorCycle(context.Background(), newTestStep(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FastLLM")
}
