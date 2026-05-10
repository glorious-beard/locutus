package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureList builds a small spec graph that exercises every match
// surface RunList covers: id-slug match, title match, body/rationale
// match, alternative-rationale match, and a non-matching node so we
// can assert filtering works.
func fixtureList(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)

	// Decision with "auth" in id and title — should rank highest.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-adopt-auth-nextauth", spec.Decision{
		ID: "dec-adopt-auth-nextauth", Title: "Adopt NextAuth for authentication",
		Status: spec.DecisionStatusProposed, Confidence: 0.9,
		Rationale: "NextAuth handles Google OIDC out of the box.",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	// Decision with "auth" only in rationale body — should still match.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-session-storage", spec.Decision{
		ID: "dec-session-storage", Title: "Session storage uses JWT",
		Status: spec.DecisionStatusProposed, Confidence: 0.8,
		Rationale: "JWT keeps the auth path stateless.",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	// Decision with "auth" only in alternative-rationale text.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-secrets-vault", spec.Decision{
		ID: "dec-secrets-vault", Title: "Adopt Vault for secrets",
		Status: spec.DecisionStatusProposed, Confidence: 0.7,
		Rationale: "Centralised secret management.",
		Alternatives: []spec.Alternative{
			{Name: "Env files", Rationale: "Plain dotenv", RejectedBecause: "no auth between services"},
		},
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	// Unrelated decision — should never match an "auth" query.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-pmtiles", spec.Decision{
		ID: "dec-pmtiles", Title: "Use PMTiles for map data",
		Status: spec.DecisionStatusProposed, Confidence: 0.9,
		Rationale: "Serverless tile delivery.",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	// Feature touching auth in description.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-user-onboarding", spec.Feature{
		ID: "feat-user-onboarding", Title: "User onboarding",
		Status:      spec.FeatureStatusProposed,
		Description: "Sign-in flow including authentication and email verification.",
		CreatedAt:   now, UpdatedAt: now,
	}, ""))

	// Strategy with auth in body.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-platform", spec.Strategy{
		ID: "strat-platform", Title: "Platform strategy",
		Kind: spec.StrategyKindFoundational, Status: "proposed",
	}, "Cross-cutting concerns: logging, auth, observability."))

	// Bug touching auth.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/bugs/bug-login-loop", spec.Bug{
		ID: "bug-login-loop", Title: "Login loop after token expiry",
		FeatureID: "feat-user-onboarding",
		Severity:  spec.BugSeverityHigh, Status: spec.BugStatusReported,
		Description: "Auth refresh fails on expired tokens.",
		CreatedAt:   now, UpdatedAt: now,
	}, ""))

	return fs
}

func TestListMatchesTitle(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "authentication", "")
	require.NoError(t, err)

	require.NotEmpty(t, r.Hits, "expected at least one hit for %q", "authentication")
	ids := hitIDs(r.Hits)
	assert.Contains(t, ids, "dec-adopt-auth-nextauth",
		"title match on the auth decision should appear in hits")
}

func TestListMatchesBodyOnly(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "auth", "")
	require.NoError(t, err)

	ids := hitIDs(r.Hits)
	assert.Contains(t, ids, "dec-session-storage",
		"body-only match on the JWT decision should appear in hits")
}

func TestListMatchesAlternativeRationale(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "auth", "")
	require.NoError(t, err)

	ids := hitIDs(r.Hits)
	assert.Contains(t, ids, "dec-secrets-vault",
		"alternative-rationale match on the vault decision should appear in hits")
}

func TestListExcludesUnrelatedNodes(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "auth", "")
	require.NoError(t, err)

	ids := hitIDs(r.Hits)
	assert.NotContains(t, ids, "dec-pmtiles",
		"unrelated decision must not appear in hits")
}

