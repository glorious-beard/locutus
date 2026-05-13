//go:build eval

// Package agent_test (build-tagged "eval") tests whether fast-tier
// models can reliably convert reasoning prose into schema-conformant
// JSON. This is the empirical check before we commit to the two-call
// "reason -> format" architecture: the format step delegates to a
// fast-tier model with no thinking, no tools, and OutputSchema set.
// If fast tiers can't reliably emit conformant JSON from prose, the
// architecture doesn't work.
//
// Gated behind a build tag because it costs real API credits and
// needs ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY (or
// GOOGLE_API_KEY). Run via:
//
//   go test -tags=eval -v -run FastTierFormatter ./internal/agent/
//
// The eval runs each fast-tier model against a set of reasoning-prose
// fixtures, asks it to emit a ChallengeBrief (the proven-failing
// schema in the production trace), and scores each output on:
//
//   - Parses as valid JSON
//   - Conforms to the schema shape
//   - No placeholder tokens in any string field (dummy / TBD / ...)
//   - Each concern field is at least minimal sentence length (defeats
//     "ok"/"yes"/"no" emit modes that pass schema but say nothing)
//   - Content is preserved from the input prose (does the formatter
//     hallucinate or copy?)
//
// Each cell is run multiple times to surface stochastic failures —
// fast-tier formatter has to be RELIABLE, not just "works once".
package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/agent/adapters"
	"github.com/joho/godotenv"
)

// formatterMatrix names the fast-tier cells the eval exercises.
// Fast tier only: the production formatter step targets the fast tier
// (Haiku / Flash-Lite / gpt-5-mini) because it's the cheapest path
// and because fast tiers ship with thinking off — exactly the regime
// where native structured output is known to work well.
var formatterMatrix = []modelTier{
	{"anthropic", "fast", "claude-haiku-4-5-20251001"},
	{"openai", "fast", "gpt-5-mini"},
	{"googleai", "fast", "gemini-3.1-flash-lite-preview"},
}

// formatterFixture is one reasoning-prose input paired with the
// minimum substantive content the formatter must preserve. The
// reasoning column mimics what a thinking-on Sonnet challenger
// produces before its emit step degenerates (see winplan trace
// 20260513/1236/19-a32fce/calls/0002 round-2 thinking).
type formatterFixture struct {
	name           string
	prose          string
	expectConcerns int      // minimum concerns the formatter should produce
	mustMention    []string // substrings that must appear somewhere in the JSON output
}

var challengeFixtures = []formatterFixture{
	{
		name: "gcp-vs-aws-azure-cost",
		prose: `The decision under review deploys Next.js + pg-boss on GCP Cloud Run.
The user challenge asks why not AWS or Azure given both have generous free tiers.

I see four substantive concerns:

1. The "already committed to GCP" reasoning is circular. The decision rejects
   AWS ECS Fargate because "all other infrastructure is already committed to
   GCP", but that initial GCP commitment was never justified against AWS or
   Azure on cost merits. A foundational compute decision can't lean on the
   assumption that GCP is already chosen.

2. Azure Container Apps is entirely missing from the alternatives. It
   provides scale-to-zero, long-running containers, and a consumption plan
   with a generous free grant (180k vCPU-seconds, 360k GB-seconds/month).
   A direct functional peer to Cloud Run wasn't evaluated.

3. AWS App Runner and ECS Fargate with Spot offer similar economics for
   election-cycle traffic profiles. The decision treats AWS only in the
   context of a hybrid (keep GCP, add AWS), never as a full migration.

4. The architecture creates significant GCP lock-in across compute,
   storage, secrets, and logging without explicitly acknowledging the
   trade-off or naming the conditions under which it should be revisited.

Counterproposals: do an explicit cost comparison across the three clouds'
free-tier and consumption pricing during the off-cycle months; add Azure
Container Apps as a named rejected alternative with rationale; document
the GCP-stack lock-in as a separate strategic decision with its own
justification.`,
		expectConcerns: 3,
		mustMention:    []string{"Azure", "AWS", "GCP", "Cloud Run"},
	},
	{
		name: "postgres-vs-mongo-write-load",
		prose: `The node commits to Postgres for OLTP storage and rejects MongoDB.
The user challenge: "won't we hit write contention on the leaderboard table?"

Two concerns:

- The rationale says "Postgres handles write contention fine via MVCC and
  row-level locking" but the leaderboard table is a hot-row pattern (every
  game-end appends a row, every page-load reads the top N). MVCC doesn't
  eliminate buffer-pool contention on the index pages a leaderboard reads
  from; the rejection of MongoDB on grounds of "we don't need document
  flexibility" sidesteps the actual write-throughput question.

- The decision lists "managed Postgres on RDS" but doesn't specify
  partitioning, replicas, or connection pooling. Under election-week load
  the leaderboard is the hottest object in the schema; without partitioning
  or a separate read replica for top-N queries, the rationale that "Postgres
  is fine" rests on assumptions the decision never tested.

Counterproposals: name the partitioning strategy (e.g. weekly partitions on
finished_at) and the connection-pool architecture (PgBouncer transaction
mode); benchmark the hot-row pattern under target QPS before locking in.`,
		expectConcerns: 2,
		mustMention:    []string{"Postgres", "leaderboard", "partitioning"},
	},
}

