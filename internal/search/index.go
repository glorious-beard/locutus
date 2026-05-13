package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/blugelabs/bluge"
	enanalyzer "github.com/blugelabs/bluge/analysis/lang/en"
	"github.com/blugelabs/bluge/search"
	"github.com/chetan/locutus/internal/specio"
)

// Index is a handle to the persisted spec FTS index. Stateless for
// the on-disk case (each Search opens its own *bluge.Reader and
// closes it); holds a writer for the in-memory case so the indexed
// data survives across Search calls within the same process.
//
// The handle is safe to share across goroutines: read operations
// open fresh readers each time, and Bluge's writer is safe for
// concurrent use.
type Index struct {
	cfg       bluge.Config
	fsys      specio.FS
	indexPath string         // "" → in-memory; absolute OS path otherwise
	memWriter *bluge.Writer  // populated only when indexPath == ""
}

// Open returns a handle to the on-disk index at
// <projectRoot>/.locutus/spec_index/. The index is built on first use
// and rebuilt whenever the on-disk fingerprint doesn't match the
// current state of .borg/spec/. The handle holds no long-lived
// resources beyond the configured paths.
//
// projectRoot must be an absolute OS path because Bluge writes
// directly to the filesystem; the fsys argument is used only for
// reading .borg/spec/ during build (so callers can substitute a
// fixture FS in tests).
func Open(fsys specio.FS, projectRoot string) (*Index, error) {
	if projectRoot == "" {
		return nil, fmt.Errorf("search: Open requires a non-empty projectRoot")
	}
	indexPath := filepath.Join(projectRoot, IndexDir)
	if err := os.MkdirAll(indexPath, 0o755); err != nil {
		return nil, fmt.Errorf("search: create index dir: %w", err)
	}

	idx := &Index{
		cfg:       bluge.DefaultConfig(indexPath),
		fsys:      fsys,
		indexPath: indexPath,
	}

	current, err := computeFingerprint(fsys)
	if err != nil {
		return nil, fmt.Errorf("search: fingerprint: %w", err)
	}
	stored := idx.readStoredFingerprint()

	if stored != current || !idx.hasUsableIndex() {
		if err := idx.rebuildOnDisk(current); err != nil {
			return nil, fmt.Errorf("search: rebuild: %w", err)
		}
	}
	return idx, nil
}

// OpenInMemory returns a handle backed by an in-memory Bluge index
// freshly built from fsys's spec graph. The caller must Close the
// handle when done to release the writer's segment buffers.
//
// Intended for tests and one-off introspection — the on-disk path
// (Open) is the production code path because it amortizes build
// cost across CLI invocations.
func OpenInMemory(fsys specio.FS) (*Index, error) {
	w, err := bluge.OpenWriter(bluge.InMemoryOnlyConfig())
	if err != nil {
		return nil, fmt.Errorf("search: open in-memory writer: %w", err)
	}
	idx := &Index{fsys: fsys, memWriter: w}
	if err := idx.populate(w); err != nil {
		_ = w.Close()
		return nil, err
	}
	return idx, nil
}

// Close releases any resources held by the Index. Safe to call
// multiple times; subsequent calls are no-ops.
func (i *Index) Close() error {
	if i.memWriter != nil {
		w := i.memWriter
		i.memWriter = nil
		return w.Close()
	}
	return nil
}

// rebuildOnDisk removes the existing on-disk index contents and
// rebuilds from .borg/spec/. The fingerprint file is written last so
// a crash mid-build leaves the next Open call to retry rather than
// trust a stale index.
func (i *Index) rebuildOnDisk(current Fingerprint) error {
	if err := wipeIndexDir(i.indexPath); err != nil {
		return fmt.Errorf("wipe index dir: %w", err)
	}
	w, err := bluge.OpenWriter(i.cfg)
	if err != nil {
		return fmt.Errorf("open writer: %w", err)
	}
	if err := i.populate(w); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	return i.writeFingerprint(current)
}

