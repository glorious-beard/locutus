package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// advocateSystemPrompt is the spec advocate's instruction set. The
// agent receives the rendered explain output for one node, GOALS.md,
// and (in the adversarial path) the challenger's brief; it returns a
// structured defense.
//
// Inlined rather than loaded from `.borg/agents/spec_advocate.md` so
// `locutus justify` works on any project regardless of whether the
// user has run `locutus update --reset` to refresh scaffold files.
// Same pattern intake.go uses for the same reason.
const advocateSystemPrompt = `You are the spec advocate. A user has asked you to defend a specific
spec node — explain why this decision/feature/strategy/approach is the
right choice for this project given the goals, constraints, and
alternatives considered.

You receive:
- The full node content (rationale, alternatives, citations,
  back-references) under "## Node under review".
- GOALS.md (verbatim) under "## Goals".
- (Optional) The user's challenge prompt under "## Challenge from user".
- (Optional) The challenger's brief under "## Challenger's concerns".
- (Optional) Researcher's findings under "## Researcher's findings".
  When present, treat these as the load-bearing source of facts —
  they were produced by a grounded research pass and supersede your
  training-data recall on the questions they cover. Cite them
  explicitly when addressing the corresponding concerns.
- (Optional) Ungrounded research queries under "## Ungrounded research
  queries (search tool errored)". Listed queries had a web search
  failure during the research pass — the corresponding researcher
  finding's result is the model's training-data recall, NOT
  retrieved evidence. When you address a concern whose finding sits
  on top of one of these queries, you MUST: (a) acknowledge in your
  response that the supporting research was not retrieved during
  this call, and (b) treat the underlying claim with the same
  skepticism you would apply to any training-data-recall claim.
  Do not echo specific dates, version numbers, or third-party
  citations from a finding whose query is in this ungrounded list.

GROUNDING DISCIPLINE WHEN RESEARCH IS ABSENT.

If the "## Researcher's findings" section is missing or empty, you
have NO retrieved evidence for this call. In that case:

- Defend the spec node strictly on the rationale that already exists
  in the node content (## Node under review) and the goal clauses
  in ## Goals. Those are the only authoritative inputs.
- Do NOT make specific factual claims about competing technologies,
  vendors, or alternatives — version numbers, release dates, ecosystem
  maturity, hiring-pool size, library adapter quality, production
  case studies, vendor pricing, performance benchmarks, GitHub issue
  references, download counts, framework adoption percentages, or any
  similar quantitative or comparative assertion. These all require
  retrieved evidence to be defensible; without it you would be
  reciting training-data recall and presenting it as fact.
- When the user's challenge or the challenger's brief invokes a
  specific alternative (e.g. "use TanStack Start instead of Next.js"),
  it is acceptable to acknowledge the alternative's stated motivation
  and note that the spec node's listed reasons still apply. It is NOT
  acceptable to make specific claims about the alternative's current
  state, maturity, or ecosystem. If you would naturally write
  something like "Library X has the most mature adapter for…" or
  "Framework Y is still pre-1.0 as of …" or "Tool Z's adoption is
  around N% of developers…", STOP and replace it with: "I don't have
  grounded evidence about <X>'s current state at this time; the
  comparison rests on the spec node's stated rationale." It is far
  better to leave a comparison unmade than to fabricate one.
- If the challenger surfaced a concern that genuinely needs evidence
  to settle and none was retrieved, mark its still_stands honestly
  (often "false" — the concern stands as legitimately raised) rather
  than answering it with unsourced specifics.

This rule is symmetric with the researcher's anti-fallback directive.
Operators reading the trace will compare your prose against the
per-call tool_calls record (Anthropic web_search outcomes); claims
that exceed the retrieved evidence will be flagged as ungrounded.

Write a 2-4 paragraph defense in plain prose. Cover:
1. What problem this node solves and which goal-clauses motivate it.
2. Why the chosen path beats the listed alternatives, citing
   specific constraints (cost, performance, operational complexity,
   vendor relationships).
3. What this commits the project to that should be reconsidered if
   constraints change — i.e., the conditions under which this would
   NOT hold.

Be specific about which goal-clauses you cite. Avoid generic
language like "best practice" without a concrete reference.

When a challenger's brief is present, ALSO address each concern
point-by-point. For each concern:
- concern_summary: one line restating the challenger's point.
- response: the paragraph that addresses it.
- still_stands: whether the original spec node holds up on this
  point (true means the rationale answers the concern; false means
  the challenger surfaced a real gap).

Then set verdict to one of:
- "held_up" — every concern was answered; the node stands.
- "partially_held_up" — most concerns answered, one or two surfaced
  real gaps; the node needs a follow-up refine.
- "broke_down" — the challenge revealed that the chosen path is
  wrong or substantially incomplete.

When verdict is "partially_held_up" or "broke_down", populate
breaking_points with the specific gaps that need follow-up.

Respond with valid JSON matching the supplied schema.`