// formatterPrompt is the canonical "reformatter" system prompt — what
// the production output_formatter agent will use. Tests with this
// prompt to keep the eval honest: if a future formatter agent diverges
// from this prompt, this test no longer predicts production behavior.
func formatterPrompt() string {
	return `You receive a reasoning agent's prose output. Extract the structured content into a JSON object matching the supplied schema.

Rules:
- Do not reason about the underlying topic. Your job is extraction, not analysis.
- Do not invent facts that aren't in the prose. If a field has no source in the prose, omit it (when optional) or copy the closest matching content.
- Preserve specifics: vendor names, version numbers, citations, quoted text.
- Each schema field receives the corresponding content from the prose. If the prose names N items, emit N entries — not more, not fewer.

Respond with valid JSON matching the supplied schema. No preamble, no markdown fences.`
}

// formatterResult captures one (cell, run) outcome.
type formatterResult struct {
	fixture       string
	parseOK       bool
	parseErr      string
	concernsCount int
	placeholders  []string
	shortFields   []string
	missingTerms  []string
	rawOutput     string
	latency       time.Duration
	err           string
}

// runFormatter calls one model with prose + ChallengeBrief schema and
// returns the parsed concerns plus per-field diagnostics.
func runFormatter(ctx context.Context, mt modelTier, fx formatterFixture) formatterResult {
	r := formatterResult{fixture: fx.name}

	adapter, err := adapterFor(ctx, mt.provider)
	if err != nil {
		r.err = err.Error()
		return r
	}

	schema, err := agent.SchemaFor("ChallengeBrief")
	if err != nil {
		r.err = fmt.Sprintf("schema reflect: %v", err)
		return r
	}

	user := "## Reasoning prose to extract\n\n" + fx.prose
	req := adapters.Request{
		Model:           mt.model,
		SystemPrompt:    formatterPrompt(),
		Messages:        []adapters.Message{{Role: adapters.RoleUser, Content: user}},
		MaxOutputTokens: 4096,
		Thinking:        adapters.ThinkingOff,
		OutputSchema:    schema,
	}

	start := time.Now()
	resp, err := adapter.Run(ctx, req)
	r.latency = time.Since(start)
	if err != nil {
		r.err = err.Error()
		return r
	}
	r.rawOutput = resp.Content

	body := extractJSON(resp.Content)
	var brief agent.ChallengeBrief
	if err := json.Unmarshal([]byte(body), &brief); err != nil {
		r.parseErr = err.Error()
		return r
	}
	r.parseOK = true
	r.concernsCount = len(brief.Concerns)

	// Placeholder check — same forbidden list as the schema-conventions
	// test. A formatter that emits "dummy" is broken.
	forbidden := []string{"dummy", "placeholder", "tbd", "foo", "bar", "baz", "lorem", "ipsum"}
	checkField := func(name, val string) {
		low := strings.ToLower(strings.TrimSpace(val))
		for _, tok := range forbidden {
			if low == tok {
				r.placeholders = append(r.placeholders, name+"="+val)
				return
			}
		}
		if len(strings.TrimSpace(val)) < 20 {
			r.shortFields = append(r.shortFields, fmt.Sprintf("%s (len=%d)", name, len(strings.TrimSpace(val))))
		}
	}
	for i, c := range brief.Concerns {
		checkField(fmt.Sprintf("concerns[%d].weakness", i), c.Weakness)
		checkField(fmt.Sprintf("concerns[%d].evidence", i), c.Evidence)
		checkField(fmt.Sprintf("concerns[%d].counterproposal", i), c.Counterproposal)
	}

	// Content-preservation check — must-mention terms.
	combined := strings.ToLower(string(body))
	for _, term := range fx.mustMention {
		if !strings.Contains(combined, strings.ToLower(term)) {
			r.missingTerms = append(r.missingTerms, term)
		}
	}

	return r
}