// populate batch-inserts all spec documents into the given writer.
// Skipped files (malformed JSON / markdown) are logged but don't
// abort the build — partial index is preferable to no index.
func (i *Index) populate(w *bluge.Writer) error {
	docs, skipped, err := buildAllDocuments(i.fsys)
	if err != nil {
		return fmt.Errorf("build documents: %w", err)
	}
	if skipped > 0 {
		slog.Warn("search: skipped malformed spec files during index build", "count", skipped)
	}
	batch := bluge.NewBatch()
	for _, d := range docs {
		batch.Update(d.ID(), d)
	}
	if err := w.Batch(batch); err != nil {
		return fmt.Errorf("batch insert: %w", err)
	}
	return nil
}

// wipeIndexDir removes every file directly under dir but leaves dir
// itself in place (so the parent .locutus/ tree doesn't disappear).
// Restricted to a single-level rm so we never accidentally recurse
// into a wrong directory.
func wipeIndexDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.MkdirAll(dir, 0o755)
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// readStoredFingerprint returns the persisted fingerprint, or "" when
// the file is absent or unreadable. A bare-string return keeps the
// caller simple: any divergence from the current value triggers a
// rebuild, regardless of root cause.
func (i *Index) readStoredFingerprint() Fingerprint {
	if i.indexPath == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(i.indexPath, fingerprintFile))
	if err != nil {
		return ""
	}
	return Fingerprint(strings.TrimSpace(string(data)))
}

