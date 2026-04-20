package spec

import "time"

// MasterPlan represents the top-level execution plan produced by the planner.
type MasterPlan struct {
	ID                   string              `json:"id" yaml:"id"`
	Version              int                 `json:"version" yaml:"version"`
	CreatedAt            time.Time           `json:"created_at" yaml:"created_at"`
	ProjectRoot          string              `json:"project_root" yaml:"project_root"`
	Prompt               string              `json:"prompt" yaml:"prompt"`
	TriggerKind          PlanAction          `json:"trigger_kind" yaml:"trigger_kind"`
	Features             []FeatureRef        `json:"features,omitempty" yaml:"features,omitempty"`
	Decisions            []DecisionRef       `json:"decisions,omitempty" yaml:"decisions,omitempty"`
	Strategies           []StrategyRef       `json:"strategies,omitempty" yaml:"strategies,omitempty"`
	Approaches           []ApproachRef       `json:"approaches,omitempty" yaml:"approaches,omitempty"`
	InterfaceContracts   []InterfaceContract `json:"interface_contracts,omitempty" yaml:"interface_contracts,omitempty"`
	Workstreams          []Workstream        `json:"workstreams,omitempty" yaml:"workstreams,omitempty"`
	GlobalAssertions     []Assertion         `json:"global_assertions,omitempty" yaml:"global_assertions,omitempty"`
	SpecDerivedArtifacts []string            `json:"spec_derived_artifacts,omitempty" yaml:"spec_derived_artifacts,omitempty"`
	Summary              string              `json:"summary,omitempty" yaml:"summary,omitempty"`
}

// FeatureRef is a lightweight snapshot of a feature within a plan.
type FeatureRef struct {
	ID     string `json:"id" yaml:"id"`
	Title  string `json:"title" yaml:"title"`
	Status string `json:"status" yaml:"status"`
}

// DecisionRef is a lightweight snapshot of a decision within a plan.
type DecisionRef struct {
	ID     string `json:"id" yaml:"id"`
	Title  string `json:"title" yaml:"title"`
	Status string `json:"status" yaml:"status"`
}

// StrategyRef is a lightweight snapshot of a strategy within a plan.
type StrategyRef struct {
	ID    string `json:"id" yaml:"id"`
	Title string `json:"title" yaml:"title"`
	Kind  string `json:"kind" yaml:"kind"`
}

// ApproachRef is a lightweight snapshot of an approach within a plan.
type ApproachRef struct {
	ID       string `json:"id" yaml:"id"`
	Title    string `json:"title" yaml:"title"`
	ParentID string `json:"parent_id" yaml:"parent_id"`
}

// InterfaceContract defines shared types or API shapes that enable parallel workstreams.
type InterfaceContract struct {
	ID          string   `json:"id" yaml:"id"`
	Description string   `json:"description" yaml:"description"`
	Artifacts   []string `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	ProducedBy  string   `json:"produced_by" yaml:"produced_by"`
	ConsumedBy  []string `json:"consumed_by,omitempty" yaml:"consumed_by,omitempty"`
}

// Workstream represents a sub-plan assigned to a single agent.
type Workstream struct {
	ID             string                 `json:"id" yaml:"id"`
	StrategyDomain string                 `json:"strategy_domain" yaml:"strategy_domain"`
	AgentID        string                 `json:"agent_id,omitempty" yaml:"agent_id,omitempty"`
	DetailLevel    DetailLevel            `json:"detail_level" yaml:"detail_level"`
	DependsOn      []WorkstreamDependency `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Steps          []PlanStep             `json:"steps,omitempty" yaml:"steps,omitempty"`
	Assertions     []Assertion            `json:"assertions,omitempty" yaml:"assertions,omitempty"`
}

// WorkstreamDependency declares a dependency between workstreams.
type WorkstreamDependency struct {
	WorkstreamID string `json:"workstream_id" yaml:"workstream_id"`
	Reason       string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// PlanStep represents a single step within a workstream.
type PlanStep struct {
	ID            string            `json:"id" yaml:"id"`
	Order         int               `json:"order" yaml:"order"`
	ApproachID    string            `json:"approach_id" yaml:"approach_id"`
	Description   string            `json:"description" yaml:"description"`
	Skills        []SkillRef        `json:"skills,omitempty" yaml:"skills,omitempty"`
	ExpectedFiles []string          `json:"expected_files,omitempty" yaml:"expected_files,omitempty"`
	DecisionIDs   []string          `json:"decision_ids,omitempty" yaml:"decision_ids,omitempty"`
	DependsOn     []StepDependency  `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Assertions    []Assertion       `json:"assertions,omitempty" yaml:"assertions,omitempty"`
	Context       map[string]string `json:"context,omitempty" yaml:"context,omitempty"`
}

// StepDependency declares a dependency between plan steps.
type StepDependency struct {
	StepID string `json:"step_id" yaml:"step_id"`
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// SkillRef references a skill file used by a plan step.
type SkillRef struct {
	ID      string `json:"id" yaml:"id"`
	Path    string `json:"path" yaml:"path"`
	Content string `json:"content,omitempty" yaml:"content,omitempty"`
}

// Assertion defines a validation check for a step, workstream, or plan.
type Assertion struct {
	Kind    AssertionKind `json:"kind" yaml:"kind"`
	Target  string        `json:"target,omitempty" yaml:"target,omitempty"`
	Pattern string        `json:"pattern,omitempty" yaml:"pattern,omitempty"`
	Prompt  string        `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Message string        `json:"message,omitempty" yaml:"message,omitempty"`
}
