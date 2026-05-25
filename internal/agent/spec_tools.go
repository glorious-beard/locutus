// Spec-lookup tools the spec-reconciler agent uses to navigate the
// persisted spec lazily instead of receiving the entire ExistingSpec
// inlined into its prompt.
//
//   - spec_list_manifest() — returns a compact index of every
//     persisted spec node (id, title, kind for strategies, one-line
//     summary).
//   - spec_get(id) — returns the full JSON content of one spec node,
//     identified by ID prefix.
//
// Both are pure reads against specio.FS rooted at the project. No
// persisted manifest file — `.borg/spec/` IS the manifest per
// DJ-068; the index is computed on-demand from the directory
// listing so there is no sync surface to maintain.
//
// Tool granularity matters: the manifest carries enough one-line
// context (title + summary) that the reconciler can decide to fetch
// full content or skip without burning a `spec_get` call per node.

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"strings"

	"github.com/chetan/locutus/internal/search"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// validSpecID restricts spec ids to the kebab-case shape locutus
// produces (see spec.SlugID): a known prefix followed by lowercase
// alphanumerics and hyphens. Anything else is rejected before we
// touch the filesystem.
//
// The strict form has two jobs:
//  1. Match what authoring agents actually emit, so legitimate
//     lookups always pass.
//  2. Prevent path-traversal exploits via crafted ids like
//     `dec-../../etc/passwd`. filepath.Join collapses `..` segments
//     up the tree; an id slug containing `.` or `/` would let a
//     prompt-injected document coerce the agent into reading any
//     .json or .md file the process can reach. Rejecting non-
//     alphanumeric-or-hyphen characters closes that door.
var validSpecID = regexp.MustCompile(`^(feat|strat|dec|bug|app)-[a-z0-9]+(-[a-z0-9]+)*$`)

// SpecManifest is the index returned by spec_list_manifest. Entries
// are grouped by kind so the model can scan the whole index at a
// glance and drill into a specific category without cross-array
// filtering.
type SpecManifest struct {
	Features   []SpecManifestEntry `json:"features,omitempty"`
	Strategies []SpecManifestEntry `json:"strategies,omitempty"`
	Decisions  []SpecManifestEntry `json:"decisions,omitempty"`
	Bugs       []SpecManifestEntry `json:"bugs,omitempty"`
	Approaches []SpecManifestEntry `json:"approaches,omitempty"`
}

// SpecManifestEntry is one node's index entry. Summary is a single-
// line truncation of the description / body / rationale — kept
// short so the manifest stays scannable but long enough that the
// reconciler can usually decide reuse vs. mint-new without a
// follow-up spec_get.
//
// Origin and Working tell the model what state this entry is in:
//
//   - Origin == OriginSettled: the node lives on disk under
//     `.borg/spec/` from a prior refine / assimilate run. Its body is
//     authoritative and the council isn't rewriting it (unless an
//     iteration revise dispatch picks it up, in which case Working
//     flips to true).
//   - Origin == OriginProposed: the node was authored or modified by
//     the council in this iteration. Lives in the in-flight proposal;
//     hasn't been persisted to `.borg/spec/` yet. The convergence loop
//     may revise it in subsequent iterations.
//   - Working == true: a fanout step is rewriting this node right now
//     (the scout reopened its axis; an open critic concern targets it;
//     it's a new scout-surfaced node whose narrative hasn't landed
//     yet). The body the model is reading is about to change, so
//     downstream agents should not commit citations or acceptance
//     criteria against this body without re-checking on the next
//     iteration. Decisions are stable across Flips at the ID level
//     under DJ-133 (axis-as-ID), so Working applies to body content
//     only — not to ID stability for decisions.
//
// Outside a council run (cmd-layer spec_list_manifest from `locutus
// status`, etc.) Origin defaults to OriginSettled and Working stays
// false — the on-disk graph is the only source visible and nothing is
// in flight.
type SpecManifestEntry struct {
	ID      string             `json:"id"`
	Title   string             `json:"title"`
	Kind    string             `json:"kind,omitempty"`
	Summary string             `json:"summary,omitempty"`
	Origin  SpecManifestOrigin `json:"origin"`
	Working bool               `json:"working,omitempty"`
}

