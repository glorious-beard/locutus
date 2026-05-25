package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// The advocate, challenger, and researcher prompts previously lived
// inline in this file. They now live as scaffold .md files —
// internal/scaffold/agents/{spec-advocate,spec-challenger,
// justify-researcher}.md — loaded via scaffold.LoadAgent at the cmd
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
func RunResearch(ctx context.Context, dispatcher AgentDispatcher, in JustifyInputs) (*ResearchBrief, error) {
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
	def.Grounding = true
	user := buildResearcherUserMessage(in)
	input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}

	resp, err := dispatcher.Dispatch(ctx, def, input, DispatchOptions{
		Role:         "research",
		OutputSchema: "ResearchBrief",
	})
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
func RunJustifyAgainst(ctx context.Context, dispatcher AgentDispatcher, in JustifyInputs) (*ChallengeBrief, *ResearchBrief, *AdversarialDefense, error) {
	if in.Challenge == "" {
		return nil, nil, nil, fmt.Errorf("justify: empty challenge")
	}
	if in.NodeMarkdown == "" {
		return nil, nil, nil, fmt.Errorf("justify: empty node content for %q", in.NodeID)
	}

	challengeUser := buildChallengerUserMessage(in)
	challengeInput := AgentInput{Messages: []Message{{Role: "user", Content: challengeUser}}}

	challenge, challengeErr := dispatchChallengerWithRetry(ctx, dispatcher, in.Challenger, challengeInput, in.NodeID)
	if challengeErr != nil {
		return challenge, nil, nil, challengeErr
	}

	researchIn := in
	researchIn.ChallengerOut = challenge

	research, err := RunResearch(ctx, dispatcher, researchIn)
	if err != nil {
		return challenge, nil, nil, err
	}

	advocateIn := researchIn
	advocateIn.ResearcherOut = research

	advocateUser := buildAdvocateUserMessage(advocateIn)
	advocateInput := AgentInput{Messages: []Message{{Role: "user", Content: advocateUser}}}

	resp, err := dispatcher.Dispatch(ctx, in.Advocate, advocateInput, DispatchOptions{
		Role:         "justification",
		OutputSchema: "AdversarialDefense",
	})
	if err != nil {
		return challenge, research, nil, fmt.Errorf("justify: advocate dispatch: %w", err)
	}
	var defense AdversarialDefense
	if perr := unmarshalAgentOutput(resp.Content, &defense); perr != nil {
		return challenge, research, nil, fmt.Errorf("justify: advocate response: %w", perr)
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
// spec-challenger before giving up. The DJ-108 schema-skeleton
// failure mode is provider-specific — empirical evidence from
// strat-frontend in winplan (2026-05-10) showed 3/3 attempts going
// to Anthropic and producing literal "dummy" tokens. Bumping
// retries didn't help because the executor's pick-policy holds
// picks[0] across non-transport failures.
//
// dispatchChallengerWithRetry rotates the agent's Models slice on
// each retry so the three attempts hit three different providers
// (anthropic → googleai → openai by default). With independent
// providers, the joint-failure probability is the product of each
// provider's per-call failure rate, not a single provider's rate
// cubed — the rotation is the real fix; the retry budget just sets
// the breadth of that spread.
//
// Three was picked to cover the standard Models slice
// [anthropic, googleai, openai] exactly once. Increase only if a
// real fourth provider lands in DefaultModels.
const challengerMaxAttempts = 3

// dispatchChallengerWithRetry runs the spec-challenger via the
// dispatcher with the degenerate-output validator wired into the
// dispatch options. On a degenerate result the dispatcher rotates
// providers and retries up to challengerMaxAttempts. The DJ-108
// schema-skeleton failure mode is provider-specific (one provider
// returns "dummy" tokens despite a valid schema); the executor's
// internal retry only advances on transport errors, so the
// dispatcher's validator-driven loop is the layer that recovers it.
//
// Returns the last challenge produced (so the cmd layer can show the
// user what the model emitted on terminal failure) and a nil error
// on success, or the offending challenge plus a degenerate-output
// error after the attempt cap is reached.
func dispatchChallengerWithRetry(ctx context.Context, dispatcher AgentDispatcher, def AgentDef, input AgentInput, nodeID string) (*ChallengeBrief, error) {
	out, dispatchErr := dispatcher.Dispatch(ctx, def, input, DispatchOptions{
		Role:         "challenge",
		OutputSchema: "ChallengeBrief",
		MaxAttempts:  challengerMaxAttempts,
		Validator: func(out *AgentOutput) (string, bool) {
			var challenge ChallengeBrief
			if jerr := json.Unmarshal([]byte(out.Content), &challenge); jerr != nil {
				return fmt.Sprintf("parse output: %s", jerr), true
			}
			if len(challenge.Concerns) == 0 {
				// Empty concerns is a valid "challenger found nothing
				// substantive to surface" outcome — not a degenerate
				// case. Don't retry; the post-loop check returns the
				// expected error to the caller.
				return "", false
			}
			if reason, degenerate := degenerateChallengerBrief(&challenge); degenerate {
				return reason, true
			}
			return "", false
		},
	})

	var challenge *ChallengeBrief
	if out != nil {
		var parsed ChallengeBrief
		if jerr := json.Unmarshal([]byte(out.Content), &parsed); jerr == nil {
			challenge = &parsed
		}
	}
	if dispatchErr != nil {
		// Wrap dispatcher's error with the legacy phrasing the cmd
		// layer renders to the user. The dispatcher's degenerate-
		// retry-exhausted error already names the agent and reason;
		// surface a node-id-tagged variant so the user sees which
		// node failed.
		if challenge != nil && len(challenge.Concerns) > 0 {
			if reason, degenerate := degenerateChallengerBrief(challenge); degenerate {
				return challenge, fmt.Errorf("justify: challenger emitted degenerate brief for %q after %d attempts (%s); re-run, or report this if it persists", nodeID, challengerMaxAttempts, reason)
			}
		}
		return challenge, fmt.Errorf("justify: challenger dispatch: %w", dispatchErr)
	}
	if challenge == nil {
		return nil, fmt.Errorf("justify: challenger dispatch returned no usable content for %q", nodeID)
	}
	if len(challenge.Concerns) == 0 {
		return challenge, fmt.Errorf("justify: challenger returned no concerns for %q", nodeID)
	}
	return challenge, nil
}

// primaryProvider returns the provider name of the first preference
// in prefs, or "default" when prefs is empty (the executor falls
// back to DefaultModels). Surfaced in the retry WARN log so an
// operator reading session traces can see which provider hit the
// degenerate output and which provider the next attempt will try.
func primaryProvider(prefs []ModelPreference) string {
	if len(prefs) == 0 {
		return "default"
	}
	return prefs[0].Provider
}

// rotateModels returns a copy of prefs rotated left by n positions
// (n is taken modulo len(prefs) for safety). With prefs =
// [anthropic, googleai, openai] and n=1 the result is [googleai,
// openai, anthropic]; n=2 → [openai, anthropic, googleai]. Returns
// the input unchanged when prefs has 0 or 1 entries (nothing to
// rotate). Used by dispatchChallengerWithRetry to land each retry
// on a different provider when the schema-skeleton failure mode
// fires — see comment at the call site.
func rotateModels(prefs []ModelPreference, n int) []ModelPreference {
	if len(prefs) <= 1 {
		return prefs
	}
	shift := ((n % len(prefs)) + len(prefs)) % len(prefs)
	if shift == 0 {
		return prefs
	}
	rotated := make([]ModelPreference, 0, len(prefs))
	rotated = append(rotated, prefs[shift:]...)
	rotated = append(rotated, prefs[:shift]...)
	return rotated
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
