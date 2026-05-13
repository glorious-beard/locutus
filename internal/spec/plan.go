package spec

import "time"

// MasterPlan represents the top-level execution plan produced by the planner.
type MasterPlan struct {
	ID                   string              `json:"id" yaml:"id" jsonschema:"description=Stable slug for the plan, starting 'plan-', lowercase, hyphen-separated. Derived from the prompt's subject (e.g. 'plan-realtime-dashboard')."`
	Version              int                 `json:"version" yaml:"version" jsonschema:"description=Monotonically increasing version of this plan within its identity. Start at 1; subsequent revisions of the same plan increment by 1."`
	CreatedAt            time.Time           `json:"created_at" yaml:"created_at"`
	ProjectRoot          string              `json:"project_root" yaml:"project_root"`
	Prompt               string              `json:"prompt" yaml:"prompt"`
	TriggerKind          PlanAction          `json:"trigger_kind" yaml:"trigger_kind"`
	Features             []FeatureRef        `json:"features,omitempty" yaml:"features,omitempty" jsonschema:"description=Snapshots of features in scope for this plan. Each entry references an existing feature by id; the planner does not invent features."`
	Decisions            []DecisionRef       `json:"decisions,omitempty" yaml:"decisions,omitempty" jsonschema:"description=Snapshots of decisions the plan honors. Each entry references an existing decision by id."`
	Strategies           []StrategyRef       `json:"strategies,omitempty" yaml:"strategies,omitempty" jsonschema:"description=Snapshots of strategies the plan honors."`
	Approaches           []ApproachRef       `json:"approaches,omitempty" yaml:"approaches,omitempty" jsonschema:"description=Snapshots of approaches in scope, one per parent feature/strategy that the plan touches."`
	InterfaceContracts   []InterfaceContract `json:"interface_contracts,omitempty" yaml:"interface_contracts,omitempty" jsonschema:"description=Shared types or API shapes that decouple parallel workstreams. Each contract names what one workstream produces and which workstreams consume it."`
	Workstreams          []Workstream        `json:"workstreams,omitempty" yaml:"workstreams,omitempty" jsonschema:"description=Sub-plans that can be assigned to a single agent each. The planner partitions work across workstreams so independent slices can run in parallel; dependencies between workstreams are declared explicitly via Workstream.DependsOn."`
	GlobalAssertions     []Assertion         `json:"global_assertions,omitempty" yaml:"global_assertions,omitempty" jsonschema:"description=Assertions that must hold across the entire plan (project-wide test suites, lints, type-checks). Workstream- and step-level assertions narrow the scope; global assertions apply unconditionally."`
	SpecDerivedArtifacts []string            `json:"spec_derived_artifacts,omitempty" yaml:"spec_derived_artifacts,omitempty" jsonschema:"description=Filesystem paths or artifact ids that this plan derives from the spec (e.g. generated SQL migrations, OpenAPI specs). Used by callers to know which artifacts are owned by the plan vs. authored by hand."`
	Summary              string              `json:"summary,omitempty" yaml:"summary,omitempty" jsonschema:"description=Human-readable summary of the plan in one or two sentences. Surfaced in status output and traces; reads as 'what this plan accomplishes', not 'how'."`
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
	ID             string                 `json:"id" yaml:"id" jsonschema:"description=Stable slug for this workstream, starting 'ws-', lowercase, hyphen-separated. Derived from the strategy domain (e.g. 'ws-frontend', 'ws-data-layer')."`
	StrategyDomain string                 `json:"strategy_domain" yaml:"strategy_domain" jsonschema:"description=The domain or layer this workstream covers (e.g. 'frontend', 'data-layer', 'observability'). Maps to one or more strategies; one strategy may span multiple workstreams when the work is large."`
	AgentID        string                 `json:"agent_id,omitempty" yaml:"agent_id,omitempty" jsonschema:"description=Id of the coding agent assigned to execute this workstream when fan-out is configured. Empty when the workstream is unassigned and the executor picks at dispatch time."`
	DetailLevel    DetailLevel            `json:"detail_level" yaml:"detail_level" jsonschema:"enum=high,enum=medium,enum=low,description=How much guidance the plan provides to the executing agent. high = explicit per-step instructions; medium = goal-level direction with constraints; low = a target and trust."`
	DependsOn      []WorkstreamDependency `json:"depends_on,omitempty" yaml:"depends_on,omitempty" jsonschema:"description=Other workstreams that must complete before this one starts. Empty when the workstream can start immediately."`
	Steps          []PlanStep             `json:"steps,omitempty" yaml:"steps,omitempty" jsonschema:"description=Ordered steps the executing agent works through. Each step is a discrete unit of progress with its own approach reference and (optionally) assertions."`
	Assertions     []Assertion            `json:"assertions,omitempty" yaml:"assertions,omitempty" jsonschema:"description=Assertions that must hold once the workstream's last step finishes (workstream-level validation gates). Step-level assertions narrow the scope; these apply at workstream completion."`
}

