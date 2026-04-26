package dispatch

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stepRecordingDriver records which step IDs were dispatched (and via which
// build path — first attempt vs. retry-with-resume) so resume tests can
// assert step skipping and sessionID pre-seeding.
type stepRecordingDriver struct {
	id              string
	mu              sync.Mutex
	buildCommandIDs []string                  // step.ID per BuildCommand call
	retryCommandIDs []string                  // step.ID per BuildRetryCommand call
	retrySessionIDs []string                  // sessionID arg per BuildRetryCommand call
}

func (d *stepRecordingDriver) BuildCommand(ctx context.Context, step spec.PlanStep, workDir string) *exec.Cmd {
	d.mu.Lock()
	d.buildCommandIDs = append(d.buildCommandIDs, step.ID)
	d.mu.Unlock()
	return exec.CommandContext(ctx, "echo", "mock:"+d.id)
}

func (d *stepRecordingDriver) BuildRetryCommand(ctx context.Context, step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd {
	d.mu.Lock()
	d.retryCommandIDs = append(d.retryCommandIDs, step.ID)
	d.retrySessionIDs = append(d.retrySessionIDs, sessionID)
	d.mu.Unlock()
	return exec.CommandContext(ctx, "echo", "mock-retry:"+d.id)
}

func (d *stepRecordingDriver) ParseStream(r io.Reader) StreamParser {
	return &fakeStreamParser{events: []AgentEvent{
		{Kind: EventInit, SessionID: "sess-" + d.id},
		{Kind: EventResult, Text: "done", SessionID: "sess-" + d.id},
	}}
}

func (d *stepRecordingDriver) RespondToAgent(ctx context.Context, sessionID, response string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "echo", "mock-respond"), nil
}

func (d *stepRecordingDriver) dispatchedSteps() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.buildCommandIDs)+len(d.retryCommandIDs))
	out = append(out, d.buildCommandIDs...)
	out = append(out, d.retryCommandIDs...)
	return out
}

func TestStepOutcomeHasSessionIDField(t *testing.T) {
	var so StepOutcome
	so.SessionID = "session-abc"
	assert.Equal(t, "session-abc", so.SessionID)
}

func TestWorkstreamResultHasAgentSessionIDField(t *testing.T) {
	var wr WorkstreamResult
	wr.AgentSessionID = "session-xyz"
	assert.Equal(t, "session-xyz", wr.AgentSessionID)
}

func TestResumePointShape(t *testing.T) {
	rp := ResumePoint{StepID: "step-3", SessionID: "session-abc"}
	assert.Equal(t, "step-3", rp.StepID)
	assert.Equal(t, "session-abc", rp.SessionID)
}

func TestRunWorkstreamSurfacesSessionIDOnSuccess(t *testing.T) {
	repoDir := setupDispatchRepo(t)
	driver := &stepRecordingDriver{id: "claude-code"}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(10),
		FastLLM: mockLLMAllPass(10),
		Drivers: map[string]StreamingDriver{"claude-code": driver},
		Runner:  alwaysPassRunner(),
	}
	ws := makeWorkstream("ws-a", "claude-code", 2)

	result := d.runWorkstream(context.Background(), ws, repoDir, 1, nil)
	require.NotNil(t, result)
	assert.True(t, result.Success, "result.Err = %v", result.Err)
	assert.Equal(t, "sess-claude-code", result.AgentSessionID,
		"WorkstreamResult.AgentSessionID is the last step's surfaced sessionID")
}

// seedFeatureBranch creates the `locutus/<ws-id>` branch on the test repo
// so resume can base a worktree on it. In production this branch would
// already exist as the prior run's merge target.
func seedFeatureBranch(t *testing.T, repoDir, workstreamID string) {
	t.Helper()
	run(t, repoDir, "git", "branch", "locutus/"+workstreamID)
}

