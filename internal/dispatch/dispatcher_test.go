package dispatch

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
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

func (m *alwaysPassDriver) BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd {
	return exec.Command("echo", "mock:"+m.id)
}
func (m *alwaysPassDriver) BuildRetryCommand(step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd {
	return exec.Command("echo", "mock-retry:"+m.id)
}
func (m *alwaysPassDriver) ParseStream(r io.Reader) StreamParser {
	return &fakeStreamParser{events: []AgentEvent{
		{Kind: EventInit, SessionID: "sess-" + m.id},
		{Kind: EventToolCall, ToolName: "Write", ToolInput: map[string]any{"file_path": "noop.go"}, FilePaths: []string{"noop.go"}},
		{Kind: EventResult, Text: "done", SessionID: "sess-" + m.id},
	}}
}
func (m *alwaysPassDriver) RespondToAgent(sessionID, response string) (*exec.Cmd, error) {
	return exec.Command("echo", "mock-resume:"+m.id), nil
}

// mockLLMAllPass returns a MockLLM that validates every call as PASS.
func mockLLMAllPass(count int) *agent.MockLLM {
	responses := make([]agent.MockResponse, count)
	for i := range responses {
		responses[i] = agent.MockResponse{Response: &agent.GenerateResponse{Content: "PASS"}}
	}
	return agent.NewMockLLM(responses...)
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
