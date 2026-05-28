package search

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/blugelabs/bluge"
	enanalyzer "github.com/blugelabs/bluge/analysis/lang/en"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
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
	if runtime.GOOS == "windows" {
		// The PID-quoting diagnostic reads bluge.pid, which Bluge
		// writes only on Unix (where it uses flock + companion PID
		// file). On Windows, Bluge uses native exclusive directory
		// access — no companion PID file is written — so our
		// readHolderPID returns 0 and the diagnostic falls back to
		// "PID unknown". The fallback message is still useful, just
		// without the holder PID; making the Windows diagnostic
		// quote the PID would mean writing our own bluge.pid after
		// every successful writer open (real engineering scope
		// beyond keeping CI green). Until then, the assertion that
		// the PID appears in the message doesn't hold on Windows.
		t.Skip("bluge.pid is a Unix-only Bluge convention; the diagnostic falls back to 'PID unknown' on Windows")
	}
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

// ----- Phase 7 tests: explainable ranking -----

// writeDecisionWithAlternatives saves a Decision carrying explicit
// Alternatives so Phase 7 tests can exercise the per-field signal
// split — matches in alt.RejectedBecause land in fieldAlternative,
// not fieldRationale.
func writeDecisionWithAlternatives(t *testing.T, fsys specio.FS, id, title, summary, rationale string, alts []spec.Alternative) {
	t.Helper()
	d := spec.Decision{
		ID:           id,
		Title:        title,
		Summary:      summary,
		Status:       spec.DecisionStatusActive,
		Confidence:   0.8,
		Rationale:    rationale,
		Alternatives: alts,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/decisions/"+id, d, ""))
}

// Bluge-invariant lockdown: a single-field MatchQuery and the same
// field used as one disjunction clause in a multi-field BooleanQuery
// must yield identical per-doc Score values for the matched
// document. That equality is what makes our per-field scan approach
// faithful — the per-field score IS the contribution to the
// disjunction's total, not an approximation. If a future Bluge
// upgrade introduces cross-clause normalization in
// CompositeSumScorer, this test fails red and we re-evaluate the
// diagnostic approach.
func TestBluge_PerFieldScoreEqualsDisjunctionContribution(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-workos",
		"Adopt WorkOS",
		"Use WorkOS for SSO.",
		"WorkOS bundles OIDC and directory sync.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	r, err := idx.reader()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	// Single-field title-only query, English analyzer to match the
	// way the title was indexed.
	an := enanalyzer.NewAnalyzer()
	soloQ := bluge.NewMatchQuery("workos").
		SetField(fieldTitle).
		SetAnalyzer(an).
		SetBoost(boostTitle)
	soloIt, err := r.Search(context.Background(), bluge.NewTopNSearch(10, soloQ))
	require.NoError(t, err)
	soloMatch, err := soloIt.Next()
	require.NoError(t, err)
	require.NotNil(t, soloMatch)
	soloScore := soloMatch.Score

	// Multi-field disjunction with title boost AND a no-op clause on
	// a field that the doc doesn't match (description). Disjunction
	// should drop the non-matching clause; the surviving title
	// contribution must equal soloScore.
	disjQ := bluge.NewBooleanQuery()
	disjQ.AddShould(bluge.NewMatchQuery("workos").SetField(fieldTitle).SetAnalyzer(an).SetBoost(boostTitle))
	disjQ.AddShould(bluge.NewMatchQuery("zzznotpresent").SetField(fieldDescription).SetAnalyzer(an).SetBoost(boostBody))
	disjQ.SetMinShould(1)
	disjIt, err := r.Search(context.Background(), bluge.NewTopNSearch(10, disjQ))
	require.NoError(t, err)
	disjMatch, err := disjIt.Next()
	require.NoError(t, err)
	require.NotNil(t, disjMatch)

	assert.InDelta(t, soloScore, disjMatch.Score, 1e-6,
		"CompositeSumScorer is supposed to be a literal sum — a disjunction with one matching clause should produce that clause's score exactly. If this fails, Bluge introduced cross-clause normalization and our per-field scan approach needs re-thinking (see scanPerFieldContributions in index.go).")
}

// When Explain is off, Matches must be nil — callers that don't ask
// for diagnostics shouldn't pay the parsing cost or carry the data.
func TestSearch_MatchesNilWhenExplainOff(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-foo", "Foo decision", "A foo decision.", "Some rationale about foo.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search("foo", Options{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Nil(t, hits[0].Matches, "Matches should be nil when Explain is off")
}

// When Explain is on, Matches is populated with the fields that
// contributed to the score. The contributions should sum to roughly
// the total Score (within float rounding).
func TestSearch_MatchesContributionSum(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-foo",
		"Foo decision",
		"Foo is the right approach.",
		"Foo wins because of foo properties; we considered bar but foo prevails.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search("foo", Options{Explain: true})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.NotNil(t, hits[0].Matches)

	var sum float64
	for _, m := range hits[0].Matches {
		sum += m.Contribution
	}
	assert.InDelta(t, hits[0].Score, sum, 1e-6,
		"sum of per-field contributions must equal the total Score")
}

// Per-field separation: a doc where the query token appears ONLY in
// alternatives surfaces its match under fieldAlternative, not under
// rationale. This is the central Phase 7 capability — the operator
// or LLM can see "this match came from rejected alternatives" and
// apply context-sensitive judgment.
func TestSearch_MatchesPerFieldSeparation(t *testing.T) {
	root, fsys := fixture(t)
	// Decision whose only "auth" mention lives in an alternative's
	// RejectedBecause text. Title, summary, and main rationale all
	// avoid the term.
	writeDecisionWithAlternatives(t, fsys, "dec-pgbouncer",
		"Adopt PgBouncer",
		"Use PgBouncer for connection pooling.",
		"PgBouncer is mature and battle-tested.",
		[]spec.Alternative{{
			Name:            "Odyssey",
			Rationale:       "Newer alternative.",
			RejectedBecause: "Odyssey's auth handoff lacks SCRAM passthrough.",
		}})

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search("auth", Options{Explain: true})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.NotNil(t, hits[0].Matches)

	altMatch, hasAlt := hits[0].Matches[fieldAlternative]
	require.True(t, hasAlt,
		"expected 'alternative' to appear in Matches when the only 'auth' is in alt.RejectedBecause")
	assert.Greater(t, altMatch.Count, 0)
	assert.Greater(t, altMatch.Contribution, 0.0)

	_, hasRationale := hits[0].Matches[fieldRationale]
	assert.False(t, hasRationale,
		"rationale should NOT appear in Matches — 'auth' is not in the main rationale")
}

// Kind-filter boost-zero: when Options.Kind is set, the kind term
// match should not appear in Matches (it has boost=0 and is a filter,
// not a scoring signal). Without this guard, every hit would carry
// a constant "kind" entry that adds noise to the diagnostic.
func TestSearch_KindFilterNotInMatches(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-foo", "Foo decision", "A foo decision.", "Rationale about foo.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search("foo", Options{Kind: string(spec.KindDecision), Explain: true})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.NotNil(t, hits[0].Matches)

	_, hasKind := hits[0].Matches[fieldKind]
	assert.False(t, hasKind, "kind filter should not pollute the Matches map")
}

// Regression: id_tokens used to omit SearchTermPositions, so a
// prefix query that matched in id_tokens would surface a non-zero
// Contribution but nil Terms and zero Count — the diagnostic looked
// "broken" even though the score was correct. Enabling positions
// closes the gap.
func TestSearch_IDTokensCarriesLocations(t *testing.T) {
	root, fsys := fixture(t)
	writeDecision(t, fsys, "dec-adopt-authentication-workos",
		"Adopt WorkOS",
		"Use WorkOS for SSO.",
		"WorkOS bundles OIDC and directory sync.")

	idx, err := Open(fsys, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, _, err := idx.Search("authen*", Options{Explain: true})
	require.NoError(t, err)
	require.Len(t, hits, 1)

	idTok, ok := hits[0].Matches[fieldIDTokens]
	require.True(t, ok, "id_tokens should appear in Matches when a prefix matches the slug body")
	assert.Greater(t, idTok.Count, 0, "id_tokens Count must reflect the slug-body match")
	assert.NotEmpty(t, idTok.Terms, "id_tokens Terms must list the stemmed forms that matched")
}
