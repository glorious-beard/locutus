package dispatch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupDispatchRepo creates a real temp git repo and returns its path.
func setupDispatchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o644)
	assert.NoError(t, err)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")
	return dir
}

// alwaysPassRunner returns a runner that always returns an empty success output.
func alwaysPassRunner() CommandRunner {
	return batchRunner([]byte(`{}`))
}

// alwaysPassDriver is a mock StreamingDriver that yields a single-event
// successful stream on every attempt: init, one tool call touching a
// fake file, and a final result.
type alwaysPassDriver struct {
	id string
}

func (m *alwaysPassDriver) BuildCommand(ctx context.Context, step spec.PlanStep, workDir string) *exec.Cmd {
	return exec.CommandContext(ctx, "echo", "mock:"+m.id)
}
func (m *alwaysPassDriver) BuildRetryCommand(ctx context.Context, step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd {
	return exec.CommandContext(ctx, "echo", "mock-retry:"+m.id)
}
func (m *alwaysPassDriver) ParseStream(r io.Reader) StreamParser {
	return &fakeStreamParser{events: []AgentEvent{
		{Kind: EventInit, SessionID: "sess-" + m.id},
		{Kind: EventToolCall, ToolName: "Write", ToolInput: map[string]any{"file_path": "noop.go"}, FilePaths: []string{"noop.go"}},
		{Kind: EventResult, Text: "done", SessionID: "sess-" + m.id},
	}}
}
func (m *alwaysPassDriver) RespondToAgent(ctx context.Context, sessionID, response string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "echo", "mock-resume:"+m.id), nil
}

// mockLLMAllPass returns a MockLLM that validates every call as PASS.
func mockLLMAllPass(count int) *agent.MockExecutor {
	responses := make([]agent.MockResponse, count)
	for i := range responses {
		responses[i] = agent.MockResponse{Response: &agent.AgentOutput{Content: "PASS"}}
	}
	return agent.NewMockExecutor(responses...)
}

// makeWorkstream creates a workstream with N trivial steps.
func makeWorkstream(id string, agentID string, stepCount int, dependsOn ...string) spec.Workstream {
	steps := make([]spec.PlanStep, stepCount)
	for i := range steps {
		steps[i] = spec.PlanStep{
			ID:          id + "-step-" + string(rune('1'+i)),
			Order:       i + 1,
			Description: "do the thing " + string(rune('1'+i)),
			Assertions: []spec.Assertion{
				{Kind: spec.AssertionKindTestPass, Target: "./..."},
			},
		}
	}
	deps := make([]spec.WorkstreamDependency, len(dependsOn))
	for i, d := range dependsOn {
		deps[i] = spec.WorkstreamDependency{WorkstreamID: d}
	}
	return spec.Workstream{
		ID:          id,
		AgentID:     agentID,
		DetailLevel: spec.DetailLevelHigh,
		DependsOn:   deps,
		Steps:       steps,
	}
}

func TestDispatchSingleWorkstream(t *testing.T) {
	repoDir := setupDispatchRepo(t)

	plan := &spec.MasterPlan{
		ID: "plan-001",
		Workstreams: []spec.Workstream{
			makeWorkstream("ws-only", "claude-code", 2),
		},
	}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(2),
		Drivers: map[string]StreamingDriver{"claude-code": &alwaysPassDriver{id: "claude-code"}},
		Runner:  alwaysPassRunner(),
	}

	results, err := d.Dispatch(context.Background(), plan, repoDir, nil)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.True(t, results[0].Success, "workstream should succeed")
	assert.Equal(t, "ws-only", results[0].WorkstreamID)
	assert.Len(t, results[0].StepResults, 2)
}

func TestDispatchMultipleWorkstreamsParallel(t *testing.T) {
	repoDir := setupDispatchRepo(t)

	plan := &spec.MasterPlan{
		ID: "plan-002",
		Workstreams: []spec.Workstream{
			makeWorkstream("ws-a", "claude-code", 1),
			makeWorkstream("ws-b", "claude-code", 1),
		},
	}

	d := &Dispatcher{
		LLM:         mockLLMAllPass(2),
		Drivers:     map[string]StreamingDriver{"claude-code": &alwaysPassDriver{id: "claude-code"}},
		Runner:      alwaysPassRunner(),
		MaxTotal:    10,
	}

	results, err := d.Dispatch(context.Background(), plan, repoDir, nil)
	assert.NoError(t, err)
	assert.Len(t, results, 2)
	for _, r := range results {
		assert.True(t, r.Success)
	}
}

