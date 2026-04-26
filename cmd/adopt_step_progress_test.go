package cmd

import (
	"context"
	"testing"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/workstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedActiveWorkstream writes a fresh ActiveWorkstream with the given
// step IDs and returns a wsStore the handler can read/write through.
func seedActiveWorkstream(t *testing.T, planID, wsID string, stepIDs ...string) *workstream.FileStore {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".locutus/workstreams/"+planID, 0o755))
	store := workstream.NewFileStore(fs, ".locutus/workstreams", planID)

	steps := make([]spec.PlanStep, len(stepIDs))
	for i, id := range stepIDs {
		steps[i] = spec.PlanStep{ID: id, Order: i + 1}
	}
	require.NoError(t, store.Save(workstream.ActiveWorkstream{
		WorkstreamID: wsID,
		PlanID:       planID,
		Plan:         spec.Workstream{ID: wsID, Steps: steps},
	}))
	return store
}

// TestPersistStepProgressMarksCompleteAndStampsSession verifies the
// per-step handler updates the on-disk record between steps so a
// SIGKILL after step N leaves StepStatus[N]=complete on disk and the
// next adopt resumes at N+1 (DJ-073).
func TestPersistStepProgressMarksCompleteAndStampsSession(t *testing.T) {
	store := seedActiveWorkstream(t, "plan-x", "ws-a", "s1", "s2", "s3")
	handler := persistStepProgress(store)

	handler(context.Background(), dispatch.StepEvent{
		WorkstreamID: "ws-a",
		StepID:       "s1",
		SessionID:    "sess-1",
		Success:      true,
	})

	rec, err := store.Load("ws-a")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", rec.AgentSessionID, "session ID should be persisted")
	assert.Equal(t, workstream.StepComplete, rec.StepByID("s1").Status)
	assert.Equal(t, workstream.StepPending, rec.StepByID("s2").Status, "untouched steps stay pending")
	assert.Equal(t, workstream.StepPending, rec.StepByID("s3").Status)
	assert.NotNil(t, rec.StepByID("s1").EndedAt, "completion timestamp should be stamped")
}

// TestPersistStepProgressMarksFailedWithMessage verifies failed steps
// land on disk with their failure reason so the resume classifier can
// surface what went wrong instead of just retrying blindly.
func TestPersistStepProgressMarksFailedWithMessage(t *testing.T) {
	store := seedActiveWorkstream(t, "plan-x", "ws-a", "s1", "s2")
	handler := persistStepProgress(store)

	handler(context.Background(), dispatch.StepEvent{
		WorkstreamID: "ws-a",
		StepID:       "s1",
		SessionID:    "sess-1",
		Success:      true,
	})
	handler(context.Background(), dispatch.StepEvent{
		WorkstreamID: "ws-a",
		StepID:       "s2",
		SessionID:    "sess-1",
		Success:      false,
		Message:      "validation failed: missing import",
	})

	rec, err := store.Load("ws-a")
	require.NoError(t, err)
	assert.Equal(t, workstream.StepComplete, rec.StepByID("s1").Status)
	s2 := rec.StepByID("s2")
	assert.Equal(t, workstream.StepFailed, s2.Status)
	assert.Equal(t, "validation failed: missing import", s2.Message)
}

// TestPersistStepProgressIsResilientToMissingRecord verifies the
// handler logs and continues when the workstream record is gone (e.g.,
// the plan was invalidated and the dir deleted concurrently). Per
// DJ-073 the per-step persistence is best-effort — the git feature
// branch is the durable source of truth, not the workstream record.
func TestPersistStepProgressIsResilientToMissingRecord(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".locutus/workstreams/plan-x", 0o755))
	store := workstream.NewFileStore(fs, ".locutus/workstreams", "plan-x")
	handler := persistStepProgress(store)

	// No record was saved — handler should not panic.
	assert.NotPanics(t, func() {
		handler(context.Background(), dispatch.StepEvent{
			WorkstreamID: "ghost-ws",
			StepID:       "s1",
			Success:      true,
		})
	})
}
