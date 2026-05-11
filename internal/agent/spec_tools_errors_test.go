package agent

import (
	"fmt"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLookupSpecNode_RejectsMalformedIDs locks the validation surface
// against the model sending crafted strings. Each case must error
// before any filesystem read happens — both for path-traversal
// defense (the load-bearing case) and because returning generic
// filesystem errors leaks implementation details the model can't act
// on.
func TestLookupSpecNode_RejectsMalformedIDs(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	// Plant a real node so we can confirm the validation runs BEFORE
	// any file read — these tests should all fail on the regex, not
	// on a missing file.
	dec := spec.Decision{ID: "dec-real", Title: "Real", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-real", dec, "body"))

	cases := []struct {
		name    string
		id      string
		wantSub string // substring expected in the error message
	}{
		{"empty", "", "empty id"},
		{"whitespace only", "   ", "empty id"},
		{"no prefix", "postgres", "malformed"},
		{"unknown prefix", "node-foo", "malformed"},
		{"path traversal up", "dec-../../../etc/passwd", "malformed"},
		{"path traversal slash", "dec-foo/bar", "malformed"},
		{"path traversal backslash", "dec-foo\\bar", "malformed"},
		{"dot segment", "dec-foo.bar", "malformed"},
		{"uppercase", "dec-Foo", "malformed"},
		{"trailing slash", "dec-foo/", "malformed"},
		{"prefix only", "dec-", "malformed"},
		{"double dash", "dec--foo", "malformed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LookupSpecNode(fs, tc.id)
			require.Error(t, err, "id %q should be rejected", tc.id)
			assert.Contains(t, err.Error(), tc.wantSub,
				"error message should be actionable; got %q", err.Error())
		})
	}
}

// TestLookupSpecNode_NotFoundReturnsActionableError verifies that a
// well-formed but unknown id surfaces a message pointing the model at
// spec_list_manifest, not a raw "no such file or directory" path.
func TestLookupSpecNode_NotFoundReturnsActionableError(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	_, err := LookupSpecNode(fs, "dec-does-not-exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dec-does-not-exist")
	assert.Contains(t, err.Error(), "no node with id")
	assert.Contains(t, err.Error(), "spec_list_manifest",
		"not-found errors should point the model at the manifest tool")
	// Negative: the error must not leak the on-disk path the model
	// can't act on.
	assert.NotContains(t, err.Error(), ".borg/spec",
		"avoid leaking filesystem paths into model-facing errors")
}

// TestLookupSpecNode_NotFoundForApproach covers the markdown-only
// approach path (different code branch).
func TestLookupSpecNode_NotFoundForApproach(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))

	_, err := LookupSpecNode(fs, "app-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app-missing")
	assert.Contains(t, err.Error(), "no node with id")
	assert.Contains(t, err.Error(), "spec_list_manifest")
}