func TestDispatchRespectsDependencies(t *testing.T) {
	repoDir := setupDispatchRepo(t)

	// ws-b depends on ws-a — ws-a must complete before ws-b starts.
	plan := &spec.MasterPlan{
		ID: "plan-003",
		Workstreams: []spec.Workstream{
			makeWorkstream("ws-a", "claude-code", 1),
			makeWorkstream("ws-b", "claude-code", 1, "ws-a"),
		},
	}

	// Track start times to verify ordering.
	var aStarted, bStarted atomic.Int64
	orderRunner := func(cmd *exec.Cmd) (io.ReadCloser, error) {
		now := time.Now().UnixNano()
		// Peek at the command args to determine which workstream.
		args := cmd.Args
		for _, a := range args {
			if a == "mock:claude-code" {
				if aStarted.Load() == 0 {
					aStarted.Store(now)
					time.Sleep(20 * time.Millisecond)
				} else if bStarted.Load() == 0 {
					bStarted.Store(now)
				}
				break
			}
		}
		return io.NopCloser(bytes.NewReader([]byte(`{}`))), nil
	}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(2),
		Drivers: map[string]StreamingDriver{"claude-code": &alwaysPassDriver{id: "claude-code"}},
		Runner:  orderRunner,
	}

	results, err := d.Dispatch(context.Background(), plan, repoDir, nil)
	assert.NoError(t, err)
	assert.Len(t, results, 2)

	// ws-b must start after ws-a started.
	assert.Greater(t, bStarted.Load(), aStarted.Load(), "ws-b should start after ws-a")
}

func TestDispatchPerAgentConcurrencyLimit(t *testing.T) {
	repoDir := setupDispatchRepo(t)

	// Three independent workstreams, all claude-code, with limit of 1.
	plan := &spec.MasterPlan{
		ID: "plan-004",
		Workstreams: []spec.Workstream{
			makeWorkstream("ws-a", "claude-code", 1),
			makeWorkstream("ws-b", "claude-code", 1),
			makeWorkstream("ws-c", "claude-code", 1),
		},
	}

	var running, peak atomic.Int32
	trackingRunner := func(cmd *exec.Cmd) (io.ReadCloser, error) {
		cur := running.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		running.Add(-1)
		return io.NopCloser(bytes.NewReader([]byte(`{}`))), nil
	}

	d := &Dispatcher{
		LLM:         mockLLMAllPass(3),
		Drivers:     map[string]StreamingDriver{"claude-code": &alwaysPassDriver{id: "claude-code"}},
		Runner:      trackingRunner,
		MaxPerAgent: map[string]int{"claude-code": 1},
	}

	results, err := d.Dispatch(context.Background(), plan, repoDir, nil)
	assert.NoError(t, err)
	assert.Len(t, results, 3)
	assert.LessOrEqual(t, int(peak.Load()), 1, "at most 1 claude-code workstream should run at a time")
}

// TestRunWorkstreamFiresOnStepCompletePerStep verifies the per-step
// persistence hook (DJ-073: "On each PlanStep completion: dispatcher
// calls Save on the affected ActiveWorkstream") fires once per step,
// in plan order, with the right StepID and SessionID. Without this,
// crash mid-workstream loses the prior steps' resume granularity.
func TestRunWorkstreamFiresOnStepCompletePerStep(t *testing.T) {
	repoDir := setupDispatchRepo(t)
	plan := &spec.MasterPlan{
		ID: "plan-step-events",
		Workstreams: []spec.Workstream{
			makeWorkstream("ws-three", "claude-code", 3),
		},
	}

	var (
		mu     sync.Mutex
		events []StepEvent
	)
	d := &Dispatcher{
		LLM:     mockLLMAllPass(3),
		Drivers: map[string]StreamingDriver{"claude-code": &alwaysPassDriver{id: "claude-code"}},
		Runner:  alwaysPassRunner(),
		OnStepComplete: func(_ context.Context, evt StepEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, evt)
		},
	}

	results, err := d.Dispatch(context.Background(), plan, repoDir, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.True(t, results[0].Success)

	require.Len(t, events, 3, "OnStepComplete should fire once per step")
	expectedIDs := []string{"ws-three-step-1", "ws-three-step-2", "ws-three-step-3"}
	for i, evt := range events {
		assert.Equal(t, "ws-three", evt.WorkstreamID)
		assert.Equal(t, expectedIDs[i], evt.StepID, "step %d ID", i+1)
		assert.True(t, evt.Success, "step %d should report success", i+1)
		assert.Equal(t, "sess-claude-code", evt.SessionID, "step %d session", i+1)
	}
}

