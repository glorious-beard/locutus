package search

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/blugelabs/bluge"
	"github.com/chetan/locutus/internal/spec"
)

// InFlightIndex is the in-memory Bluge index backed by an agent-council
// run's RawProposal JSON. Same document shape, same query parser, same
// per-field diagnostics as the on-disk Index — by construction, via the
// shared featureDoc / strategyDoc / decisionDoc builders and the
// runSearch helper in index.go. The split exists because the data
// source differs (JSON-in-memory vs .borg/spec/ on disk), not the
// search semantics.
//
// Lifecycle: NewInFlightIndex constructs an empty index; Rebuild
// replaces it from a fresh RawProposal payload; Search runs queries
// against whatever the most recent Rebuild produced; Close releases the
// backing writer's segment buffers. Safe for concurrent Search calls;
// Rebuild serialises against in-flight searches under an RWMutex.
type InFlightIndex struct {
	mu     sync.RWMutex
	writer *bluge.Writer
}

// NewInFlightIndex returns an empty in-memory index. Search on a fresh
// InFlightIndex returns no hits — Rebuild populates the backing store
// from a RawProposal JSON payload.
func NewInFlightIndex() (*InFlightIndex, error) {
	w, err := bluge.OpenWriter(bluge.InMemoryOnlyConfig())
	if err != nil {
		return nil, fmt.Errorf("search: open in-flight writer: %w", err)
	}
	return &InFlightIndex{writer: w}, nil
}

// Rebuild replaces the backing store with documents derived from the
// supplied RawProposal JSON. The previous writer is closed atomically
// after the new one is in place; an in-flight Search that opened a
// reader against the previous writer keeps reading consistent data
// until it releases that reader.
//
// Robustness contract: empty or whitespace input produces an empty
// index without error. Structurally invalid JSON produces an empty
// index AND returns the parse error — the council pipeline keeps
// moving (next merge step's Rebuild call will retry against a fresh
// payload) but the caller can log the diagnostic.
//
// Re-indexing on every council merge is intentional (see DJ-123,
// resolved design question 2): Bluge in-memory writes for a several-
// dozen-node proposal are sub-millisecond. The dirty-flag /
// deferred-rebuild bookkeeping a debounce layer would add is pure
// overhead at this corpus size.
func (i *InFlightIndex) Rebuild(rawProposal string) error {
	docs, parseErr := decodeInFlightDocs(rawProposal)

	// Always swap to a fresh writer so a parse error leaves the index
	// in an empty-but-useable state — never mid-state with stale docs
	// from a prior payload mixed with new partial output.
	fresh, err := bluge.OpenWriter(bluge.InMemoryOnlyConfig())
	if err != nil {
		return fmt.Errorf("search: open in-flight writer: %w", err)
	}
	if len(docs) > 0 {
		batch := bluge.NewBatch()
		for _, d := range docs {
			batch.Update(d.ID(), d)
		}
		if err := fresh.Batch(batch); err != nil {
			_ = fresh.Close()
			return fmt.Errorf("search: in-flight batch insert: %w", err)
		}
	}

	i.mu.Lock()
	prev := i.writer
	i.writer = fresh
	i.mu.Unlock()

	if prev != nil {
		// Close the previous writer outside the lock so a long-running
		// segment flush never blocks subsequent Search / Rebuild calls.
		_ = prev.Close()
	}
	return parseErr
}

// Search runs a query against the most recent Rebuild's output. Mirrors
// (*Index).Search exactly — same query syntax, same Options, same Hit
// shape, same FieldMatch diagnostics. See the package doc on
// (*Index).Search for the query grammar.
func (i *InFlightIndex) Search(query string, opts Options) ([]Hit, int, error) {
	// The Reader() call must happen under the RLock so the captured
	// snapshot's segment refcount is incremented before Rebuild can
	// Close the previous writer. Closing a Bluge writer runs
	// replaceRoot(nil, ...) — a subsequent Reader() on it returns a
	// *Reader with a nil internal snapshot and no error, which would
	// nil-deref on use. Once Reader() returns successfully the segments
	// stay pinned for the reader's lifetime, so dropping the RLock here
	// is safe.
	i.mu.RLock()
	w := i.writer
	if w == nil {
		i.mu.RUnlock()
		return nil, 0, fmt.Errorf("search: in-flight index is closed")
	}
	r, err := w.Reader()
	i.mu.RUnlock()
	if err != nil {
		return nil, 0, fmt.Errorf("search: open in-flight reader: %w", err)
	}
	defer r.Close()
	return runSearch(r, query, opts)
}

// Compile-time guard that *InFlightIndex satisfies the read-side
// Backend interface declared in search.go — the agent-facing
// spec_search tool can be wired to either backing store at
// registration time (DJ-123 Phase 2).
var _ Backend = (*InFlightIndex)(nil)

// Close releases the in-memory writer's segment buffers. Safe to call
// multiple times; subsequent calls are no-ops.
func (i *InFlightIndex) Close() error {
	i.mu.Lock()
	w := i.writer
	i.writer = nil
	i.mu.Unlock()
	if w == nil {
		return nil
	}
	return w.Close()
}

