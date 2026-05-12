package search

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture builds a tempdir-rooted project on disk with .borg/manifest.json
// plus the per-kind spec subdirectories, returning the project root and
// an OSFS handle pointed at it. Tests drive the index against the
// real filesystem because Bluge's on-disk path is OS-bound.
func fixture(t *testing.T) (string, specio.FS) {
	t.Helper()
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".borg"), 0o755))
	manifest := map[string]any{
		"project_name": "test",
		"version":      "0.0.1",
		"created_at":   time.Now().UTC(),
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	must(t, os.WriteFile(filepath.Join(root, ".borg", "manifest.json"), data, 0o644))
	for _, kind := range specKinds {
		must(t, os.MkdirAll(filepath.Join(root, ".borg", "spec", kind), 0o755))
	}
	return root, specio.NewOSFS(root)
}

func must(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}

// writeDecision saves a Decision into the fixture's decisions/ directory.
func writeDecision(t *testing.T, fsys specio.FS, id, title, summary, rationale string) {
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

func writeFeature(t *testing.T, fsys specio.FS, id, title, summary, description string) {
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

// ----- Phase 1 tests -----

// Index build + persist + load round-trip with fingerprint validation.
// Two Open calls in sequence: the first builds, the second sees a
// matching fingerprint and skips the rebuild — verified indirectly by
// the fact that searches return the same results and the on-disk
// fingerprint file is non-empty.
func TestOpen_RoundTrip(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-postgres-with-pgvector",
		"Adopt Postgres with pgvector",
		"Use Postgres pgvector for the embedding store.",
		"We need similarity search; pgvector keeps it in the OLTP store we already operate.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, total, err := idx.Search("postgres", Options{})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, hits, 1)
	assert.Equal(t, "dec-postgres-with-pgvector", hits[0].ID)
	assert.Equal(t, string(spec.KindDecision), hits[0].Kind)

	fpPath := filepath.Join(root, IndexDir, fingerprintFile)
	fpBytes, err := os.ReadFile(fpPath)
	require.NoError(t, err)
	require.NotEmpty(t, fpBytes, "fingerprint file should be written")

	// Re-open — should hit the fast path and still find the same hit.
	idx2, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx2.Close() })

	hits2, _, err := idx2.Search("postgres", Options{})
	require.NoError(t, err)
	require.Len(t, hits2, 1)
	assert.Equal(t, "dec-postgres-with-pgvector", hits2[0].ID)
}

// Fingerprint mismatch triggers rebuild: corrupt the on-disk
// fingerprint file and verify the next Open notices and rebuilds
// (search continues to work against the current spec state).
func TestOpen_FingerprintMismatchTriggersRebuild(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-foo", "Foo", "A foo decision.", "Some rationale.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	_ = idx.Close()

	// Corrupt the fingerprint — anything that doesn't match the
	// freshly-computed value should force a rebuild.
	fpPath := filepath.Join(root, IndexDir, fingerprintFile)
	require.NoError(t, os.WriteFile(fpPath, []byte("0:corrupted"), 0o644))

	// Add a new node that wasn't in the prior index. If the rebuild
	// fires, the new node is searchable.
	writeDecision(t, fsys, "dec-bar", "Bar", "A bar decision.", "Other rationale.")

	idx2, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx2.Close() })

	hits, _, err := idx2.Search("bar", Options{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "dec-bar", hits[0].ID)
}

