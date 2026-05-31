package state

import (
	"time"

	"github.com/glorious-beard/locutus/internal/spec"
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
// Stored at .borg/state/<approach-id>.yaml — written by the reconciler, never by the planner.
type ReconciliationState struct {
	ApproachID       string            `yaml:"approach_id"`                     // always an Approach node ID
	// SpecHashes captures the one-hop upstream subgraph the approach
	// was reconciled against, keyed by spec id. Includes approach.id +
	// approach.parent_id + each entry in approach.decisions[] +
	// approach.advances[] + approach.respects[]. Set-diff drift
	// (added/removed keys — catches renames as coincident add+remove)
	// AND hash-diff drift (body changed on a same-id key) both surface
	// via key-by-key comparison. Per DJ-149.
	SpecHashes       map[string]string `yaml:"spec_hashes,omitempty"`
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
