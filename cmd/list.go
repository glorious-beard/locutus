package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/search"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// ListCmd searches the spec graph for nodes whose id, title, summary,
// or body match the query. Pure read of `.borg/`; no LLM. Companion
// to `explain` — `list` finds the ids, `explain` renders one.
//
// Backed by the BM25 index in internal/search: the on-disk index lives
// at .locutus/spec_index/ and is rebuilt automatically when stale.
// Output formats: markdown (default) or json.
type ListCmd struct {
	Query  string `arg:"" help:"Free-text query. Quote for phrases, trailing * for prefix."`
	Kind   string `help:"Filter to one node kind: decision, feature, strategy, approach, bug." enum:",decision,feature,strategy,approach,bug" default:""`
	Format string `help:"Output format: markdown or json." enum:"markdown,json" default:"markdown"`
}

// ListResult is the JSON shape emitted with --format=json. Markdown is
// included so downstream consumers can pick either form without
// re-running the search.
type ListResult struct {
	Query    string    `json:"query"`
	Kind     string    `json:"kind,omitempty"`
	Hits     []ListHit `json:"hits"`
	Markdown string    `json:"markdown"`
}

// ListHit is one matching spec node. Score is an opaque BM25 relevance
// number — order, not magnitude, is the API. Invalidated is true for
// approaches that carry InvalidatedByEventID; the markdown renderer
// surfaces this via an `[invalidated]` badge so operators can spot
// pending-reconcile work without filtering.
//
// Matches surfaces per-field diagnostics: which field the query
// matched in, how often, and the BM25 contribution. Lets an operator
// (or LLM consumer of JSON output) see that a high-scoring hit is
// dominated by, say, "alternative" — and apply context-sensitive
// judgment to whether that match is what they're looking for.
type ListHit struct {
	ID          string                       `json:"id"`
	Kind        string                       `json:"kind"`
	Title       string                       `json:"title"`
	Score       float64                      `json:"score"`
	Invalidated bool                         `json:"invalidated,omitempty"`
	Matches     map[string]ListFieldMatch    `json:"matches,omitempty"`
}

// ListFieldMatch mirrors search.FieldMatch with JSON tags suited to
// the list output. Order of fields in the JSON encoder is fixed so
// the output is stable across runs. ContributionPct is included
// alongside the absolute Contribution so JSON-consuming agents can
// pick whichever shape they find easier to reason against.
type ListFieldMatch struct {
	Terms           []string `json:"terms"`
	Count           int      `json:"count"`
	Contribution    float64  `json:"contribution"`
	ContributionPct float64  `json:"contribution_pct"`
}

func (c *ListCmd) Run(cli *CLI) error {
	fsys, root, err := projectFS()
	if err != nil {
		return err
	}
	result, err := RunList(fsys, root, c.Query, c.Kind)
	if err != nil {
		return err
	}

	format := c.Format
	if cli.JSON && format == "markdown" {
		format = "json"
	}
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		fmt.Print(result.Markdown)
		return nil
	}
}

// RunList opens the persistent BM25 index and returns nodes matching
// the query. Shared between the CLI handler and the MCP tool.
//
// projectRoot is the absolute OS path of the project — search.Open
// writes its segments directly to disk under .locutus/spec_index/.
func RunList(fsys specio.FS, projectRoot, query, kindFilter string) (*ListResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	kind, err := normaliseKindFilter(kindFilter)
	if err != nil {
		return nil, err
	}

	idx, err := search.Open(fsys, projectRoot)
	if err != nil {
		return nil, fmt.Errorf("open search index: %w", err)
	}
	defer idx.Close()

	// Explain: yes. The list verb is operator-facing; the per-field
	// diagnostic is the entire reason a human can tell whether a
	// match came from a title hit (what they want) or from prose
	// inside a rejected alternative (probably not what they want).
	rawHits, _, err := idx.Search(query, search.Options{
		Kind:    kind,
		Limit:   search.DefaultLimit,
		Explain: true,
	})
	if err != nil {
		return nil, err
	}

	hits := make([]ListHit, 0, len(rawHits))
	for _, h := range rawHits {
		hit := ListHit{ID: h.ID, Kind: h.Kind, Title: h.Title, Score: h.Score}
		if h.Kind == string(spec.KindApproach) {
			hit.Invalidated = approachInvalidated(fsys, h.ID)
		}
		if len(h.Matches) > 0 {
			hit.Matches = make(map[string]ListFieldMatch, len(h.Matches))
			for field, fm := range h.Matches {
				hit.Matches[field] = ListFieldMatch{
					Terms:           fm.Terms,
					Count:           fm.Count,
					Contribution:    fm.Contribution,
					ContributionPct: fm.ContributionPct,
				}
			}
		}
		hits = append(hits, hit)
	}

	return &ListResult{
		Query:    query,
		Kind:     kind,
		Hits:     hits,
		Markdown: renderListMarkdown(query, kind, hits),
	}, nil
}

