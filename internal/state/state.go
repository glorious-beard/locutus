package state

import (
	"time"

	"github.com/chetan/locutus/internal/spec"
)

// ReconcileStatus represents the observed lifecycle state of an Approach.
type ReconcileStatus string

const (
	StatusUnplanned  ReconcileStatus = "unplanned"
	StatusPlanned    ReconcileStatus = "planned"
	StatusPreFlight  ReconcileStatus = "pre_flight"   // DJ-071: clarification round before implementation
	StatusInProgress ReconcileStatus = "in_progress"
	StatusLive       ReconcileStatus = "live"
	StatusFailed     ReconcileStatus = "failed"
	StatusDrifted    ReconcileStatus = "drifted"    // spec changed after live
	StatusOutOfSpec  ReconcileStatus = "out_of_spec" // artifact changed outside Locutus
)

// ReconciliationState is the observed state for a single Approach node.
// Stored at .locutus/state/<approach-id>.yaml — written by the reconciler, never by the planner.
type ReconciliationState struct {
	ApproachID       string            `yaml:"approach_id"`                     // always an Approach node ID
	SpecHash         string            `yaml:"spec_hash"`                       // hash of the Approach spec node
	Artifacts        map[string]string `yaml:"artifacts,omitempty"`             // path → sha256; per-file drift detection
	Status           ReconcileStatus   `yaml:"status"`
	Message          string            `yaml:"message,omitempty"`               // reconciler-authored reason for current status
	LastReconciled   time.Time         `yaml:"last_reconciled,omitempty"`
	WorkstreamID     string            `yaml:"workstream_id,omitempty"`         // N Approaches share one WorkstreamID
	AssertionResults []AssertionResult `yaml:"assertion_results,omitempty"`     // results from last reconciliation run
}

// AssertionResult records the outcome of a single assertion.
// Embeds the assertion definition so re-evaluation requires no spec lookup.
type AssertionResult struct {
	spec.Assertion `yaml:",inline"`
	Passed         bool      `yaml:"passed"`
	Output         string    `yaml:"output,omitempty"`
	RunAt          time.Time `yaml:"run_at"`
}