// scoreFormatter assigns a 0..3 score per run:
//   - +1 parses
//   - +1 no placeholders AND no short fields AND concerns count >= expected
//   - +1 preserves all must-mention terms
func scoreFormatter(r formatterResult, fx formatterFixture) int {
	score := 0
	if r.parseOK {
		score++
	}
	if r.parseOK && len(r.placeholders) == 0 && len(r.shortFields) == 0 && r.concernsCount >= fx.expectConcerns {
		score++
	}
	if r.parseOK && len(r.missingTerms) == 0 {
		score++
	}
	return score
}

// TestFastTierFormatterReliability runs each fast-tier cell against
// the fixture set N times and prints a reliability table. The test
// always passes — the artifact is the report. Treat persistent
// failures (score < max-1) as a signal that the two-call architecture
// won't be reliable on that cell, NOT as a test failure to ignore.
func TestFastTierFormatterReliability(t *testing.T) {
	for _, p := range []string{".env", "../.env", "../../.env"} {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			break
		}
	}

	ctx := context.Background()
	const runsPerCell = 3 // surface stochastic failures cheaply

	type aggregateRow struct {
		mt       modelTier
		fixture  string
		runs     []formatterResult
		scores   []int
		maxScore int
	}

	var rows []aggregateRow
	for _, mt := range formatterMatrix {
		// Skip if env not set (adapterFor surfaces a clean error).
		if _, err := adapterFor(ctx, mt.provider); err != nil {
			t.Logf("skipping %s/%s: %v", mt.provider, mt.tier, err)
			continue
		}
		for _, fx := range challengeFixtures {
			row := aggregateRow{mt: mt, fixture: fx.name, maxScore: 3}
			for i := 0; i < runsPerCell; i++ {
				r := runFormatter(ctx, mt, fx)
				if r.err != "" && errors.Is(ctx.Err(), context.Canceled) {
					t.Fatalf("context cancelled mid-eval: %v", ctx.Err())
				}
				row.runs = append(row.runs, r)
				row.scores = append(row.scores, scoreFormatter(r, fx))
			}
			rows = append(rows, row)
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].mt.provider != rows[j].mt.provider {
			return rows[i].mt.provider < rows[j].mt.provider
		}
		return rows[i].fixture < rows[j].fixture
	})

	var b strings.Builder
	b.WriteString("\n=== Fast-tier formatter reliability eval ===\n\n")
	b.WriteString("| Provider | Model | Fixture | Scores (per run) | Mean | Total runs | Successful | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, row := range rows {
		var scoreStrs []string
		successful := 0
		sum := 0
		var notes []string
		for i, s := range row.scores {
			scoreStrs = append(scoreStrs, fmt.Sprintf("%d/%d", s, row.maxScore))
			sum += s
			if s == row.maxScore {
				successful++
			}
			r := row.runs[i]
			if r.err != "" {
				notes = append(notes, fmt.Sprintf("run %d err: %s", i+1, truncate(r.err, 60)))
			} else if r.parseErr != "" {
				notes = append(notes, fmt.Sprintf("run %d parse: %s", i+1, truncate(r.parseErr, 60)))
			} else if len(r.placeholders) > 0 {
				notes = append(notes, fmt.Sprintf("run %d placeholders: %s", i+1, strings.Join(r.placeholders, ",")))
			} else if len(r.shortFields) > 0 {
				notes = append(notes, fmt.Sprintf("run %d short: %s", i+1, strings.Join(r.shortFields, ",")))
			} else if len(r.missingTerms) > 0 {
				notes = append(notes, fmt.Sprintf("run %d missing: %s", i+1, strings.Join(r.missingTerms, ",")))
			}
		}
		mean := float64(sum) / float64(len(row.scores))
		notesStr := strings.Join(notes, "; ")
		if notesStr == "" {
			notesStr = "—"
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %.2f | %d | %d | %s |\n",
			row.mt.provider, row.mt.model, row.fixture,
			strings.Join(scoreStrs, ", "), mean, len(row.scores), successful,
			truncate(notesStr, 120))
	}

	// Per-cell sample output for the first successful run (helps the
	// operator eyeball whether content preservation is real or
	// superficial).
	b.WriteString("\n=== Sample outputs (first successful run per cell) ===\n\n")
	for _, row := range rows {
		var sample formatterResult
		for _, r := range row.runs {
			if r.parseOK {
				sample = r
				break
			}
		}
		if !sample.parseOK {
			continue
		}
		fmt.Fprintf(&b, "**%s / %s / %s**\n```json\n%s\n```\n\n",
			row.mt.provider, row.mt.model, row.fixture,
			truncate(sample.rawOutput, 2000))
	}

	t.Log(b.String())
}
