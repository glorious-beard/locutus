package spec

// DecisionStatus represents the lifecycle state of a decision.
type DecisionStatus string

const (
	DecisionStatusProposed   DecisionStatus = "proposed"
	DecisionStatusAssumed    DecisionStatus = "assumed"
	DecisionStatusInferred   DecisionStatus = "inferred"
	DecisionStatusActive     DecisionStatus = "active"
	DecisionStatusSuperseded DecisionStatus = "superseded"
	DecisionStatusRejected   DecisionStatus = "rejected"
)

// StrategyKind represents the category of a strategy.
type StrategyKind string

const (
	StrategyKindFoundational StrategyKind = "foundational"
	StrategyKindDerived      StrategyKind = "derived"
	StrategyKindQuality      StrategyKind = "quality"
)

// FeatureStatus represents the lifecycle state of a feature.
type FeatureStatus string

const (
	FeatureStatusProposed FeatureStatus = "proposed"
	FeatureStatusActive   FeatureStatus = "active"
	FeatureStatusRemoved  FeatureStatus = "removed"
)

// BugStatus represents the lifecycle state of a bug.
type BugStatus string

const (
	BugStatusReported BugStatus = "reported"
	BugStatusTriaged  BugStatus = "triaged"
	BugStatusFixing   BugStatus = "fixing"
	BugStatusFixed    BugStatus = "fixed"
)

// BugSeverity represents the severity level of a bug.
type BugSeverity string

const (
	BugSeverityCritical BugSeverity = "critical"
	BugSeverityHigh     BugSeverity = "high"
	BugSeverityMedium   BugSeverity = "medium"
	BugSeverityLow      BugSeverity = "low"
)

// PlanAction represents the trigger that created an execution plan.
type PlanAction string

const (
	PlanActionInit       PlanAction = "init"
	PlanActionRevisit    PlanAction = "revisit"
	PlanActionRegen      PlanAction = "regen"
	PlanActionBrownfield PlanAction = "brownfield"
)

// DetailLevel represents the granularity of a workstream's plan.
type DetailLevel string

const (
	DetailLevelHigh     DetailLevel = "high"
	DetailLevelMedium   DetailLevel = "medium"
	DetailLevelDetailed DetailLevel = "detailed"
)

// AssertionKind represents the type of validation assertion.
type AssertionKind string

const (
	AssertionKindTestPass        AssertionKind = "test_pass"
	AssertionKindFileExists      AssertionKind = "file_exists"
	AssertionKindFileNotExists   AssertionKind = "file_not_exists"
	AssertionKindContains        AssertionKind = "contains"
	AssertionKindNotContains     AssertionKind = "not_contains"
	AssertionKindCommandExitZero AssertionKind = "command_exit_zero"
	AssertionKindCompiles        AssertionKind = "compiles"
	AssertionKindLintClean       AssertionKind = "lint_clean"
	AssertionKindLLMReview       AssertionKind = "llm_review"
)