func TestListRanksTitleAboveBody(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "auth", "")
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(r.Hits), 2, "need at least two hits to compare ranking")
	// dec-adopt-auth-nextauth has "auth" in id + title + body.
	// dec-session-storage has "auth" only in body.
	// Title+id hit must outrank body-only hit.
	pos := func(id string) int {
		for i, h := range r.Hits {
			if h.ID == id {
				return i
			}
		}
		return -1
	}
	titleHit := pos("dec-adopt-auth-nextauth")
	bodyHit := pos("dec-session-storage")
	require.GreaterOrEqual(t, titleHit, 0)
	require.GreaterOrEqual(t, bodyHit, 0)
	assert.Less(t, titleHit, bodyHit,
		"title+id hit must be ranked above body-only hit")
}

func TestListMatchesAcrossNodeKinds(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "auth", "")
	require.NoError(t, err)

	ids := hitIDs(r.Hits)
	// Sanity: every kind that contains "auth" somewhere should land
	// in the result set (decisions, feature, strategy, bug).
	for _, want := range []string{
		"dec-adopt-auth-nextauth",
		"feat-user-onboarding",
		"strat-platform",
		"bug-login-loop",
	} {
		assert.Contains(t, ids, want, "expected %q in cross-kind hits", want)
	}
}

func TestListKindFilterDecision(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "auth", "decision")
	require.NoError(t, err)

	require.NotEmpty(t, r.Hits)
	for _, h := range r.Hits {
		assert.Equal(t, "decision", h.Kind,
			"kind=decision filter must exclude non-decision hits, got %q (%s)", h.Kind, h.ID)
	}
}

func TestListKindFilterFeature(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "auth", "feature")
	require.NoError(t, err)

	require.NotEmpty(t, r.Hits)
	for _, h := range r.Hits {
		assert.Equal(t, "feature", h.Kind,
			"kind=feature filter must exclude non-feature hits, got %q (%s)", h.Kind, h.ID)
	}
}

func TestListUnknownKindRejected(t *testing.T) {
	fs := fixtureList(t)
	_, err := RunList(fs, "auth", "widget")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown kind")
}

func TestListEmptyQueryRejected(t *testing.T) {
	fs := fixtureList(t)
	_, err := RunList(fs, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query")
}

func TestListWhitespaceOnlyQueryRejected(t *testing.T) {
	fs := fixtureList(t)
	_, err := RunList(fs, "   \t\n", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query")
}

func TestListNoMatchesIsNotAnError(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "blockchain", "")
	require.NoError(t, err)
	assert.Empty(t, r.Hits, "no matches should produce zero hits, not an error")
}

func TestListCaseInsensitive(t *testing.T) {
	fs := fixtureList(t)
	rLower, err := RunList(fs, "auth", "")
	require.NoError(t, err)
	rUpper, err := RunList(fs, "AUTH", "")
	require.NoError(t, err)
	assert.Equal(t, hitIDs(rLower.Hits), hitIDs(rUpper.Hits),
		"case-folded query must produce identical hit set")
}

func TestListMultiTokenRanksAllTokensAboveOne(t *testing.T) {
	fs := fixtureList(t)
	// "nextauth google" — both tokens hit dec-adopt-auth-nextauth's
	// title + rationale. Only "google" or only "nextauth" alone would
	// match the same node. The two-token query should still surface
	// that node at rank 0.
	r, err := RunList(fs, "nextauth google", "")
	require.NoError(t, err)
	require.NotEmpty(t, r.Hits)
	assert.Equal(t, "dec-adopt-auth-nextauth", r.Hits[0].ID,
		"node hit by both tokens must rank first")
}

func TestListMarkdownIncludesIDsAndTitles(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "auth", "")
	require.NoError(t, err)
	require.NotEmpty(t, r.Markdown)
	assert.Contains(t, r.Markdown, "dec-adopt-auth-nextauth")
	assert.Contains(t, r.Markdown, "Adopt NextAuth for authentication")
}

func TestListMarkdownEmptyHitsMessage(t *testing.T) {
	fs := fixtureList(t)
	r, err := RunList(fs, "blockchain", "")
	require.NoError(t, err)
	assert.Contains(t, strings.ToLower(r.Markdown), "no",
		"empty-hits markdown should explicitly say nothing matched")
}

// hitIDs is a tiny helper so each assertion reads as a set comparison
// instead of a loop — keeps test bodies short and intent obvious.
func hitIDs(hits []ListHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}
