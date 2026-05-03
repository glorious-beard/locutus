package agent

import (
	"context"
	"fmt"
	"path"
	"sort"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRoleContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", RoleFromContext(ctx))

	ctx = WithRole(ctx, "proposer")
	assert.Equal(t, "proposer", RoleFromContext(ctx))

	ctx = WithRole(ctx, "critic")
	assert.Equal(t, "critic", RoleFromContext(ctx),
		"WithRole should overwrite the prior role on the chain")
}

// loadSessionManifest unmarshals the manifest at <rec.Path()>/session.yaml.
// Test helper so individual cases don't repeat the read+unmarshal dance.
func loadSessionManifest(t *testing.T, fs specio.FS, rec *SessionRecorder) sessionManifest {
	t.Helper()
	data, err := fs.ReadFile(rec.ManifestPath())
	require.NoError(t, err, "manifest must be on disk")
	var m sessionManifest
	require.NoError(t, yaml.Unmarshal(data, &m))
	return m
}

// loadSessionCalls reads every per-call file under <rec.Path()>/calls/,
// returning them sorted by index. Stitches the per-file shape back into
// the slice that the old single-file tests used.
func loadSessionCalls(t *testing.T, fs specio.FS, rec *SessionRecorder) []recordedCall {
	t.Helper()
	dir := path.Join(rec.Path(), CallsDirName)
	files, err := fs.ListDir(dir)
	require.NoError(t, err, "calls dir must exist")
	calls := make([]recordedCall, 0, len(files))
	for _, p := range files {
		data, err := fs.ReadFile(p)
		require.NoError(t, err)
		var c recordedCall
		require.NoError(t, yaml.Unmarshal(data, &c), "decode %s", p)
		calls = append(calls, c)
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].Index < calls[j].Index })
	return calls
}

func TestSessionRecorderWritesToProjectFS(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "refine goals", "/test/project")
	require.NoError(t, err)
	require.NotEmpty(t, rec.SessionID())

	// Nested layout — .locutus/sessions/<YYYYMMDD>/<HHMM>/<SS>-<short>/.
	// Per-minute directory (not per-second) avoids exploding into
	// single-file directories when sessions don't actually fire that
	// fast; `rm -rf .locutus/sessions/20260420` still drops a day.
	// Asserted via regex so we don't pin the test to whatever clock is
	// active at run time. Note: a directory now, not a single file.
	assert.Regexp(t,
		`^\.locutus/sessions/\d{8}/\d{4}/\d{2}-[0-9a-f]{6}$`,
		rec.Path(),
		"session path must be a directory nested by date and HHMM so each day/minute is one rm -rf away")

	manifest := loadSessionManifest(t, fs, rec)
	assert.Equal(t, "refine goals", manifest.Command)
	assert.Equal(t, "/test/project", manifest.ProjectRoot)
	assert.Empty(t, manifest.CompletedAt,
		"completed_at must remain empty until Close so tooling can detect interrupted sessions")
	calls := loadSessionCalls(t, fs, rec)
	assert.Empty(t, calls, "no calls flushed before Begin/Record")
}

func TestSessionRecorderRecordsCallsInOrder(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	t1 := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	rec.Record("proposer", "spec_architect", "",
		GenerateRequest{
			Model: "googleai/gemini-2.5-pro",
			Messages: []Message{
				{Role: "system", Content: "you are an architect"},
				{Role: "user", Content: "build it"},
			},
		},
		&GenerateResponse{Content: `{"features":[]}`, Model: "googleai/gemini-2.5-pro"},
		nil, t1, 1234*time.Millisecond,
	)
	rec.Record("critic", "architect_critic", "",
		GenerateRequest{
			Model: "googleai/gemini-2.5-pro",
			Messages: []Message{
				{Role: "system", Content: "you are a critic"},
				{Role: "user", Content: "review this"},
			},
		},
		&GenerateResponse{Content: `{"issues":["x"]}`},
		nil, t1.Add(2*time.Second), 500*time.Millisecond,
	)

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 2)
	assert.Equal(t, 1, calls[0].Index)
	assert.Equal(t, "proposer", calls[0].Role)
	assert.Equal(t, "spec_architect", calls[0].AgentID,
		"agent_id must be recorded so trace consumers can identify the source agent")
	assert.Equal(t, int64(1234), calls[0].DurationMS)
	assert.Equal(t, 2, calls[1].Index)
	assert.Equal(t, "critic", calls[1].Role)
	assert.Equal(t, "architect_critic", calls[1].AgentID)

	// agent_id is also encoded into the per-call filename so `ls calls/`
	// is a useful at-a-glance view of what happened in the session.
	files, err := fs.ListDir(path.Join(rec.Path(), CallsDirName))
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Contains(t, files[0], "0001-spec_architect")
	assert.Contains(t, files[1], "0002-architect_critic")
}

