package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// ListCmd searches the spec graph for nodes whose id, title, body, or
// (for decisions) alternative rationales match the query. Pure read of
// `.borg/`; no LLM. Companion to `explain` — `list` finds the ids,
// `explain` renders one.
//
// Output formats: markdown (default) or json. JSON emits the structured
// hits so downstream tooling can pipe ids into other verbs.
type ListCmd struct {
	Query  string `arg:"" help:"Free-text query (case-insensitive, whitespace-tokenised)."`
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

// ListHit is one matching spec node. Score is an opaque relevance
// number — order, not magnitude, is the API. Invalidated is true for
// approaches that carry InvalidatedByEventID; the markdown renderer
// surfaces this via an `[invalidated]` badge so operators can spot
// pending-reconcile work without filtering.
type ListHit struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Score       int    `json:"score"`
	Invalidated bool   `json:"invalidated,omitempty"`
}

func (c *ListCmd) Run(cli *CLI) error {
	fsys, _, err := projectFS()
	if err != nil {
		return err
	}
	result, err := RunList(fsys, c.Query, c.Kind)
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

// RunList loads the spec graph and returns nodes matching the query.
// Shared between the CLI handler and the MCP tool.
func RunList(fsys specio.FS, query, kindFilter string) (*ListResult, error) {
	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("query is required")
	}
	kind, err := normaliseKindFilter(kindFilter)
	if err != nil {
		return nil, err
	}

	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return nil, err
	}

	hits := scanLoaded(loaded, tokens, kind)
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})

	return &ListResult{
		Query:    query,
		Kind:     kind,
		Hits:     hits,
		Markdown: renderListMarkdown(query, kind, hits),
	}, nil
}

// Field weights. Title weighs the most because that's the curated
// human-readable headline; the id slug is derived from the title so
// matching it is a strong signal too. Summary sits between ID and
// Title: more curated than the slug (DJ-114 authored "what" line),
// but with more tokens than the headline, so each match shouldn't
// count for as much as a Title hit. Body / rationale matches are
// real but noisy — a token can show up in passing prose without the
// node being "about" that topic.
const (
	weightTitle   = 3
	weightID      = 2
	weightSummary = 2
	weightBody    = 1
)

func scanLoaded(loaded *spec.Loaded, tokens []string, kindFilter string) []ListHit {
	var hits []ListHit

	if kindFilter == "" || kindFilter == string(spec.KindDecision) {
		for _, n := range loaded.Decisions {
			score := scoreDecision(n, tokens)
			if score > 0 {
				hits = append(hits, ListHit{
					ID: n.Spec.ID, Kind: string(spec.KindDecision),
					Title: n.Spec.Title, Score: score,
				})
			}
		}
	}
	if kindFilter == "" || kindFilter == string(spec.KindFeature) {
		for _, n := range loaded.Features {
			score := scoreFeature(n, tokens)
			if score > 0 {
				hits = append(hits, ListHit{
					ID: n.Spec.ID, Kind: string(spec.KindFeature),
					Title: n.Spec.Title, Score: score,
				})
			}
		}
	}
	if kindFilter == "" || kindFilter == string(spec.KindStrategy) {
		for _, n := range loaded.Strategies {
			score := scoreStrategy(n, tokens)
			if score > 0 {
				hits = append(hits, ListHit{
					ID: n.Spec.ID, Kind: string(spec.KindStrategy),
					Title: n.Spec.Title, Score: score,
				})
			}
		}
	}
	if kindFilter == "" || kindFilter == string(spec.KindApproach) {
		for _, n := range loaded.Approaches {
			score := scoreApproach(n, tokens)
			if score > 0 {
				hits = append(hits, ListHit{
					ID: n.Spec.ID, Kind: string(spec.KindApproach),
					Title:       n.Spec.Title,
					Score:       score,
					Invalidated: n.Spec.IsInvalidated(),
				})
			}
		}
	}
	if kindFilter == "" || kindFilter == string(spec.KindBug) {
		for _, n := range loaded.Bugs {
			score := scoreBug(n, tokens)
			if score > 0 {
				hits = append(hits, ListHit{
					ID: n.Spec.ID, Kind: string(spec.KindBug),
					Title: n.Spec.Title, Score: score,
				})
			}
		}
	}
	return hits
}

