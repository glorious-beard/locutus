package agent

import "time"

// Concern is a challenge raised by the critic or stakeholder.
type Concern struct {
	AgentID  string `json:"agent_id"`
	Severity string `json:"severity"` // "high", "medium", "low"
	Text     string `json:"text"`
}

// Finding is a research result from the researcher.
type Finding struct {
	Query  string `json:"query"`
	Result string `json:"result"`
}

// PlanningState is the typed blackboard for council workflow execution.
// It is owned exclusively by the workflow orchestrator goroutine — parallel
// agents receive read-only snapshots via StateSnapshot, and their results are
// merged back by the orchestrator after all parallel agents complete.
type PlanningState struct {
	Round            int        `json:"round"`
	Prompt           string     `json:"prompt"`
	ProposedSpec     string     `json:"proposed_spec,omitempty"`
	Concerns         []Concern  `json:"concerns,omitempty"`
	ResearchResults  []Finding  `json:"research_results,omitempty"`
	Revisions        string     `json:"revisions,omitempty"`
	Record           string     `json:"record,omitempty"`
	OpenConcerns     []string   `json:"open_concerns,omitempty"`
	ResolvedConcerns []string   `json:"resolved_concerns,omitempty"`
}

// StateSnapshot is a read-only copy of PlanningState fields relevant to a
// specific agent. Agents receive this instead of the full mutable state.
type StateSnapshot struct {
	Round           int
	Prompt          string
	ProposedSpec    string
	Concerns        []Concern
	ResearchResults []Finding
	Revisions       string
	OpenConcerns    []string
}

// Snapshot creates a read-only copy of the current state. Slice fields are
// copied so the snapshot is safe for concurrent reads while the orchestrator
// continues to mutate the original.
func (s *PlanningState) Snapshot() StateSnapshot {
	snap := StateSnapshot{
		Round:        s.Round,
		Prompt:       s.Prompt,
		ProposedSpec: s.ProposedSpec,
		Revisions:    s.Revisions,
	}
	if len(s.Concerns) > 0 {
		snap.Concerns = make([]Concern, len(s.Concerns))
		copy(snap.Concerns, s.Concerns)
	}
	if len(s.ResearchResults) > 0 {
		snap.ResearchResults = make([]Finding, len(s.ResearchResults))
		copy(snap.ResearchResults, s.ResearchResults)
	}
	if len(s.OpenConcerns) > 0 {
		snap.OpenConcerns = make([]string, len(s.OpenConcerns))
		copy(snap.OpenConcerns, s.OpenConcerns)
	}
	return snap
}

// HasOpenConcerns returns true if there are unresolved concerns.
func (s *PlanningState) HasOpenConcerns() bool {
	return len(s.OpenConcerns) > 0
}

// WorkflowEvent reports progress during workflow execution.
type WorkflowEvent struct {
	StepID    string    `json:"step_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	Status    string    `json:"status"` // "started", "completed", "retrying", "skipped", "error"
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