// inFlightProposal mirrors the on-the-wire RawSpecProposal shape that
// assembleRawProposal / assembleRevisedRawProposal emit into
// PlanningState.RawProposal. Defined locally because internal/agent
// imports internal/search — the reverse import would cycle. JSON tags
// only; this struct never travels into an LLM-facing OutputSchema (see
// CLAUDE.md on jsonschema tagging).
type inFlightProposal struct {
	Features   []inFlightFeature  `json:"features"`
	Strategies []inFlightStrategy `json:"strategies"`
}

type inFlightFeature struct {
	ID                 string             `json:"id"`
	Summary            string             `json:"summary"`
	Title              string             `json:"title"`
	Description        string             `json:"description"`
	AcceptanceCriteria []string           `json:"acceptance_criteria"`
	Decisions          []inFlightDecision `json:"decisions"`
}

type inFlightStrategy struct {
	ID        string             `json:"id"`
	Summary   string             `json:"summary"`
	Title     string             `json:"title"`
	Kind      string             `json:"kind"`
	Body      string             `json:"body"`
	Decisions []inFlightDecision `json:"decisions"`
}

// inFlightDecision is the inline-decision shape: no ID (the reconciler
// assigns canonical IDs later; the index synthesises a per-parent slug
// so the document identifier is unique), no InfluencedBy.
type inFlightDecision struct {
	Summary            string             `json:"summary"`
	Title              string             `json:"title"`
	Rationale          string             `json:"rationale"`
	Confidence         float64            `json:"confidence"`
	Alternatives       []spec.Alternative `json:"alternatives"`
	Citations          []spec.Citation    `json:"citations"`
	ArchitectRationale string             `json:"architect_rationale"`
}

// decodeInFlightDocs parses the RawProposal JSON and returns the Bluge
// documents that would populate the in-flight index. Returns (nil, nil)
// for empty / whitespace input; (nil, parseErr) for malformed JSON;
// (docs, nil) on success. Per-feature and per-strategy inline decisions
// are emitted as their own documents so a council agent searching for a
// rationale term lands directly on the decision rather than only on the
// enclosing feature.
//
// Body-field asymmetry below — featureDoc(..., "") and decisionDoc(..., "")
// pass empty bodies while strategyDoc(..., s.Body) passes s.Body — is
// load-bearing: the on-the-wire RawSpecProposal envelope only carries a
// body field on strategies. Feature and decision prose lives in their
// typed fields (Description, AcceptanceCriteria, Rationale, Alternatives,
// Provenance) and is already projected into the shared *Doc builders.
func decodeInFlightDocs(rawProposal string) ([]*bluge.Document, error) {
	trimmed := strings.TrimSpace(rawProposal)
	if trimmed == "" {
		return nil, nil
	}
	var prop inFlightProposal
	if err := json.Unmarshal([]byte(trimmed), &prop); err != nil {
		return nil, fmt.Errorf("search: parse in-flight proposal: %w", err)
	}

	var docs []*bluge.Document
	for _, f := range prop.Features {
		if f.ID == "" {
			continue
		}
		docs = append(docs, featureDoc(spec.Feature{
			ID:                 f.ID,
			Title:              f.Title,
			Summary:            f.Summary,
			Description:        f.Description,
			AcceptanceCriteria: f.AcceptanceCriteria,
		}, ""))
		docs = append(docs, inlineDecisionDocs(f.ID, f.Decisions)...)
	}
	for _, s := range prop.Strategies {
		if s.ID == "" {
			continue
		}
		docs = append(docs, strategyDoc(spec.Strategy{
			ID:      s.ID,
			Title:   s.Title,
			Summary: s.Summary,
			Kind:    spec.StrategyKind(s.Kind),
		}, s.Body))
		docs = append(docs, inlineDecisionDocs(s.ID, s.Decisions)...)
	}
	return docs, nil
}

// inlineDecisionDocs turns the inline-decision array on a feature or
// strategy into standalone Bluge documents. Inline decisions have no
// canonical ID at this council stage (the reconciler assigns those
// later), so the index synthesises a stable per-parent slug. The "dec-"
// prefix means slugTokens emits a meaningful body — "inline parent id
// idx" — which is what the slug-body field would otherwise carry.
func inlineDecisionDocs(parentID string, decisions []inFlightDecision) []*bluge.Document {
	if len(decisions) == 0 {
		return nil
	}
	docs := make([]*bluge.Document, 0, len(decisions))
	for idx, d := range decisions {
		dec := spec.Decision{
			ID:           fmt.Sprintf("dec-inline-%s-%d", parentID, idx),
			Title:        d.Title,
			Summary:      d.Summary,
			Rationale:    d.Rationale,
			Confidence:   d.Confidence,
			Alternatives: d.Alternatives,
		}
		if d.ArchitectRationale != "" || len(d.Citations) > 0 {
			dec.Provenance = &spec.DecisionProvenance{
				ArchitectRationale: d.ArchitectRationale,
				Citations:          d.Citations,
			}
		}
		docs = append(docs, decisionDoc(dec, ""))
	}
	return docs
}