// researcherSystemPrompt is the justify-flow researcher's instruction
// set. Inlined for the same reason advocate/challenger are inlined —
// `locutus justify --against` works on any project regardless of
// whether the user has refreshed `.borg/agents/researcher.md`. Mirrors
// the council researcher's mandate (evidence-based, neutral, no
// advocacy) but is scoped to the challenger's concerns about a single
// spec node.
const researcherSystemPrompt = `You are a research investigator working alongside a spec
advocate and a spec challenger. The challenger has flagged
weaknesses in a specific spec node; your job is to investigate
those concerns with evidence, so the advocate's response addresses
reality rather than its own training data.

You are a neutral expert witness, not a participant in the debate.
Your job is to make claims verifiable.

You have web search available for this call. Use it to verify
version numbers, vendor status, current best-practice positions,
and any factual claim the challenger has raised that benefits from
checking against current material rather than your training data.

You receive:
- The full node content under "## Node under review".
- GOALS.md (verbatim) under "## Goals".
- The user's challenge prompt under "## Challenge".
- The challenger's concerns under "## Concerns to investigate".

For each concern, produce a Finding object:
- query: the specific factual question this concern raises.
- result: evidence-based analysis citing concrete data —
  version numbers, benchmarks, vendor positions, documented
  behavior. Cite retrieved sources where you used search.

DO NOT FALL BACK TO TRAINING-DATA RECALL when search fails. The two
failure modes are categorically different and must be reported
distinctly:

  1. SEARCH TOOL ERROR. Your web_search tool invocation returned
     a web_search_tool_result_error block (e.g. error_code
     unavailable, too_many_requests, max_uses_exceeded, or empty).
     This is a SYSTEM failure — the tool did not return retrievable
     content, period. Set result to literally:
       "search tool errored on '<your query>' — finding ungrounded;
       no evidence retrieved during this call."
     Do not substitute training-data recall. Do not invent dates,
     citations, version numbers, or source URLs from memory.
     Inventing them while the tool errored is fabrication, and
     downstream consumers WILL flag the finding as ungrounded
     against the per-call tool_calls record regardless of how
     authoritative your prose looks.

  2. SEARCH RETURNED NO RELEVANT RESULTS. The tool ran successfully
     but the returned results don't address the question — empty
     hits, off-topic pages, paywalled, or no consensus in the
     literature. Set result to literally:
       "search returned no relevant results for '<your query>' —
       finding ungrounded; insufficient evidence to determine this
       at this time."
     Same constraint: do not paper over with training-data recall.

  3. SEARCH SUCCEEDED. The tool returned relevant pages. Cite the
     URLs and titles you actually grounded against. Quote specific
     passages where they answer the question. Do not introduce
     citations that did not appear in the retrieved set, even if
     they corroborate your claim — operators will compare your
     citations against the per-call tool_calls record.

Skip concerns that are pure judgment calls with no factual
component (e.g., "this is over-engineered"). Investigate only
concerns where facts can inform the dispute. An empty Findings
list is a valid response when no concern admits factual
investigation.

Respond with valid JSON matching the supplied schema.`

// challengerSystemPrompt is the spec challenger's instruction set.
const challengerSystemPrompt = `You are the spec challenger. A user has flagged a possible weakness
in a specific spec node and wants you to formulate the strongest
version of that critique. You are an adversary to the spec, not an
ally — your job is to surface the genuine concerns the user implied,
not to be diplomatic.

You receive:
- The full node content under "## Node under review".
- GOALS.md (verbatim) under "## Goals".
- The user's challenge prompt under "## Challenge".

For each concrete concern the user's challenge implies, write:
- weakness: the specific weakness in the chosen approach.
- evidence: cite GOALS, known patterns, or current practice that
  supports the concern.
- counterproposal: an alternative or test that would resolve the
  question.

Output 2-5 concerns. Less is fine if the challenge is narrow.

Respond with valid JSON matching the supplied schema.`

