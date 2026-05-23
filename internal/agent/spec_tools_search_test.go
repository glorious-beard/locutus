package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/search"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openSearchBackend opens an on-disk *search.Index against the fixture
// root, registering Close as a test cleanup. Used by the validation /
// end-to-end tests that need the real on-disk path; the stub-backend
// dispatch tests below construct a fakeBackend directly instead.
func openSearchBackend(t *testing.T, fsys specio.FS, root string) search.Backend {
	t.Helper()
	idx, err := search.Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

// specSearchFixture mirrors the fixture helper in internal/search/search_test.go:
// a tempdir-rooted project on disk with .borg/manifest.json plus per-kind spec
// subdirectories. Bluge requires the OS path, so we cannot drive this test
// through MemFS.
func specSearchFixture(t *testing.T) (string, specio.FS) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".borg"), 0o755))
	manifest := map[string]any{
		"project_name": "test",
		"version":      "0.0.1",
		"created_at":   time.Now().UTC(),
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".borg", "manifest.json"), data, 0o644))
	for _, kind := range []string{"features", "strategies", "decisions", "bugs", "approaches"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".borg", "spec", kind), 0o755))
	}
	return root, specio.NewOSFS(root)
}

func writeSearchDecision(t *testing.T, fsys specio.FS, id, title, summary, rationale string) {
	t.Helper()
	d := spec.Decision{
		ID:         id,
		Title:      title,
		Summary:    summary,
		Status:     spec.DecisionStatusActive,
		Confidence: 0.8,
		Rationale:  rationale,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/decisions/"+id, d, ""))
}

func writeSearchFeature(t *testing.T, fsys specio.FS, id, title, summary, description string) {
	t.Helper()
	f := spec.Feature{
		ID:          id,
		Title:       title,
		Summary:     summary,
		Status:      spec.FeatureStatusProposed,
		Description: description,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/features/"+id, f, ""))
}

func TestSearchSpecNodes_RejectsEmptyQuery(t *testing.T) {
	root, fsys := specSearchFixture(t)
	backend := openSearchBackend(t, fsys, root)

	for _, q := range []string{"", "   ", "\t\n"} {
		_, err := SearchSpecNodes(fsys, backend, SpecSearchInput{Query: q})
		require.Error(t, err, "query %q must be rejected", q)
		assert.Contains(t, err.Error(), "empty query")
	}
}

func TestSearchSpecNodes_RejectsUnknownKind(t *testing.T) {
	root, fsys := specSearchFixture(t)
	backend := openSearchBackend(t, fsys, root)

	_, err := SearchSpecNodes(fsys, backend, SpecSearchInput{Query: "anything", Kind: "not-a-kind"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kind")
	assert.Contains(t, err.Error(), "not-a-kind")
}

// TestSearchSpecNodes_LimitClamping verifies the agent-side defaults and
// caps. 0 → 20 (smaller than the CLI's 100 because agents prefer compact
// context); >100 → 100; negative → default.
func TestSearchSpecNodes_LimitClamping(t *testing.T) {
	root, fsys := specSearchFixture(t)
	// Plant 40 decisions sharing the token "common" so the result set is
	// big enough to observe each clamp regime.
	for i := 0; i < 40; i++ {
		id := "dec-common-node-" + string(rune('a'+(i/10))) + string(rune('0'+(i%10)))
		writeSearchDecision(t, fsys, id, "Common subject", "Common authored summary.", "Long rationale referencing common subject.")
	}
	backend := openSearchBackend(t, fsys, root)

	// Limit 0 → default 20.
	res, err := SearchSpecNodes(fsys, backend, SpecSearchInput{Query: "common", Limit: 0})
	require.NoError(t, err)
	assert.Len(t, res.Hits, 20, "limit=0 must clamp to the agent-surface default (20)")
	assert.Equal(t, 40, res.TotalMatches, "TotalMatches reflects the full match set, not the slice")

	// Limit 200 → clamp to 100.
	res, err = SearchSpecNodes(fsys, backend, SpecSearchInput{Query: "common", Limit: 200})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(res.Hits), 100, "limit>max must clamp to 100")

	// Negative limit → default.
	res, err = SearchSpecNodes(fsys, backend, SpecSearchInput{Query: "common", Limit: -1})
	require.NoError(t, err)
	assert.Len(t, res.Hits, 20, "negative limit must fall back to the default")
}

// TestSearchSpecNodes_EndToEnd plants 3 decisions, queries, and asserts the
// expected hit ranks first plus the output shape.
func TestSearchSpecNodes_EndToEnd(t *testing.T) {
	root, fsys := specSearchFixture(t)
	writeSearchDecision(t, fsys, "dec-postgres-with-pgvector",
		"Adopt Postgres with pgvector",
		"Use Postgres pgvector for the embedding store.",
		"Similarity search inside the OLTP store.")
	writeSearchDecision(t, fsys, "dec-redis-cache",
		"Adopt Redis cache",
		"Cache hot query results in Redis.",
		"Reduce read latency on the dashboard.")
	writeSearchDecision(t, fsys, "dec-rls",
		"Adopt row level security",
		"Enable Postgres RLS on tenant tables.",
		"Defense in depth.")
	backend := openSearchBackend(t, fsys, root)

	res, err := SearchSpecNodes(fsys, backend, SpecSearchInput{Query: "pgvector"})
	require.NoError(t, err)
	require.NotEmpty(t, res.Hits)
	assert.Equal(t, "dec-postgres-with-pgvector", res.Hits[0].ID)
	assert.Greater(t, res.TotalMatches, 0)

	// Output shape: each hit carries id, title, kind, summary.
	top := res.Hits[0]
	assert.Equal(t, "Adopt Postgres with pgvector", top.Title)
	assert.Equal(t, string(spec.KindDecision), top.Kind)
	assert.Equal(t, "Use Postgres pgvector for the embedding store.", top.Summary,
		"per-hit Summary must mirror the authored Summary field")
}

// TestSearchSpecNodes_KindFilter scopes the same fixture to a single kind.
func TestSearchSpecNodes_KindFilter(t *testing.T) {
	root, fsys := specSearchFixture(t)
	writeSearchDecision(t, fsys, "dec-postgres", "Use Postgres",
		"Adopt Postgres for OLTP.", "Mature, well-known.")
	writeSearchFeature(t, fsys, "feat-postgres-migrations", "Postgres migrations",
		"Run Postgres migrations on deploy.", "Migrate schema on each deploy.")
	backend := openSearchBackend(t, fsys, root)

	// Unscoped: both nodes match.
	all, err := SearchSpecNodes(fsys, backend, SpecSearchInput{Query: "postgres"})
	require.NoError(t, err)
	require.Len(t, all.Hits, 2)

	// Scoped to decisions only.
	scoped, err := SearchSpecNodes(fsys, backend, SpecSearchInput{Query: "postgres", Kind: string(spec.KindDecision)})
	require.NoError(t, err)
	require.Len(t, scoped.Hits, 1)
	assert.Equal(t, "dec-postgres", scoped.Hits[0].ID)
	assert.Equal(t, string(spec.KindDecision), scoped.Hits[0].Kind)
}

// TestRegisterSpecTools_RegistersAllThree confirms that registering
// against a SpecStore exposes spec_list_manifest, spec_get, and
// spec_search in the registry. Under DJ-134 the store is mandatory;
// no nil-backend variant exists.
func TestRegisterSpecTools_RegistersAllThree(t *testing.T) {
	_, fsys := specSearchFixture(t)
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)
	registry := NewToolRegistry()
	RegisterSpecTools(registry, store)

	for _, name := range []string{ToolNameSpecListManifest, ToolNameSpecGet, ToolNameSpecSearch} {
		_, ok := registry.Resolve(name)
		assert.True(t, ok, "registry should expose %s", name)
	}
}

// TestRegisterSpecTools_NilStoreIsNoop confirms that calling
// RegisterSpecTools with a nil store leaves the registry empty —
// defensive: a caller that constructed the store-less harness
// shouldn't get a registry with non-functional tools.
func TestRegisterSpecTools_NilStoreIsNoop(t *testing.T) {
	registry := NewToolRegistry()
	RegisterSpecTools(registry, nil)

	_, ok := registry.Resolve(ToolNameSpecListManifest)
	assert.False(t, ok, "nil store: no tools should register")
	_, ok = registry.Resolve(ToolNameSpecGet)
	assert.False(t, ok)
	_, ok = registry.Resolve(ToolNameSpecSearch)
	assert.False(t, ok)
}
