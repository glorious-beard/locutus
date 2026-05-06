package history_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const historyDir = ".borg/history"

// recordingFn is a GenerateFn implementation that serves scripted
// responses from a queue and captures every (system, user) prompt pair.
// Plays the same role agent.MockExecutor plays elsewhere, but decoupled from
// the agent package so history tests don't re-introduce the import
// cycle the narrative generator was built to avoid.
type recordingFn struct {
	mu        sync.Mutex
	responses []string
	pos       int
	calls     []recordedCall
}

type recordedCall struct {
	System string
	User   string
}

func newRecordingFn(responses ...string) *recordingFn {
	return &recordingFn{responses: responses}
}

// Fn returns a history.GenerateFn bound to this recorder. The caller
// (production code) supplies the agent's system prompt via the
// per-agent closure in cmd/history.go; tests don't simulate that layer,
// so the System field in recordedCall records the role label the test
// chose when building the recorder. Use FnWithSystem to tag calls.
func (r *recordingFn) Fn() history.GenerateFn {
	return r.FnWithSystem("test-agent")
}

// FnWithSystem tags every captured call with a label so tests that
// interleave archivist/analyst invocations can distinguish them.
func (r *recordingFn) FnWithSystem(systemLabel string) history.GenerateFn {
	return func(_ context.Context, user string) (string, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, recordedCall{System: systemLabel, User: user})
		if r.pos >= len(r.responses) {
			return "", assertExhausted(r)
		}
		out := r.responses[r.pos]
		r.pos++
		return out, nil
	}
}

func (r *recordingFn) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingFn) Calls() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// assertExhausted is the sentinel error returned when a test scripts too
// few responses — signals that the production code issued an extra call
// beyond what the scenario covered.
func assertExhausted(r *recordingFn) error {
	return &exhaustedErr{count: r.pos}
}

type exhaustedErr struct{ count int }

func (e *exhaustedErr) Error() string {
	return "recordingFn: no more scripted responses (consumed " + itoa(e.count) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

func seedHistorian(t *testing.T) (*history.Historian, specio.FS) {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(historyDir, 0o755))
	require.NoError(t, fs.MkdirAll(historyDir+"/details", 0o755))
	h := history.NewHistorian(fs, historyDir)

	ts := func(day int) time.Time {
		return time.Date(2026, 4, day, 10, 0, 0, 0, time.UTC)
	}
	require.NoError(t, h.Record(history.Event{
		ID: "evt-001", Timestamp: ts(15),
		Kind: "decision_created", TargetID: "dec-lang",
		NewValue: "Go", Rationale: "Team prior art + runtime fit.",
	}))
	require.NoError(t, h.Record(history.Event{
		ID: "evt-002", Timestamp: ts(18),
		Kind: "decision_updated", TargetID: "dec-lang",
		OldValue: "Go", NewValue: "Go 1.22", Rationale: "Pin minimum version.",
	}))
	require.NoError(t, h.Record(history.Event{
		ID: "evt-003", Timestamp: ts(20),
		Kind: "feature_added", TargetID: "feat-auth",
		NewValue: "OAuth login", Rationale: "Early customer ask.",
	}))
	return h, fs
}

const archivistBody = `# Project History

_Last updated: 2026-04-23_
_Based on 3 events, 2026-04-15 to 2026-04-20._

## Timeline

- 2026-04-15 — decision_created dec-lang: Go
- 2026-04-18 — decision_updated dec-lang: Go 1.22
- 2026-04-20 — feature_added feat-auth: OAuth login

## Targets with history

- [dec-lang](details/dec-lang.md) — 2 events
- [feat-auth](details/feat-auth.md) — 1 event
`

func analystBody(target string) string {
	return "# " + target + "\n\nThe " + target + " story evolved through a handful of\nchanges as the team worked out its language choice.\n"
}

func TestGenerateNarrativeWritesManifestAndDetails(t *testing.T) {
	h, fs := seedHistorian(t)
	fn := newRecordingFn(
		archivistBody,
		analystBody("dec-lang"),
		analystBody("feat-auth"),
	)

	report, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           fn.Fn(),
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, report)

	summaryData, err := fs.ReadFile(historyDir + "/summary.md")
	require.NoError(t, err)
	assert.Contains(t, string(summaryData), "Project History")

	decDetail, err := fs.ReadFile(historyDir + "/details/dec-lang.md")
	require.NoError(t, err)
	assert.Contains(t, string(decDetail), "dec-lang")

	featDetail, err := fs.ReadFile(historyDir + "/details/feat-auth.md")
	require.NoError(t, err)
	assert.Contains(t, string(featDetail), "feat-auth")

	assert.Equal(t, 1, report.ArchivistCalls)
	assert.Equal(t, 2, report.AnalystCalls)
	assert.False(t, report.Skipped)
}

