package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

// registerStateTools wires state_record_reconciliation and
// state_refresh_artifacts onto the server. Per DJ-149.
func registerStateTools(server *mcp.Server, store *agent.SpecStore, stateStore *state.FileStateStore) {
	// state_record_reconciliation
	mcp.AddTool(server, &mcp.Tool{
		Name:        "state_record_reconciliation",
		Description: descStateRecordReconciliation,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in stateRecordReconciliationInput) (*mcp.CallToolResult, any, error) {
		rs, err := buildReconciliationStateRecord(in, time.Now().UTC())
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		// Compute SpecHashes server-side from the current spec graph.
		approach, ok := loadApproachForStateRecord(store, in.ApproachID)
		if !ok {
			return errorResult(fmt.Sprintf("state_record_reconciliation: approach %q not found in manifest", in.ApproachID)), nil, nil
		}
		hashes, err := state.ComputeSpecHashes(approach, specStoreBodyGetter(store))
		if err != nil {
			return errorResult(fmt.Sprintf("state_record_reconciliation: %v", err)), nil, nil
		}
		rs.SpecHashes = hashes
		if stateStore == nil {
			return errorResult("state_record_reconciliation: daemon has no FileStateStore wired"), nil, nil
		}
		if err := stateStore.Save(rs); err != nil {
			return errorResult(fmt.Sprintf("state_record_reconciliation: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Recorded reconciliation for %s (status=%s, %d artifacts, branch=%s).", rs.ApproachID, rs.Status, len(rs.Artifacts), in.BranchName)), nil, nil
	}, captureStateRecordReconciliation(store)))

	// state_refresh_artifacts
	mcp.AddTool(server, &mcp.Tool{
		Name:        "state_refresh_artifacts",
		Description: descStateRefreshArtifacts,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in stateRefreshArtifactsInput) (*mcp.CallToolResult, any, error) {
		if stateStore == nil {
			return errorResult("state_refresh_artifacts: daemon has no FileStateStore wired"), nil, nil
		}
		existing, err := stateStore.Load(in.ApproachID)
		if err != nil {
			return errorResult(fmt.Sprintf("state_refresh_artifacts: no state record for %q", in.ApproachID)), nil, nil
		}
		existing.Artifacts = in.Artifacts
		existing.LastReconciled = time.Now().UTC()
		if in.Reason != "" {
			existing.Message = "Trivial drift accepted: " + in.Reason
		}
		if err := stateStore.Save(existing); err != nil {
			return errorResult(fmt.Sprintf("state_refresh_artifacts: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Refreshed artifacts for %s (%d files).", in.ApproachID, len(in.Artifacts))), nil, nil
	}, captureStateRefreshArtifacts(store)))

	// state_mark_status
	mcp.AddTool(server, &mcp.Tool{
		Name:        "state_mark_status",
		Description: descStateMarkStatus,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in stateMarkStatusInput) (*mcp.CallToolResult, any, error) {
		if stateStore == nil {
			return errorResult("state_mark_status: daemon has no FileStateStore wired"), nil, nil
		}
		st, err := validateReconcileStatus(in.Status)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		existing, err := stateStore.Load(in.ApproachID)
		if err != nil {
			// Create a minimal record if none exists; operators marking
			// a planned status on a fresh approach is a valid path.
			existing = state.ReconciliationState{ApproachID: in.ApproachID}
		}
		existing.Status = st
		existing.LastReconciled = time.Now().UTC()
		if in.Message != "" {
			existing.Message = in.Message
		}
		if err := stateStore.Save(existing); err != nil {
			return errorResult(fmt.Sprintf("state_mark_status: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Marked %s as %s.", in.ApproachID, st)), nil, nil
	}, captureStateMarkStatus(store)))

	// state_delete_record
	mcp.AddTool(server, &mcp.Tool{
		Name:        "state_delete_record",
		Description: descStateDeleteRecord,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in stateDeleteRecordInput) (*mcp.CallToolResult, any, error) {
		if stateStore == nil {
			return errorResult("state_delete_record: daemon has no FileStateStore wired"), nil, nil
		}
		if strings.TrimSpace(in.Reason) == "" {
			return errorResult("state_delete_record: reason is required"), nil, nil
		}
		if err := stateStore.Delete(in.ApproachID); err != nil {
			return errorResult(fmt.Sprintf("state_delete_record: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Deleted state record for %s (reason: %s).", in.ApproachID, in.Reason)), nil, nil
	}, captureStateDeleteRecord(store)))

	// state_list_records (read-only; overlay-aware via OverlayView.ListStateRecords)
	type stateListEntry struct {
		ApproachID     string `json:"approach_id"`
		Status         string `json:"status"`
		LastReconciled string `json:"last_reconciled,omitempty"`
		BranchName     string `json:"branch_name,omitempty"`
	}
	type stateListRecordsOutput struct {
		Records []stateListEntry `json:"records"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "state_list_records",
		Description: descStateListRecords,
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, stateListRecordsOutput, error) {
		view := store.OverlayView(req.Session)
		ids := view.ListStateRecords()
		out := stateListRecordsOutput{Records: make([]stateListEntry, 0, len(ids))}
		for _, id := range ids {
			rs, ok := view.GetState(id)
			if !ok {
				continue
			}
			entry := stateListEntry{
				ApproachID: id,
				Status:     string(rs.Status),
				BranchName: rs.BranchName,
			}
			if !rs.LastReconciled.IsZero() {
				entry.LastReconciled = rs.LastReconciled.Format(time.RFC3339)
			}
			out.Records = append(out.Records, entry)
		}
		return nil, out, nil
	})

	// state_get_record (batched body fetch; overlay-aware)
	// stateRecordBody is a schema-safe projection of ReconciliationState.
	// ReconciliationState embeds spec.Assertion via AssertionResult, and
	// spec.Assertion's jsonschema tags confuse the go-sdk schema generator
	// (description values containing WORD= sequences). We project the
	// fields needed by callers without pulling in the problematic embedded
	// type. Per DJ-149.
	type stateRecordBody struct {
		ApproachID     string            `json:"approach_id"`
		Status         string            `json:"status"`
		SpecHashes     map[string]string `json:"spec_hashes,omitempty"`
		Artifacts      map[string]string `json:"artifacts,omitempty"`
		Message        string            `json:"message,omitempty"`
		LastReconciled string            `json:"last_reconciled,omitempty"`
		WorkstreamID   string            `json:"workstream_id,omitempty"`
		BranchName     string            `json:"branch_name,omitempty"`
	}
	type stateGetRecordOutput struct {
		Results      map[string]stateRecordBody `json:"results"`
		AvailableIDs []string                   `json:"available_ids"`
		Missing      []string                   `json:"missing"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "state_get_record",
		Description: descStateGetRecord,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in stateGetRecordInput) (*mcp.CallToolResult, stateGetRecordOutput, error) {
		view := store.OverlayView(req.Session)
		out := stateGetRecordOutput{
			Results: make(map[string]stateRecordBody, len(in.ApproachIDs)),
		}
		for _, id := range in.ApproachIDs {
			rs, ok := view.GetState(id)
			if ok {
				body := stateRecordBody{
					ApproachID:   rs.ApproachID,
					Status:       string(rs.Status),
					SpecHashes:   rs.SpecHashes,
					Artifacts:    rs.Artifacts,
					Message:      rs.Message,
					WorkstreamID: rs.WorkstreamID,
					BranchName:   rs.BranchName,
				}
				if !rs.LastReconciled.IsZero() {
					body.LastReconciled = rs.LastReconciled.Format(time.RFC3339)
				}
				out.Results[id] = body
				out.AvailableIDs = append(out.AvailableIDs, id)
			} else {
				out.Missing = append(out.Missing, id)
			}
		}
		return nil, out, nil
	})
}

