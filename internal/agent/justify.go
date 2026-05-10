package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// The advocate, challenger, and researcher prompts previously lived
// inline in this file. They now live as scaffold .md files —
// internal/scaffold/agents/{spec_advocate,spec_challenger,
// justify_researcher}.md — loaded via scaffold.LoadAgent at the cmd
// layer with embedded fallback for projects that haven't run
// `update --reset`. The cmd layer threads the loaded AgentDef values
// through JustifyInputs.{Advocate,Challenger,Researcher}.
//
// Why move them: prompt iteration shouldn't require recompiling
// locutus. The original inlining argument (DJ-101) cited the need
// for a "stable contract regardless of bootstrap state" — but
// scaffold.LoadAgent already has embedded fallback, so the same
// stability holds without the hard-coded prompts.

// JustifyInputs bundles the project context the orchestrator needs
// for one justify run. Built by the cmd-layer wrapper from
// render.ExplainNode + readGoals; kept as a separate struct so tests
// can fabricate them without touching the FS.
//
// Advocate / Challenger / Researcher carry the loaded AgentDef for
// each role. The cmd layer populates these via scaffold.LoadAgent
// (which falls back to the embedded scaffold .md when the project
// hasn't run `update --reset`). Tests construct minimal AgentDef
// literals — only ID and OutputSchema are exercised against the
// mock executor; SystemPrompt content isn't read by the mock.
type JustifyInputs struct {
	NodeID        string
	NodeMarkdown  string
	GoalsBody     string
	Challenge     string
	ChallengerOut *ChallengeBrief
	ResearcherOut *ResearchBrief

	Advocate   AgentDef
	Challenger AgentDef
	Researcher AgentDef
}

// RunJustify dispatches the spec_advocate agent against the rendered
// node + GOALS and returns its structured defense. No challenger
// involvement; the caller should leave Challenge and ChallengerOut
// empty on JustifyInputs.
func RunJustify(ctx context.Context, exec AgentExecutor, in JustifyInputs) (*JustificationBrief, error) {
	if in.NodeMarkdown == "" {
		return nil, fmt.Errorf("justify: empty node content for %q", in.NodeID)
	}

	def := in.Advocate
	def.OutputSchema = "JustificationBrief"
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

	def := in.Researcher
	def.OutputSchema = "ResearchBrief"
	def.Grounding = true
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

	challengeDef := in.Challenger
	challengeDef.OutputSchema = "ChallengeBrief"
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

	advocateDef := in.Advocate
	advocateDef.OutputSchema = "AdversarialDefense"
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
// is intermittent. Empirical evidence from a 4-run sample on
// strat-frontend in winplan (2026-05-10) showed 3/4 calls producing
// degenerate briefs even at attempt 2 — closer to ~75% per-call
// failure on that specific prompt vs the ~50–67% earlier observation.
// Three attempts (one initial + two retries) gives 1 - (0.75)^3 ≈
// 58% success vs the prior 1 - (0.75)^2 = 44% on the same input.
//
// The companion prompt clarification (broadening the allowed
// evidence sources to include the node's own rationale and naming
// the validator's length floor in-prompt) attacks the root cause;
// this retry bump is belt-and-suspenders.
//
// Increase further only with evidence: a 4th attempt against the
// same prompt has rapidly diminishing returns and starts looking
// like stubbornness — re-run by the user is cheaper than burning
// tokens on the same shape.
const challengerMaxAttempts = 3

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
