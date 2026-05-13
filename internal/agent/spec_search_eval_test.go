//go:build eval

// Package agent_test (build-tagged "eval") runs the spec_search
// interpretation eval across Anthropic, OpenAI, and Google AI tiers.
// Gated behind a build tag because it costs real API credits and
// needs ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY (or
// GOOGLE_API_KEY) set in the environment. Run via:
//
//   go test -tags=eval -v -run SpecSearchInterpretation ./internal/agent/
//
// The eval answers the question "if an LLM consumes a spec_search
// response, can it correctly distinguish topically-relevant hits
// from incidental matches?" The fixture corpus mimics the winplan
// auth pattern (one clean-signal auth decision, one pgbouncer-style
// alternative-only match, one author-stem polysemy hit, etc.); the
// rubric scores each model on whether it puts each hit in the right
// bucket. Output is a markdown table printed via t.Log so the
// operator running the eval has an artifact to commit alongside
// any follow-up design decisions.
package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/agent/adapters"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

// fixtureCorpus builds a tempdir-rooted project with five spec nodes
// chosen so the right classification is unambiguous from the
// per-field diagnostics alone. Returns the project root.
//
// Right answers (ground truth for the rubric):
//
//   Relevant to "authentication":
//     - dec-adopt-workos-for-authentication — clean signal:
//       title and summary stem to `authent`, rationale references
//       the auth flow.
//
//   Incidental (matches the query but is NOT about authentication):
//     - dec-deploy-pgbouncer — only matches in alternative
//       (rejected pooler options discuss auth_type handling).
//     - dec-pdf-report-renderer — only matches the `author` stem,
//       not `authent`; "author" refers to PDF report authors, not
//       authentication.
//     - dec-ci-negative-testing — same `author` stem trap.
//     - strat-data-store — incidental body mention (auth_type in
//       pg_hba.conf reference); not a decision about auth.
func fixtureCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".borg"), 0o755))
	manifest := map[string]any{
		"project_name": "eval",
		"version":      "0.0.1",
		"created_at":   time.Now().UTC(),
	}
	mb, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".borg", "manifest.json"), mb, 0o644))
	for _, k := range []string{"features", "strategies", "decisions", "bugs", "approaches"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".borg", "spec", k), 0o755))
	}
	fsys := specio.NewOSFS(root)

	// Relevant: authentication decision with clean title/summary/
	// rationale signal. Alternatives mention `auth.js` and `auth0`
	// shorthand (real); provenance carries citations and rationale.
	writeDecisionFull(t, fsys, spec.Decision{
		ID:         "dec-adopt-workos-for-authentication",
		Title:      "Adopt WorkOS for authentication and SSO",
		Summary:    "Authenticate operators via WorkOS SSO with OIDC.",
		Status:     spec.DecisionStatusActive,
		Confidence: 0.9,
		Rationale:  "WorkOS centralises auth across web and mobile. Auth.js handles the OIDC handshake; sessions live in Postgres.",
		Alternatives: []spec.Alternative{
			{
				Name:            "Auth0",
				Rationale:       "Auth0 is mature.",
				RejectedBecause: "Pricing scales steeply with monthly active users; WorkOS pricing fits our cohort.",
			},
		},
	})

	// Incidental (alternative-only): pgbouncer decision. The only
	// auth mention lives in a rejected alternative's RejectedBecause.
	writeDecisionFull(t, fsys, spec.Decision{
		ID:         "dec-deploy-pgbouncer",
		Title:      "Deploy PgBouncer for connection pooling",
		Summary:    "Use PgBouncer in transaction mode for connection multiplexing.",
		Status:     spec.DecisionStatusActive,
		Confidence: 0.8,
		Rationale:  "PgBouncer is mature; transaction mode fits our short-lived sessions.",
		Alternatives: []spec.Alternative{
			{
				Name:            "Odyssey",
				Rationale:       "Newer pooler.",
				RejectedBecause: "Odyssey's auth handoff lacks SCRAM passthrough and the auth_type configuration is awkward.",
			},
		},
	})

	// Incidental (`author` stem polysemy trap): PDF report decision.
	// Title says "report authorship"; body discusses PDF authors.
	// Never about authentication; matches "auth*" via the `author`
	// stem only.
	writeDecisionFull(t, fsys, spec.Decision{
		ID:         "dec-pdf-report-renderer",
		Title:      "Use @react-pdf/renderer for report authoring",
		Summary:    "Generate PDF reports server-side; preserve report authorship metadata.",
		Status:     spec.DecisionStatusActive,
		Confidence: 0.8,
		Rationale:  "Author bylines must survive export. The library handles authored layouts.",
	})

	// Incidental (`author` stem): CI testing decision. Same trap.
	writeDecisionFull(t, fsys, spec.Decision{
		ID:         "dec-ci-negative-testing",
		Title:      "CI-enforced negative testing",
		Summary:    "Author negative test cases alongside every new feature.",
		Status:     spec.DecisionStatusActive,
		Confidence: 0.7,
		Rationale:  "Every author of a new feature is responsible for the negative cases. Reviewers verify authored coverage.",
	})

	// Incidental (body-only): strategy whose body briefly references
	// pg_hba.conf auth_type. Not a decision about authentication.
	body := "Postgres with PostGIS for OLTP. Roles configured via pg_hba.conf with auth_type=scram-sha-256."
	require.NoError(t, specio.SaveMarkdown(fsys, ".borg/spec/strategies/strat-data-store.md", spec.Strategy{
		ID:      "strat-data-store",
		Title:   "Data layer: Postgres with PostGIS",
		Summary: "OLTP store, spatial indexing, materialised views for reporting.",
		Kind:    spec.StrategyKindFoundational,
		Status:  "active",
	}, body))

	// Need the json sidecar too — SaveMarkdown is markdown-only. For
	// strategies, SavePair writes both.
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/strategies/strat-data-store", spec.Strategy{
		ID:      "strat-data-store",
		Title:   "Data layer: Postgres with PostGIS",
		Summary: "OLTP store, spatial indexing, materialised views for reporting.",
		Kind:    spec.StrategyKindFoundational,
		Status:  "active",
	}, body))

	return root
}