// captureStateRecordReconciliation lands a state_record_reconciliation
// call in the session's overlay under dry-run. Status is derived
// from test_outcome the same way the production handler derives it.
func captureStateRecordReconciliation(store *agent.SpecStore) func(sess *mcp.ServerSession, in stateRecordReconciliationInput) (any, error) {
	return func(sess *mcp.ServerSession, in stateRecordReconciliationInput) (any, error) {
		rs, err := buildReconciliationStateRecord(in, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		// SpecHashes left empty under dry-run — daemon would compute
		// them from the spec graph in the production path. Surfacing
		// them in the captured overlay would require fetching the
		// approach + cited bodies via the spec store at capture time;
		// the report's purpose is to surface the would-be mutation
		// shape, not to fully realize spec_hashes. Operators inspecting
		// dry-run output see "would write reconciliation; spec_hashes
		// computed at apply time."
		if err := store.OverlayPutState(sess, "state_record_reconciliation", in.ApproachID, rs); err != nil {
			return nil, err
		}
		return rs, nil
	}
}

// captureStateRefreshArtifacts lands a state_refresh_artifacts call
// in the session's overlay under dry-run.
func captureStateRefreshArtifacts(store *agent.SpecStore) func(sess *mcp.ServerSession, in stateRefreshArtifactsInput) (any, error) {
	return func(sess *mcp.ServerSession, in stateRefreshArtifactsInput) (any, error) {
		// Under dry-run, look up the existing record from the overlay or
		// base store. If neither has one, create a minimal synthetic
		// record so the capture can still record the would-be artifact
		// refresh — consistent with dry-run's intent of previewing
		// mutations without requiring all preconditions to be live.
		existing := captureReadStateForRefresh(store, sess, in.ApproachID)
		existing.Artifacts = in.Artifacts
		existing.LastReconciled = time.Now().UTC()
		if err := store.OverlayPutState(sess, "state_refresh_artifacts", in.ApproachID, *existing); err != nil {
			return nil, err
		}
		return existing, nil
	}
}

// captureStateMarkStatus lands a state_mark_status call in the
// session's overlay under dry-run.
func captureStateMarkStatus(store *agent.SpecStore) func(sess *mcp.ServerSession, in stateMarkStatusInput) (any, error) {
	return func(sess *mcp.ServerSession, in stateMarkStatusInput) (any, error) {
		st, err := validateReconcileStatus(in.Status)
		if err != nil {
			return nil, err
		}
		existing := captureReadStateForRefresh(store, sess, in.ApproachID)
		existing.Status = st
		existing.LastReconciled = time.Now().UTC()
		if in.Message != "" {
			existing.Message = in.Message
		}
		if err := store.OverlayPutState(sess, "state_mark_status", in.ApproachID, *existing); err != nil {
			return nil, err
		}
		return existing, nil
	}
}

// captureStateDeleteRecord lands a state_delete_record call in the
// session's overlay under dry-run. Enforces non-empty reason to
// match spec_delete_goal's audit shape.
func captureStateDeleteRecord(store *agent.SpecStore) func(sess *mcp.ServerSession, in stateDeleteRecordInput) (any, error) {
	return func(sess *mcp.ServerSession, in stateDeleteRecordInput) (any, error) {
		if strings.TrimSpace(in.Reason) == "" {
			return nil, fmt.Errorf("reason is required")
		}
		if err := store.OverlayDeleteState(sess, "state_delete_record", in.ApproachID); err != nil {
			return nil, err
		}
		return map[string]string{"approach_id": in.ApproachID, "reason": in.Reason}, nil
	}
}

// validateReconcileStatus returns the typed ReconcileStatus for s or
// an error naming all 8 valid values. Per DJ-149.
func validateReconcileStatus(s string) (state.ReconcileStatus, error) {
	switch state.ReconcileStatus(strings.TrimSpace(s)) {
	case state.StatusUnplanned, state.StatusPlanned, state.StatusPreFlight,
		state.StatusInProgress, state.StatusLive, state.StatusFailed,
		state.StatusDrifted, state.StatusOutOfSpec:
		return state.ReconcileStatus(strings.TrimSpace(s)), nil
	default:
		return "", fmt.Errorf("status %q is not one of the 8 valid ReconcileStatus values (unplanned/planned/pre_flight/in_progress/live/failed/drifted/out_of_spec)", s)
	}
}

// buildReconciliationStateRecord constructs the ReconciliationState
// from a stateRecordReconciliationInput. The production handler
// additionally populates SpecHashes from the spec graph.
func buildReconciliationStateRecord(in stateRecordReconciliationInput, syncedAt time.Time) (state.ReconciliationState, error) {
	if strings.TrimSpace(in.ApproachID) == "" {
		return state.ReconciliationState{}, fmt.Errorf("approach_id is required")
	}
	if !strings.HasPrefix(in.ApproachID, "app-") {
		return state.ReconciliationState{}, fmt.Errorf("approach_id %q must use the app- prefix", in.ApproachID)
	}
	if len(in.Artifacts) == 0 {
		return state.ReconciliationState{}, fmt.Errorf("artifacts is required (at least one path → hash)")
	}
	for path, hash := range in.Artifacts {
		if !strings.HasPrefix(hash, "sha256:") || len(hash) <= len("sha256:") {
			return state.ReconciliationState{}, fmt.Errorf("artifacts[%q] must be in sha256:<hex> form; got %q", path, hash)
		}
	}
	var status state.ReconcileStatus
	switch strings.TrimSpace(in.TestOutcome) {
	case "passed":
		status = state.StatusLive
	case "failed":
		status = state.StatusFailed
	default:
		return state.ReconciliationState{}, fmt.Errorf("test_outcome must be \"passed\" or \"failed\"; got %q", in.TestOutcome)
	}
	msg := in.Message
	if in.TestOutputExcerpt != "" {
		if msg != "" {
			msg += "\n\n"
		}
		msg += "Test command: " + in.TestCommand + "\nOutput excerpt:\n" + in.TestOutputExcerpt
	}
	return state.ReconciliationState{
		ApproachID:     in.ApproachID,
		Artifacts:      in.Artifacts,
		Status:         status,
		Message:        msg,
		LastReconciled: syncedAt,
		BranchName:     in.BranchName,
	}, nil
}

// captureReadStateForRefresh reads the would-be-current state for
// an approach: consult overlay first, then base store. If neither has
// a record, returns a minimal synthetic record for dry-run purposes.
func captureReadStateForRefresh(store *agent.SpecStore, sess *mcp.ServerSession, approachID string) *state.ReconciliationState {
	view := store.OverlayView(sess)
	if rs, ok := view.GetState(approachID); ok {
		return rs
	}
	return &state.ReconciliationState{
		ApproachID: approachID,
		Status:     state.StatusUnplanned,
	}
}

// loadApproachForStateRecord fetches an Approach body via the
// SpecStore's batched GetSpec for hash computation. Per DJ-149.
func loadApproachForStateRecord(store *agent.SpecStore, approachID string) (spec.Approach, bool) {
	res := store.GetSpec([]string{approachID})
	entry, ok := res.Results[approachID]
	if !ok || entry.Status == agent.SpecGetMissing {
		return spec.Approach{}, false
	}
	a, ok := entry.Body.(spec.Approach)
	return a, ok
}

// specStoreBodyGetter adapts *agent.SpecStore to the state.BodyGetter
// interface required by state.ComputeSpecHashes. Per DJ-149.
func specStoreBodyGetter(s *agent.SpecStore) state.BodyGetter {
	return s
}