// Schema version mismatch triggers rebuild: write a fingerprint
// claiming a different schema version, verify Open rebuilds.
func TestOpen_SchemaVersionMismatchTriggersRebuild(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-x", "X", "An X decision.", "Why X.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	_ = idx.Close()

	fpPath := filepath.Join(root, IndexDir, fingerprintFile)
	// Write a syntactically valid fingerprint with a wrong schema
	// version: the format check is "<schema>:<hash>", any mismatch
	// flips to rebuild. We don't need the hash to match — the schema
	// prefix is enough.
	require.NoError(t, os.WriteFile(fpPath, []byte("999:deadbeef"), 0o644))

	writeDecision(t, fsys, "dec-y", "Y", "A Y decision.", "Why Y.")

	idx2, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx2.Close() })

	hits, _, err := idx2.Search("y", Options{Kind: string(spec.KindDecision)})
	require.NoError(t, err)
	// dec-x and dec-y both match "y" via id_tokens (single-character
	// tokens are dropped by the en stopword filter so the match comes
	// via the y in id_tokens). What matters: dec-y is at least present.
	var foundY bool
	for _, h := range hits {
		if h.ID == "dec-y" {
			foundY = true
		}
	}
	assert.True(t, foundY, "rebuild should have indexed dec-y")
}

// Stemming: a query for "authentication" should match a node whose
// summary uses "authenticate". The English analyzer's Porter stemmer
// folds both to the same stem.
func TestSearch_Stemming(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-adopt-workos",
		"Adopt WorkOS",
		"Authenticate users via WorkOS SSO.",
		"WorkOS gives us OIDC + directory sync in one vendor.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search("authentication", Options{})
	require.NoError(t, err)
	require.Len(t, hits, 1, "Porter stemmer should fold authentication ↔ authenticate")
	assert.Equal(t, "dec-adopt-workos", hits[0].ID)
}

// Phrase: a quoted phrase only matches when the tokens appear together
// in the same field, not when they are scattered.
func TestSearch_Phrase(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-rls",
		"Adopt row level security",
		"Enable Postgres row level security on every tenant-scoped table.",
		"RLS gives us defense-in-depth alongside the app-layer tenant filter.")
	writeDecision(t, fsys, "dec-scattered",
		"Mixed levels of security",
		"Row-by-row inspection of every level of the security model.",
		"Not actually RLS — the words just happen to co-occur in other order.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search(`"row level security"`, Options{})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, "dec-rls", hits[0].ID, "phrase query should rank the contiguous match first")
}

// Prefix: a trailing-* query matches via PrefixQuery on the indexed
// tokens. Verifies the slug-body tokens are searchable.
func TestSearch_Prefix(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-adopt-authentication-workos",
		"Adopt WorkOS for SSO",
		"Use WorkOS to authenticate operators.",
		"WorkOS centralises auth.")
	writeDecision(t, fsys, "dec-database",
		"Pick a database",
		"Use Postgres.",
		"Mature, well-known.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search("authen*", Options{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "dec-adopt-authentication-workos", hits[0].ID)
}

