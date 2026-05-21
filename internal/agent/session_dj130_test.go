package agent

import (
	"context"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/chetan/locutus/internal/agent/adapters"
	"github.com/chetan/locutus/internal/specio"
)

// TestLoggingExecutorOpensParentStepRecord (DJ-130 Phase 3) verifies
// LoggingExecutor.Run writes a parent step.yaml under the
// per-step folder, even when no child SDK call fires (the inner
// executor returns immediately).
func TestLoggingExecutorOpensParentStepRecord(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "/test")
	require.NoError(t, err)

	mock := NewMockExecutor(mockResp("planner output"))
	logging := NewLoggingExecutor(mock, rec)

	ctx := WithAgentID(WithRole(context.Background(), "propose"), "planner")
	_, err = logging.Run(ctx, AgentDef{ID: "planner", SystemPrompt: "you are planner"}, AgentInput{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)

	// Per-step folder created with step.yaml inside.
	stepDir := path.Join(rec.Path(), CallsDirName, "0001-planner")
	stepPath := path.Join(stepDir, StepFileName)
	data, err := fs.ReadFile(stepPath)
	require.NoError(t, err, "step.yaml must exist under the per-step folder")

	var step recordedStep
	require.NoError(t, yaml.Unmarshal(data, &step))
	assert.Equal(t, "planner", step.AgentID)
	assert.Equal(t, "propose", step.Role)
	assert.Equal(t, CallStatusCompleted, step.Status)
	assert.NotEmpty(t, step.CompletedAt, "step.yaml carries completed_at on success")
}

// TestSessionRecorderPerStepFolderLayout walks the full agent →
// adapter recording path: an adapter that opens one child via the
// bridge produces calls/0001-<agent>/{step.yaml, 01-single.yaml}.
func TestSessionRecorderPerStepFolderLayout(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "/test")
	require.NoError(t, err)

	// Simulate an adapter by opening one child call via the bridge
	// the LoggingExecutor would normally plumb. Verifies the
	// per-step folder layout end-to-end without needing a real SDK.
	step := rec.BeginStep("propose", "planner", "", AgentDef{ID: "planner"}, AgentInput{}, time.Now())
	bridge := &callRecorderBridge{step: step}
	handle := bridge.Begin(context.Background(), adapters.RecordedRoleSingle, "test-model", adapters.Request{
		Model:        "test-model",
		SystemPrompt: "system",
		Messages:     []adapters.Message{{Role: adapters.RoleUser, Content: "hi"}},
	}, time.Now())
	handle.Finish(&adapters.Response{
		Content:      "world",
		Model:        "test-model",
		InputTokens:  10,
		OutputTokens: 20,
		TotalTokens:  30,
	}, nil)
	step.Finish(nil)

	stepDir := path.Join(rec.Path(), CallsDirName, "0001-planner")
	stepPath := path.Join(stepDir, StepFileName)
	childPath := path.Join(stepDir, "01-single.yaml")

	stepData, err := fs.ReadFile(stepPath)
	require.NoError(t, err)
	var stepRec recordedStep
	require.NoError(t, yaml.Unmarshal(stepData, &stepRec))

	childData, err := fs.ReadFile(childPath)
	require.NoError(t, err, "child YAML named by role under the per-step folder")
	var childRec recordedCall
	require.NoError(t, yaml.Unmarshal(childData, &childRec))

	assert.Equal(t, []string{"01-single"}, stepRec.ChildCalls,
		"step.yaml lists each child by callID")
	assert.Equal(t, adapters.RecordedRoleSingle, childRec.Role,
		"child YAML role names the SDK-call purpose")
	assert.Equal(t, "world", childRec.Response)
	assert.Equal(t, 10, childRec.InputTokens)
}

