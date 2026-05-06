package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefineFeatureRecordsAutoHistoryWithOldNew verifies the refine
// path emits a spec_refined event carrying both prior and new bytes.
// Feeds rollback and --diff downstream.
func TestRefineFeatureRecordsAutoHistoryWithOldNew(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, true, "Authenticate users with OAuth2.", "reflect dec-lang")

	_, err := RunRefineFeature(context.Background(), llm, fs, "feat-auth")
	require.NoError(t, err)

	hist := history.NewHistorian(fs, ".borg/history")
	evt, err := history.LatestRefinedEvent(hist, "feat-auth")
	require.NoError(t, err)
	require.NotNil(t, evt)
	assert.Equal(t, "feat-auth", evt.TargetID)
	assert.Equal(t, history.EventKindRefined, evt.Kind)
	assert.Contains(t, evt.OldValue, `"feat-auth"`, "old value carries the prior JSON")
	assert.Contains(t, evt.NewValue, "OAuth2", "new value carries the rewritten JSON")
}

// TestRefineThreadsBriefIntoRewriterPrompt asserts the user's --brief
// argument lands in the rewriter agent's user message under a
// "Refinement intent" header. The MockExecutor captures every call;
// the brief travels via cascade.WithBrief on the context.
func TestRefineThreadsBriefIntoRewriterPrompt(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, true, "Authenticate users with OAuth2.", "reflect dec-lang")

	ctx := cascade.WithBrief(context.Background(), "Consider passkeys as an alternative")
	_, err := RunRefineFeature(ctx, llm, fs, "feat-auth")
	require.NoError(t, err)

	calls := llm.Calls()
	require.Len(t, calls, 1)
	user := calls[0].Input.Messages[0].Content
	assert.Contains(t, user, "## Refinement intent")
	assert.Contains(t, user, "Consider passkeys as an alternative")
}

// TestRefineNoBriefOmitsIntentSection — a refine without --brief
// must not splice an empty Refinement intent header into the prompt.
func TestRefineNoBriefOmitsIntentSection(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, true, "Authenticate users with OAuth2.", "reflect dec-lang")

	_, err := RunRefineFeature(context.Background(), llm, fs, "feat-auth")
	require.NoError(t, err)

	calls := llm.Calls()
	require.Len(t, calls, 1)
	user := calls[0].Input.Messages[0].Content
	assert.NotContains(t, user, "## Refinement intent")
}

// TestDispatchRefineWithDiffPopulatesResult — when --diff is set and
// the refine actually changed the node, the result carries a unified
// diff against the prior version.
func TestDispatchRefineWithDiffPopulatesResult(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, true, "Authenticate users with OAuth2 instead of LDAP.", "reflect dec-lang")

	opts := RefineOptions{Diff: true}
	result, err := dispatchRefineWithOptions(context.Background(), llm, fs, "feat-auth", spec.KindFeature, opts, nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.Diff, "--diff must populate the result Diff field")

	// The diff contains the new prose and unified-diff markers.
	assert.Contains(t, result.Diff, "+++")
	assert.Contains(t, result.Diff, "OAuth2")
}

// TestDispatchRefineNoDiffWhenUnchanged — a no-op refine produces no
// diff, even with --diff set.
func TestDispatchRefineNoDiffWhenUnchanged(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, false, "no change", "already accurate")

	opts := RefineOptions{Diff: true}
	result, err := dispatchRefineWithOptions(context.Background(), llm, fs, "feat-auth", spec.KindFeature, opts, nil)
	require.NoError(t, err)
	assert.Empty(t, result.Diff)
}

// TestRollbackRestoresPriorBytes — refine then rollback returns the
// node's JSON to byte-identical pre-refine state.
func TestRollbackRestoresPriorBytes(t *testing.T) {
	fs := setupRefineFS(t)
	priorBytes, err := fs.ReadFile(".borg/spec/features/feat-auth.json")
	require.NoError(t, err)

	llm := scriptRewriter(t, true, "Authenticate users with OAuth2.", "reflect dec-lang")
	_, err = RunRefineFeature(context.Background(), llm, fs, "feat-auth")
	require.NoError(t, err)

	postBytes, err := fs.ReadFile(".borg/spec/features/feat-auth.json")
	require.NoError(t, err)
	require.NotEqual(t, string(priorBytes), string(postBytes), "refine must have changed the file")

	result, err := RunRollback(fs, "feat-auth")
	require.NoError(t, err)
	require.NotNil(t, result.Rollback)
	assert.True(t, result.Rollback.Restored)
	assert.NotEmpty(t, result.Rollback.SourceEventID)

	restored, err := fs.ReadFile(".borg/spec/features/feat-auth.json")
	require.NoError(t, err)
	assert.Equal(t, string(priorBytes), string(restored), "rollback restores byte-identical pre-refine state")
}