func writeDecisionFull(t *testing.T, fsys specio.FS, d spec.Decision) {
	t.Helper()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
		d.UpdatedAt = d.CreatedAt
	}
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/decisions/"+d.ID, d, ""))
}

// expectedRelevant is the ground-truth set of IDs that ARE about
// authentication. Anything not in this set, that the model placed in
// the "relevant" bucket, is a false positive.
var expectedRelevant = map[string]bool{
	"dec-adopt-workos-for-authentication": true,
}

// expectedIncidental is the ground-truth set of IDs that match the
// query but aren't about authentication. The eval scores +1 for each
// of these the model correctly identifies as incidental.
var expectedIncidental = map[string]bool{
	"dec-deploy-pgbouncer":    true,
	"dec-pdf-report-renderer": true,
	"dec-ci-negative-testing": true,
	"strat-data-store":        true,
}

// modelTier describes one cell in the (provider, tier, model) grid.
// model is the concrete provider-side identifier; tier is the
// label the rubric prints. label is the human-friendly cell name
// used in the markdown table.
type modelTier struct {
	provider string
	tier     string
	model    string
}

// modelMatrix is the (provider, tier, model) grid the eval exercises.
// Pulled by hand from internal/agent/models.yaml; if that file's
// model strings change, this list needs to follow. Keeping the eval
// list explicit (rather than reading models.yaml) means a
// provider-yaml refactor can't silently change what the eval runs.
var modelMatrix = []modelTier{
	{"anthropic", "strong", "claude-opus-4-7"},
	{"anthropic", "balanced", "claude-sonnet-4-6"},
	{"anthropic", "fast", "claude-haiku-4-5-20251001"},
	// o3-pro 404s on /v1/responses with this account; gpt-5 is the
	// honest "strong" stand-in for OpenAI until that's sorted.
	{"openai", "strong", "gpt-5"},
	{"openai", "balanced", "gpt-5"},
	{"openai", "fast", "gpt-5-mini"},
	// The gemini-3.x preview models all 429'd on first call — likely
	// no preview access on this key. Falling back to the stable
	// 2.5 family which the same key can reach.
	{"googleai", "strong", "gemini-2.5-pro"},
	{"googleai", "balanced", "gemini-2.5-flash"},
	{"googleai", "fast", "gemini-2.5-flash-lite"},
}