func TestRunWorkstreamSkipsToResumeStep(t *testing.T) {
	repoDir := setupDispatchRepo(t)
	seedFeatureBranch(t, repoDir, "ws-a")
	driver := &stepRecordingDriver{id: "claude-code"}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(10),
		FastLLM: mockLLMAllPass(10),
		Drivers: map[string]StreamingDriver{"claude-code": driver},
		Runner:  alwaysPassRunner(),
	}
	ws := makeWorkstream("ws-a", "claude-code", 3)
	// Steps are ws-a-step-1, ws-a-step-2, ws-a-step-3 per makeWorkstream.

	result := d.runWorkstream(context.Background(), ws, repoDir, 1, &ResumePoint{
		StepID:    "ws-a-step-2",
		SessionID: "session-from-prior-run",
	})
	require.NotNil(t, result)
	require.NoError(t, result.Err)

	dispatched := driver.dispatchedSteps()
	assert.NotContains(t, dispatched, "ws-a-step-1", "step-1 must be skipped on resume to step-2")
	assert.Contains(t, dispatched, "ws-a-step-2", "resume target must be dispatched")
	assert.Contains(t, dispatched, "ws-a-step-3", "subsequent steps must run")
}

func TestRunWorkstreamResumePreSeedsSessionIDIntoFirstAttempt(t *testing.T) {
	repoDir := setupDispatchRepo(t)
	seedFeatureBranch(t, repoDir, "ws-a")
	driver := &stepRecordingDriver{id: "claude-code"}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(10),
		FastLLM: mockLLMAllPass(10),
		Drivers: map[string]StreamingDriver{"claude-code": driver},
		Runner:  alwaysPassRunner(),
	}
	ws := makeWorkstream("ws-a", "claude-code", 2)

	result := d.runWorkstream(context.Background(), ws, repoDir, 1, &ResumePoint{
		StepID:    "ws-a-step-1",
		SessionID: "session-sentinel",
	})
	require.NotNil(t, result)
	require.NoError(t, result.Err)

	driver.mu.Lock()
	defer driver.mu.Unlock()
	require.NotEmpty(t, driver.retryCommandIDs, "resume must use BuildRetryCommand for the first attempt of the resumed step")
	assert.Equal(t, "ws-a-step-1", driver.retryCommandIDs[0])
	assert.Equal(t, "session-sentinel", driver.retrySessionIDs[0],
		"first BuildRetryCommand call must receive the persisted sessionID")
}

// TestStepOutcomeSessionIDPropagatesToWorkstreamResult checks the
// integration point Phase B persistence depends on: runWorkstream rolls
// the most recent step's SessionID up to AgentSessionID. recordStepProgress
// in cmd/adopt.go reads that field and stamps it on ActiveWorkstream.
func TestStepOutcomeSessionIDPropagatesToWorkstreamResult(t *testing.T) {
	repoDir := setupDispatchRepo(t)
	driver := &stepRecordingDriver{id: "claude-code"}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(10),
		FastLLM: mockLLMAllPass(10),
		Drivers: map[string]StreamingDriver{"claude-code": driver},
		Runner:  alwaysPassRunner(),
	}
	ws := makeWorkstream("ws-a", "claude-code", 3)
	result := d.runWorkstream(context.Background(), ws, repoDir, 1, nil)
	require.NoError(t, result.Err)
	assert.Equal(t, "sess-claude-code", result.AgentSessionID)
	for i, so := range result.StepResults {
		assert.Equal(t, "sess-claude-code", so.SessionID, "step %d carries sessionID", i)
	}
}

func TestRunWorkstreamResumeUnknownStepIDIsError(t *testing.T) {
	repoDir := setupDispatchRepo(t)
	driver := &stepRecordingDriver{id: "claude-code"}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(2),
		FastLLM: mockLLMAllPass(2),
		Drivers: map[string]StreamingDriver{"claude-code": driver},
		Runner:  alwaysPassRunner(),
	}
	ws := makeWorkstream("ws-a", "claude-code", 1)

	result := d.runWorkstream(context.Background(), ws, repoDir, 1, &ResumePoint{StepID: "ghost-step"})
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), "ghost-step")
}