// writeFingerprint persists the fingerprint atomically (write+rename)
// so a crash mid-write leaves the previous fingerprint intact rather
// than a truncated value the next Open would parse as stale.
func (i *Index) writeFingerprint(fp Fingerprint) error {
	if i.indexPath == "" {
		return nil
	}
	target := filepath.Join(i.indexPath, fingerprintFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(fp), 0o644); err != nil {
		return fmt.Errorf("write fingerprint tmp: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("rename fingerprint: %w", err)
	}
	return nil
}

// hasUsableIndex returns true when the on-disk index directory looks
// like it contains real Bluge segments. A directory carrying only a
// stale fingerprint file (or nothing) needs a rebuild.
func (i *Index) hasUsableIndex() bool {
	if i.indexPath == "" {
		return false
	}
	entries, err := os.ReadDir(i.indexPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if name == fingerprintFile || name == fingerprintFile+".tmp" {
			continue
		}
		return true
	}
	return false
}

// Search runs a query against the index and returns the ranked top
// hits plus a total-match count. The total count is the full match
// set, not the capped slice — callers can use it to know when their
// results are truncated.
//
// Query syntax:
//   - Free text → multi-field BM25 match over title, summary,
//     id_tokens, and the prose fields (rationale, description,
//     alternative, acceptance, provenance, bug_detail, body), with
//     per-field boosts (title=3, summary=2, id_tokens=2; prose=1).
//   - "..." quoted phrase → multi-field MatchPhraseQuery on the same
//     fields.
//   - trailing * (e.g., "auth*") → multi-field PrefixQuery.
//
// When opts.Explain is set, each Hit carries a Matches map keyed by
// field name with per-field Terms / Count / Contribution — the
// signal an LLM or operator uses to apply context-sensitive judgment
// to the ranked list (e.g. discounting a hit whose Score is mostly
// "alternative" contribution).
func (i *Index) Search(query string, opts Options) ([]Hit, int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, 0, ErrEmptyQuery
	}
	if opts.Kind != "" {
		if _, ok := kindValues[opts.Kind]; !ok {
			return nil, 0, fmt.Errorf("%w: %q", ErrUnknownKind, opts.Kind)
		}
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	q := buildQuery(query, opts.Kind)
	// WithStandardAggregations registers the "count" aggregation so
	// it.Aggregations().Count() returns the full match set size (not
	// just the slice we materialise into hits). Without this, Count()
	// silently returns 0 — a Bluge gotcha worth a one-line note.
	req := bluge.NewTopNSearch(limit, q).WithStandardAggregations()
	if opts.Explain {
		// IncludeLocations carries per-field/term hit positions so we
		// can populate Count and Terms in FieldMatch. Contribution
		// comes from the per-field scan below, not from Bluge's
		// Explanation tree — see scanPerFieldContributions.
		req = req.IncludeLocations()
	}

	r, err := i.reader()
	if err != nil {
		return nil, 0, fmt.Errorf("search: open reader: %w", err)
	}
	defer r.Close()

	// Run per-field score scans before the main query so the same
	// Reader serves both — saves a second OpenReader and keeps the
	// "snapshot of the index at this instant" property consistent
	// across the diagnostics and the ranking.
	var perField perFieldContributions
	if opts.Explain {
		perField, err = scanPerFieldContributions(r, query, opts.Kind)
		if err != nil {
			return nil, 0, err
		}
	}

	it, err := r.Search(context.Background(), req)
	if err != nil {
		return nil, 0, fmt.Errorf("search: execute: %w", err)
	}

	var hits []Hit
	for {
		match, err := it.Next()
		if err != nil {
			return nil, 0, fmt.Errorf("search: iterate: %w", err)
		}
		if match == nil {
			break
		}
		hit := Hit{Score: match.Score}
		err = match.VisitStoredFields(func(field string, value []byte) bool {
			switch field {
			case fieldID:
				hit.ID = string(value)
			case fieldKind:
				hit.Kind = string(value)
			case fieldTitle:
				hit.Title = string(value)
			}
			return true
		})
		if err != nil {
			return nil, 0, fmt.Errorf("search: visit stored fields: %w", err)
		}
		if opts.Explain {
			hit.Matches = buildMatchDiagnostics(match, hit.ID, perField)
		}
		hits = append(hits, hit)
	}

	total := int(it.Aggregations().Count())
	return hits, total, nil
}

// reader returns a *bluge.Reader appropriate for this Index. For
// in-memory indexes the reader is bound to the held writer; for
// on-disk indexes each call opens a fresh disk reader so concurrent
// writers (in other processes or future Update calls) aren't blocked.
//
// The caller owns the returned reader and must Close it. The in-memory
// path's Reader is created via writer.Reader() which is documented as
// near-real-time over the writer's segments — fine for the same-
// process tests this path serves.
func (i *Index) reader() (*bluge.Reader, error) {
	if i.memWriter != nil {
		return i.memWriter.Reader()
	}
	return bluge.OpenReader(i.cfg)
}

// buildQuery constructs the Bluge query that powers Search. Three
// dispatch branches:
//
//  1. Quoted phrase ("...") → MatchPhraseQuery across the prose
//     fields, fused as a disjunction so any one field matching
//     contributes.
//  2. Prefix glob (foo*) → PrefixQuery across the prose fields plus
//     id_tokens, again fused as a disjunction.
//  3. Free text → MatchQuery across the prose fields with per-field
//     boost mirroring the heuristic scorer's weights.
//
// When opts.Kind is non-empty the scoring query is wrapped in a
// BooleanQuery that AND's a Must keyword term on the kind field with
// boost=0 — kind is a filter, not a ranking signal, and giving it
// boost would let kind-IDF leak into the ranking (rare-kind docs
// would outrank common-kind docs by an accident of corpus shape).
func buildQuery(text, kind string) bluge.Query {
	body := chooseInnerQuery(text)
	if kind == "" {
		return body
	}
	return bluge.NewBooleanQuery().
		AddMust(body).
		AddMust(bluge.NewTermQuery(kind).SetField(fieldKind).SetBoost(0))
}

func chooseInnerQuery(text string) bluge.Query {
	if isQuotedPhrase(text) {
		phrase := strings.Trim(text, `"`)
		return phraseDisjunction(phrase)
	}
	if isPrefixGlob(text) {
		prefix := strings.TrimSuffix(text, "*")
		return prefixDisjunction(prefix)
	}
	return matchDisjunction(text)
}

// isQuotedPhrase recognises a query that is wholly wrapped in double
// quotes: the model-facing convention for phrase search. Anything
// with internal quotes is left to the match-query branch — Bluge's
// analyzer will tokenize sensibly.
func isQuotedPhrase(text string) bool {
	return len(text) >= 2 && strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`)
}

// isPrefixGlob recognises a trailing-* glob (and only a trailing
// glob — bluge's PrefixQuery doesn't support mid-string wildcards
// and exposing them here would mislead the caller).
func isPrefixGlob(text string) bool {
	return strings.HasSuffix(text, "*") && !strings.Contains(text[:len(text)-1], "*")
}

func matchDisjunction(text string) bluge.Query {
	q := bluge.NewBooleanQuery()
	an := enanalyzer.NewAnalyzer()
	q.AddShould(bluge.NewMatchQuery(text).SetField(fieldTitle).SetAnalyzer(an).SetBoost(boostTitle))
	q.AddShould(bluge.NewMatchQuery(text).SetField(fieldSummary).SetAnalyzer(an).SetBoost(boostSummary))
	q.AddShould(bluge.NewMatchQuery(text).SetField(fieldIDTokens).SetAnalyzer(an).SetBoost(boostIDTokens))
	for _, f := range proseFields {
		q.AddShould(bluge.NewMatchQuery(text).SetField(f).SetAnalyzer(an).SetBoost(boostBody))
	}
	q.SetMinShould(1)
	return q
}

func phraseDisjunction(phrase string) bluge.Query {
	q := bluge.NewBooleanQuery()
	an := enanalyzer.NewAnalyzer()
	q.AddShould(bluge.NewMatchPhraseQuery(phrase).SetField(fieldTitle).SetAnalyzer(an).SetBoost(boostTitle))
	q.AddShould(bluge.NewMatchPhraseQuery(phrase).SetField(fieldSummary).SetAnalyzer(an).SetBoost(boostSummary))
	for _, f := range proseFields {
		q.AddShould(bluge.NewMatchPhraseQuery(phrase).SetField(f).SetAnalyzer(an).SetBoost(boostBody))
	}
	q.SetMinShould(1)
	return q
}

func prefixDisjunction(prefix string) bluge.Query {
	lower := strings.ToLower(prefix)
	q := bluge.NewBooleanQuery()
	q.AddShould(bluge.NewPrefixQuery(lower).SetField(fieldTitle).SetBoost(boostTitle))
	q.AddShould(bluge.NewPrefixQuery(lower).SetField(fieldSummary).SetBoost(boostSummary))
	q.AddShould(bluge.NewPrefixQuery(lower).SetField(fieldIDTokens).SetBoost(boostIDTokens))
	for _, f := range proseFields {
		q.AddShould(bluge.NewPrefixQuery(lower).SetField(f).SetBoost(boostBody))
	}
	q.SetMinShould(1)
	return q
}

// scoredFieldSet is allScoredFields lifted into a set for O(1)
// lookups when filtering match-location data to fields we care about
// — drops the kind-filter clause (boost=0, filter not signal) and
// any future virtual field Bluge might surface.
var scoredFieldSet = func() map[string]struct{} {
	s := make(map[string]struct{}, len(allScoredFields))
	for _, f := range allScoredFields {
		s[f] = struct{}{}
	}
	return s
}()

// fieldBoost returns the query-time boost for a scored field. Used by
// the per-field score scans so each single-field query mirrors the
// boost it would have inside the main disjunction — that's the
// invariant that lets the per-field score sum equal the disjunction's
// total Score.
func fieldBoost(field string) float64 {
	switch field {
	case fieldTitle:
		return boostTitle
	case fieldSummary:
		return boostSummary
	case fieldIDTokens:
		return boostIDTokens
	}
	return boostBody
}

// perFieldContributions maps a docID (the spec node's id, since we
// use it as the Bluge document identifier) to a per-field score. The
// outer map is keyed by field name. Built once per Search call when
// Options.Explain is set; consulted at hit-construction time.
type perFieldContributions map[string]map[string]float64

// scanPerFieldContributions runs one targeted MatchQuery (or phrase /
// prefix variant) per scored field and records (docID → score) per
// field. Bluge's CompositeSumScorer is a literal sum, so a per-field
// MatchQuery with the same boost we used in the main disjunction
// returns the same contribution that field made to the disjunction's
// total Score. That's what makes this approach faithful instead of
// approximate: the per-field score IS the contribution, by
// construction.
//
// Cost: one targeted search per scored field per Search call when
// Explain is on. At our field count (~10) and corpus size, this is
// well under the disjunction's own cost.
//
// kind filter, when set, is AND'd into each per-field query with
// boost=0 so the per-field scores match the filtered disjunction's
// per-field contributions exactly (kind adds zero to the sum).
func scanPerFieldContributions(r *bluge.Reader, text, kind string) (perFieldContributions, error) {
	out := make(perFieldContributions, len(allScoredFields))
	for _, field := range allScoredFields {
		q := singleFieldScoreQuery(text, field)
		if kind != "" {
			q = bluge.NewBooleanQuery().
				AddMust(q).
				AddMust(bluge.NewTermQuery(kind).SetField(fieldKind).SetBoost(0))
		}
		req := bluge.NewAllMatches(q)
		it, err := r.Search(context.Background(), req)
		if err != nil {
			return nil, fmt.Errorf("search: per-field scan for %q: %w", field, err)
		}
		fieldMap := make(map[string]float64)
		for {
			m, err := it.Next()
			if err != nil {
				return nil, fmt.Errorf("search: per-field iterate for %q: %w", field, err)
			}
			if m == nil {
				break
			}
			var id string
			if err := m.VisitStoredFields(func(name string, value []byte) bool {
				if name == fieldID {
					id = string(value)
					return false
				}
				return true
			}); err != nil {
				return nil, fmt.Errorf("search: per-field visit for %q: %w", field, err)
			}
			if id != "" {
				fieldMap[id] = m.Score
			}
		}
		if len(fieldMap) > 0 {
			out[field] = fieldMap
		}
	}
	return out, nil
}

// singleFieldScoreQuery builds the single-field analogue of the inner
// scoring query chosen by chooseInnerQuery — match for free text,
// phrase for "...", prefix for foo*. The shape mirrors the main
// disjunction's per-clause structure so the per-field score equals
// the contribution that clause makes to the disjunction's total.
func singleFieldScoreQuery(text, field string) bluge.Query {
	boost := fieldBoost(field)
	if isQuotedPhrase(text) {
		// Phrase queries on id_tokens make little sense (the analyzer
		// strips most phrase semantics there); skip to a match query.
		phrase := strings.Trim(text, `"`)
		if field == fieldIDTokens {
			return bluge.NewMatchQuery(phrase).
				SetField(field).
				SetAnalyzer(enanalyzer.NewAnalyzer()).
				SetBoost(boost)
		}
		return bluge.NewMatchPhraseQuery(phrase).
			SetField(field).
			SetAnalyzer(enanalyzer.NewAnalyzer()).
			SetBoost(boost)
	}
	if isPrefixGlob(text) {
		return bluge.NewPrefixQuery(strings.ToLower(strings.TrimSuffix(text, "*"))).
			SetField(field).
			SetBoost(boost)
	}
	return bluge.NewMatchQuery(text).
		SetField(field).
		SetAnalyzer(enanalyzer.NewAnalyzer()).
		SetBoost(boost)
}

