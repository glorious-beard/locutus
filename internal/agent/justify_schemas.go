package agent

// JustificationBrief is the spec advocate's structured defense of a
// spec node. Defense is the prose argument; the two array fields
// surface what the model committed to so callers can render them
// alongside or display structured pull-quotes.
type JustificationBrief struct {
	Defense                     string   `json:"defense"`
	GoalClausesCited            []string `json:"goal_clauses_cited"`
	ConditionsUnderWhichInvalid []string `json:"conditions_under_which_invalid"`
}

// ChallengeBrief is the spec challenger's structured critique. Each
// concern carries the weakness, supporting evidence, and a concrete
// counterproposal so the advocate can address them point-by-point.
type ChallengeBrief struct {
	Concerns []AdversarialConcern `json:"concerns"`
}

// AdversarialConcern is one entry in the challenger's brief. The
// Adversarial prefix disambiguates this from the existing Concern
// type used by monitor verdicts.
type AdversarialConcern struct {
	Weakness        string `json:"weakness"`
	Evidence        string `json:"evidence"`
	Counterproposal string `json:"counterproposal"`
}

// AddressedConcern is the advocate's reply to one of the challenger's
// concerns. StillStands captures whether the original spec node holds
// up to the critique on this point.
type AddressedConcern struct {
	ConcernSummary string `json:"concern_summary"`
	Response       string `json:"response"`
	StillStands    bool   `json:"still_stands"`
}

// AdversarialDefense extends JustificationBrief with the per-concern
// rebuttal and a verdict. BreakingPoints is populated when the
// advocate concedes that the concerns surfaced a real gap in the
// node's rationale; the cmd layer uses these to suggest a refine
// brief.
//
// Verdict is constrained to one of {"held_up", "partially_held_up",
// "broke_down"} via the schema enum.
type AdversarialDefense struct {
	JustificationBrief
	PointByPointAddressed []AddressedConcern `json:"point_by_point_addressed"`
	Verdict               string             `json:"verdict"`
	BreakingPoints        []string           `json:"breaking_points,omitempty"`
}