// TestGenerateNarrativeDebouncesWhenUnchanged confirms unchanged event
// sets skip the LLM entirely on re-run.
func TestGenerateNarrativeDebouncesWhenUnchanged(t *testing.T) {
	h, _ := seedHistorian(t)
	first := newRecordingFn(
		archivistBody,
		analystBody("dec-lang"),
		analystBody("feat-auth"),
	)
	_, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           first.Fn(),
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)

	second := newRecordingFn() // no scripted responses — must not be called
	report, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           second.Fn(),
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)
	assert.True(t, report.Skipped, "unchanged events must skip regen")
	assert.Equal(t, 0, second.CallCount())
}

func TestGenerateNarrativeForceBypassesDebounce(t *testing.T) {
	h, _ := seedHistorian(t)
	first := newRecordingFn(
		archivistBody,
		analystBody("dec-lang"),
		analystBody("feat-auth"),
	)
	_, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           first.Fn(),
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)

	forced := newRecordingFn(
		archivistBody,
		analystBody("dec-lang"),
		analystBody("feat-auth"),
	)
	report, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           forced.Fn(),
		Force:              true,
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)
	assert.False(t, report.Skipped)
	assert.Greater(t, forced.CallCount(), 0)
}

// TestGenerateNarrativePerTargetDebounce confirms only targets whose
// events have changed get re-analysed; targets with unchanged event
// subsets are skipped even when other parts of the manifest changed.
func TestGenerateNarrativePerTargetDebounce(t *testing.T) {
	h, _ := seedHistorian(t)

	first := newRecordingFn(
		archivistBody,
		analystBody("dec-lang"),
		analystBody("feat-auth"),
	)
	_, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           first.Fn(),
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)

	// Add a new event for feat-auth only.
	require.NoError(t, h.Record(history.Event{
		ID: "evt-004", Timestamp: time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC),
		Kind: "feature_refined", TargetID: "feat-auth",
		Rationale: "Added password reset to OAuth scope.",
	}))

	second := newRecordingFn(
		archivistBody, // archivist must run — overall set changed
		analystBody("feat-auth"),
	)
	report, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           second.Fn(),
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, report.ArchivistCalls)
	assert.Equal(t, 1, report.AnalystCalls)
	assert.Contains(t, report.DetailsRegenerated, "feat-auth")
	assert.NotContains(t, report.DetailsRegenerated, "dec-lang")
}

func TestGenerateNarrativeSinceFilter(t *testing.T) {
	h, _ := seedHistorian(t)
	fn := newRecordingFn(
		archivistBody,
		analystBody("feat-auth"),
	)

	since := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	report, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           fn.Fn(),
		Since:              &since,
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)
	assert.NotContains(t, report.DetailsRegenerated, "dec-lang",
		"events before --since must be excluded from the analyst scope")
	assert.Contains(t, report.DetailsRegenerated, "feat-auth")
}

func TestGenerateNarrativeMinEventsThreshold(t *testing.T) {
	h, _ := seedHistorian(t)
	fn := newRecordingFn(
		archivistBody,
		analystBody("dec-lang"),
	)
	report, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           fn.Fn(),
		MinEventsForDetail: 2,
	})
	require.NoError(t, err)
	assert.Contains(t, report.DetailsRegenerated, "dec-lang")
	assert.NotContains(t, report.DetailsRegenerated, "feat-auth")
}

// TestGenerateNarrativeArchivistPromptIncludesEvents pins the prompt
// shape — events must reach the archivist callback so future refactors
// don't silently drop them. The system-prompt content lives in the
// agent def file (DJ-036) and is the cmd-layer's concern, not ours.
func TestGenerateNarrativeArchivistPromptIncludesEvents(t *testing.T) {
	h, _ := seedHistorian(t)
	fn := newRecordingFn(
		archivistBody,
		analystBody("dec-lang"),
		analystBody("feat-auth"),
	)
	_, err := h.GenerateNarrative(context.Background(), history.NarrativeConfig{
		Generate:           fn.Fn(),
		MinEventsForDetail: 1,
	})
	require.NoError(t, err)

	calls := fn.Calls()
	require.NotEmpty(t, calls)
	archPrompt := calls[0].User
	assert.Contains(t, archPrompt, "dec-lang")
	assert.Contains(t, archPrompt, "feat-auth")
	assert.Contains(t, archPrompt, "evt-001")
}