// TestLookupSpecNode_HappyPath ensures the validation doesn't reject
// legitimate ids — anything spec.SlugID produces (lowercase kebab
// alphanumeric with an optional trailing -hexsuffix) must pass.
func TestLookupSpecNode_HappyPath(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))

	dec := spec.Decision{ID: "dec-postgres-with-pgvector", Title: "Postgres", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-postgres-with-pgvector", dec, "body"))

	// IDs with a hex-suffix-style collision tag (UniqueID format).
	feat := spec.Feature{ID: "feat-dashboard-a1b2c3", Title: "Dashboard", Status: spec.FeatureStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-dashboard-a1b2c3", feat, "body"))

	app := spec.Approach{ID: "app-fetch-list", Title: "Fetch", ParentID: "feat-dashboard-a1b2c3"}
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-fetch-list.md", app, "body"))

	for _, id := range []string{"dec-postgres-with-pgvector", "feat-dashboard-a1b2c3", "app-fetch-list"} {
		t.Run(id, func(t *testing.T) {
			out, err := LookupSpecNode(fs, id)
			require.NoError(t, err, "id %q should round-trip cleanly", id)
			assert.NotEmpty(t, out)
		})
	}
}

// TestLookupSpecNode_CorruptJSONReportsCorruption confirms the
// invalid-JSON path returns a distinct message ("the spec file is
// corrupt") rather than a generic id-lookup error. The model
// understands it cannot recover by retrying with a different id.
func TestLookupSpecNode_CorruptJSONReportsCorruption(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	// Write a real JSON file but with malformed content.
	require.NoError(t, fs.WriteFile(".borg/spec/decisions/dec-corrupt.json", []byte("{not json"), 0o644))

	_, err := LookupSpecNode(fs, "dec-corrupt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
	assert.Contains(t, err.Error(), "not an id problem",
		"corruption errors should distinguish themselves from id-lookup errors")
}

// TestValidSpecIDRegex_AcceptsRealSlugs sanity-checks that
// validSpecID accepts the output of spec.SlugID + UniqueID for a
// variety of titles. If a future change to SlugID's output shape
// drifts from the regex, this test will catch it.
func TestValidSpecIDRegex_AcceptsRealSlugs(t *testing.T) {
	titles := []string{
		"Use Postgres",
		"OAuth login via Google",
		"Adopt WorkOS for authentication, organization management and audit logging",
		"AES-256-GCM application-layer column encryption",
	}
	for _, title := range titles {
		slug := spec.SlugID(title)
		require.NotEmpty(t, slug)
		for _, prefix := range []string{"feat-", "strat-", "dec-", "bug-", "app-"} {
			id := prefix + slug
			assert.True(t, validSpecID.MatchString(id),
				"id %q derived from title %q should match validSpecID", id, title)
		}
	}

	// UniqueID-style ids with the 6-hex collision suffix.
	hexSuffixed := "dec-use-postgres-a1b2c3"
	assert.True(t, validSpecID.MatchString(hexSuffixed))

	// And the reconciler's -2, -3 dedupe suffix from mintDecisionID.
	dedupeSuffixed := "dec-use-postgres-2"
	assert.True(t, validSpecID.MatchString(dedupeSuffixed))
}

// TestLookupSpecNode_NotFoundOffersSuggestions verifies the not-found
// error surfaces near-miss candidates when the model's typo'd id is
// close to a real one. The common-case error mode: model picked from
// the manifest and transcribed one segment wrong.
func TestLookupSpecNode_NotFoundOffersSuggestions(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	// Plant decisions sharing tokens with the model's typo.
	near := spec.Decision{
		ID:      "dec-postgres-with-pgvector",
		Title:   "Postgres",
		Summary: "Adopt Postgres with the pgvector extension.",
		Status:  spec.DecisionStatusActive,
	}
	farther := spec.Decision{
		ID:      "dec-postgres-logical-replication",
		Title:   "Replication",
		Summary: "Adopt logical replication for cross-region reads.",
		Status:  spec.DecisionStatusActive,
	}
	unrelated := spec.Decision{
		ID:      "dec-vercel-deployment",
		Title:   "Vercel",
		Summary: "Deploy via Vercel.",
		Status:  spec.DecisionStatusActive,
	}
	for _, d := range []spec.Decision{near, farther, unrelated} {
		require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/"+d.ID, d, "body"))
	}

	_, err := LookupSpecNode(fs, "dec-postgres-pgvector") // missing "with-"
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "dec-postgres-pgvector")
	assert.Contains(t, msg, "Did you mean")
	// The near-miss must appear in the suggestion list with its summary.
	assert.Contains(t, msg, "dec-postgres-with-pgvector")
	assert.Contains(t, msg, "Adopt Postgres with the pgvector extension.")
	// The farther match (shares "postgres" but not "pgvector") may
	// also appear; the unrelated decision must NOT appear.
	assert.NotContains(t, msg, "dec-vercel-deployment")
	// Hint to fall back to the full manifest stays in case none of
	// the suggestions are the right answer.
	assert.Contains(t, msg, "spec_list_manifest")
}

// TestLookupSpecNode_SuggestionsScopedToPrefix confirms a missing
// `dec-` id never surfaces a `feat-` candidate. Same-prefix scoping
// keeps the suggestions on-kind and the error focused.
func TestLookupSpecNode_SuggestionsScopedToPrefix(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	// A feature shares the entire slug body with the missing decision id.
	feat := spec.Feature{ID: "feat-shared-body", Title: "X", Status: spec.FeatureStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-shared-body", feat, "body"))

	// No decisions exist; the only candidate would be the feature.
	_, err := LookupSpecNode(fs, "dec-shared-body")
	require.Error(t, err)
	// Falls through to the manifest hint — no cross-prefix suggestions.
	assert.NotContains(t, err.Error(), "feat-shared-body",
		"suggestions must not cross prefix boundaries")
	assert.Contains(t, err.Error(), "spec_list_manifest")
}

// TestLookupSpecNode_NoCandidatesFallsThrough covers the case where
// the missing id shares zero tokens with anything in the manifest —
// model invented a concept that doesn't exist. The error message
// reverts to pointing at spec_list_manifest without an empty "Did
// you mean" header.
func TestLookupSpecNode_NoCandidatesFallsThrough(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	dec := spec.Decision{ID: "dec-vercel", Title: "Vercel", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-vercel", dec, "body"))

	_, err := LookupSpecNode(fs, "dec-completely-unrelated-concept")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "Did you mean")
	assert.Contains(t, err.Error(), "spec_list_manifest")
}

// TestSuggestSpecIDCandidates_RankingIsDeterministic locks the
// ordering so tests downstream can rely on it: highest Jaccard first,
// ties broken by id ascending.
func TestSuggestSpecIDCandidates_RankingIsDeterministic(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	ids := []string{
		"dec-postgres-with-pgvector",  // shares postgres + pgvector
		"dec-postgres-replication",    // shares postgres
		"dec-postgres-backups",        // shares postgres
		"dec-pgvector-index",          // shares pgvector
		"dec-completely-unrelated",    // shares nothing
	}
	for _, id := range ids {
		d := spec.Decision{ID: id, Title: id, Summary: "Summary of " + id + ".", Status: spec.DecisionStatusActive}
		require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/"+id, d, "body"))
	}

	got := suggestSpecIDCandidates(fs, "dec-postgres-pgvector", 5)
	require.NotEmpty(t, got)

	// First entry must be the closest token-Jaccard match.
	assert.Equal(t, "dec-postgres-with-pgvector", got[0].ID)
	// dec-completely-unrelated shares zero tokens and must not appear.
	for _, c := range got {
		assert.NotEqual(t, "dec-completely-unrelated", c.ID)
	}
	// Scores must be non-increasing.
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i].Score, got[i-1].Score,
			"suggestions must be ordered by descending score")
	}
}

