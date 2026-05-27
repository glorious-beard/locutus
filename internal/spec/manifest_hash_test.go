// DJ-139 phase 4 — manifest goals_md_hash + goals_md_synced_at fields
// and the ComputeGoalsMdHash helper.
//
// These tests guard the backward-compat invariant that existing
// manifests (without the new fields) round-trip cleanly, the
// determinism of the hash function, and the canonical sha256:<hex>
// format the codebase already standardizes on (internal/spec/hash.go,
// internal/history/narrative.go).

package spec_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComputeGoalsMdHashDeterministic — same input bytes produce the
// same hash on every call. The hash is the short-circuit signal the
// Phase 6 playbook reads to decide whether a goal-layer sync is
// needed; non-determinism would defeat the optimization.
func TestComputeGoalsMdHashDeterministic(t *testing.T) {
	content := []byte("# Project\n\n## In Scope\n- Build a thing\n")
	h1 := spec.ComputeGoalsMdHash(content)
	h2 := spec.ComputeGoalsMdHash(content)
	assert.Equal(t, h1, h2, "same input bytes must produce identical hashes")

	// Format check: every hash must start with the stable sha256:
	// prefix so future hash-algo changes can extend without breaking
	// existing manifest values.
	assert.True(t, strings.HasPrefix(h1, "sha256:"), "hash must use the sha256: prefix; got %q", h1)
	// The hex tail is 64 characters for sha-256.
	assert.Len(t, strings.TrimPrefix(h1, "sha256:"), 64, "hex tail must be 64 chars for sha-256")
}

// TestComputeGoalsMdHashDifferentInputsProduceDifferentHashes — even
// one-byte changes in GOALS.md flip the hash, so the playbook reliably
// detects edits.
func TestComputeGoalsMdHashDifferentInputsProduceDifferentHashes(t *testing.T) {
	a := []byte("# Project\n\n## In Scope\n- Build a thing\n")
	b := []byte("# Project\n\n## In Scope\n- Build another thing\n")
	c := []byte("# Project\n\n## In Scope\n- Build a thing") // missing trailing newline
	empty := []byte{}

	hA := spec.ComputeGoalsMdHash(a)
	hB := spec.ComputeGoalsMdHash(b)
	hC := spec.ComputeGoalsMdHash(c)
	hEmpty := spec.ComputeGoalsMdHash(empty)

	assert.NotEqual(t, hA, hB, "different content must yield different hashes")
	assert.NotEqual(t, hA, hC, "trailing-newline difference must yield different hashes")
	assert.NotEqual(t, hA, hEmpty, "empty input must hash differently from populated input")

	// The empty input still produces a valid hash — the helper is
	// total over byte slices including nil/empty.
	assert.True(t, strings.HasPrefix(hEmpty, "sha256:"))
}

// TestManifestRoundTripsWithGoalsMdHashFields — a manifest with the
// new fields populated encodes, decodes, and produces equal values.
func TestManifestRoundTripsWithGoalsMdHashFields(t *testing.T) {
	synced := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	original := spec.Manifest{
		ProjectName:     "locutus",
		Version:         "0.1.0",
		CreatedAt:       time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
		GoalsMdHash:     "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		GoalsMdSyncedAt: synced,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded spec.Manifest
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original.ProjectName, decoded.ProjectName)
	assert.Equal(t, original.Version, decoded.Version)
	assert.True(t, decoded.CreatedAt.Equal(original.CreatedAt))
	assert.Equal(t, original.GoalsMdHash, decoded.GoalsMdHash)
	assert.True(t, decoded.GoalsMdSyncedAt.Equal(synced), "goals_md_synced_at must round-trip")
}

// TestManifestBackwardCompatWithoutGoalsMdHashFields — a manifest
// encoded by the pre-DJ-139 codebase (no goals_md_hash, no
// goals_md_synced_at) decodes cleanly into the new struct with zero
// values, and re-encoding it does not introduce empty-field noise on
// disk (omitempty discipline).
func TestManifestBackwardCompatWithoutGoalsMdHashFields(t *testing.T) {
	// Legacy JSON shape — no goals_md_hash or goals_md_synced_at keys.
	legacy := []byte(`{
  "project_name": "winplan",
  "version": "0.1.0",
  "created_at": "2026-04-01T09:00:00Z"
}`)

	var m spec.Manifest
	require.NoError(t, json.Unmarshal(legacy, &m))

	// The new fields decode as zero values.
	assert.Equal(t, "", m.GoalsMdHash, "missing goals_md_hash must decode as empty string")
	assert.True(t, m.GoalsMdSyncedAt.IsZero(), "missing goals_md_synced_at must decode as zero time")

	// Re-encode and verify the empty string field is omitted (omitempty
	// works for strings). time.Time's zero value isn't a Go-encoding
	// zero, so goals_md_synced_at will appear with the RFC3339 zero
	// time — that's fine: decoding it back produces IsZero() true,
	// matching the original semantics. The empty-hash omission is the
	// load-bearing guarantee here, because it gates the Phase 6
	// short-circuit comparison.
	out, err := json.Marshal(m)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "goals_md_hash", "empty goals_md_hash must be omitted")

	// Sanity: the kept fields survive.
	assert.Contains(t, string(out), "winplan")
	assert.Contains(t, string(out), "0.1.0")
}
