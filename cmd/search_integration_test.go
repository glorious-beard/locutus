package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blugelabs/bluge"
	"github.com/glorious-beard/locutus/internal/search"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchHook_SavePairIndexesNewDecision exercises the full
// production hook end-to-end: install the search callback, write a
// new decision via specio.SavePair, and verify a fresh Search picks
// up the new node without a full rebuild.
func TestSearchHook_SavePairIndexesNewDecision(t *testing.T) {
	t.Cleanup(func() { specio.SetSpecWriteCallback(nil) })

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".borg"), 0o755))
	manifest := map[string]any{"project_name": "test", "version": "0.0.1", "created_at": time.Now().UTC()}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".borg", "manifest.json"), data, 0o644))
	for _, kind := range []string{"features", "strategies", "decisions", "bugs", "approaches"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".borg", "spec", kind), 0o755))
	}
	fsys := specio.NewOSFS(root)

	// Open the index against an empty project so the on-disk skeleton
	// (and a fingerprint matching the empty state) exists. Close
	// before installing the hook so the test process doesn't hold any
	// reader state across the upcoming writer acquisition.
	idx, err := search.Open(fsys, root)
	require.NoError(t, err)
	require.NoError(t, idx.Close())

	indexPath := filepath.Join(root, search.IndexDir)
	specio.SetSpecWriteCallback(makeSearchHook(fsys, indexPath))

	d := spec.Decision{
		ID:         "dec-hook-postgres",
		Title:      "Adopt Postgres",
		Summary:    "Use Postgres for OLTP.",
		Status:     spec.DecisionStatusActive,
		Confidence: 0.9,
		Rationale:  "Mature relational store.",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/decisions/dec-hook-postgres", d, ""))

	// Read the index directly via bluge — bypassing search.Open's
	// fingerprint-mismatch rebuild — so the test proves the hook's
	// writer actually committed (not just that a fresh rebuild from
	// disk would have caught up).
	reader, err := bluge.OpenReader(bluge.DefaultConfig(indexPath))
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })

	q := bluge.NewTermQuery("dec-hook-postgres").SetField("id")
	req := bluge.NewTopNSearch(10, q).WithStandardAggregations()
	it, err := reader.Search(context.Background(), req)
	require.NoError(t, err)

	match, err := it.Next()
	require.NoError(t, err)
	require.NotNil(t, match, "writer hook should have committed the new decision")
	var gotID string
	require.NoError(t, match.VisitStoredFields(func(field string, value []byte) bool {
		if field == "id" {
			gotID = string(value)
		}
		return true
	}))
	assert.Equal(t, "dec-hook-postgres", gotID)
}