func TestSessionRecorderEmitsLiteralBlocksForMultilineContent(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("proposer", "spec_architect", "",
		GenerateRequest{
			Model: "test-model",
			Messages: []Message{
				{Role: "system", Content: "rule one\nrule two\nrule three"},
				{Role: "user", Content: "## Header\n\nbody line\n"},
			},
		},
		&GenerateResponse{Content: `{"k":"v"}`},
		nil, time.Now(), 0,
	)

	files, err := fs.ListDir(path.Join(rec.Path(), CallsDirName))
	require.NoError(t, err)
	require.Len(t, files, 1)
	raw, err := fs.ReadFile(files[0])
	require.NoError(t, err)
	out := string(raw)

	// yaml.v3 picks block scalar styles for multiline strings so the
	// transcript reads as the original prose. The multi-line system
	// rule should land under a `|` (or `|-`) literal indicator, not as
	// a quoted "rule one\nrule two\nrule three".
	assert.Regexp(t, `content: \|-?\n\s+rule one\n\s+rule two\n\s+rule three`, out,
		"expected literal block for multiline system content")
	assert.NotContains(t, out, `"rule one\nrule two\nrule three"`,
		"multiline content must not collapse to an escaped single-line string")
}

func TestSessionRecorderRecordsErrors(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("proposer", "spec_architect", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		nil,
		fmt.Errorf("model unavailable"),
		time.Now(), 0,
	)

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1)
	assert.Equal(t, "model unavailable", calls[0].Error)
	assert.Empty(t, calls[0].Response)
	assert.Equal(t, CallStatusError, calls[0].Status)
}

func TestLoggingLLMRecordsAndDelegates(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: `{"ok":true}`, Model: "test-model"},
	})
	logging := NewLoggingLLM(mock, rec)

	ctx := WithRole(context.Background(), "proposer")
	resp, err := logging.Generate(ctx, GenerateRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, resp.Content)
	assert.Equal(t, 1, mock.CallCount(), "inner LLM should still be called exactly once")

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1)
	assert.Equal(t, "proposer", calls[0].Role,
		"role from context should land on the recorded call")
	assert.Equal(t, `{"ok":true}`, calls[0].Response)
}

func TestSessionRecorderBeginWritesInProgressEntry(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	started := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	handle := rec.Begin("proposer", "spec_architect", "",
		GenerateRequest{
			Model:    "googleai/gemini-2.5-pro",
			Messages: []Message{{Role: "user", Content: "go"}},
		},
		started,
	)
	require.NotNil(t, handle)

	// Tail-of-file behavior: an in-flight call's per-call file is on
	// disk before Begin returns. This is the whole point of the
	// placeholder — an operator watching the directory knows what's
	// blocking right now. With per-call files the property is even
	// stronger: a SIGKILL between Begin and Finish leaves the input
	// messages on disk so the operator can debug "why did this call
	// take forever?" without losing the prompt.
	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1)
	assert.Equal(t, CallStatusInProgress, calls[0].Status)
	assert.Equal(t, "proposer", calls[0].Role)
	assert.Empty(t, calls[0].Response, "response should be empty until Finish")
	assert.Empty(t, calls[0].CompletedAt)
	assert.Zero(t, calls[0].DurationMS)
	assert.Equal(t, 1, rec.inFlightCount(),
		"the call must be tracked as in-flight until Finish drops it")

	handle.Finish(&GenerateResponse{
		Content:      `{"ok":true}`,
		InputTokens:  120,
		OutputTokens: 45,
		TotalTokens:  165,
	}, nil)

	calls = loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1, "Finish must update the same per-call file, not append a new one")
	assert.Equal(t, CallStatusCompleted, calls[0].Status)
	assert.Equal(t, `{"ok":true}`, calls[0].Response)
	assert.Equal(t, 120, calls[0].InputTokens)
	assert.Equal(t, 45, calls[0].OutputTokens)
	assert.Equal(t, 165, calls[0].TotalTokens)
	assert.NotEmpty(t, calls[0].CompletedAt)
	assert.Zero(t, rec.inFlightCount(),
		"Finish must release the in-flight slot so the call's payload is GC-eligible")
}