// SpecManifestOrigin classifies where a manifest entry lives.
type SpecManifestOrigin string

const (
	// OriginSettled marks entries from the on-disk persisted graph
	// (`.borg/spec/`). The body is committed and won't change unless
	// a subsequent iteration's revise dispatch picks it up.
	OriginSettled SpecManifestOrigin = "settled"
	// OriginProposed marks entries the council added or modified in
	// the current iteration. The in-flight body is the authoritative
	// view; the on-disk graph (if any matching ID) is stale relative
	// to it.
	OriginProposed SpecManifestOrigin = "proposed"
)

// summaryMaxRunes caps the per-entry summary length. ~200 chars
// keeps the full manifest comfortably under a few KB even with 100
// nodes (200 chars × 100 ≈ 20 KB), which is the whole point — the
// manifest must be cheap to scan in one tool call.
const summaryMaxRunes = 200

// BuildSpecManifest walks `.borg/spec/` and returns the index. Pure
// reads; missing kind directories (greenfield) yield empty arrays
// rather than errors. Malformed individual files are skipped with a
// slog.Warn — one bad file shouldn't poison the whole manifest.
//
// Per-node summary derivation:
//   - If the node's authored Summary field is non-empty, use it
//     verbatim. This is the primary path; the SummariesPresent prereq
//     guarantees every persisted node carries one.
//   - Else fall back to a truncated lead-in of the most-summary-like
//     prose field on each kind. The fallback exists for two cases:
//     (a) defensive — if the prereq somehow didn't run, the manifest
//     stays usable; (b) tests that construct typed nodes without
//     going through the authoring agents. Producing a misleading
//     truncation is strictly worse than producing an authored
//     one-liner, but strictly better than emitting an empty summary.
func BuildSpecManifest(fsys specio.FS) SpecManifest {
	var m SpecManifest

	if pairs, err := specio.WalkPairs[spec.Feature](fsys, ".borg/spec/features"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("spec manifest: skipping malformed feature", "path", p.Path, "error", p.Err)
				continue
			}
			m.Features = append(m.Features, SpecManifestEntry{
				ID:      p.Object.ID,
				Title:   p.Object.Title,
				Summary: summaryOrFallback(p.Object.Summary, p.Object.Description),
				Origin:  OriginSettled,
			})
		}
	}
	if pairs, err := specio.WalkPairs[spec.Strategy](fsys, ".borg/spec/strategies"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("spec manifest: skipping malformed strategy", "path", p.Path, "error", p.Err)
				continue
			}
			m.Strategies = append(m.Strategies, SpecManifestEntry{
				ID:      p.Object.ID,
				Title:   p.Object.Title,
				Kind:    string(p.Object.Kind),
				Summary: summaryOrFallback(p.Object.Summary, p.Body),
				Origin:  OriginSettled,
			})
		}
	}
	if pairs, err := specio.WalkPairs[spec.Decision](fsys, ".borg/spec/decisions"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("spec manifest: skipping malformed decision", "path", p.Path, "error", p.Err)
				continue
			}
			m.Decisions = append(m.Decisions, SpecManifestEntry{
				ID:      p.Object.ID,
				Title:   p.Object.Title,
				Summary: summaryOrFallback(p.Object.Summary, p.Object.Rationale),
				Origin:  OriginSettled,
			})
		}
	}
	if pairs, err := specio.WalkPairs[spec.Bug](fsys, ".borg/spec/bugs"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("spec manifest: skipping malformed bug", "path", p.Path, "error", p.Err)
				continue
			}
			m.Bugs = append(m.Bugs, SpecManifestEntry{
				ID:      p.Object.ID,
				Title:   p.Object.Title,
				Summary: summaryOrFallback(p.Object.Summary, p.Object.Description),
				Origin:  OriginSettled,
			})
		}
	}
	if files, err := fsys.ListDir(".borg/spec/approaches"); err == nil {
		for _, f := range files {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			obj, body, err := specio.LoadMarkdown[spec.Approach](fsys, f)
			if err != nil {
				slog.Warn("spec manifest: skipping malformed approach", "path", f, "error", err)
				continue
			}
			m.Approaches = append(m.Approaches, SpecManifestEntry{
				ID:      obj.ID,
				Title:   obj.Title,
				Summary: summaryOrFallback(obj.Summary, body),
				Origin:  OriginSettled,
			})
		}
	}

	return m
}

