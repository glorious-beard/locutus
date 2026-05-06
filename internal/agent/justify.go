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
	NodeID         string
	NodeMarkdown   string
	GoalsBody      string
	Challenge      string
	ChallengerOut  *ChallengeBrief
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

// RunJustifyAgainst dispatches the challenger first, then the
// advocate with the challenger's brief, returning the adversarial
// defense. The challenge string MUST be non-empty; an empty one is a
// programming error caught by the cmd layer.
func RunJustifyAgainst(ctx context.Context, exec AgentExecutor, in JustifyInputs) (*ChallengeBrief, *AdversarialDefense, error) {
	if in.Challenge == "" {
		return nil, nil, fmt.Errorf("justify: empty challenge")
	}
	if in.NodeMarkdown == "" {
		return nil, nil, fmt.Errorf("justify: empty node content for %q", in.NodeID)
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
		return nil, nil, fmt.Errorf("justify: challenger dispatch: %w", err)
	}
	if len(challenge.Concerns) == 0 {
		return &challenge, nil, fmt.Errorf("justify: challenger returned no concerns for %q", in.NodeID)
	}

	advocateIn := in
	advocateIn.ChallengerOut = &challenge

	advocateDef := AgentDef{
		ID:           "spec_advocate",
		SystemPrompt: advocateSystemPrompt,
		OutputSchema: "AdversarialDefense",
	}
	advocateUser := buildAdvocateUserMessage(advocateIn)
	advocateInput := AgentInput{Messages: []Message{{Role: "user", Content: advocateUser}}}

	var defense AdversarialDefense
	if err := RunInto(WithRole(ctx, "justification"), exec, advocateDef, advocateInput, &defense); err != nil {
		return &challenge, nil, fmt.Errorf("justify: advocate dispatch: %w", err)
	}
	if defense.Defense == "" {
		return &challenge, &defense, fmt.Errorf("justify: advocate returned empty defense for %q", in.NodeID)
	}
	if !validVerdict(defense.Verdict) {
		return &challenge, &defense, fmt.Errorf("justify: advocate returned invalid verdict %q (want held_up|partially_held_up|broke_down)", defense.Verdict)
	}
	return &challenge, &defense, nil
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

func formatConcerns(concerns []AdversarialConcern) string {
	out := ""
	for i, c := range concerns {
		out += fmt.Sprintf("%d. **Weakness:** %s\n   **Evidence:** %s\n   **Counterproposal:** %s\n\n",
			i+1, c.Weakness, c.Evidence, c.Counterproposal)
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
