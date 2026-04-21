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
	d := spec.Decision{ID: "dec-1", Title: "D1", Status: spec.DecisionStatusActive, Confidence: 0.9}

	h1 := spec.ComputeSpecHash(a, []spec.Decision{d})
	h2 := spec.ComputeSpecHash(a, []spec.Decision{d})
	assert.Equal(t, h1, h2, "same inputs must produce same hash")
}

func TestComputeSpecHashChangesOnBody(t *testing.T) {
	a := spec.Approach{ID: "app-a", Body: "original"}
	h1 := spec.ComputeSpecHash(a, nil)

	a.Body = "revised"
	h2 := spec.ComputeSpecHash(a, nil)

	assert.NotEqual(t, h1, h2, "body change must produce a different hash")
}

func TestComputeSpecHashChangesOnDecision(t *testing.T) {
	a := spec.Approach{ID: "app-a", Body: "same"}
	d1 := spec.Decision{ID: "dec", Title: "First", Status: spec.DecisionStatusActive}
	d2 := spec.Decision{ID: "dec", Title: "Revised", Status: spec.DecisionStatusActive}

	h1 := spec.ComputeSpecHash(a, []spec.Decision{d1})
	h2 := spec.ComputeSpecHash(a, []spec.Decision{d2})
	assert.NotEqual(t, h1, h2)
}

func TestComputeSpecHashOrderInsensitive(t *testing.T) {
	a := spec.Approach{ID: "app-a", Body: "same", Decisions: []string{"b", "a"}}
	aSwap := spec.Approach{ID: "app-a", Body: "same", Decisions: []string{"a", "b"}}
	d1 := spec.Decision{ID: "a", Title: "A"}
	d2 := spec.Decision{ID: "b", Title: "B"}

	h1 := spec.ComputeSpecHash(a, []spec.Decision{d1, d2})
	h2 := spec.ComputeSpecHash(aSwap, []spec.Decision{d2, d1})
	assert.Equal(t, h1, h2, "hash must be order-insensitive on decisions")
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