// Kind filter: Options.Kind restricts the result set to one kind even
// when other-kind nodes match the free-text scope.
func TestSearch_KindFilter(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-postgres", "Use Postgres", "Adopt Postgres for OLTP.", "Mature.")
	writeFeature(t, fsys, "feat-postgres-migrations", "Postgres migrations", "Run Postgres migrations on deploy.", "")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	all, _, err := idx.Search("postgres", Options{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	decsOnly, _, err := idx.Search("postgres", Options{Kind: string(spec.KindDecision)})
	require.NoError(t, err)
	require.Len(t, decsOnly, 1)
	assert.Equal(t, "dec-postgres", decsOnly[0].ID)
	assert.Equal(t, string(spec.KindDecision), decsOnly[0].Kind)

	featsOnly, _, err := idx.Search("postgres", Options{Kind: string(spec.KindFeature)})
	require.NoError(t, err)
	require.Len(t, featsOnly, 1)
	assert.Equal(t, "feat-postgres-migrations", featsOnly[0].ID)
}

// BM25 IDF: when one doc carries both query terms and the rest carry
// only the common term, the doc with both wins — the rare term's
// high IDF lifts it above the saturated common-term score on the
// neighbours. The heuristic scorer (sum of token weights, no IDF)
// couldn't tell these apart.
func TestSearch_BM25IDF(t *testing.T) {
	root, fsys := fixture(t)
	// "data" appears in many nodes — low IDF after a handful of repeats.
	writeDecision(t, fsys, "dec-data-a", "Data pipeline A", "Process data for use case A.", "Routine data plumbing.")
	writeDecision(t, fsys, "dec-data-b", "Data pipeline B", "Process data for use case B.", "Routine data plumbing.")
	writeDecision(t, fsys, "dec-data-c", "Data pipeline C", "Process data for use case C.", "Routine data plumbing.")
	writeDecision(t, fsys, "dec-data-d", "Data pipeline D", "Process data for use case D.", "Routine data plumbing.")
	// One doc with BOTH terms. "pgvector" is unique → high IDF.
	writeDecision(t, fsys, "dec-vectors", "Store data with pgvector",
		"Stash data embeddings via pgvector inside Postgres.",
		"pgvector keeps the vector store in our OLTP database.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search("data pgvector", Options{})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, "dec-vectors", hits[0].ID,
		"doc carrying both query terms should win over docs carrying only the saturated common term")
}

// Empty / malformed inputs are rejected with typed sentinels so
// callers can branch without string comparison.
func TestSearch_InputValidation(t *testing.T) {
	root, fsys := fixture(t)
	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	_, _, err = idx.Search("   ", Options{})
	assert.ErrorIs(t, err, ErrEmptyQuery)

	_, _, err = idx.Search("anything", Options{Kind: "not-a-kind"})
	assert.ErrorIs(t, err, ErrUnknownKind)
}

// Fingerprint determinism: same fsys state ⇒ same fingerprint string.
// Sanity check that the hash isn't accidentally non-deterministic.
func TestFingerprint_Deterministic(t *testing.T) {
	_, fsys := fixture(t)
	writeDecision(t, fsys, "dec-x", "X", "X summary.", "X rationale.")
	writeDecision(t, fsys, "dec-y", "Y", "Y summary.", "Y rationale.")

	first, err := computeFingerprint(fsys)
	require.NoError(t, err)
	second, err := computeFingerprint(fsys)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

// OpenWriterWithRetry: a second concurrent attempt to open the writer
// finds the lock busy, retries, and succeeds once the holder releases.
// Verifies the retry schedule and the lock-busy detection in one pass.
func TestOpenWriterWithRetry_RecoversAfterHolderReleases(t *testing.T) {
	root, fsys := fixture(t)

	// Build the index first so the directory exists.
	idx, err := Open(fsys, root)
	require.NoError(t, err)
	require.NoError(t, idx.Close())

	indexPath := filepath.Join(root, IndexDir)

	// Hold the writer for ~300ms — long enough that the second
	// attempt's first retry (0ms wait) misses, the 250ms attempt
	// likely misses, and the 500ms attempt lands cleanly.
	holder, err := OpenWriterWithRetry(indexPath)
	require.NoError(t, err)

	released := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(300 * time.Millisecond)
		require.NoError(t, holder.Close())
		close(released)
	}()

	second, err := OpenWriterWithRetry(indexPath)
	require.NoError(t, err, "second writer should succeed after retry")
	require.NoError(t, second.Close())

	<-released
	wg.Wait()
}

// OpenWriterWithRetry: when the retry budget exhausts, the error
// names the holder PID so the operator has something to act on.
func TestOpenWriterWithRetry_ExhaustionNamesPID(t *testing.T) {
	root, fsys := fixture(t)
	idx, err := Open(fsys, root)
	require.NoError(t, err)
	require.NoError(t, idx.Close())

	indexPath := filepath.Join(root, IndexDir)

	holder, err := OpenWriterWithRetry(indexPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() })

	// With the holder alive for the entire retry window (1.75s worst
	// case), the second attempt should give up and return the lock-
	// busy diagnostic. We confirm the message names the current PID
	// — the holder is this very test process.
	_, err = OpenWriterWithRetry(indexPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lock held by another locutus process")
	pid := os.Getpid()
	assert.Contains(t, err.Error(), strconv.Itoa(pid), "diagnostic should quote holder PID")
}