// TestRollbackNoEventReturnsCleanResult — rollback on a node with no
// refine history is not an error; the summary surfaces the no-op.
func TestRollbackNoEventReturnsCleanResult(t *testing.T) {
	fs := setupRefineFS(t)

	result, err := RunRollback(fs, "feat-auth")
	require.NoError(t, err)
	require.NotNil(t, result.Rollback)
	assert.False(t, result.Rollback.Restored)
	assert.Contains(t, result.Rollback.Note, "no spec_refined event found")
}

// TestRollbackTwiceWalksThroughHistory — refine A, refine B,
// rollback (returns to post-A), rollback (returns to pre-A). Each
// rollback restores the OldValue of the most-recent un-rolled-back
// refine.
func TestRollbackTwiceWalksThroughHistory(t *testing.T) {
	fs := setupRefineFS(t)
	pre, _ := fs.ReadFile(".borg/spec/features/feat-auth.json")

	llm1 := scriptRewriter(t, true, "Body after A.", "refine A")
	_, err := RunRefineFeature(context.Background(), llm1, fs, "feat-auth")
	require.NoError(t, err)
	postA, _ := fs.ReadFile(".borg/spec/features/feat-auth.json")
	require.NotEqual(t, string(pre), string(postA))

	llm2 := scriptRewriter(t, true, "Body after B.", "refine B")
	_, err = RunRefineFeature(context.Background(), llm2, fs, "feat-auth")
	require.NoError(t, err)
	postB, _ := fs.ReadFile(".borg/spec/features/feat-auth.json")
	require.NotEqual(t, string(postA), string(postB))

	// First rollback: B → A.
	r1, err := RunRollback(fs, "feat-auth")
	require.NoError(t, err)
	require.True(t, r1.Rollback.Restored)
	got, _ := fs.ReadFile(".borg/spec/features/feat-auth.json")
	assert.Equal(t, string(postA), string(got), "rollback after refine B restores post-A state")

	// Second rollback: A → pre.
	r2, err := RunRollback(fs, "feat-auth")
	require.NoError(t, err)
	require.True(t, r2.Rollback.Restored)
	got, _ = fs.ReadFile(".borg/spec/features/feat-auth.json")
	assert.Equal(t, string(pre), string(got), "second rollback restores pre-refine state")

	// Third rollback: nothing left to undo.
	r3, err := RunRollback(fs, "feat-auth")
	require.NoError(t, err)
	assert.False(t, r3.Rollback.Restored, "no remaining standing refines to undo")
}

// TestRefineCmdValidateMutualExclusion verifies the flag-combo guards.
func TestRefineCmdValidateMutualExclusion(t *testing.T) {
	cases := []struct {
		name string
		c    RefineCmd
		ok   bool
	}{
		{"plain refine", RefineCmd{ID: "feat-x"}, true},
		{"with brief", RefineCmd{ID: "feat-x", Brief: "x"}, true},
		{"with diff", RefineCmd{ID: "feat-x", Diff: true}, true},
		{"with rollback alone", RefineCmd{ID: "feat-x", Rollback: true}, true},
		{"rollback + brief rejected", RefineCmd{ID: "feat-x", Rollback: true, Brief: "x"}, false},
		{"rollback + diff rejected", RefineCmd{ID: "feat-x", Rollback: true, Diff: true}, false},
		{"rollback + dry-run rejected", RefineCmd{ID: "feat-x", Rollback: true, DryRun: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.validate()
			if tc.ok {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.True(t, strings.Contains(err.Error(), "mutually exclusive"))
			}
		})
	}
}

// helper assert specio.FS satisfies the read interface used in tests.
var _ specio.FS = (specio.FS)(nil)
