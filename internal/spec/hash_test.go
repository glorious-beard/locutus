package spec_test

import (
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeSpecHashStable(t *testing.T) {
	a := spec.Approach{ID: "app-a", Title: "A", ParentID: "feat-x", Body: "body"}

	h1 := spec.ComputeSpecHash(a)
	h2 := spec.ComputeSpecHash(a)
	assert.Equal(t, h1, h2, "same inputs must produce same hash")
}

// TestComputeSpecHashReflectsBody verifies the central invariant from DJ-069:
// since Approaches are denormalized, an upstream Decision or parent change
// must manifest as a Body change after re-synthesis — and the hash follows.
func TestComputeSpecHashReflectsBody(t *testing.T) {
	a := spec.Approach{ID: "app-a", Body: "original synthesis"}
	h1 := spec.ComputeSpecHash(a)

	a.Body = "re-synthesized after decision revised"
	h2 := spec.ComputeSpecHash(a)

	assert.NotEqual(t, h1, h2, "Body change after re-synthesis must produce a different hash")
}

// TestComputeSpecHashIgnoresDecisionContent verifies the inverse invariant:
// per DJ-069, Decision contents are NOT directly hashed. A Decision can
// change in-place (rationale edited, confidence adjusted) without altering
// an Approach — and until cascade re-synthesizes the Approach Body, the
// hash must NOT change. A cascade bug is not drift.
func TestComputeSpecHashIgnoresDecisionContent(t *testing.T) {
	// Two Approaches identical in all fields; whatever a Decision node's
	// current contents are, they don't appear in this hash.
	a1 := spec.Approach{ID: "app-a", Body: "same", Decisions: []string{"dec-1"}}
	a2 := spec.Approach{ID: "app-a", Body: "same", Decisions: []string{"dec-1"}}

	assert.Equal(t, spec.ComputeSpecHash(a1), spec.ComputeSpecHash(a2))
}

func TestComputeSpecHashChangesOnDecisionSet(t *testing.T) {
	// The *set* of decisions consulted IS Approach-owned metadata; adding
	// or removing entries from Approach.Decisions changes the hash even if
	// Body is held constant. This lets the hash catch cases where synthesis
	// should have re-run against a new applicable set.
	a := spec.Approach{ID: "app-a", Body: "same", Decisions: []string{"a"}}
	b := spec.Approach{ID: "app-a", Body: "same", Decisions: []string{"a", "b"}}

	assert.NotEqual(t, spec.ComputeSpecHash(a), spec.ComputeSpecHash(b))
}

func TestComputeSpecHashDecisionOrderInsensitive(t *testing.T) {
	a := spec.Approach{ID: "app-a", Body: "same", Decisions: []string{"b", "a"}}
	aSwap := spec.Approach{ID: "app-a", Body: "same", Decisions: []string{"a", "b"}}

	assert.Equal(t, spec.ComputeSpecHash(a), spec.ComputeSpecHash(aSwap),
		"hash must be order-insensitive on Decisions audit list")
}

func TestComputeSpecHashChangesOnArtifactPaths(t *testing.T) {
	a := spec.Approach{ID: "app-a", Body: "same", ArtifactPaths: []string{"a.go"}}
	b := spec.Approach{ID: "app-a", Body: "same", ArtifactPaths: []string{"a.go", "b.go"}}

	assert.NotEqual(t, spec.ComputeSpecHash(a), spec.ComputeSpecHash(b))
}

func TestComputeArtifactHashes(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.WriteFile("a.go", []byte("hello"), 0o644))
	require.NoError(t, fs.WriteFile("b.go", []byte("world"), 0o644))

	a := spec.Approach{ArtifactPaths: []string{"a.go", "b.go", "missing.go"}}
	hashes := spec.ComputeArtifactHashes(fs.ReadFile, a)
	assert.NotEmpty(t, hashes["a.go"])
	assert.NotEmpty(t, hashes["b.go"])
	assert.Empty(t, hashes["missing.go"], "missing file must yield empty hash")
	assert.NotEqual(t, hashes["a.go"], hashes["b.go"])
}

func TestArtifactsEqual(t *testing.T) {
	a := map[string]string{"x": "1", "y": "2"}
	b := map[string]string{"x": "1", "y": "2"}
	c := map[string]string{"x": "1", "y": "3"}
	d := map[string]string{"x": "1"}

	assert.True(t, spec.ArtifactsEqual(a, b))
	assert.False(t, spec.ArtifactsEqual(a, c))
	assert.False(t, spec.ArtifactsEqual(a, d))
	assert.True(t, spec.ArtifactsEqual(nil, nil))
}
