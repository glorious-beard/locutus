package mcp

const (
	descStateRecordReconciliation = "Record a per-approach reconciliation outcome to .borg/state/<approach-id>.yaml. Called by the runtime after each adopt-phase implementation. Inputs: approach_id (app- prefix; must exist in the manifest), artifacts (map of relative file path to sha256:<hex> hash for every file in the phase's worktree diff), branch_name (the adopt/<NNN>-<approach-id> branch — for audit + operator review), test_outcome (\"passed\" or \"failed\" — honors DJ-068's test-asserted-live principle), test_command (what was actually run, e.g. \"go test ./...\"), test_output_excerpt (last N lines for context), message (optional free-form note). Server fills spec_hashes from the current spec graph (one-hop upstream subgraph) and stamps last_reconciled=now. Status derived: passed → live, failed → failed. Per DJ-149."

	descStateRefreshArtifacts = "Refresh the per-file artifact hashes on an existing state record without changing status. Used after the drift-classifier judges a code change trivial (formatting/imports/comments) — adopt accepts the change as the new baseline without regenerating. Inputs: approach_id, artifacts (the new hashes), reason (free-form, e.g. \"gofmt-style cleanup auto-accepted as trivial by drift-classifier\"). Server stamps last_reconciled=now. Status and spec_hashes unchanged. Per DJ-149."

	descStateMarkStatus = "Transition an approach's state record to an explicit status. Used by the runtime (e.g., marking in_progress before a long phase) or by the operator (e.g., manual planned/failed/out_of_spec). Inputs: approach_id, status (one of the 8 ReconcileStatus values: unplanned, planned, pre_flight, in_progress, live, failed, drifted, out_of_spec), message (optional free-form reason). Server stamps last_reconciled=now. spec_hashes and artifacts unchanged. Per DJ-149."

	descStateDeleteRecord = "Delete an approach's state record entirely. Used by the operator to retire an approach (parent superseded) or to forget previous reconciliation (start fresh on next adopt run). Inputs: approach_id, reason (required — matches spec_delete_goal's audit shape). Server removes the FileStateStore entry on commit. Per DJ-149."

	descStateListRecords = "Return a compact index of every state record: approach_id, status, last_reconciled, branch_name. Used by adopt's Step 1 to enumerate which approaches have been reconciled. No input. Overlay-aware: under dry-run, includes session-pending writes and excludes session-pending deletes. Per DJ-149."

	descStateGetRecord = "Batched body fetch for state records. Input: approach_ids (list of app- prefixed ids). Output: results map keyed by approach_id with full ReconciliationState (spec_hashes, artifacts, status, message, last_reconciled, branch_name); available_ids for ids that exist; missing for requested ids without records. Overlay-aware. Per DJ-149."
)

type stateRecordReconciliationInput struct {
	ApproachID        string            `json:"approach_id"`
	Artifacts         map[string]string `json:"artifacts"`
	BranchName        string            `json:"branch_name"`
	TestOutcome       string            `json:"test_outcome"`
	TestCommand       string            `json:"test_command"`
	TestOutputExcerpt string            `json:"test_output_excerpt,omitempty"`
	Message           string            `json:"message,omitempty"`
}

type stateRefreshArtifactsInput struct {
	ApproachID string            `json:"approach_id"`
	Artifacts  map[string]string `json:"artifacts"`
	Reason     string            `json:"reason"`
}

type stateMarkStatusInput struct {
	ApproachID string `json:"approach_id"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

type stateDeleteRecordInput struct {
	ApproachID string `json:"approach_id"`
	Reason     string `json:"reason"`
}

type stateGetRecordInput struct {
	ApproachIDs []string `json:"approach_ids"`
}
