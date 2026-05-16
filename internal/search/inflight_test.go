package search

import (
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureInFlightProposal is the canonical RawSpecProposal-shaped JSON
// fed into the four InFlightIndex tests. Three nodes, distinct query
// terms per node — "pgvector" is unique to the strategy, "workos" is
// unique to the operator-login feature, "dashboard" is unique to the
// realtime feature. Any one of these can land a deterministic single-
// hit assertion.
const fixtureInFlightProposal = `{
  "features": [
    {
      "id": "feat-realtime-dashboard",
      "title": "Realtime dashboard",
      "summary": "Live metric tiles updated every five seconds.",
      "description": "Operators see latency error and throughput tiles that refresh without page reload.",
      "acceptance_criteria": ["When a metric value changes then the tile updates within five seconds."],
      "decisions": ["dec-websocket-transport"]
    },
    {
      "id": "feat-operator-login",
      "title": "Operator login",
      "summary": "Authenticate operators via SSO.",
      "description": "Operators sign in through the corporate identity provider; no local password store.",
      "decisions": ["dec-adopt-workos"]
    }
  ],
  "strategies": [
    {
      "id": "strat-storage-platform",
      "title": "Storage platform",
      "summary": "Adopt Postgres with pgvector for OLTP and embeddings.",
      "kind": "data",
      "body": "Postgres with pgvector keeps embeddings co-located with OLTP rows so transactional and similarity queries share one session.",
      "decisions": ["dec-postgres-pgvector"]
    }
  ],
  "decisions": [
    {
      "id": "dec-websocket-transport",
      "title": "WebSocket transport",
      "summary": "Push updates over WebSocket rather than polling.",
      "rationale": "WebSocket bidirectional channel keeps the wire cost per tile bounded.",
      "confidence": 0.8
    },
    {
      "id": "dec-adopt-workos",
      "title": "Adopt WorkOS for SSO",
      "summary": "Use WorkOS as the identity provider.",
      "rationale": "WorkOS bundles OIDC and directory sync in one vendor.",
      "alternatives": [
        {"name": "Auth0", "rationale": "Broader feature set.", "rejected_because": "Higher operational cost without matching value."}
      ],
      "architect_rationale": "Centralises identity at the SSO layer."
    },
    {
      "id": "dec-postgres-pgvector",
      "title": "Postgres with pgvector",
      "summary": "Use the pgvector extension on Postgres for embeddings.",
      "rationale": "Single-store property dominates over the marginal performance gap a dedicated vector store would offer."
    }
  ]
}`

// TestInFlightIndexBuildsFromRawProposal feeds the canonical fixture
// into a fresh InFlightIndex and asserts that distinctive terms on each
// node kind (feature / strategy / inline-decision) are reachable via
// the same query syntax the on-disk index uses.
func TestInFlightIndexBuildsFromRawProposal(t *testing.T) {
	idx, err := NewInFlightIndex()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Rebuild(fixtureInFlightProposal))

	// Each case names a distinctive term and the (kind, id) that owns
	// the most direct mention of that term — feature title, strategy
	// summary, inline-decision rationale. Together they cover all three
	// node kinds the in-flight builder emits.
	cases := []struct {
		name     string
		query    string
		wantKind string
		wantID   string
	}{
		{"feature title hit", "dashboard", string(spec.KindFeature), "feat-realtime-dashboard"},
		{"strategy summary hit", "pgvector", string(spec.KindStrategy), "strat-storage-platform"},
		{"top-level decision hit", "workos", string(spec.KindDecision), "dec-adopt-workos"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits, _, err := idx.Search(tc.query, Options{})
			require.NoError(t, err)
			require.NotEmpty(t, hits, "expected a hit for %q", tc.query)
			// Among the hits there must be one with the expected kind/id
			// (inline decisions on the same node share the parent's
			// title/summary text so they may co-rank).
			var found bool
			for _, h := range hits {
				if h.ID == tc.wantID && h.Kind == tc.wantKind {
					found = true
					break
				}
			}
			assert.True(t, found, "hits did not include %s/%s; got %+v", tc.wantKind, tc.wantID, hits)
		})
	}
}

// TestInFlightIndexRebuildReplacesPriorState runs two sequential
// Rebuilds; terms that landed against the first proposal must not be
// reachable after the second proposal lands. Verifies Rebuild swaps
// the backing writer atomically rather than merging.
func TestInFlightIndexRebuildReplacesPriorState(t *testing.T) {
	idx, err := NewInFlightIndex()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Rebuild(fixtureInFlightProposal))

	hits, _, err := idx.Search("pgvector", Options{})
	require.NoError(t, err)
	require.NotEmpty(t, hits, "fixture should land pgvector before swap")

	const replacement = `{
      "features": [
        {
          "id": "feat-billing",
          "title": "Billing console",
          "summary": "Operators see ledger entries grouped by tenant.",
          "description": "Tenant-scoped ledger view with monthly rollups.",
          "decisions": ["dec-stripe-billing"]
        }
      ],
      "decisions": [
        {
          "id": "dec-stripe-billing",
          "title": "Stripe as billing provider",
          "summary": "Use Stripe for ledger and invoicing.",
          "rationale": "Stripe handles the ledger primitives we would otherwise build."
        }
      ]
    }`
	require.NoError(t, idx.Rebuild(replacement))

	gone, _, err := idx.Search("pgvector", Options{})
	require.NoError(t, err)
	assert.Empty(t, gone, "pgvector should be unreachable after the replacement Rebuild")

	fresh, _, err := idx.Search("stripe", Options{})
	require.NoError(t, err)
	require.NotEmpty(t, fresh, "stripe should land against the replacement proposal")
	// "stripe" appears only in the decision content; the feature itself
	// doesn't mention the vendor. Assert the canonical decision id is
	// among the hits — exact ranking against the feature is not the
	// invariant under test here (Rebuild atomicity is).
	var sawBillingDecision bool
	for _, h := range fresh {
		if h.ID == "dec-stripe-billing" {
			sawBillingDecision = true
		}
	}
	assert.True(t, sawBillingDecision, "expected the new corpus's stripe decision among hits; got %+v", fresh)
}