// classification is the model's answer shape. The prompt asks for
// this exact JSON so parsing is straightforward.
type classification struct {
	Relevant   []string `json:"relevant"`
	Incidental []string `json:"incidental"`
	Reasoning  string   `json:"reasoning"`
}

// rubric scores a classification against ground truth. Five points
// possible: 1 for the relevant decision being marked relevant; 1 for
// each of the four incidental nodes being marked incidental. False
// positives (incidental in relevant set) cost a point each, floored
// at zero.
func rubric(c classification) (int, int, []string) {
	maxScore := 1 + len(expectedIncidental)
	score := 0
	var notes []string

	relevantSet := make(map[string]bool, len(c.Relevant))
	for _, id := range c.Relevant {
		relevantSet[id] = true
	}
	incidentalSet := make(map[string]bool, len(c.Incidental))
	for _, id := range c.Incidental {
		incidentalSet[id] = true
	}

	for id := range expectedRelevant {
		if relevantSet[id] {
			score++
		} else {
			notes = append(notes, "missed relevant: "+id)
		}
	}
	for id := range expectedIncidental {
		if incidentalSet[id] {
			score++
		} else if relevantSet[id] {
			notes = append(notes, "false positive: "+id)
		} else {
			notes = append(notes, "missed incidental: "+id)
		}
	}
	if score < 0 {
		score = 0
	}
	return score, maxScore, notes
}

// adapterFor returns the right adapter for a provider name. Returns
// nil + skip-style error when the env var isn't set so the caller can
// drop that provider's rows cleanly.
func adapterFor(ctx context.Context, provider string) (adapters.Adapter, error) {
	switch provider {
	case "anthropic":
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return nil, errors.New("ANTHROPIC_API_KEY not set")
		}
		return adapters.NewAnthropicAdapter()
	case "openai":
		if os.Getenv("OPENAI_API_KEY") == "" {
			return nil, errors.New("OPENAI_API_KEY not set")
		}
		return adapters.NewOpenAIResponsesAdapter()
	case "googleai":
		if os.Getenv("GEMINI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
			return nil, errors.New("GEMINI_API_KEY / GOOGLE_API_KEY not set")
		}
		return adapters.NewGeminiAdapter(ctx)
	}
	return nil, fmt.Errorf("unknown provider %q", provider)
}

// buildPrompt embeds the SpecSearchToolDescription verbatim (the
// production tool description the model would see in any real call)
// and presents the spec_search JSON, asking for a strict
// classification response.
//
// The model never CALLS the tool here — we pre-ran the search and
// hand it the output. The eval question is "can the model interpret
// what the tool returned?" — orthogonal to tool-calling itself.
func buildPrompt(searchResultJSON string) (system, user string) {
	system = "You are a research assistant interpreting search results from a project's spec graph. " +
		"Respond ONLY with the JSON object the user asks for — no preamble, no markdown fences, no commentary outside the JSON."

	user = "The spec_search tool is documented as follows:\n\n---\n" +
		agent.SpecSearchToolDescription + "\n---\n\n" +
		"I asked spec_search for `auth*`, intending to find decisions ABOUT authentication. " +
		"Here is the response:\n\n```json\n" + searchResultJSON + "\n```\n\n" +
		"Classify each hit into one of two buckets:\n" +
		"- `relevant`: the node is actually about authentication.\n" +
		"- `incidental`: the node matched the query but isn't about authentication " +
		"(e.g. matches via rejected alternatives, or via the `author` stem rather than `authent`).\n\n" +
		"Respond with this exact JSON shape (and nothing else):\n" +
		"```json\n" +
		"{\n" +
		"  \"relevant\": [\"id1\", ...],\n" +
		"  \"incidental\": [\"id2\", ...],\n" +
		"  \"reasoning\": \"one or two sentences\"\n" +
		"}\n" +
		"```"
	return system, user
}

