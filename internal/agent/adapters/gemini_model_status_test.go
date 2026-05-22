package adapters

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// withCapturedSlog redirects slog default output to a buffer for
// the duration of the test, restoring the prior handler afterward.
func withCapturedSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prior) })
	return buf
}

// resetGeminiModelStatusWarned clears the dedupe cache between
// subtests so each subtest gets a clean warning surface.
func resetGeminiModelStatusWarned(t *testing.T) {
	t.Helper()
	geminiModelStatusWarned = sync.Map{}
}

// TestNoteGeminiModelStatus_WarnsOnRetirement verifies that a
// non-zero RetirementTime triggers a slog.Warn carrying the model
// name, the formatted retirement time, and the time-until field
// operators read to decide migration urgency.
func TestNoteGeminiModelStatus_WarnsOnRetirement(t *testing.T) {
	resetGeminiModelStatusWarned(t)
	buf := withCapturedSlog(t)

	retire := time.Now().Add(60 * 24 * time.Hour).UTC()
	noteGeminiModelStatus("gemini-3.1-pro-preview", &genai.ModelStatus{
		RetirementTime: retire,
	})

	logged := buf.String()
	assert.Contains(t, logged, "level=WARN", "ModelStatus retirement notice must log at WARN level so operators see it without trace forensics")
	assert.Contains(t, logged, "model=gemini-3.1-pro-preview")
	assert.Contains(t, logged, "retirement_time="+retire.Format(time.RFC3339))
	assert.Contains(t, logged, "retires_in=", "retires_in is the human-readable countdown that drives migration urgency")
}

// TestNoteGeminiModelStatus_DedupesAcrossCalls locks in the
// dedupe-by-(model, retirement-time) policy. The council fires
// dozens of LLM calls per refine; an undeduped warning would log
// the same retirement notice on every call and bury the rest of
// the operator's log output.
func TestNoteGeminiModelStatus_DedupesAcrossCalls(t *testing.T) {
	resetGeminiModelStatusWarned(t)
	buf := withCapturedSlog(t)

	status := &genai.ModelStatus{RetirementTime: time.Now().Add(30 * 24 * time.Hour).UTC()}
	for range 5 {
		noteGeminiModelStatus("gemini-3.1-pro-preview", status)
	}

	count := strings.Count(buf.String(), "level=WARN")
	assert.Equal(t, 1, count, "the same (model, retirement-time) pair must produce exactly one WARN line across many calls")
}

// TestNoteGeminiModelStatus_NewRetirementTimeReopensWarning
// verifies that when Google updates the retirement time on a model
// (extension; bring-forward), the new (model, retirement-time) pair
// is treated as a fresh notice and gets its own warning. Without
// this, a deadline change would silently land in traces but operators
// wouldn't see the new deadline.
func TestNoteGeminiModelStatus_NewRetirementTimeReopensWarning(t *testing.T) {
	resetGeminiModelStatusWarned(t)
	buf := withCapturedSlog(t)

	first := time.Now().Add(30 * 24 * time.Hour).UTC()
	second := time.Now().Add(7 * 24 * time.Hour).UTC()

	noteGeminiModelStatus("gemini-3.1-pro-preview", &genai.ModelStatus{RetirementTime: first})
	noteGeminiModelStatus("gemini-3.1-pro-preview", &genai.ModelStatus{RetirementTime: second})

	count := strings.Count(buf.String(), "level=WARN")
	require.Equal(t, 2, count, "a new retirement time on the same model must produce a fresh warning")
	assert.Contains(t, buf.String(), first.Format(time.RFC3339))
	assert.Contains(t, buf.String(), second.Format(time.RFC3339))
}

// TestNoteGeminiModelStatus_NilStatusIsSilent verifies that a nil
// ModelStatus (the common case — every call without a deprecation
// notice) produces no log output. Without this guard the warning
// surface would fire on every call, defeating the deduper's purpose.
func TestNoteGeminiModelStatus_NilStatusIsSilent(t *testing.T) {
	resetGeminiModelStatusWarned(t)
	buf := withCapturedSlog(t)

	noteGeminiModelStatus("gemini-3.5-flash", nil)

	assert.Empty(t, buf.String(), "nil ModelStatus must not log")
}

// TestNoteGeminiModelStatus_EmptyStatusIsSilent verifies that a
// non-nil but empty ModelStatus (Google sending the field with no
// meaningful content) also produces no log output. Operators don't
// care about "model has no deprecation notice"; they care about
// "model has a deprecation notice."
func TestNoteGeminiModelStatus_EmptyStatusIsSilent(t *testing.T) {
	resetGeminiModelStatusWarned(t)
	buf := withCapturedSlog(t)

	noteGeminiModelStatus("gemini-3.5-flash", &genai.ModelStatus{})

	assert.Empty(t, buf.String(), "empty ModelStatus must not log")
}

// TestNoteGeminiModelStatus_MessageOnlyAlsoWarns covers the operator-
// notice path where Google ships a Message but no RetirementTime —
// e.g. capacity migrations or temporary platform notices. These are
// also worth surfacing to operators.
func TestNoteGeminiModelStatus_MessageOnlyAlsoWarns(t *testing.T) {
	resetGeminiModelStatusWarned(t)
	buf := withCapturedSlog(t)

	noteGeminiModelStatus("gemini-3.5-flash", &genai.ModelStatus{
		Message: "capacity is migrating to a new region; transient latency increase expected",
	})

	logged := buf.String()
	assert.Contains(t, logged, "level=WARN")
	assert.Contains(t, logged, "message=", "Message field must thread through to the log")
	assert.Contains(t, logged, "capacity is migrating")
}