// approachInvalidated returns whether an approach node carries the
// InvalidatedByEventID marker. search.Hit doesn't carry the flag so
// we resolve it post-search via a single markdown load per approach
// hit. Bounded by the Limit=100 ceiling.
func approachInvalidated(fsys specio.FS, id string) bool {
	a, _, err := specio.LoadMarkdown[spec.Approach](fsys, path.Join(".borg/spec/approaches", id+".md"))
	if err != nil {
		return false
	}
	return a.IsInvalidated()
}

func normaliseKindFilter(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(spec.KindDecision):
		return string(spec.KindDecision), nil
	case string(spec.KindFeature):
		return string(spec.KindFeature), nil
	case string(spec.KindStrategy):
		return string(spec.KindStrategy), nil
	case string(spec.KindApproach):
		return string(spec.KindApproach), nil
	case string(spec.KindBug):
		return string(spec.KindBug), nil
	}
	return "", fmt.Errorf("unknown kind %q (want decision, feature, strategy, approach, or bug)", raw)
}

func renderListMarkdown(query, kind string, hits []ListHit) string {
	var b strings.Builder
	header := fmt.Sprintf("# Search: %q", query)
	if kind != "" {
		header += fmt.Sprintf(" (kind=%s)", kind)
	}
	b.WriteString(header)
	b.WriteString("\n\n")

	if len(hits) == 0 {
		b.WriteString("No matches.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "%d match", len(hits))
	if len(hits) != 1 {
		b.WriteString("es")
	}
	b.WriteString(":\n\n")
	for _, h := range hits {
		matched := formatMatchedFields(h.Matches)
		switch {
		case h.Invalidated && matched != "":
			fmt.Fprintf(&b, "- `%s` (%s) [invalidated] [matched: %s] — %s\n", h.ID, h.Kind, matched, h.Title)
		case h.Invalidated:
			fmt.Fprintf(&b, "- `%s` (%s) [invalidated] — %s\n", h.ID, h.Kind, h.Title)
		case matched != "":
			fmt.Fprintf(&b, "- `%s` (%s) [matched: %s] — %s\n", h.ID, h.Kind, matched, h.Title)
		default:
			fmt.Fprintf(&b, "- `%s` (%s) — %s\n", h.ID, h.Kind, h.Title)
		}
	}
	return b.String()
}

// formatMatchedFields renders Matches into a compact inline label
// for the markdown list — "title, alternative ×7" — ordered by
// Contribution descending so the dominant signal reads first. Ties
// break by field name for determinism. Count is shown only when > 1
// since "×1" is noise.
func formatMatchedFields(m map[string]ListFieldMatch) string {
	if len(m) == 0 {
		return ""
	}
	type entry struct {
		field string
		fm    ListFieldMatch
	}
	entries := make([]entry, 0, len(m))
	for f, fm := range m {
		entries = append(entries, entry{f, fm})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].fm.Contribution != entries[j].fm.Contribution {
			return entries[i].fm.Contribution > entries[j].fm.Contribution
		}
		return entries[i].field < entries[j].field
	})
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.fm.Count > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", e.field, e.fm.Count))
		} else {
			parts = append(parts, e.field)
		}
	}
	return strings.Join(parts, ", ")
}