// extractJSON pulls the first JSON object from the model's response,
// tolerating models that wrap their answer in markdown fences despite
// the system-prompt instruction. Returns the raw object slice ready
// for json.Unmarshal.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json ... ``` fences.
	if strings.HasPrefix(s, "```") {
		// Find the first newline, drop the language tag line, then
		// trim the trailing fence.
		if nl := strings.Index(s, "\n"); nl != -1 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end != -1 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// callModel runs one model with the eval prompt and returns the
// parsed classification (or an error message).
func callModel(ctx context.Context, mt modelTier, system, user string) (classification, error) {
	adapter, err := adapterFor(ctx, mt.provider)
	if err != nil {
		return classification{}, err
	}
	req := adapters.Request{
		Model:           mt.model,
		SystemPrompt:    system,
		Messages:        []adapters.Message{{Role: adapters.RoleUser, Content: user}},
		MaxOutputTokens: 4096,
		Thinking:        adapters.ThinkingOff,
	}
	resp, err := adapter.Run(ctx, req)
	if err != nil {
		return classification{}, err
	}
	body := extractJSON(resp.Content)
	var c classification
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		return classification{}, fmt.Errorf("parse JSON: %w (raw=%.200s)", err, resp.Content)
	}
	return c, nil
}

// TestSpecSearchInterpretation exercises the (provider, tier, model)
// grid and prints a markdown table summarising each model's grading.
// Always passes — the artifact IS the table; this isn't a regression
// guard, it's a data-collection run.
func TestSpecSearchInterpretation(t *testing.T) {
	// Mirror the main binary's dotenv behaviour so API keys land in
	// the environment from the project's .env file when present.
	// Walks up to two parents so the test still finds the repo's .env
	// regardless of which package directory go test was invoked from.
	for _, p := range []string{".env", "../.env", "../../.env"} {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			break
		}
	}
	ctx := context.Background()
	root := fixtureCorpus(t)
	fsys := specio.NewOSFS(root)

	// Run a real spec_search call against the fixture corpus so the
	// JSON the model sees is byte-identical to what would land in a
	// real conversation.
	result, err := agent.SearchSpecNodes(fsys, root, agent.SpecSearchInput{
		Query: "auth*",
		Limit: 50,
	})
	require.NoError(t, err)
	require.Greater(t, len(result.Hits), 0, "fixture should produce hits")

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	system, user := buildPrompt(string(resultJSON))

	t.Logf("=== Fixture spec_search response ===\n%s\n", string(resultJSON))

	type row struct {
		mt           modelTier
		score        int
		max          int
		err          string
		notes        []string
		reasoning    string
		hits         classification
		latency      time.Duration
	}
	rows := make([]row, 0, len(modelMatrix))

	for _, mt := range modelMatrix {
		r := row{mt: mt}
		start := time.Now()
		hits, err := callModel(ctx, mt, system, user)
		r.latency = time.Since(start)
		if err != nil {
			r.err = err.Error()
			rows = append(rows, r)
			continue
		}
		score, max, notes := rubric(hits)
		r.score = score
		r.max = max
		r.notes = notes
		r.reasoning = hits.Reasoning
		r.hits = hits
		rows = append(rows, r)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		// Group by provider, then tier-strength (strong > balanced > fast).
		if rows[i].mt.provider != rows[j].mt.provider {
			return rows[i].mt.provider < rows[j].mt.provider
		}
		return tierRank(rows[i].mt.tier) < tierRank(rows[j].mt.tier)
	})

	var b strings.Builder
	b.WriteString("\n=== spec_search interpretation eval ===\n\n")
	b.WriteString("| Provider | Tier | Model | Score | Latency | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, r := range rows {
		if r.err != "" {
			fmt.Fprintf(&b, "| %s | %s | `%s` | — | — | error: %s |\n",
				r.mt.provider, r.mt.tier, r.mt.model, truncate(r.err, 60))
			continue
		}
		notes := strings.Join(r.notes, "; ")
		if notes == "" {
			notes = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | %d/%d | %s | %s |\n",
			r.mt.provider, r.mt.tier, r.mt.model,
			r.score, r.max, r.latency.Round(time.Millisecond), truncate(notes, 80))
	}
	b.WriteString("\n=== Per-model reasoning ===\n\n")
	for _, r := range rows {
		if r.err != "" {
			continue
		}
		fmt.Fprintf(&b, "- **%s/%s/%s** (%d/%d): %s\n",
			r.mt.provider, r.mt.tier, r.mt.model, r.score, r.max, r.reasoning)
	}
	t.Log(b.String())
}

func tierRank(t string) int {
	switch t {
	case "strong":
		return 0
	case "balanced":
		return 1
	case "fast":
		return 2
	}
	return 3
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