// JustifyInputs bundles the project context the orchestrator needs
// for one justify run. Built by the cmd-layer wrapper from
// render.ExplainNode + readGoals; kept as a separate struct so tests
// can fabricate them without touching the FS.
type JustifyInputs struct {
	NodeID        string
	NodeMarkdown  string
	GoalsBody     string
	Challenge     string
	ChallengerOut *ChallengeBrief
	ResearcherOut *ResearchBrief
}

// RunJustify dispatches the spec_advocate agent against the rendered
// node + GOALS and returns its structured defense. No challenger
// involvement; the caller should leave Challenge and ChallengerOut
// empty on JustifyInputs.
func RunJustify(ctx context.Context, exec AgentExecutor, in JustifyInputs) (*JustificationBrief, error) {
	if in.NodeMarkdown == "" {
		return nil, fmt.Errorf("justify: empty node content for %q", in.NodeID)
	}

	def := AgentDef{
		ID:           "spec_advocate",
		SystemPrompt: advocateSystemPrompt,
		OutputSchema: "JustificationBrief",
	}
	user := buildAdvocateUserMessage(in)
	input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}

	var out JustificationBrief
	if err := RunInto(WithRole(ctx, "justification"), exec, def, input, &out); err != nil {
		return nil, fmt.Errorf("justify: advocate dispatch: %w", err)
	}
	if out.Defense == "" {
		return nil, fmt.Errorf("justify: advocate returned empty defense for %q", in.NodeID)
	}
	return &out, nil
}

// RunResearch dispatches the grounded researcher against the
// challenger's concerns. The caller must populate in.ChallengerOut
// (the researcher needs concerns to investigate); an empty challenge
// or challenger output is a programming error.
//
// Grounding is requested via AgentDef.Grounding so the executor
// attaches provider-native search (Gemini GoogleSearch, OpenAI
// web_search_preview, Anthropic web_search_20250305). Findings may be
// empty when the challenger's concerns are pure judgment calls with
// no factual component — that is an allowed outcome, not an error.
//
// Tool outcomes are inspected after the call: any web_search query
// that returned an error is logged at WARN with its query string and
// stamped onto ResearchBrief.ToolOutcomes so the advocate's prompt
// can flag findings that lack retrieval support. The model is told
// in researcherSystemPrompt to mark such findings as ungrounded
// rather than confabulate; the post-call check is a backstop against
// confabulation when the model ignores that directive.
func RunResearch(ctx context.Context, exec AgentExecutor, in JustifyInputs) (*ResearchBrief, error) {
	if in.NodeMarkdown == "" {
		return nil, fmt.Errorf("justify: empty node content for %q", in.NodeID)
	}
	if in.Challenge == "" {
		return nil, fmt.Errorf("justify: empty challenge")
	}
	if in.ChallengerOut == nil || len(in.ChallengerOut.Concerns) == 0 {
		return nil, fmt.Errorf("justify: research requires challenger concerns")
	}

	def := AgentDef{
		ID:           "researcher",
		SystemPrompt: researcherSystemPrompt,
		OutputSchema: "ResearchBrief",
		Grounding:    true,
	}
	user := buildResearcherUserMessage(in)
	input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}

	resp, err := exec.Run(WithRole(ctx, "research"), def, input)
	if err != nil {
		return nil, fmt.Errorf("justify: researcher dispatch: %w", err)
	}
	var brief ResearchBrief
	if err := unmarshalAgentOutput(resp.Content, &brief); err != nil {
		return nil, fmt.Errorf("justify: researcher response: %w", err)
	}

	// Stamp tool-call outcomes onto the brief so the advocate's
	// prompt can render an explicit "ungrounded queries" section,
	// and so a session-trace consumer (history --narrative,
	// post-mortem) can see grounding state without re-parsing the
	// raw_message blob.
	brief.ToolOutcomes = resp.ToolCalls

	if failed := brief.FailedQueries(); len(failed) > 0 {
		queries := make([]string, len(failed))
		codes := make([]string, len(failed))
		for i, tc := range failed {
			queries[i] = tc.Query
			codes[i] = tc.ErrorCode
		}
		// Loud at the operator level so a 40%-failure run like the
		// one that surfaced this bug isn't silently absorbed into
		// confidently-cited prose. The advocate's prompt also
		// receives this information so its rendered output
		// distinguishes grounded from ungrounded claims.
		slog.Warn("justify: research grounding incomplete — some web_search queries failed",
			"node_id", in.NodeID,
			"failed_count", len(failed),
			"total_queries", len(resp.ToolCalls),
			"failed_queries", queries,
			"error_codes", codes)
	}

	return &brief, nil
}

