package state

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubGetter is the minimal BodyGetter for tests: id → body bytes.
type stubGetter map[string][]byte

func (s stubGetter) BodyBytes(id string) ([]byte, bool) {
	b, ok := s[id]
	return b, ok
}

func TestComputeSpecHashes_IncludesAllCitations(t *testing.T) {
	approach := spec.Approach{
		ID:        "app-feat-auth",
		ParentID:  "feat-auth",
		Decisions: []string{"dec-auth-approach", "dec-jwt-library"},
		Advances:  []string{"goal-secure-by-default"},
		Respects:  []string{"agoal-fundraising"},
	}
	getter := stubGetter{
		"app-feat-auth":          []byte("approach body"),
		"feat-auth":              []byte("feature body"),
		"dec-auth-approach":      []byte("decision body"),
		"dec-jwt-library":        []byte("another decision"),
		"goal-secure-by-default": []byte("goal body"),
		"agoal-fundraising":      []byte("anti-goal body"),
	}
	hashes, err := ComputeSpecHashes(approach, getter)
	require.NoError(t, err)
	// All 6 ids present.
	assert.Contains(t, hashes, "app-feat-auth")
	assert.Contains(t, hashes, "feat-auth")
	assert.Contains(t, hashes, "dec-auth-approach")
	assert.Contains(t, hashes, "dec-jwt-library")
	assert.Contains(t, hashes, "goal-secure-by-default")
	assert.Contains(t, hashes, "agoal-fundraising")
	// Format is sha256:<hex>.
	for id, h := range hashes {
		require.Greater(t, len(h), 7, "id %s hash too short", id)
		assert.Equal(t, "sha256:", h[:7], "id %s hash missing sha256: prefix: %q", id, h)
	}
}

func TestComputeSpecHashes_OrderIndependent(t *testing.T) {
	a1 := spec.Approach{
		ID:        "app-x",
		ParentID:  "feat-x",
		Decisions: []string{"dec-a", "dec-b"},
	}
	a2 := spec.Approach{
		ID:        "app-x",
		ParentID:  "feat-x",
		Decisions: []string{"dec-b", "dec-a"}, // reordered
	}
	getter := stubGetter{
		"app-x":  []byte("body"),
		"feat-x": []byte("feat"),
		"dec-a":  []byte("a"),
		"dec-b":  []byte("b"),
	}
	h1, _ := ComputeSpecHashes(a1, getter)
	h2, _ := ComputeSpecHashes(a2, getter)
	assert.Equal(t, h1, h2, "decision reordering must not change the hash map")
}

func TestComputeSpecHashes_DedupesRepeatedIDs(t *testing.T) {
	approach := spec.Approach{
		ID:        "app-x",
		ParentID:  "feat-x",
		Decisions: []string{"dec-a", "dec-a"}, // duplicate
	}
	getter := stubGetter{
		"app-x":  []byte("body"),
		"feat-x": []byte("feat"),
		"dec-a":  []byte("a"),
	}
	h, err := ComputeSpecHashes(approach, getter)
	require.NoError(t, err)
	assert.Len(t, h, 3, "duplicates should not produce duplicate entries (map is naturally a set)")
}

func TestComputeSpecHashes_SkipsEmptyIDs(t *testing.T) {
	approach := spec.Approach{
		ID:        "app-x",
		ParentID:  "", // empty — should be skipped
		Decisions: []string{},
	}
	getter := stubGetter{
		"app-x": []byte("body"),
	}
	h, err := ComputeSpecHashes(approach, getter)
	require.NoError(t, err)
	assert.Len(t, h, 1, "empty ParentID should not be in the map")
	assert.Contains(t, h, "app-x")
}

func TestComputeSpecHashes_MissingIdReturnsError(t *testing.T) {
	approach := spec.Approach{ID: "app-x", ParentID: "feat-x"}
	getter := stubGetter{
		"app-x": []byte("body"),
		// feat-x missing
	}
	_, err := ComputeSpecHashes(approach, getter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "feat-x")
}
