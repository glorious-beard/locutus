package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
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
type ListHit struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Score       float64 `json:"score"`
	Invalidated bool    `json:"invalidated,omitempty"`
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

	rawHits, _, err := idx.Search(query, search.Options{Kind: kind, Limit: search.DefaultLimit})
	if err != nil {
		return nil, err
	}

	hits := make([]ListHit, 0, len(rawHits))
	for _, h := range rawHits {
		hit := ListHit{ID: h.ID, Kind: h.Kind, Title: h.Title, Score: h.Score}
		if h.Kind == string(spec.KindApproach) {
			hit.Invalidated = approachInvalidated(fsys, h.ID)
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
		if h.Invalidated {
			fmt.Fprintf(&b, "- `%s` (%s) [invalidated] — %s\n", h.ID, h.Kind, h.Title)
		} else {
			fmt.Fprintf(&b, "- `%s` (%s) — %s\n", h.ID, h.Kind, h.Title)
		}
	}
	return b.String()
}