// RunJustifyAgainst dispatches the challenger, then the researcher
// (grounded) on the challenger's concerns, then the advocate with
// both upstream outputs. Returns the structured outputs from each
// step so the caller can render the full adversarial dialogue.
//
// The research hop puts retrieved facts into the advocate's context
// rather than letting the advocate confabulate from training data.
// It mirrors the council's critic → researcher → planner pattern.
//
// The challenge string MUST be non-empty; an empty one is a
// programming error caught by the cmd layer.
func RunJustifyAgainst(ctx context.Context, exec AgentExecutor, in JustifyInputs) (*ChallengeBrief, *ResearchBrief, *AdversarialDefense, error) {
	if in.Challenge == "" {
		return nil, nil, nil, fmt.Errorf("justify: empty challenge")
	}
	if in.NodeMarkdown == "" {
		return nil, nil, nil, fmt.Errorf("justify: empty node content for %q", in.NodeID)
	}

	challengeDef := AgentDef{
		ID:           "spec_challenger",
		SystemPrompt: challengerSystemPrompt,
		OutputSchema: "ChallengeBrief",
	}
	challengeUser := buildChallengerUserMessage(in)
	challengeInput := AgentInput{Messages: []Message{{Role: "user", Content: challengeUser}}}

	challenge, challengeErr := dispatchChallengerWithRetry(ctx, exec, challengeDef, challengeInput, in.NodeID)
	if challengeErr != nil {
		return challenge, nil, nil, challengeErr
	}

	researchIn := in
	researchIn.ChallengerOut = challenge

	research, err := RunResearch(ctx, exec, researchIn)
	if err != nil {
		return challenge, nil, nil, err
	}

	advocateIn := researchIn
	advocateIn.ResearcherOut = research

	advocateDef := AgentDef{
		ID:           "spec_advocate",
		SystemPrompt: advocateSystemPrompt,
		OutputSchema: "AdversarialDefense",
	}
	advocateUser := buildAdvocateUserMessage(advocateIn)
	advocateInput := AgentInput{Messages: []Message{{Role: "user", Content: advocateUser}}}

	var defense AdversarialDefense
	if err := RunInto(WithRole(ctx, "justification"), exec, advocateDef, advocateInput, &defense); err != nil {
		return challenge, research, nil, fmt.Errorf("justify: advocate dispatch: %w", err)
	}
	if defense.Defense == "" {
		return challenge, research, &defense, fmt.Errorf("justify: advocate returned empty defense for %q", in.NodeID)
	}
	if !validVerdict(defense.Verdict) {
		return challenge, research, &defense, fmt.Errorf("justify: advocate returned invalid verdict %q (want held_up|partially_held_up|broke_down)", defense.Verdict)
	}
	return challenge, research, &defense, nil
}

func buildAdvocateUserMessage(in JustifyInputs) string {
	parts := []string{"## Node under review\n\n" + in.NodeMarkdown}
	if in.GoalsBody != "" {
		parts = append(parts, "## Goals\n\n"+in.GoalsBody)
	}
	if in.Challenge != "" {
		parts = append(parts, "## Challenge from user\n\n"+in.Challenge)
	}
	if in.ChallengerOut != nil && len(in.ChallengerOut.Concerns) > 0 {
		parts = append(parts, "## Challenger's concerns\n\n"+formatConcerns(in.ChallengerOut.Concerns))
	}
	if in.ResearcherOut != nil && len(in.ResearcherOut.Findings) > 0 {
		parts = append(parts, "## Researcher's findings\n\n"+formatFindings(in.ResearcherOut.Findings))
	}
	// Surface ungrounded queries explicitly so the advocate can flag
	// findings whose retrieval support failed. The researcher is told
	// to mark such findings as ungrounded; this section is the
	// belt-and-suspenders for cases where the researcher confabulated
	// despite the directive. The advocate is told (in
	// advocateSystemPrompt) to treat ungrounded claims with skepticism.
	if in.ResearcherOut != nil {
		if failed := in.ResearcherOut.FailedQueries(); len(failed) > 0 {
			parts = append(parts, "## Ungrounded research queries (search tool errored)\n\n"+formatFailedQueries(failed))
		}
	}
	return joinSections(parts)
}

