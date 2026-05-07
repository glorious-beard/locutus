package agent

import (
	"context"
	"fmt"
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
  behavior. Cite retrieved sources where you used search. When
  evidence is insufficient, say "insufficient evidence to
  determine this" rather than speculating.

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

	var brief ResearchBrief
	if err := RunInto(WithRole(ctx, "research"), exec, def, input, &brief); err != nil {
		return nil, fmt.Errorf("justify: researcher dispatch: %w", err)
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

	var challenge ChallengeBrief
	if err := RunInto(WithRole(ctx, "challenge"), exec, challengeDef, challengeInput, &challenge); err != nil {
		return nil, nil, nil, fmt.Errorf("justify: challenger dispatch: %w", err)
	}
	if len(challenge.Concerns) == 0 {
		return &challenge, nil, nil, fmt.Errorf("justify: challenger returned no concerns for %q", in.NodeID)
	}

	researchIn := in
	researchIn.ChallengerOut = &challenge

	research, err := RunResearch(ctx, exec, researchIn)
	if err != nil {
		return &challenge, nil, nil, err
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
		return &challenge, research, nil, fmt.Errorf("justify: advocate dispatch: %w", err)
	}
	if defense.Defense == "" {
		return &challenge, research, &defense, fmt.Errorf("justify: advocate returned empty defense for %q", in.NodeID)
	}
	if !validVerdict(defense.Verdict) {
		return &challenge, research, &defense, fmt.Errorf("justify: advocate returned invalid verdict %q (want held_up|partially_held_up|broke_down)", defense.Verdict)
	}
	return &challenge, research, &defense, nil
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
	return joinSections(parts)
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

func validVerdict(v string) bool {
	switch v {
	case "held_up", "partially_held_up", "broke_down":
		return true
	}
	return false
}