func scoreDecision(n spec.DecisionNode, tokens []string) int {
	score := scoreField(n.Spec.ID, tokens, weightID)
	score += scoreField(n.Spec.Title, tokens, weightTitle)
	score += scoreField(n.Spec.Summary, tokens, weightSummary)
	score += scoreField(n.Spec.Rationale, tokens, weightBody)
	score += scoreField(n.Body, tokens, weightBody)
	if n.Spec.Provenance != nil {
		score += scoreField(n.Spec.Provenance.ArchitectRationale, tokens, weightBody)
	}
	for _, alt := range n.Spec.Alternatives {
		score += scoreField(alt.Name, tokens, weightBody)
		score += scoreField(alt.Rationale, tokens, weightBody)
		score += scoreField(alt.RejectedBecause, tokens, weightBody)
	}
	return score
}

func scoreFeature(n spec.FeatureNode, tokens []string) int {
	score := scoreField(n.Spec.ID, tokens, weightID)
	score += scoreField(n.Spec.Title, tokens, weightTitle)
	score += scoreField(n.Spec.Summary, tokens, weightSummary)
	score += scoreField(n.Spec.Description, tokens, weightBody)
	score += scoreField(n.Body, tokens, weightBody)
	for _, ac := range n.Spec.AcceptanceCriteria {
		score += scoreField(ac, tokens, weightBody)
	}
	return score
}

func scoreStrategy(n spec.StrategyNode, tokens []string) int {
	score := scoreField(n.Spec.ID, tokens, weightID)
	score += scoreField(n.Spec.Title, tokens, weightTitle)
	score += scoreField(n.Spec.Summary, tokens, weightSummary)
	score += scoreField(n.Body, tokens, weightBody)
	return score
}

func scoreApproach(n spec.ApproachNode, tokens []string) int {
	score := scoreField(n.Spec.ID, tokens, weightID)
	score += scoreField(n.Spec.Title, tokens, weightTitle)
	score += scoreField(n.Spec.Summary, tokens, weightSummary)
	score += scoreField(n.Spec.Body, tokens, weightBody)
	score += scoreField(n.Body, tokens, weightBody)
	return score
}

func scoreBug(n spec.BugNode, tokens []string) int {
	score := scoreField(n.Spec.ID, tokens, weightID)
	score += scoreField(n.Spec.Title, tokens, weightTitle)
	score += scoreField(n.Spec.Summary, tokens, weightSummary)
	score += scoreField(n.Spec.Description, tokens, weightBody)
	score += scoreField(n.Spec.RootCause, tokens, weightBody)
	score += scoreField(n.Spec.FixPlan, tokens, weightBody)
	score += scoreField(n.Body, tokens, weightBody)
	for _, step := range n.Spec.ReproductionSteps {
		score += scoreField(step, tokens, weightBody)
	}
	return score
}

// scoreField returns weight * (sum of token occurrences in haystack).
// Case-insensitive substring counting — sufficient for the slug + prose
// shapes the spec graph carries.
func scoreField(haystack string, tokens []string, weight int) int {
	if haystack == "" {
		return 0
	}
	folded := strings.ToLower(haystack)
	total := 0
	for _, tok := range tokens {
		total += strings.Count(folded, tok)
	}
	return weight * total
}

// tokenizeQuery splits on whitespace and lowercases. Empty / whitespace
// queries return a nil slice so the caller can reject them uniformly.
func tokenizeQuery(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	if len(fields) == 0 {
		return nil
	}
	return fields
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