// summaryOrFallback returns the authored Summary verbatim when
// non-empty, else a truncated lead-in of the fallback prose. The
// fallback path emits a one-line collapse capped at summaryMaxRunes —
// strictly worse than an authored summary (often picks up framing
// instead of substance) but a graceful degradation for legacy nodes
// the prereq hasn't filled.
func summaryOrFallback(authored, fallback string) string {
	if spec.HasSummary(authored) {
		return strings.TrimSpace(authored)
	}
	return truncate(fallback, summaryMaxRunes)
}

// LookupSpecNode returns the raw JSON of one spec node by id. The
// kind is inferred from the id prefix (`feat-`, `strat-`, `dec-`,
// `bug-`, `app-`).
//
// Validation is strict (validSpecID): malformed ids — empty, wrong
// prefix, non-alphanumeric characters, embedded `..` segments —
// return an actionable error before any filesystem access. This
// closes the path-traversal door a permissive prefix check would
// leave open and gives the model a clear message it can act on.
//
// For approach nodes (markdown only, no JSON sidecar), returns the
// full markdown body wrapped as a JSON string so the tool's contract
// stays "JSON in, JSON out."
//
// Missing files (id well-formed but no such node) return a wrapped
// error naming the id and pointing at spec_list_manifest so the
// model can recover by scanning the index for the correct id rather
// than re-guessing.
func LookupSpecNode(fsys specio.FS, id string) (json.RawMessage, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("spec_get: empty id")
	}
	if !validSpecID.MatchString(id) {
		return nil, fmt.Errorf("spec_get: id %q is malformed (expected kebab-case with prefix feat-, strat-, dec-, bug-, or app-)", id)
	}
	var p string
	switch {
	case strings.HasPrefix(id, "feat-"):
		p = ".borg/spec/features/" + id + ".json"
	case strings.HasPrefix(id, "strat-"):
		p = ".borg/spec/strategies/" + id + ".json"
	case strings.HasPrefix(id, "dec-"):
		p = ".borg/spec/decisions/" + id + ".json"
	case strings.HasPrefix(id, "bug-"):
		p = ".borg/spec/bugs/" + id + ".json"
	case strings.HasPrefix(id, "app-"):
		body, err := fsys.ReadFile(".borg/spec/approaches/" + id + ".md")
		if err != nil {
			return nil, wrapSpecGetReadError(fsys, id, err)
		}
		out, mErr := json.Marshal(string(body))
		if mErr != nil {
			return nil, mErr
		}
		return out, nil
	default:
		// Unreachable: validSpecID enforces one of the known prefixes.
		return nil, fmt.Errorf("spec_get: id %q has unknown prefix", id)
	}
	return readJSON(fsys, id, p)
}

// wrapSpecGetReadError translates a filesystem read error into a
// message the model can act on. Not-found is the common recoverable
// case: model picked a typo'd or near-miss id from the manifest;
// surface up to suggestSpecIDLimit nearest neighbors (token-Jaccard
// over slug parts, scoped to the same prefix) so the model can fix
// its guess in one round-trip instead of re-fetching the entire
// manifest. When no candidate is close, fall back to pointing at
// spec_list_manifest for the full list.
//
// Other errors pass through with the path stripped so the model
// isn't asked to reason about filesystem details.
func wrapSpecGetReadError(fsys specio.FS, id string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		suggestions := suggestSpecIDCandidates(fsys, id, suggestSpecIDLimit)
		if len(suggestions) == 0 {
			return fmt.Errorf("spec_get: no node with id %q (call spec_list_manifest to see available ids)", id)
		}
		return fmt.Errorf("spec_get: no node with id %q. Did you mean one of:\n%s\nOtherwise call spec_list_manifest for the full list.",
			id, formatSpecIDSuggestions(suggestions))
	}
	return fmt.Errorf("spec_get: reading %s: %w", id, err)
}

// suggestSpecIDLimit caps how many near-miss candidates the not-found
// error surfaces. Five entries is roughly 1KB of prompt overhead with
// summaries attached — comfortably below the manifest dump cost while
// still covering the typo / one-segment-off / sibling-confusion error
// modes the model lands in most often.
const suggestSpecIDLimit = 5