// WorkstreamDependency declares a dependency between workstreams.
type WorkstreamDependency struct {
	WorkstreamID string `json:"workstream_id" yaml:"workstream_id"`
	Reason       string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// PlanStep represents a single step within a workstream.
type PlanStep struct {
	ID            string            `json:"id" yaml:"id" jsonschema:"description=Stable slug for this step, starting 'step-', or simply 'step-N' where N is the ordinal. Unique within the workstream."`
	Order         int               `json:"order" yaml:"order" jsonschema:"description=Ordinal position of this step within the workstream, 1-based. Strictly increasing; gaps allowed but discouraged."`
	ApproachID    string            `json:"approach_id" yaml:"approach_id" jsonschema:"description=Id of the approach (starting 'strat-' or 'feat-' depending on parent kind) this step implements. Must reference an existing approach in the spec graph."`
	Description   string            `json:"description" yaml:"description" jsonschema:"description=What the executing agent does in this step, in one to three sentences. Concrete and verifiable — names the artifacts touched and the outcome. Not aspirational."`
	Skills        []SkillRef        `json:"skills,omitempty" yaml:"skills,omitempty" jsonschema:"description=Skill files the executing agent loads before working on this step. Each entry names a stable id and the on-disk path."`
	ExpectedFiles []string          `json:"expected_files,omitempty" yaml:"expected_files,omitempty" jsonschema:"description=Filesystem paths the step is expected to create or modify (relative to project root). Used by reviewers to spot drift between intent and what the agent actually touched."`
	DecisionIDs   []string          `json:"decision_ids,omitempty" yaml:"decision_ids,omitempty" jsonschema:"description=Ids (starting 'dec-') of the decisions this step honors. Provides traceability from execution back to the rationale that drives it."`
	DependsOn     []StepDependency  `json:"depends_on,omitempty" yaml:"depends_on,omitempty" jsonschema:"description=Other steps within the same workstream that must complete before this one starts. Cross-workstream dependencies belong on the Workstream level."`
	Assertions    []Assertion       `json:"assertions,omitempty" yaml:"assertions,omitempty" jsonschema:"description=Validation checks that must hold once the step finishes (tests, lints, type-checks scoped to this step's changes)."`
	Context       map[string]string `json:"context,omitempty" yaml:"context,omitempty" jsonschema:"description=Free-form key-value hints passed to the executing agent (e.g. 'package=./internal/api', 'style=existing'). Keep entries short and concrete; this is not a place for long instructions — those belong in Description."`
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
	Kind    AssertionKind `json:"kind" yaml:"kind" jsonschema:"description=The kind of validation check. Determines which fields the validator reads — test_pass needs Target (a 'go test ./pkg/...' target); file_exists needs Target (a path); pattern_match needs Target plus Pattern; llm_assert needs Prompt."`
	Target  string        `json:"target,omitempty" yaml:"target,omitempty" jsonschema:"description=The target the assertion runs against. Format depends on Kind: a test target ('./internal/...'), a filesystem path, a build target. Required for all non-llm_assert kinds."`
	Pattern string        `json:"pattern,omitempty" yaml:"pattern,omitempty" jsonschema:"description=The pattern to match against Target's content. Used only by pattern_match assertions; typically a regular expression or a literal substring. Empty for all other kinds."`
	Prompt  string        `json:"prompt,omitempty" yaml:"prompt,omitempty" jsonschema:"description=Natural-language assertion the llm_assert kind evaluates against the workspace state. Phrased as a yes/no question ('does the README document the new flag?'). Empty for all other kinds."`
	Message string        `json:"message,omitempty" yaml:"message,omitempty" jsonschema:"description=Human-readable explanation of what this assertion proves and why it matters. Surfaced when the assertion fails so the executing agent or operator understands the gap."`
}
