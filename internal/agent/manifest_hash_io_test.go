// DJ-139 phase 4 — atomic read-modify-write helpers for
// .borg/manifest.json's goals_md_hash + goals_md_synced_at fields.
//
// The Phase 6 `refine goals` playbook calls these via the
// spec_update_goals_md_hash MCP tool at the end of a successful
// goal-layer sync. The helpers must preserve every other manifest
// field so the hash update never clobbers ProjectName, Version,
// CreatedAt, or any future top-level manifest field that lands here.

package agent_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedManifest writes a manifest with the standard fields populated so
// the atomicity test has something concrete to assert preservation
// against.
func seedManifest(t *testing.T, fsys specio.FS) spec.Manifest {
	t.Helper()
	m := spec.Manifest{
		ProjectName: "locutus",
		Version:     "0.1.0",
		Model:       "claude-sonnet-4-5",
		CreatedAt:   time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
	}
	data, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/manifest.json", data, 0o644))
	return m
}

// TestManifestHashAtomicReadModifyWrite — WriteManifestHash preserves
// every other manifest field on disk. The hash update path is
// read-modify-write so concurrent edits to ProjectName/Version/
// CreatedAt cannot be clobbered.
func TestManifestHashAtomicReadModifyWrite(t *testing.T) {
	fsys := specio.NewMemFS()
	original := seedManifest(t, fsys)

	hash := spec.ComputeGoalsMdHash([]byte("# Project\n\n## In Scope\n- A\n"))
	syncedAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	require.NoError(t, agent.WriteManifestHash(fsys, hash, syncedAt))

	// Read the manifest back from disk and assert every field.
	data, err := fsys.ReadFile(".borg/manifest.json")
	require.NoError(t, err)

	var got spec.Manifest
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, original.ProjectName, got.ProjectName, "WriteManifestHash must preserve project_name")
	assert.Equal(t, original.Version, got.Version, "WriteManifestHash must preserve version")
	assert.Equal(t, original.Model, got.Model, "WriteManifestHash must preserve model")
	assert.True(t, got.CreatedAt.Equal(original.CreatedAt), "WriteManifestHash must preserve created_at")
	assert.Equal(t, hash, got.GoalsMdHash, "hash must land in the manifest")
	assert.True(t, got.GoalsMdSyncedAt.Equal(syncedAt), "synced_at must land in the manifest")
}

// TestReadManifestHashReturnsEmptyOnLegacyManifest — a pre-DJ-139
// manifest with no goals_md_hash key reads back as empty string +
// zero time, not an error. The playbook treats "empty hash" as "no
// previous sync; do the full bootstrap."
func TestReadManifestHashReturnsEmptyOnLegacyManifest(t *testing.T) {
	fsys := specio.NewMemFS()
	seedManifest(t, fsys)

	hash, syncedAt, err := agent.ReadManifestHash(fsys)
	require.NoError(t, err)
	assert.Equal(t, "", hash, "legacy manifest reads as empty hash")
	assert.True(t, syncedAt.IsZero(), "legacy manifest reads as zero time")
}

// TestReadManifestHashRoundTripsAfterWrite — write then read returns
// the values we wrote. End-to-end check of the helper pair.
func TestReadManifestHashRoundTripsAfterWrite(t *testing.T) {
	fsys := specio.NewMemFS()
	seedManifest(t, fsys)

	hash := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	syncedAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	require.NoError(t, agent.WriteManifestHash(fsys, hash, syncedAt))

	gotHash, gotSyncedAt, err := agent.ReadManifestHash(fsys)
	require.NoError(t, err)
	assert.Equal(t, hash, gotHash)
	assert.True(t, gotSyncedAt.Equal(syncedAt))
}

// TestWriteManifestHashUpdatesExistingHash — writing a second hash
// over an existing one replaces the value without disturbing other
// fields.
func TestWriteManifestHashUpdatesExistingHash(t *testing.T) {
	fsys := specio.NewMemFS()
	original := seedManifest(t, fsys)

	first := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	second := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	syncedAt1 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	syncedAt2 := time.Date(2026, 5, 28, 13, 0, 0, 0, time.UTC)

	require.NoError(t, agent.WriteManifestHash(fsys, first, syncedAt1))
	require.NoError(t, agent.WriteManifestHash(fsys, second, syncedAt2))

	gotHash, gotSyncedAt, err := agent.ReadManifestHash(fsys)
	require.NoError(t, err)
	assert.Equal(t, second, gotHash, "second write must overwrite first")
	assert.True(t, gotSyncedAt.Equal(syncedAt2), "second write must update synced_at")

	// And the other fields still survive both writes.
	data, err := fsys.ReadFile(".borg/manifest.json")
	require.NoError(t, err)
	var m spec.Manifest
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, original.ProjectName, m.ProjectName)
	assert.Equal(t, original.Version, m.Version)
	assert.True(t, m.CreatedAt.Equal(original.CreatedAt))
}