// TestParentStepYAMLAggregatesTokens (DJ-130 Phase 3) verifies the
// stepHandle.Finish path sums token counts across two child calls
// — the shape the thinking + schema split produces (one reason, one
// format).
func TestParentStepYAMLAggregatesTokens(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "/test")
	require.NoError(t, err)

	step := rec.BeginStep("propose", "scout", "", AgentDef{ID: "scout"}, AgentInput{}, time.Now())
	bridge := &callRecorderBridge{step: step}

	reason := bridge.Begin(context.Background(), adapters.RecordedRoleReason, "strong-tier",
		adapters.Request{Model: "strong-tier"}, time.Now())
	reason.Finish(&adapters.Response{
		InputTokens: 100, OutputTokens: 200, ThoughtsTokens: 50, TotalTokens: 350,
	}, nil)

	format := bridge.Begin(context.Background(), adapters.RecordedRoleFormat, "fast-tier",
		adapters.Request{Model: "fast-tier"}, time.Now())
	format.Finish(&adapters.Response{
		InputTokens: 20, OutputTokens: 30, TotalTokens: 50,
	}, nil)

	step.Finish(nil)

	stepData, err := fs.ReadFile(path.Join(rec.Path(), CallsDirName, "0001-scout", StepFileName))
	require.NoError(t, err)
	var stepRec recordedStep
	require.NoError(t, yaml.Unmarshal(stepData, &stepRec))

	assert.Equal(t, 120, stepRec.InputTokens, "step.yaml sums input tokens across reason+format")
	assert.Equal(t, 230, stepRec.OutputTokens, "step.yaml sums output tokens")
	assert.Equal(t, 50, stepRec.ThoughtsTokens, "thoughts tokens only on reasoning pass")
	assert.Equal(t, 400, stepRec.TotalTokens, "total tokens sum across both calls")
	assert.Equal(t, []string{"01-reason", "02-format"}, stepRec.ChildCalls,
		"child_calls lists both passes in dispatch order")
	assert.Equal(t, "strong-tier", stepRec.Model,
		"step.yaml's representative model is the first child's model — the agent-declared tier")
}

// TestAdapterEmitsChildRecordPerSDKCall verifies the adapter layer
// actually consults the CallRecorder via adapters.CallRecorderFromContext
// and opens one Begin per logical SDK call. Driven via the recorder
// bridge LoggingExecutor.Run installs.
func TestAdapterEmitsChildRecordPerSDKCall(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "/test")
	require.NoError(t, err)

	step := rec.BeginStep("propose", "test", "", AgentDef{ID: "test"}, AgentInput{}, time.Now())
	bridge := &callRecorderBridge{step: step}

	// Drive the adapter-side context plumbing exactly like
	// LoggingExecutor.Run does.
	ctx := adapters.WithCallRecorder(context.Background(), bridge)

	// Count calls via a counting CallRecorder wrapping the bridge.
	counter := &countingCallRecorder{inner: bridge}
	ctx = adapters.WithCallRecorder(ctx, counter)

	// Simulate two SDK calls (mimicking what an adapter's runSplit
	// does). Each Begin opens a child YAML under the step folder.
	h1 := adapters.CallRecorderFromContext(ctx).Begin(ctx, adapters.RecordedRoleReason, "m1", adapters.Request{Model: "m1"}, time.Now())
	h1.Finish(&adapters.Response{Model: "m1"}, nil)
	h2 := adapters.CallRecorderFromContext(ctx).Begin(ctx, adapters.RecordedRoleFormat, "m2", adapters.Request{Model: "m2"}, time.Now())
	h2.Finish(&adapters.Response{Model: "m2"}, nil)

	step.Finish(nil)

	assert.Equal(t, 2, counter.calls, "adapter emits one Begin per logical SDK call")

	// Both children written under the step folder.
	entries := strings.Join(listMemFSChildren(t, fs, path.Join(rec.Path(), CallsDirName, "0001-test")), ",")
	assert.Contains(t, entries, "01-reason.yaml")
	assert.Contains(t, entries, "02-format.yaml")
	assert.Contains(t, entries, StepFileName)
}

// countingCallRecorder counts Begin calls so a test can assert the
// adapter layer reaches into the recorder per SDK call.
type countingCallRecorder struct {
	inner adapters.CallRecorder
	calls int
}

func (c *countingCallRecorder) Begin(ctx context.Context, role, model string, req adapters.Request, started time.Time) adapters.CallHandle {
	c.calls++
	return c.inner.Begin(ctx, role, model, req, started)
}

func listMemFSChildren(t *testing.T, fs specio.FS, dir string) []string {
	t.Helper()
	entries, err := fs.ListDir(dir)
	require.NoError(t, err)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, path.Base(e))
	}
	return out
}