// TestInFlightIndexEmitsFieldMatchDiagnostics confirms the in-flight
// backing produces the same per-field diagnostic shape the on-disk
// index does — same Matches map, same Contribution / Count / Terms.
// Without this the DJ-117 explainability story breaks the moment a
// council agent searches the in-flight index instead of disk.
func TestInFlightIndexEmitsFieldMatchDiagnostics(t *testing.T) {
	idx, err := NewInFlightIndex()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Rebuild(fixtureInFlightProposal))

	hits, _, err := idx.Search("pgvector", Options{Explain: true})
	require.NoError(t, err)
	require.NotEmpty(t, hits)

	top := hits[0]
	require.NotNil(t, top.Matches, "Explain=true must populate Matches")

	var sum float64
	for _, fm := range top.Matches {
		sum += fm.Contribution
	}
	assert.InDelta(t, top.Score, sum, 1e-6,
		"sum of per-field Contribution must equal total Score (same invariant the on-disk index holds)")

	// The fixture wires "pgvector" into the strategy's summary, rationale
	// (via its inline decision), and body — at minimum one of those
	// fields surfaces in the diagnostic.
	_, hasSummary := top.Matches[fieldSummary]
	_, hasBody := top.Matches[fieldBody]
	_, hasRationale := top.Matches[fieldRationale]
	assert.True(t, hasSummary || hasBody || hasRationale,
		"expected at least one of summary/body/rationale in Matches; got %+v", top.Matches)
}

// TestInFlightIndexHandlesMalformedJSON covers the robustness contract:
// empty input, whitespace input, and structurally invalid JSON must all
// leave the index in a useable empty state. Search on the resulting
// index returns no hits and does not panic. Rebuild's error contract
// is intentionally lenient — the council pipeline keeps moving even
// when an in-flight payload is briefly malformed (partial assembly,
// truncated streaming write).
func TestInFlightIndexHandlesMalformedJSON(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"empty string", ""},
		{"whitespace only", "   \n\t "},
		{"not json at all", "not-json"},
		{"valid json but wrong shape", `{"unrelated": 42}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, err := NewInFlightIndex()
			require.NoError(t, err)
			t.Cleanup(func() { _ = idx.Close() })

			// Rebuild may or may not return an error for malformed input
			// — we explicitly do not assert on it here. The contract
			// the test enforces is: the index remains useable and
			// search returns no hits.
			_ = idx.Rebuild(tc.payload)

			hits, total, err := idx.Search("anything", Options{})
			require.NoError(t, err)
			assert.Empty(t, hits)
			assert.Equal(t, 0, total)
		})
	}
}

// TestInFlightIndexConcurrentRebuildAndSearch exercises the
// Rebuild/Search swap race directly. One goroutine repeatedly rebuilds
// the index from the canonical fixture; another repeatedly runs a query
// that the fixture is known to match. The regression we guard against:
// Search captured the writer pointer under RLock and then released the
// lock BEFORE calling Writer.Reader(). A concurrent Rebuild that closed
// the previous writer in that window left Reader() returning a *Reader
// with a nil internal snapshot and no error — nil-deref on use, or a
// data race the -race detector would flag.
//
// Each Search call is required to either succeed (with at least one hit,
// since the fixture always indexes a node matching the query) or return
// the "in-flight index is closed" sentinel if Close raced ahead. An NPE
// or a data race fails the test. Must be run under -race to be
// meaningful.
func TestInFlightIndexConcurrentRebuildAndSearch(t *testing.T) {
	t.Parallel()

	idx, err := NewInFlightIndex()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	// Seed the index so the very first Search has a corpus to hit.
	require.NoError(t, idx.Rebuild(fixtureInFlightProposal))

	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(2)

	// Rebuilder: re-indexes the same fixture in a tight loop so each
	// iteration forces a writer swap + Close of the previous writer.
	go func() {
		defer wg.Done()
		for n := 0; n < iterations; n++ {
			if err := idx.Rebuild(fixtureInFlightProposal); err != nil {
				t.Errorf("rebuild iter %d: %v", n, err)
				return
			}
			runtime.Gosched()
		}
	}()

	// Searcher: "pgvector" is unique to the strategy in the fixture, so
	// a clean Search must always return at least one hit. The only other
	// acceptable outcome is the closed-index sentinel.
	go func() {
		defer wg.Done()
		for n := 0; n < iterations; n++ {
			hits, _, err := idx.Search("pgvector", Options{})
			if err != nil {
				if !strings.Contains(err.Error(), "in-flight index is closed") {
					t.Errorf("search iter %d: unexpected error: %v", n, err)
					return
				}
				continue
			}
			if len(hits) == 0 {
				t.Errorf("search iter %d: expected pgvector to land at least one hit, got 0", n)
				return
			}
			runtime.Gosched()
		}
	}()

	wg.Wait()
}