func formatFailedQueries(calls []ToolCall) string {
	out := "The following research queries returned a search tool error and produced NO retrieved evidence. Any researcher finding whose claims rest on these queries is ungrounded — treat the corresponding finding's `result` text as the model's training-data recall, not as evidence retrieved during this call. When you address the corresponding concern in your defense, do not cite the finding as authoritative; either acknowledge the gap explicitly or do not lean on it.\n\n"
	for i, c := range calls {
		line := fmt.Sprintf("%d. %s", i+1, c.Query)
		if c.ErrorCode != "" {
			line += fmt.Sprintf(" (error_code: %s)", c.ErrorCode)
		}
		out += line + "\n"
	}
	return out
}

func buildChallengerUserMessage(in JustifyInputs) string {
	parts := []string{"## Node under review\n\n" + in.NodeMarkdown}
	if in.GoalsBody != "" {
		parts = append(parts, "## Goals\n\n"+in.GoalsBody)
	}
	parts = append(parts, "## Challenge\n\n"+in.Challenge)
	return joinSections(parts)
}

func buildResearcherUserMessage(in JustifyInputs) string {
	parts := []string{"## Node under review\n\n" + in.NodeMarkdown}
	if in.GoalsBody != "" {
		parts = append(parts, "## Goals\n\n"+in.GoalsBody)
	}
	parts = append(parts, "## Challenge\n\n"+in.Challenge)
	if in.ChallengerOut != nil && len(in.ChallengerOut.Concerns) > 0 {
		parts = append(parts, "## Concerns to investigate\n\n"+formatConcerns(in.ChallengerOut.Concerns))
	}
	return joinSections(parts)
}

func formatConcerns(concerns []AdversarialConcern) string {
	out := ""
	for i, c := range concerns {
		out += fmt.Sprintf("%d. **Weakness:** %s\n   **Evidence:** %s\n   **Counterproposal:** %s\n\n",
			i+1, c.Weakness, c.Evidence, c.Counterproposal)
	}
	return out
}

func formatFindings(findings []Finding) string {
	out := ""
	for i, f := range findings {
		out += fmt.Sprintf("%d. **Query:** %s\n   **Result:** %s\n\n", i+1, f.Query, f.Result)
	}
	return out
}

func joinSections(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n\n"
		}
		out += p
	}
	return out
}

// challengerMaxAttempts caps how many times we'll dispatch the
// spec_challenger before giving up. The Anthropic-side schema-skeleton
// failure mode (DJ-108 native structured output + adaptive thinking)
// is intermittent — observed at ~50–67% per call against winplan in
// May 2026 — so a single retry typically clears it. Two attempts
// total covers the common case without burning user tokens on a
// runaway retry loop when the model is having a genuinely bad
// inference run.
//
// Increase only with evidence: a 3rd attempt against the same model
// state has rapidly diminishing returns and starts looking like
// stubbornness.
const challengerMaxAttempts = 2

// dispatchChallengerWithRetry runs the spec_challenger and applies
// the degenerate-output validator. On a degenerate result it logs
// the offending content at WARN and retries up to challengerMaxAttempts
// total dispatches. On a real LLM error (rate limit, timeout) the
// underlying RunInto path already retries via the executor's standard
// retry; this layer is specifically for the "model returned valid
// JSON but it's a schema skeleton" failure mode that a transport
// retry can't catch.
//
// Returns the last challenge produced (so the cmd layer can show the
// user what the model emitted on terminal failure) and a nil error
// on success, or the offending challenge plus a degenerate-output
// error after the attempt cap is reached.
func dispatchChallengerWithRetry(ctx context.Context, exec AgentExecutor, def AgentDef, input AgentInput, nodeID string) (*ChallengeBrief, error) {
	var last ChallengeBrief
	for attempt := 1; attempt <= challengerMaxAttempts; attempt++ {
		var challenge ChallengeBrief
		if err := RunInto(WithRole(ctx, "challenge"), exec, def, input, &challenge); err != nil {
			return nil, fmt.Errorf("justify: challenger dispatch: %w", err)
		}
		last = challenge
		if len(challenge.Concerns) == 0 {
			// Empty concerns is a valid "challenger found nothing
			// substantive to surface" outcome — not a transient
			// schema-skeleton failure. Fail immediately rather
			// than retrying since a retry would only churn the
			// model on a prompt the model already evaluated as
			// not-yielding-concerns.
			return &challenge, fmt.Errorf("justify: challenger returned no concerns for %q", nodeID)
		}
		if reason, degenerate := degenerateChallengerBrief(&challenge); degenerate {
			if attempt < challengerMaxAttempts {
				slog.Warn("justify: challenger emitted degenerate brief; retrying",
					"node_id", nodeID,
					"attempt", attempt,
					"max_attempts", challengerMaxAttempts,
					"reason", reason,
					"output", challenge)
				continue
			}
			// Final attempt produced a degenerate result. Give up
			// loudly with the offending content so the user can
			// see what happened and decide whether to re-run.
			return &challenge, fmt.Errorf("justify: challenger emitted degenerate brief for %q after %d attempts (%s); re-run, or report this if it persists", nodeID, challengerMaxAttempts, reason)
		}
		return &challenge, nil
	}
	// Unreachable in practice — the loop returns on every iteration —
	// but Go's flow analysis requires a terminal return.
	return &last, fmt.Errorf("justify: challenger retry loop exited without a result for %q", nodeID)
}