func TestSessionRecorderFinishWithErrorMarksStatus(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	handle := rec.Begin("critic", "architect_critic", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		time.Now(),
	)
	handle.Finish(nil, fmt.Errorf("context canceled"))

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1)
	assert.Equal(t, CallStatusError, calls[0].Status)
	assert.Equal(t, "context canceled", calls[0].Error)
	assert.Empty(t, calls[0].Response)
}

func TestLoggingLLMPersistsTokenCounts(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{
			Content:      `{"ok":true}`,
			Model:        "test-model",
			InputTokens:  200,
			OutputTokens: 80,
			TotalTokens:  280,
		},
	})
	logging := NewLoggingLLM(mock, rec)

	_, err = logging.Generate(context.Background(), GenerateRequest{
		Model: "test-model", Messages: []Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1)
	assert.Equal(t, 200, calls[0].InputTokens)
	assert.Equal(t, 80, calls[0].OutputTokens)
	assert.Equal(t, 280, calls[0].TotalTokens)
	assert.Equal(t, CallStatusCompleted, calls[0].Status)
}

func TestLoggingLLMRecordsErrorPath(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	mock := NewMockLLM(MockResponse{Err: fmt.Errorf("rate limited")})
	logging := NewLoggingLLM(mock, rec)

	_, err = logging.Generate(context.Background(), GenerateRequest{
		Model: "m", Messages: []Message{{Role: "user", Content: "x"}},
	})
	require.Error(t, err)

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1)
	assert.Equal(t, "rate limited", calls[0].Error)
}

// TestSessionRecorderSurvivesCrashMidCall is the load-bearing assertion
// for Phase 1's crash-safety property: when Begin fires but Finish never
// runs (process killed mid-call), the input messages must still be on
// disk under status: in_progress so an operator can debug "why did this
// call take forever?" without losing the prompt.
func TestSessionRecorderSurvivesCrashMidCall(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	// Begin a call but DO NOT call Finish. Simulates a SIGKILL mid-call.
	rec.Begin("proposer", "spec_architect", "",
		GenerateRequest{
			Model: "test-model",
			Messages: []Message{
				{Role: "system", Content: "elaborate this strategy"},
				{Role: "user", Content: "the prompt that hung"},
			},
		},
		time.Now(),
	)

	// Discard the recorder reference — the in-memory state is gone, the
	// way it would be after a process restart. The on-disk state is all
	// we have.
	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1, "in-progress call must be on disk even without Finish")
	assert.Equal(t, CallStatusInProgress, calls[0].Status)
	assert.Equal(t, "spec_architect", calls[0].AgentID)
	require.Len(t, calls[0].Messages, 2,
		"both input messages must be on disk so an operator can read the prompt that hung")
	assert.Equal(t, "the prompt that hung", calls[0].Messages[1].Content)
}

// TestSessionRecorderInFlightDoesNotGrow is the memory-bound assertion:
// after each Finish, the recorder's in-flight set returns to zero so
// completed calls' payloads become GC-eligible. Without this,
// a 100-call session would hoard 100 calls' worth of YAML in memory.
func TestSessionRecorderInFlightDoesNotGrow(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	for i := 0; i < 50; i++ {
		h := rec.Begin("proposer", "spec_architect", "",
			GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
			time.Now(),
		)
		assert.Equal(t, 1, rec.inFlightCount(),
			"in-flight count climbs to 1 during the call")
		h.Finish(&GenerateResponse{Content: "ok"}, nil)
		assert.Equal(t, 0, rec.inFlightCount(),
			"in-flight count must drop to 0 after each Finish")
	}

	calls := loadSessionCalls(t, fs, rec)
	assert.Len(t, calls, 50, "all 50 calls must be persisted to per-call files")
}