// buildMatchDiagnostics turns the DocumentMatch's locations (for
// Count/Terms) and the precomputed perFieldContributions (for
// Contribution) into the Matches map exposed on a Hit. Filters to
// scoredFieldSet so the kind-filter clause never appears.
//
// A field shows up in the result if it has either a non-zero
// Contribution OR at least one location for the doc. Both signals
// are necessary: a phrase that matches in the prose but produces a
// score below the cutoff still has locations; conversely, a doc
// scored without positions enabled has a contribution but no
// locations. In practice every prose field has SearchTermPositions
// set, so both arrive together.
func buildMatchDiagnostics(match *search.DocumentMatch, docID string, perField perFieldContributions) map[string]FieldMatch {
	if match == nil {
		return nil
	}
	out := make(map[string]FieldMatch)

	for field, perTerm := range match.Locations {
		if _, ok := scoredFieldSet[field]; !ok {
			continue
		}
		fm := out[field]
		for term, positions := range perTerm {
			if len(positions) == 0 {
				continue
			}
			fm.Terms = append(fm.Terms, term)
			fm.Count += len(positions)
		}
		out[field] = fm
	}
	for field, perDoc := range perField {
		if score, ok := perDoc[docID]; ok && score != 0 {
			fm := out[field]
			fm.Contribution = score
			out[field] = fm
		}
	}

	// Drop fields that have neither a Contribution nor any matched
	// terms — they got into the map only because of an empty
	// locations entry, and surfacing them would add noise.
	for field, fm := range out {
		if fm.Contribution == 0 && fm.Count == 0 {
			delete(out, field)
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// Compile-time guard that io.Closer is satisfied — we expose Close on
// the public API and want callers to be able to defer it like any
// other resource handle.
var _ io.Closer = (*Index)(nil)