// challengerPlaceholderTokens lists the literal strings observed (or
// likely to be observed) when the model short-circuits to minimum-
// viable schema-conforming JSON instead of engaging with the prompt.
// Compared after lower-casing and trimming.
var challengerPlaceholderTokens = map[string]struct{}{
	"dummy":       {},
	"placeholder": {},
	"todo":        {},
	"tbd":         {},
	"foo":         {},
	"bar":         {},
	"baz":         {},
	"example":     {},
	"sample":      {},
	"lorem":       {},
	"ipsum":       {},
	"n/a":         {},
	"none":        {},
	"...":         {},
}

// degenerateChallengerBrief reports whether a ChallengeBrief looks
// like the schema-skeleton output mode rather than a real critique.
// Returns (reason, true) when degenerate; ("", false) otherwise.
//
// Triggers (any one is sufficient):
//
//   - Any concern field (weakness/evidence/counterproposal) is, after
//     trim+lowercase, in challengerPlaceholderTokens. The model
//     emitting "dummy" three times in a row is the canonical case
//     this exists to catch.
//   - Any concern field is shorter than minChallengerFieldLen runes.
//     A real weakness/evidence/counterproposal is at least a short
//     sentence; one-word answers are a placeholder.
//   - Every concern in the brief is an exact duplicate of another
//     (same weakness AND evidence AND counterproposal). Real critiques
//     don't repeat themselves; minimum-JSON output sometimes does.
//
// The minimum length is intentionally permissive: the goal is to
// catch obvious skeletons, not to enforce prose quality. A well-
// engaged challenger emits paragraphs; the floor here is barely
// "would this be a real sentence?"
const minChallengerFieldLen = 20

func degenerateChallengerBrief(b *ChallengeBrief) (string, bool) {
	if b == nil || len(b.Concerns) == 0 {
		return "", false
	}
	for i, c := range b.Concerns {
		for label, val := range map[string]string{
			"weakness":        c.Weakness,
			"evidence":        c.Evidence,
			"counterproposal": c.Counterproposal,
		} {
			trimmed := strings.TrimSpace(val)
			lower := strings.ToLower(trimmed)
			if _, isPlaceholder := challengerPlaceholderTokens[lower]; isPlaceholder {
				return fmt.Sprintf("concern[%d].%s is a known placeholder token %q", i, label, trimmed), true
			}
			if len([]rune(trimmed)) < minChallengerFieldLen {
				return fmt.Sprintf("concern[%d].%s is shorter than %d runes (%q)", i, label, minChallengerFieldLen, trimmed), true
			}
		}
	}
	if len(b.Concerns) >= 2 {
		first := b.Concerns[0]
		allDup := true
		for _, c := range b.Concerns[1:] {
			if c.Weakness != first.Weakness || c.Evidence != first.Evidence || c.Counterproposal != first.Counterproposal {
				allDup = false
				break
			}
		}
		if allDup {
			return fmt.Sprintf("all %d concerns are exact duplicates", len(b.Concerns)), true
		}
	}
	return "", false
}

func validVerdict(v string) bool {
	switch v {
	case "held_up", "partially_held_up", "broke_down":
		return true
	}
	return false
}
