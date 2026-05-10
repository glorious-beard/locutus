package agent

// PlanningWorkflow drives the greenfield planning council:
// propose → challenge (parallel critic + stakeholder) → research
// (conditional) → revise → record. Convergence loop bounded at 5
// iterations.
var PlanningWorkflow = &Workflow[PlanningState]{
	Snapshot:       snapshotPlanningState,
	DefaultProject: projectDefault,
	Rounds: []WorkflowStep[PlanningState]{
		{
			ID:      "propose",
			Agents:  []string{"planner"},
			Project: projectPropose,
			Merge:   mergeProposedSpec,
		},
		{
			ID:        "challenge",
			Agents:    []string{"critic", "stakeholder"},
			Parallel:  true,
			DependsOn: []string{"propose"},
			Project:   projectChallenge,
			Merge:     mergeChallengeConcerns,
		},
		{
			ID:          "research",
			Agents:      []string{"researcher"},
			DependsOn:   []string{"challenge"},
			Conditional: hasOpenQuestions,
			Project:     projectResearch,
			Merge:       mergeResearch,
		},
		{
			ID:        "revise",
			Agents:    []string{"planner"},
			DependsOn: []string{"research"},
			Project:   projectRevise,
			Merge:     mergeRevisions,
		},
		{
			ID:        "record",
			Agents:    []string{"historian"},
			DependsOn: []string{"revise"},
			Project:   projectRecord,
			Merge:     mergeRecord,
		},
	},
	MaxRounds: 5,
}

// hasOpenQuestions gates the planning council's research step. True when
// the convergence monitor (or the prior-round logic) flagged unresolved
// concerns that warrant investigation.
func hasOpenQuestions(s *PlanningState) bool { return s.HasOpenConcerns() }

// mergeProposedSpec stores the planner's proposal verbatim. Last writer
// wins — the planning workflow's revise step rewrites Revisions, not
// ProposedSpec.
func mergeProposedSpec(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.ProposedSpec = v
	}
}

// mergeChallengeConcerns flattens each challenge round result into one
// Concern attributed to the agent that raised it. Severity defaults to
// "medium" — the challenge step's agents (critic, stakeholder) emit free
// text, not structured CriticIssues.
func mergeChallengeConcerns(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		s.Concerns = append(s.Concerns, Concern{
			AgentID:  r.AgentID,
			Severity: "medium",
			Text:     r.Output,
		})
	}
}

// mergeResearch appends researcher findings to ResearchResults. The
// researcher emits free text per concern; the projection feeds it the
// concern list, so we tag the finding's Query as "investigation" rather
// than parsing structured output.
func mergeResearch(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		s.ResearchResults = append(s.ResearchResults, Finding{
			Query:  "investigation",
			Result: r.Output,
		})
	}
}

// mergeRevisions stores the revised plan output verbatim.
func mergeRevisions(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.Revisions = v
	}
}

// mergeRecord stores the historian's session record.
func mergeRecord(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.Record = v
	}
}