// TestSuggestSpecIDCandidates_RespectsLimit confirms the candidate
// cap is honored even when many entries are similar.
func TestSuggestSpecIDCandidates_RespectsLimit(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	// Plant 10 decisions all sharing the "postgres" token.
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("dec-postgres-variant-%d", i)
		d := spec.Decision{ID: id, Title: id, Status: spec.DecisionStatusActive}
		require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/"+id, d, "body"))
	}

	got := suggestSpecIDCandidates(fs, "dec-postgres-foo", 5)
	assert.Len(t, got, 5, "limit of 5 must be respected")
}

// TestValidSpecIDRegex_RejectsTraversal closes the path-traversal
// surface. Every adversarial id must be rejected — these are the
// strings a prompt-injected document might coerce an agent into
// passing to spec_get.
func TestValidSpecIDRegex_RejectsTraversal(t *testing.T) {
	hostile := []string{
		"dec-..",
		"dec-../etc/passwd",
		"dec-..%2f..%2fetc",
		"feat-/etc/passwd",
		"strat-\\windows\\system32",
		"bug-foo\x00bar", // null byte
		"app-foo;rm-rf",  // shell injection (in case the id is interpolated)
		"dec-foo bar",    // whitespace
	}
	for _, id := range hostile {
		assert.False(t, validSpecID.MatchString(id), "hostile id %q must not match", id)
	}
}