// TestSessionRecorderPerCallFileIsAtomic checks the bounded-write
// property: a Begin/Finish pair touches exactly one per-call file,
// leaving sibling per-call files byte-identical. This is the structural
// reason memory and disk I/O scale O(1) per call instead of O(N).
func TestSessionRecorderPerCallFileIsAtomic(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("proposer", "spec_architect", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "first"}}},
		&GenerateResponse{Content: "first reply"},
		nil, time.Now(), 0,
	)
	files1, err := fs.ListDir(path.Join(rec.Path(), CallsDirName))
	require.NoError(t, err)
	require.Len(t, files1, 1)
	firstSnapshot, err := fs.ReadFile(files1[0])
	require.NoError(t, err)

	rec.Record("critic", "architect_critic", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "second"}}},
		&GenerateResponse{Content: "second reply"},
		nil, time.Now(), 0,
	)
	files2, err := fs.ListDir(path.Join(rec.Path(), CallsDirName))
	require.NoError(t, err)
	require.Len(t, files2, 2)

	// First call's file is byte-identical — the second call's flush
	// must not touch any prior call's file. Without this property the
	// recorder would still be O(N) on every flush.
	firstUnchanged, err := fs.ReadFile(files1[0])
	require.NoError(t, err)
	assert.Equal(t, firstSnapshot, firstUnchanged,
		"flushing call N must not rewrite call N-1's file")
}

// TestSessionRecorderPersistsRoundsForToolUseCalls — when the
// GenerateResponse carries per-round captures (multi-round tool-use
// loop), they must land in the per-call YAML so an operator debugging
// "what did the model ask the tools to do" can read each round's
// emitted message instead of seeing only the final response. Single-
// round calls leave Rounds nil — the top-level fields suffice and a
// one-element slice would just duplicate.
func TestSessionRecorderPersistsRoundsForToolUseCalls(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("reconciler", "spec_reconciler", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		&GenerateResponse{
			Content: `{"actions":[]}`,
			Rounds: []GenerateRound{
				{Index: 1, Text: "", Message: `{"tool_request":{"name":"spec_list_manifest"}}`, OutputTokens: 50},
				{Index: 2, Text: `{"actions":[]}`, Message: `{"text":"actions=[]"}`, OutputTokens: 80},
			},
		},
		nil, time.Now(), 0,
	)

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1)
	require.Len(t, calls[0].Rounds, 2,
		"per-round captures must round-trip into the per-call YAML for tool-use traces")
	assert.Equal(t, 1, calls[0].Rounds[0].Index)
	assert.Contains(t, calls[0].Rounds[0].Message, "spec_list_manifest",
		"round 1 must carry the tool_request part — without this an operator can't see what tools the model invoked")
	assert.Equal(t, `{"actions":[]}`, calls[0].Rounds[1].Text,
		"round 2 carries the model's final text answer")
	assert.Equal(t, 80, calls[0].Rounds[1].OutputTokens,
		"per-round token counts must persist so cost-per-round is debuggable")
}

// TestSessionRecorderOmitsRoundsForSingleRoundCalls — keep traces tight
// for the common single-round case. The top-level Reasoning/Response/
// RawMessage fields already carry the data; emitting a one-entry
// Rounds slice would double the YAML for no information.
func TestSessionRecorderOmitsRoundsForSingleRoundCalls(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("proposer", "spec_architect", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		&GenerateResponse{
			Content: `{"ok":true}`,
			// No Rounds — single-round call (the GenKit Generate path
			// only sets Rounds when len > 1).
		},
		nil, time.Now(), 0,
	)

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 1)
	assert.Empty(t, calls[0].Rounds,
		"single-round calls must NOT emit a Rounds field; the top-level Response carries the same data")
}

// TestCallTagContextRoundTrip — the WithCallTag context helper
// behaves the same way WithRole / WithAgentID do.
func TestCallTagContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", CallTagFromContext(ctx))
	ctx = WithCallTag(ctx, "feat-dashboard")
	assert.Equal(t, "feat-dashboard", CallTagFromContext(ctx))
	ctx = WithCallTag(ctx, "feat-other")
	assert.Equal(t, "feat-other", CallTagFromContext(ctx),
		"WithCallTag should overwrite the prior tag on the chain")
}

