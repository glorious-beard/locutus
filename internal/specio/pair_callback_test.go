package specio_test

import (
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captured records each callback invocation so tests can assert on the
// (kind, id, deleted) tuple after a write.
type capturedCall struct {
	Kind    string
	ID      string
	Deleted bool
}

func capturingCallback(out *[]capturedCall) specio.SpecWriteCallback {
	return func(kind, id string, deleted bool) {
		*out = append(*out, capturedCall{Kind: kind, ID: id, Deleted: deleted})
	}
}

// resetCallback wires t.Cleanup so a test's installed callback doesn't
// bleed into siblings. Tests MUST defer this immediately after setting.
func resetCallback(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { specio.SetSpecWriteCallback(nil) })
}

func TestSavePair_FiresCallback(t *testing.T) {
	resetCallback(t)
	var calls []capturedCall
	specio.SetSpecWriteCallback(capturingCallback(&calls))

	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	d := spec.Decision{ID: "dec-foo", Title: "Foo", Status: spec.DecisionStatusActive, Confidence: 0.8}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-foo", d, "body"))

	require.Len(t, calls, 1)
	assert.Equal(t, capturedCall{Kind: "decision", ID: "dec-foo", Deleted: false}, calls[0])
}

func TestSaveMarkdown_FiresCallback(t *testing.T) {
	resetCallback(t)
	var calls []capturedCall
	specio.SetSpecWriteCallback(capturingCallback(&calls))

	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))

	a := spec.Approach{ID: "app-oauth", Title: "OAuth"}
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-oauth.md", a, "## body\n"))

	require.Len(t, calls, 1)
	assert.Equal(t, capturedCall{Kind: "approach", ID: "app-oauth", Deleted: false}, calls[0])
}

func TestRemovePair_FiresCallbackAndDeletesBoth(t *testing.T) {
	resetCallback(t)
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	d := spec.Decision{ID: "dec-gone", Title: "Gone", Status: spec.DecisionStatusActive, Confidence: 0.8}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-gone", d, "body"))

	var calls []capturedCall
	specio.SetSpecWriteCallback(capturingCallback(&calls))

	require.NoError(t, specio.RemovePair(fs, ".borg/spec/decisions/dec-gone"))

	require.Len(t, calls, 1)
	assert.Equal(t, capturedCall{Kind: "decision", ID: "dec-gone", Deleted: true}, calls[0])

	_, err := fs.ReadFile(".borg/spec/decisions/dec-gone.json")
	assert.Error(t, err, ".json should be gone")
	_, err = fs.ReadFile(".borg/spec/decisions/dec-gone.md")
	assert.Error(t, err, ".md should be gone")
}

func TestSavePair_NilCallback(t *testing.T) {
	resetCallback(t)
	specio.SetSpecWriteCallback(nil)

	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	f := spec.Feature{ID: "feat-x", Title: "X", Status: spec.FeatureStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-x", f, "body"))
}

func TestRemovePair_MissingFilesNotFatal(t *testing.T) {
	resetCallback(t)
	var calls []capturedCall
	specio.SetSpecWriteCallback(capturingCallback(&calls))

	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	require.NoError(t, specio.RemovePair(fs, ".borg/spec/decisions/dec-absent"))
	require.Len(t, calls, 1)
	assert.True(t, calls[0].Deleted)
}
