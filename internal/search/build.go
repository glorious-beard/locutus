package search

import (
	"fmt"
	"path"
	"strings"

	"github.com/blugelabs/bluge"
	enanalyzer "github.com/blugelabs/bluge/analysis/lang/en"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// Field names. Centralised here so Search and Build reference one
// source — drift between indexing and querying field names is a
// classic silent-zero-results bug.
const (
	fieldID        = "id"         // exact-match keyword (e.g., id:dec-postgres)
	fieldIDTokens  = "id_tokens"  // slug-body tokens (postgres pgvector)
	fieldKind      = "kind"       // keyword filter, never free-text searched
	fieldTitle     = "title"      // en-analyzed, highest boost
	fieldSummary   = "summary"    // en-analyzed, mid boost (DJ-114)
	fieldBody      = "body"       // en-analyzed, base boost — long prose
	fieldComposite = "_all"       // synthetic composite for free-text fallback
)

// Per-field boost weights applied at query time (Bluge doesn't carry
// index-time field boost; the analyzer + length normalization handle
// the rest of the BM25 math). Numbers mirror the heuristic scorer's
// weights so the BM25 ranking starts in the same neighbourhood as
// what operators are used to, then improves from there.
const (
	boostTitle    = 3.0
	boostIDTokens = 2.0
	boostSummary  = 2.0
	boostBody     = 1.0
)

// kindValues holds the accepted Kind filter values. Mirrors
// internal/spec.NodeKind for the kinds we index (excluding the
// synthetic "goals" root, which has no individual spec file).
var kindValues = map[string]struct{}{
	string(spec.KindFeature):  {},
	string(spec.KindStrategy): {},
	string(spec.KindDecision): {},
	string(spec.KindBug):      {},
	string(spec.KindApproach): {},
}

// buildAllDocuments walks .borg/spec/ over fsys and returns one Bluge
// document per node, plus a count of files skipped because they were
// malformed. Skipped files are logged via slog at the call site (we
// keep slog out of this helper so it's pure-data for tests).
func buildAllDocuments(fsys specio.FS) ([]*bluge.Document, int, error) {
	var docs []*bluge.Document
	skipped := 0

	if pairs, err := specio.WalkPairs[spec.Feature](fsys, path.Join(specRoot, "features")); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				skipped++
				continue
			}
			docs = append(docs, featureDoc(p.Object, p.Body))
		}
	} else if !errorsIsNotExist(err) {
		return nil, 0, fmt.Errorf("walk features: %w", err)
	}

	if pairs, err := specio.WalkPairs[spec.Strategy](fsys, path.Join(specRoot, "strategies")); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				skipped++
				continue
			}
			docs = append(docs, strategyDoc(p.Object, p.Body))
		}
	} else if !errorsIsNotExist(err) {
		return nil, 0, fmt.Errorf("walk strategies: %w", err)
	}

	if pairs, err := specio.WalkPairs[spec.Decision](fsys, path.Join(specRoot, "decisions")); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				skipped++
				continue
			}
			docs = append(docs, decisionDoc(p.Object, p.Body))
		}
	} else if !errorsIsNotExist(err) {
		return nil, 0, fmt.Errorf("walk decisions: %w", err)
	}

	if pairs, err := specio.WalkPairs[spec.Bug](fsys, path.Join(specRoot, "bugs")); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				skipped++
				continue
			}
			docs = append(docs, bugDoc(p.Object, p.Body))
		}
	} else if !errorsIsNotExist(err) {
		return nil, 0, fmt.Errorf("walk bugs: %w", err)
	}

	// Approaches are markdown-only on disk. Walk the directory and
	// load each .md via specio.LoadMarkdown so the typed frontmatter
	// surfaces alongside the body.
	if files, err := fsys.ListDir(path.Join(specRoot, "approaches")); err == nil {
		for _, f := range files {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			obj, body, lerr := specio.LoadMarkdown[spec.Approach](fsys, f)
			if lerr != nil {
				skipped++
				continue
			}
			docs = append(docs, approachDoc(obj, body))
		}
	} else if !errorsIsNotExist(err) {
		return nil, 0, fmt.Errorf("list approaches: %w", err)
	}

	return docs, skipped, nil
}

