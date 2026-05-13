package agent

// JustificationBrief is the spec advocate's structured defense of a
// spec node. Defense is the prose argument; the two array fields
// surface what the model committed to so callers can render them
// alongside or display structured pull-quotes.
type JustificationBrief struct {
	Defense                     string   `json:"defense" jsonschema:"description=Two to four paragraphs of prose argument defending the spec node against the challenge. Cite specific goal clauses by their text. Avoid generic 'best practice' language without a concrete reference."`
	GoalClausesCited            []string `json:"goal_clauses_cited" jsonschema:"description=Verbatim excerpts from GOALS.md that the defense relies on. Each entry is a real quote from the goals document; paraphrases are not accepted."`
	ConditionsUnderWhichInvalid []string `json:"conditions_under_which_invalid" jsonschema:"description=Concrete conditions (constraint changes, new evidence) that would prompt revisiting this node. Each entry names a specific trigger; vague entries like 'if circumstances change' are not useful."`
}

// ChallengeBrief is the spec challenger's structured critique. Each
// concern carries the weakness, supporting evidence, and a concrete
// counterproposal so the advocate can address them point-by-point.
type ChallengeBrief struct {
	Concerns []AdversarialConcern `json:"concerns" jsonschema:"description=Between 2 and 5 distinct concerns. Less is fine if the challenge is narrow. Each concern is substantive (not a placeholder)."`
}

// AdversarialConcern is one entry in the challenger's brief. The
// Adversarial prefix disambiguates this from the existing Concern
// type used by monitor verdicts.
//
// All three fields are required to be full sentences carrying real
// content. A downstream validator (degenerateChallengerBrief) rejects
// outputs whose fields contain placeholder tokens like "dummy" /
// "placeholder" / "TBD" / "foo" / one-word answers — see
// challengerPlaceholderTokens in justify.go. The description tags
// here carry the same constraint into the schema doc the model sees.
type AdversarialConcern struct {
	Weakness        string `json:"weakness" jsonschema:"description=A complete sentence describing the specific weakness in the chosen approach. NOT a one-word label and NOT a placeholder like 'dummy' or 'TBD' — the validator rejects those and the call is retried at cost to the user."`
	Evidence        string `json:"evidence" jsonschema:"description=A complete sentence with concrete support for the weakness. Draws from: the node's own rationale or alternatives ('the rationale claims X but does not address Y'); GOALS.md clauses (cite the relevant text); named engineering practices ('12-factor app: stateless processes'); or current vendor/library behaviour. Evidence may be conceptual when the challenge is conceptual."`
	Counterproposal string `json:"counterproposal" jsonschema:"description=A complete sentence describing an alternative, mitigation, or test that would resolve the question. NOT a one-word label and NOT a placeholder. Specific enough that the advocate can address it point-by-point."`
}

// AddressedConcern is the advocate's reply to one of the challenger's
// concerns. StillStands captures whether the original spec node holds
// up to the critique on this point.
type AddressedConcern struct {
	ConcernSummary string `json:"concern_summary" jsonschema:"description=One-line restatement of the challenger's concern in the advocate's own words. Lets the rendered output stand alone without forcing the reader to cross-reference the challenger brief."`
	Response       string `json:"response" jsonschema:"description=The advocate's paragraph addressing this specific concern. Names what in the original node's rationale answers the concern, or concedes the gap if the concern surfaces a real one."`
	StillStands    bool   `json:"still_stands" jsonschema:"description=True when the original spec node's rationale answers this concern. False when the challenger surfaced a real gap that requires follow-up."`
}

// ResearchBrief is the researcher's structured output for the
// justify adversarial flow. Wraps a list of Findings (one per
// challenger concern investigated) so strict-mode JSON schemas have
// a struct root. Mirrors the council's Finding shape used in
// PlanningState.ResearchResults.
//
// ToolOutcomes is populated post-call from the AgentOutput's
// per-tool-invocation summary; it is NOT part of the schema the
// model is asked to produce. Any entry with Status="error"
// represents a query the model tried to ground via web search but
// for which the tool returned no usable content. Findings whose
// claims correspond to those queries are unsupported by retrieval
// and should be treated as ungrounded by downstream consumers
// (the advocate's prompt cites them explicitly).
type ResearchBrief struct {
	Findings     []Finding  `json:"findings"`
	ToolOutcomes []ToolCall `json:"-"`
}

// FailedQueries returns the subset of ToolOutcomes whose Status is
// "error" — the queries that produced no retrieved evidence. Used by
// buildAdvocateUserMessage to flag ungrounded findings to the
// advocate, and by RunResearch's slog warning so the operator sees
// which questions had no grounding.
func (r *ResearchBrief) FailedQueries() []ToolCall {
	if r == nil || len(r.ToolOutcomes) == 0 {
		return nil
	}
	var out []ToolCall
	for _, tc := range r.ToolOutcomes {
		if tc.Status == "error" {
			out = append(out, tc)
		}
	}
	return out
}

// AdversarialDefense extends JustificationBrief with the per-concern
// rebuttal and a verdict. BreakingPoints is populated when the
// advocate concedes that the concerns surfaced a real gap in the
// node's rationale; the cmd layer uses these to suggest a refine
// brief.
type AdversarialDefense struct {
	JustificationBrief
	PointByPointAddressed []AddressedConcern `json:"point_by_point_addressed" jsonschema:"description=One entry per challenger concern. Order matches the challenger brief so the rendered output can be read top-to-bottom."`
	Verdict               string             `json:"verdict" jsonschema:"enum=held_up,enum=partially_held_up,enum=broke_down,description=The overall verdict aggregated across the point-by-point addresses. 'held_up' when every concern was answered; 'partially_held_up' when most were answered but one or two surfaced real gaps; 'broke_down' when the challenge revealed the chosen path is wrong or substantially incomplete."`
	BreakingPoints        []string           `json:"breaking_points,omitempty" jsonschema:"description=Specific gaps the challenge surfaced that need follow-up via refine. Populated when verdict is 'partially_held_up' or 'broke_down'; empty otherwise. Each entry is concrete enough that the user can target it with 'refine <node> --brief'."`
}