// suggestSpecIDCandidates returns up to limit near-miss entries from
// the manifest, scoped to the same prefix kind as the missing id and
// ranked by token-Jaccard similarity over the slug parts.
//
// Why same-prefix scoping: a model that typed `dec-xyz` doesn't want
// `feat-xyz` suggested — wrong kind = wrong answer. Restricting the
// candidate pool to the same prefix tightens the suggestion quality
// at zero cost.
//
// Why token-Jaccard: cheap, no embeddings, catches the common error
// modes (typo of one segment, missing/extra hyphen, dropped suffix)
// without paying for semantic similarity. Sufficient when the model's
// guess is structurally close; falls through cleanly to the manifest
// hint when it isn't.
func suggestSpecIDCandidates(fsys specio.FS, missing string, limit int) []specIDSuggestion {
	prefix := specIDPrefix(missing)
	if prefix == "" {
		return nil
	}
	manifest := BuildSpecManifest(fsys)
	pool := manifestEntriesForPrefix(manifest, prefix)
	if len(pool) == 0 {
		return nil
	}
	wantTokens := slugTokens(missing)
	if len(wantTokens) == 0 {
		return nil
	}

	scored := make([]specIDSuggestion, 0, len(pool))
	for _, entry := range pool {
		score := jaccardSimilarity(wantTokens, slugTokens(entry.ID))
		if score == 0 {
			continue
		}
		scored = append(scored, specIDSuggestion{
			ID:      entry.ID,
			Summary: entry.Summary,
			Score:   score,
		})
	}

	// Sort by score descending; stable ordering on ties via ID for
	// deterministic output the tests can lock down.
	sortSpecIDSuggestions(scored)
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// specIDSuggestion is one entry in the not-found error's "did you
// mean" list. Carries the id, its authored Summary, and the
// similarity score (kept for test ordering verification; not surfaced
// to the model).
type specIDSuggestion struct {
	ID      string
	Summary string
	Score   float64
}

// specIDPrefix returns the prefix segment of a spec id (one of feat-,
// strat-, dec-, bug-, app-), or "" when the id doesn't carry one.
// Used to scope the candidate pool to the requested kind.
func specIDPrefix(id string) string {
	for _, p := range []string{"feat-", "strat-", "dec-", "bug-", "app-"} {
		if strings.HasPrefix(id, p) {
			return p
		}
	}
	return ""
}

// manifestEntriesForPrefix returns the manifest entries for one kind
// (matched by the id-prefix), flattened to a single slice so the
// caller can rank uniformly.
func manifestEntriesForPrefix(m SpecManifest, prefix string) []SpecManifestEntry {
	switch prefix {
	case "feat-":
		return m.Features
	case "strat-":
		return m.Strategies
	case "dec-":
		return m.Decisions
	case "bug-":
		return m.Bugs
	case "app-":
		return m.Approaches
	}
	return nil
}

// slugTokens splits an id on hyphens and drops the prefix segment so
// the similarity score reflects the meaningful slug body, not the
// (constant) kind prefix every candidate shares.
//
// "dec-postgres-with-pgvector" → ["postgres", "with", "pgvector"]
func slugTokens(id string) []string {
	parts := strings.Split(id, "-")
	if len(parts) <= 1 {
		return nil
	}
	// Drop the prefix segment ("dec", "feat", etc.); the surviving
	// parts are the slug body.
	body := parts[1:]
	out := make([]string, 0, len(body))
	for _, p := range body {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// jaccardSimilarity returns |A ∩ B| / |A ∪ B| over two token sets.
// Returns 0 when either side is empty.
func jaccardSimilarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(a))
	for _, t := range a {
		setA[t] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, t := range b {
		setB[t] = struct{}{}
	}
	var intersect int
	for t := range setA {
		if _, ok := setB[t]; ok {
			intersect++
		}
	}
	union := len(setA) + len(setB) - intersect
	if union == 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

// sortSpecIDSuggestions orders by score descending, then by ID
// ascending for stable tie-breaking. Local helper kept inline so the
// test suite can verify a deterministic ranking.
func sortSpecIDSuggestions(s []specIDSuggestion) {
	// Insertion sort — N≤ pool size; for any realistic project this
	// is fine, and avoiding a sort.Slice closure keeps the call cheap
	// and stable.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0; j-- {
			if specIDSuggestionLess(s[j], s[j-1]) {
				s[j], s[j-1] = s[j-1], s[j]
				continue
			}
			break
		}
	}
}

func specIDSuggestionLess(a, b specIDSuggestion) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ID < b.ID
}