// fileWritingDriver actually writes a file in the worktree as a side
// effect of "running" the agent, so per-step CommitIfChanges has real
// diffs to merge. Used to verify that DJ-074's "already-completed
// steps' merged work" promise is now delivered: each successful step's
// changes land on the feature branch as we go, not in one tail commit.
type fileWritingDriver struct{ id string }

func (d *fileWritingDriver) BuildCommand(ctx context.Context, step spec.PlanStep, workDir string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c",
		fmt.Sprintf("printf %q > %s/%s.txt", step.Description, workDir, step.ID))
}
func (d *fileWritingDriver) BuildRetryCommand(ctx context.Context, step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd {
	return d.BuildCommand(ctx, step, workDir)
}
func (d *fileWritingDriver) ParseStream(_ io.Reader) StreamParser {
	return &fakeStreamParser{events: []AgentEvent{
		{Kind: EventInit, SessionID: "sess-" + d.id},
		{Kind: EventResult, Text: "done", SessionID: "sess-" + d.id},
	}}
}
func (d *fileWritingDriver) RespondToAgent(ctx context.Context, sessionID, response string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "echo", "ok"), nil
}

// TestRunWorkstreamPerStepMergeAccumulatesOnFeatureBranch verifies the
// per-step merge actually creates the feature branch with one commit
// per step, so a SIGKILL'd workstream's resume can pick up from
// `locutus/<wsID>` and skip already-merged steps (DJ-074).
func TestRunWorkstreamPerStepMergeAccumulatesOnFeatureBranch(t *testing.T) {
	repoDir := setupDispatchRepo(t)
	plan := &spec.MasterPlan{
		ID:          "plan-merge",
		Workstreams: []spec.Workstream{makeWorkstream("ws-merge", "claude-code", 3)},
	}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(3),
		Drivers: map[string]StreamingDriver{"claude-code": &fileWritingDriver{id: "claude-code"}},
		Runner:  ProductionRunner,
	}

	results, err := d.Dispatch(context.Background(), plan, repoDir, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.True(t, results[0].Success, "workstream should succeed; err=%v", results[0].Err)
	assert.Equal(t, "locutus/ws-merge", results[0].BranchName,
		"BranchName should advance to the feature branch once any step has merged")

	// Verify the feature branch carries one commit per step (plus
	// merge commits from --no-ff). We grep for the per-step commit
	// message format "workstream <wsID>: step <stepID>".
	logOut := runOutput(t, repoDir, "git", "log", "--oneline", "locutus/ws-merge")
	stepCommits := 0
	for _, line := range strings.Split(strings.TrimSpace(logOut), "\n") {
		if strings.Contains(line, "workstream ws-merge: step ws-merge-step-") {
			stepCommits++
		}
	}
	assert.Equal(t, 3, stepCommits,
		"feature branch should have one commit per successful step; got log:\n%s", logOut)
}

func TestDispatchMissingDriver(t *testing.T) {
	repoDir := setupDispatchRepo(t)

	plan := &spec.MasterPlan{
		ID: "plan-005",
		Workstreams: []spec.Workstream{
			makeWorkstream("ws-unknown", "unknown-agent", 1),
		},
	}

	d := &Dispatcher{
		LLM:     mockLLMAllPass(1),
		Drivers: map[string]StreamingDriver{}, // no drivers registered
		Runner:  alwaysPassRunner(),
	}

	results, err := d.Dispatch(context.Background(), plan, repoDir, nil)
	// Dispatch itself completes, but the workstream fails.
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.False(t, results[0].Success)
	assert.Error(t, results[0].Err)
}
