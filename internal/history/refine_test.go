package history_test

import (
	"testing"
	"time"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newHistFS(t *testing.T) (specio.FS, *history.Historian) {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	return fs, history.NewHistorian(fs, ".borg/history")
}

func TestRecordRefinedRoundTrip(t *testing.T) {
	_, h := newHistFS(t)

	require.NoError(t, history.RecordRefined(h, "feat-a", "OLD", "NEW", "consider X"))

	evt, err := history.LatestRefinedEvent(h, "feat-a")
	require.NoError(t, err)
	require.NotNil(t, evt)

	assert.Equal(t, history.EventKindRefined, evt.Kind)
	assert.Equal(t, "feat-a", evt.TargetID)
	assert.Equal(t, "OLD", evt.OldValue)
	assert.Equal(t, "NEW", evt.NewValue)
	assert.Equal(t, "consider X", evt.Rationale)
}

func TestLatestRefinedEventNoneFound(t *testing.T) {
	_, h := newHistFS(t)
	evt, err := history.LatestRefinedEvent(h, "feat-nope")
	require.NoError(t, err)
	assert.Nil(t, evt)
}

func TestLatestRefinedEventReturnsMostRecent(t *testing.T) {
	_, h := newHistFS(t)

	require.NoError(t, history.RecordRefined(h, "feat-a", "v1", "v2", "first"))
	time.Sleep(2 * time.Millisecond) // separate timestamps
	require.NoError(t, history.RecordRefined(h, "feat-a", "v2", "v3", "second"))

	evt, err := history.LatestRefinedEvent(h, "feat-a")
	require.NoError(t, err)
	require.NotNil(t, evt)
	assert.Equal(t, "v2", evt.OldValue)
	assert.Equal(t, "v3", evt.NewValue)
	assert.Equal(t, "second", evt.Rationale)
}

func TestLatestRefinedEventSkipsRolledBack(t *testing.T) {
	_, h := newHistFS(t)

	// refine #1 → rollback #1: this pair cancels out.
	require.NoError(t, history.RecordRefined(h, "feat-a", "v1", "v2", "r1"))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, history.RecordRolledBack(h, "feat-a", "v2", "v1", "evt-refined-feat-a-irrelevant"))
	time.Sleep(2 * time.Millisecond)

	// refine #2: this is the standing refine.
	require.NoError(t, history.RecordRefined(h, "feat-a", "v1", "v2-prime", "r2"))

	evt, err := history.LatestRefinedEvent(h, "feat-a")
	require.NoError(t, err)
	require.NotNil(t, evt)
	assert.Equal(t, "r2", evt.Rationale, "rollback cancels the most recent refine; r2 is now the latest standing event")
}

func TestLatestRefinedEventAllRolledBack(t *testing.T) {
	_, h := newHistFS(t)

	require.NoError(t, history.RecordRefined(h, "feat-a", "v1", "v2", "r1"))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, history.RecordRolledBack(h, "feat-a", "v2", "v1", "evt-refined-feat-a-irrelevant"))

	evt, err := history.LatestRefinedEvent(h, "feat-a")
	require.NoError(t, err)
	assert.Nil(t, evt, "every refine has been rolled back; nothing to undo")
}

func TestRecordRefinedDefaultsRationaleWhenBriefEmpty(t *testing.T) {
	_, h := newHistFS(t)
	require.NoError(t, history.RecordRefined(h, "feat-a", "OLD", "NEW", ""))

	evt, err := history.LatestRefinedEvent(h, "feat-a")
	require.NoError(t, err)
	require.NotNil(t, evt)
	assert.Equal(t, "refine without focused brief", evt.Rationale)
}