// TestSessionRecorderAppendsCallTagToFilename — when callTag is set,
// the per-call filename gets `-<tag>` appended after the agent id so
// `ls .locutus/sessions/<sid>/calls/` reads as named nodes rather
// than indistinguishable per-agent siblings. The agent_id field
// inside the YAML stays as the bare agent name.
func TestSessionRecorderAppendsCallTagToFilename(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("planning", "spec_feature_elaborator", "feat-dashboard",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		&GenerateResponse{Content: `{"id":"feat-dashboard"}`},
		nil, time.Now(), 0,
	)
	rec.Record("planning", "spec_feature_elaborator", "feat-export",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		&GenerateResponse{Content: `{"id":"feat-export"}`},
		nil, time.Now(), 0,
	)

	files, err := fs.ListDir(path.Join(rec.Path(), CallsDirName))
	require.NoError(t, err)
	require.Len(t, files, 2)

	// Filename suffix carries the tag — tells siblings apart at a
	// glance without having to grep messages content.
	assert.Contains(t, files[0], "0001-spec_feature_elaborator-feat-dashboard")
	assert.Contains(t, files[1], "0002-spec_feature_elaborator-feat-export")

	// The recordedCall.AgentID stays as the bare agent name; the tag
	// is filename-only (it's already in the call's messages content).
	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 2)
	assert.Equal(t, "spec_feature_elaborator", calls[0].AgentID,
		"AgentID inside the YAML must NOT carry the per-call tag — that's filename-only metadata")
}

// TestSessionRecorderEmptyCallTagBehavesLikePriorVersion — the
// callTag parameter is optional. Empty produces the original
// `<NNNN>-<agent>.yaml` naming so non-fanout calls are unchanged.
func TestSessionRecorderEmptyCallTagBehavesLikePriorVersion(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("proposer", "spec_architect", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		&GenerateResponse{Content: `{"ok":true}`},
		nil, time.Now(), 0,
	)

	files, err := fs.ListDir(path.Join(rec.Path(), CallsDirName))
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Contains(t, files[0], "0001-spec_architect.yaml")
	assert.NotContains(t, files[0], "spec_architect-",
		"empty callTag must NOT produce a trailing dash on the filename")
}

// TestLoggingLLMReadsCallTagFromContext — production wiring: the
// LoggingLLM reads the tag from context (the workflow's fanout
// dispatcher sets it via WithCallTag). Without this, the per-call
// filename suffix never reaches the recorder.
func TestLoggingLLMReadsCallTagFromContext(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: `{"ok":true}`, Model: "m"},
	})
	logging := NewLoggingLLM(mock, rec)

	ctx := WithAgentID(context.Background(), "spec_feature_elaborator")
	ctx = WithCallTag(ctx, "feat-dashboard")
	_, err = logging.Generate(ctx, GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	require.NoError(t, err)

	files, err := fs.ListDir(path.Join(rec.Path(), CallsDirName))
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Contains(t, files[0], "spec_feature_elaborator-feat-dashboard",
		"LoggingLLM must thread the call-tag from ctx into the recorder so the filename carries the tag")
}

// TestSessionRecorderCloseStampsManifestAndInterrupted verifies the
// clean-shutdown path: Close stamps completed_at on the manifest and
// flips any still-in-flight calls to status: interrupted on disk so
// post-mortem tooling can tell "session ended cleanly" from "session
// ended cleanly but had stragglers" from "session crashed."
func TestSessionRecorderCloseStampsManifestAndInterrupted(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	// Two calls: one finished normally, one left in flight to test the
	// straggler path.
	h1 := rec.Begin("proposer", "spec_architect", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "a"}}},
		time.Now(),
	)
	h1.Finish(&GenerateResponse{Content: "ok"}, nil)

	rec.Begin("critic", "architect_critic", "",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "b"}}},
		time.Now(),
	)
	// Note: no Finish — Close should stamp this as interrupted.

	require.NoError(t, rec.Close())

	manifest := loadSessionManifest(t, fs, rec)
	assert.NotEmpty(t, manifest.CompletedAt,
		"completed_at must be stamped after Close so post-mortem can detect clean shutdown")

	calls := loadSessionCalls(t, fs, rec)
	require.Len(t, calls, 2)
	assert.Equal(t, CallStatusCompleted, calls[0].Status)
	assert.Equal(t, CallStatusInterrupted, calls[1].Status,
		"calls still in flight at Close must be flipped to interrupted on disk")
}