// formatSpecIDSuggestions renders the candidate list into the
// not-found error body. Bullet-list shape so the model can scan
// without parsing custom delimiters.
func formatSpecIDSuggestions(s []specIDSuggestion) string {
	var b strings.Builder
	for _, c := range s {
		b.WriteString("  - ")
		b.WriteString(c.ID)
		if c.Summary != "" {
			b.WriteString(" — ")
			b.WriteString(c.Summary)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func readJSON(fsys specio.FS, id, p string) (json.RawMessage, error) {
	data, err := fsys.ReadFile(p)
	if err != nil {
		return nil, wrapSpecGetReadError(fsys, id, err)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("spec_get: %s contains invalid JSON (the spec file on disk is corrupt; this is not an id problem)", path.Base(p))
	}
	return data, nil
}

// truncate trims s to maxRunes, appending "…" when truncated.
// Operates on runes so multibyte characters don't get sliced
// mid-codepoint. Newlines collapse to single spaces so the manifest
// entries stay one-line — multi-paragraph descriptions would defeat
// the point of a scannable index.
func truncate(s string, maxRunes int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	runes := []rune(collapsed)
	if len(runes) <= maxRunes {
		return collapsed
	}
	return string(runes[:maxRunes]) + "…"
}

// SpecGetInput is the tool input shape for spec_get. IDs is plural
// by design: spec_get fetches a batch in one call so an agent that
// needs N node bodies (e.g. the scout grading several open concerns
// at once) pays one round-trip against the tool-loop cap rather
// than N (DJ-134). There is no scalar-id variant of the tool — the
// shape itself forces batching.
type SpecGetInput struct {
	IDs []string `json:"ids"`
}

// Spec-tool names exported so frontmatter parsers and tests can
// reference them without literal-string drift.
const (
	ToolNameSpecListManifest = "spec_list_manifest"
	ToolNameSpecGet          = "spec_get"
	ToolNameSpecSearch       = "spec_search"
)

// SpecSearchInput is the tool input shape for spec_search. Query is
// required; Kind and Limit are optional. The struct mirrors the JSON
// schema we expose to the LLM.
type SpecSearchInput struct {
	Query string `json:"query"`
	Kind  string `json:"kind,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// SpecSearchResult wraps the ranked hits with a full-match count so
// the agent can tell when its slice is truncated.
//
// Per-hit payload is SpecSearchHit (not SpecManifestEntry as in the
// initial Phase 4 shape): the agent benefits from per-field match
// diagnostics that let it discount a hit whose score is dominated by
// a non-relevant field (e.g. a query for "authentication" rolling up
// matches inside a rejected alternative's prose). The id/title/kind/
// summary subset is unchanged — adding fields, not removing.
type SpecSearchResult struct {
	Hits         []SpecSearchHit `json:"hits"`
	TotalMatches int             `json:"total_matches"`
}

// SpecSearchHit is one ranked spec node with per-field diagnostics.
// Mirrors SpecManifestEntry for identity (id, title, kind, summary)
// and adds Score plus Matches so the agent can apply context-
// sensitive judgment.
//
// Matches is a map keyed by field name (title, summary, rationale,
// alternative, description, acceptance, provenance, bug_detail,
// body, id_tokens). Each entry carries the query Terms that matched
// in that field, the Count of occurrences, and the BM25
// Contribution to the overall Score. Sum of contributions equals
// Score within float rounding.
type SpecSearchHit struct {
	ID      string                    `json:"id"`
	Title   string                    `json:"title"`
	Kind    string                    `json:"kind,omitempty"`
	Summary string                    `json:"summary,omitempty"`
	Score   float64                   `json:"score"`
	Matches map[string]SpecFieldMatch `json:"matches,omitempty"`
}

// SpecFieldMatch is the per-field diagnostic surfaced by spec_search.
// Mirrors search.FieldMatch with JSON tags tuned for the agent
// surface.
//
// ContributionPct is the field's share of the hit's total Score,
// expressed as a fraction in [0, 1]. Mid-tier model consumers find
// percentages easier to reason against than absolute BM25 numbers
// (which vary with corpus size and query shape). Sum across fields
// is approximately 1.0; small float drift is fine.
type SpecFieldMatch struct {
	Terms           []string `json:"terms"`
	Count           int      `json:"count"`
	Contribution    float64  `json:"contribution"`
	ContributionPct float64  `json:"contribution_pct"`
}

// specSearchAgentDefaultLimit is the agent-surface default. Smaller
// than internal/search.DefaultLimit (100) because agent contexts
// prefer compact result sets — the wrapper's TotalMatches signals
// when more is available.
const specSearchAgentDefaultLimit = 20

// specSearchAgentMaxLimit caps the per-call result count. Matches
// internal/search.DefaultLimit so the agent surface and CLI surface
// agree on the ceiling.
const specSearchAgentMaxLimit = 100

// specSearchKinds is the kind-filter whitelist. Mirrors the values
// search.Options.Kind accepts. Lifting it here lets us reject unknown
// kinds with an actionable agent-facing message before opening the
// index.
var specSearchKinds = map[string]struct{}{
	string(spec.KindFeature):  {},
	string(spec.KindStrategy): {},
	string(spec.KindDecision): {},
	string(spec.KindBug):      {},
	string(spec.KindApproach): {},
}

// SearchSpecNodes runs a ranked free-text search over the spec graph
// and returns the top hits plus the full match count.
//
// Validation: empty query is rejected; unknown Kind is rejected;
// Limit is clamped (0 / negative → specSearchAgentDefaultLimit, >max
// → specSearchAgentMaxLimit). Validation runs before any backend
// access so misuse fails fast.
//
// backend is the read-side seam (DJ-123 Phase 2): production wires in
// the on-disk *search.Index, council runs wire in a *search.InFlightIndex.
// Both share identical Hit / Options / FieldMatch shapes, so the rest
// of this function is backend-agnostic.
//
// Summary derivation: each Bluge hit carries id/kind/title but not
// the authored Summary. We build the manifest once per call and look
// each id up in an id→entry map — cheaper than calling LookupSpecNode
// per hit (which re-reads the file from disk) and small at the
// target scale (<100KB for 3000 nodes). In-flight inline-decision
// hits (synthesised ids absent from the on-disk manifest) get empty
// summary; that's acceptable until Phase 3 wires a richer source.
func SearchSpecNodes(fsys specio.FS, backend search.Backend, in SpecSearchInput) (SpecSearchResult, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return SpecSearchResult{}, fmt.Errorf("spec_search: empty query")
	}
	if in.Kind != "" {
		if _, ok := specSearchKinds[in.Kind]; !ok {
			return SpecSearchResult{}, fmt.Errorf("spec_search: unknown kind %q (accepted: feature, strategy, decision, bug, approach)", in.Kind)
		}
	}
	if backend == nil {
		return SpecSearchResult{}, fmt.Errorf("spec_search: no backend wired")
	}
	limit := in.Limit
	switch {
	case limit <= 0:
		limit = specSearchAgentDefaultLimit
	case limit > specSearchAgentMaxLimit:
		limit = specSearchAgentMaxLimit
	}

	// Explain: yes. The whole point of the agent-facing tool is that
	// the LLM can reason about per-field contributions — e.g. discount
	// a hit whose Score is dominated by an alternative's prose when
	// the question is about the chosen direction. The cost (one
	// per-field scan per Search call) is fine at our scale.
	hits, total, err := backend.Search(q, search.Options{
		Kind:    in.Kind,
		Limit:   limit,
		Explain: true,
	})
	if err != nil {
		return SpecSearchResult{}, fmt.Errorf("spec_search: %w", err)
	}

	summaries := summaryByID(BuildSpecManifest(fsys))
	out := make([]SpecSearchHit, 0, len(hits))
	for _, h := range hits {
		hit := SpecSearchHit{
			ID:      h.ID,
			Title:   h.Title,
			Kind:    h.Kind,
			Summary: summaries[h.ID],
			Score:   h.Score,
		}
		if len(h.Matches) > 0 {
			hit.Matches = make(map[string]SpecFieldMatch, len(h.Matches))
			for field, fm := range h.Matches {
				hit.Matches[field] = SpecFieldMatch{
					Terms:           fm.Terms,
					Count:           fm.Count,
					Contribution:    fm.Contribution,
					ContributionPct: fm.ContributionPct,
				}
			}
		}
		out = append(out, hit)
	}
	return SpecSearchResult{Hits: out, TotalMatches: total}, nil
}

// summaryByID flattens a manifest into an id→Summary lookup. Used to
// attach authored summaries to spec_search hits without re-reading
// each node's JSON.
func summaryByID(m SpecManifest) map[string]string {
	out := make(map[string]string, len(m.Features)+len(m.Strategies)+len(m.Decisions)+len(m.Bugs)+len(m.Approaches))
	for _, e := range m.Features {
		out[e.ID] = e.Summary
	}
	for _, e := range m.Strategies {
		out[e.ID] = e.Summary
	}
	for _, e := range m.Decisions {
		out[e.ID] = e.Summary
	}
	for _, e := range m.Bugs {
		out[e.ID] = e.Summary
	}
	for _, e := range m.Approaches {
		out[e.ID] = e.Summary
	}
	return out
}

// RegisterSpecTools retired in DJ-135 phase 5. The council-side
// ToolRegistry it registered against is gone; the spec graph is now
// exposed via internal/mcp.NewSpecServer (which calls store.ListManifest /
// GetSpec / Search directly, sharing the same SpecStore).

// SpecSearchToolDescription documents the tool surface — what it
// does, when to reach for it, and how to interpret the per-field
// match diagnostics. The interpretation section is the bit that
// turns the tool from "ranked list of ids" into "ranked list with
// context" for agents that haven't been told what the field names
// mean: the LLM sees "this hit is 72% alternative" and knows to
// discount it for chosen-direction questions.
//
// Kept as a package-level constant so it survives schema-export
// flows without escaping or layout headaches.
const SpecSearchToolDescription = `Returns the ranked top-N spec nodes matching a free-text query (BM25 over title/summary/body, with optional kind filter). Use for topic-scoped lookups (e.g. "what do we have on authentication?"); prefer spec_list_manifest when you need to enumerate the full graph structure.

During a spec-generation council run, spec_search queries the in-flight proposal — what the architects in this run have already committed to (including inline decisions surfaced under synthesised dec-inline-<parent>-<n> ids that the reconciler reassigns downstream). Outside a council run, it queries the persisted spec graph at .borg/spec/. The input/output shape and the per-field match diagnostics described below are identical across both surfaces. spec_list_manifest and spec_get continue to read the persisted graph in either context.

Each hit carries a "matches" map keyed by field name. The fields and what a match in each one tells you about relevance:

- title — the node's headline. A match here is strong signal the node is *about* the topic.
- summary — the authored one-line "what" for this node. Strong signal.
- rationale — a decision's main rationale (the "why" for the chosen direction). Strong signal for decisions.
- description — feature or bug description prose. Strong signal for features and bugs.
- alternative — prose inside a decision's REJECTED alternatives. A match here usually means the topic was *considered and discarded*, not chosen. Discount unless your question is specifically about rejected options.
- acceptance — feature acceptance criteria. Moderate signal — direct relevance to a feature's contract.
- provenance — citations and architect_rationale on a decision. Mixed signal: may quote external docs (real evidence) or reference paper/document authorship (incidental).
- bug_detail — bug root_cause, fix_plan, reproduction_steps. Strong signal for bug-related queries.
- body — the markdown body of the spec file (anything after frontmatter). Moderate signal.
- id_tokens — the node's slug body. A match here means the topic is in the id itself.

Each FieldMatch carries terms (the stemmed query tokens that matched there — note these are post-stemming forms like "authent" from "authentication"), count (occurrences in this field), contribution (absolute BM25 contribution to score), and contribution_pct (the fraction of the hit's total score from this field, in [0,1]). Use contribution_pct for threshold reasoning: a hit whose score is >60% alternative or >60% provenance is usually incidental rather than topically relevant.`