func featureDoc(f spec.Feature, body string) *bluge.Document {
	d := newDoc(f.ID, string(spec.KindFeature), f.Title, f.Summary)
	addTextField(d, fieldBody, f.Description)
	addTextField(d, fieldBody, body)
	for _, ac := range f.AcceptanceCriteria {
		addTextField(d, fieldBody, ac)
	}
	return d
}

func strategyDoc(s spec.Strategy, body string) *bluge.Document {
	d := newDoc(s.ID, string(spec.KindStrategy), s.Title, s.Summary)
	addTextField(d, fieldBody, body)
	return d
}

func decisionDoc(dec spec.Decision, body string) *bluge.Document {
	d := newDoc(dec.ID, string(spec.KindDecision), dec.Title, dec.Summary)
	addTextField(d, fieldBody, dec.Rationale)
	addTextField(d, fieldBody, body)
	if dec.Provenance != nil {
		addTextField(d, fieldBody, dec.Provenance.ArchitectRationale)
		for _, cit := range dec.Provenance.Citations {
			addTextField(d, fieldBody, cit.Excerpt)
		}
	}
	for _, alt := range dec.Alternatives {
		addTextField(d, fieldBody, alt.Name)
		addTextField(d, fieldBody, alt.Rationale)
		addTextField(d, fieldBody, alt.RejectedBecause)
	}
	return d
}

func bugDoc(b spec.Bug, body string) *bluge.Document {
	d := newDoc(b.ID, string(spec.KindBug), b.Title, b.Summary)
	addTextField(d, fieldBody, b.Description)
	addTextField(d, fieldBody, b.RootCause)
	addTextField(d, fieldBody, b.FixPlan)
	addTextField(d, fieldBody, body)
	for _, step := range b.ReproductionSteps {
		addTextField(d, fieldBody, step)
	}
	return d
}

func approachDoc(a spec.Approach, body string) *bluge.Document {
	d := newDoc(a.ID, string(spec.KindApproach), a.Title, a.Summary)
	addTextField(d, fieldBody, a.Body)
	addTextField(d, fieldBody, body)
	return d
}

// newDoc seeds the per-node document with the four always-present
// fields: id (keyword + tokenized slug body), kind (filter), title
// (boosted), and summary (boosted). The id+kind+title are stored
// so search results can return them without re-reading .borg/.
//
// The Bluge document identifier (used by Update/Delete) is the spec
// id verbatim — kept stable across rebuilds so incremental updates
// in Phase 2 land cleanly.
func newDoc(id, kind, title, summary string) *bluge.Document {
	d := bluge.NewDocument(id)
	d.AddField(bluge.NewKeywordField(fieldID, id).StoreValue())
	d.AddField(bluge.NewTextField(fieldIDTokens, slugTokens(id)).WithAnalyzer(enanalyzer.NewAnalyzer()))
	d.AddField(bluge.NewKeywordField(fieldKind, kind).StoreValue())
	if title != "" {
		d.AddField(bluge.NewTextField(fieldTitle, title).WithAnalyzer(enanalyzer.NewAnalyzer()).SearchTermPositions().StoreValue())
	}
	if spec.HasSummary(summary) {
		d.AddField(bluge.NewTextField(fieldSummary, summary).WithAnalyzer(enanalyzer.NewAnalyzer()).SearchTermPositions())
	}
	return d
}

// addTextField appends a body-class field (rationale, description,
// citation excerpt, ...) when the value is non-empty. SearchTermPositions
// is enabled so phrase queries against the body field find matches
// embedded in the longer prose (the most common case). Skipping empty
// values keeps the inverted index tight and avoids polluting BM25's
// length normalization with zero-content fields.
func addTextField(d *bluge.Document, name, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	d.AddField(bluge.NewTextField(name, value).WithAnalyzer(enanalyzer.NewAnalyzer()).SearchTermPositions())
}

// slugTokens converts a spec id like "dec-postgres-with-pgvector"
// into the tokenized slug body "postgres with pgvector". The kind
// prefix is dropped because every node with the same prefix shares
// it; it adds no discriminating signal and would inflate BM25 token
// frequencies for the prefix term.
//
// Numeric segments and hyphenated multi-segment ids ("aes-256-gcm")
// pass through unchanged — the analyzer downstream tokenises them.
func slugTokens(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[1:], " ")
}
