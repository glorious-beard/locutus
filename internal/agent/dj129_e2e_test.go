// DJ-129 Phase 6 — end-to-end tests for the dimension-driven critique
// flow. These verify the full path: scout surfaces a dimension; the
// critique fanout dispatches spec_critic_elaborator against it; the
// critic emits a CriticIssue with counterproposals; the elaborator
// revises in response; the loop converges.

package agent

import (
	"context"
	"testing"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDJ129ScoutSurfacedComplianceDimensionDrivesCritic — the scout
// surfaces a project-specific dimension (compliance) that isn't one of
// the legacy four lenses; the critic-elaborator dispatches against it
// and emits a compliance-shaped concern; the elaborator's revise pass
// flips dec-auth to honor the compliance constraint; the loop
// converges with the compliance dimension stable.
func TestDJ129ScoutSurfacedComplianceDimensionDrivesCritic(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)
	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	complianceDim := CritiqueDimension{
		ID: "voter-file-privacy", Lens: "compliance",
		FocusQuestion:  "Does the auth flow honor per-state privacy regimes for voter-file access?",
		SourceEvidence: []string{"GOALS §Compliance: state-level privacy regimes require named-account auditing"},
		Disciplines:    []string{"goals_grounded", "best_practice_grounded"},
		SeverityFloor:  "high",
	}

	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "campaign software with state-level privacy regimes",
		AxesOpen: []OpenAxis{{
			ID: "auth-provider", Description: "How do organizers authenticate?",
			SourceEvidence: []string{"GOALS §Users"}, SurfacedBy: []string{"feat-organizing"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-organizing", Title: "Organizing dashboard",
			Summary: "Field organizer access to voter file.", Decisions: []string{},
		}},
		CritiqueDimensions: []CritiqueDimension{complianceDim},
		Converged:          false,
	})
	decAuth := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-auth", Title: "Adopt shared service-account auth",
		Rationale:  "Single service account simplifies the dashboard's database connection model.",
		Confidence: 0.7,
		Alternatives: []spec.Alternative{{
			Name: "Per-user accounts via Auth0", Rationale: "individual auditing", RejectedBecause: "more complex",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://auth0.com", Excerpt: "Per-user accounts"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "auth"}},
		Axes:       []string{"auth-provider"},
		SurfacedBy: []string{"feat-organizing"},
	})
	featOrganizing := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-organizing", Title: "Organizing dashboard",
		Description: "Field organizer access.", Decisions: []string{"dec-auth"},
	})

	// The compliance-lens critic flags the shared-account decision
	// against GOALS §Compliance.
	complianceIssue := mustJSON(t, CriticIssues{Issues: []CriticIssue{{
		Weakness: "The dec-auth shared service-account model conflicts with GOALS §Compliance's named-account auditing requirement.",
		Evidence: "GOALS §Compliance: 'state-level privacy regimes require named-account auditing, not shared logins.'",
		Counterproposals: []CriticCounterproposal{{
			Option:   "Adopt per-user Auth0 accounts with audit-log forwarding to S3",
			Argument: "Per-user Auth0 accounts honor GOALS §Compliance's named-account requirement and Auth0's audit-log streaming covers the audit-trail surface.",
			Citations: []spec.Citation{
				{Kind: "goals", Reference: "GOALS.md", Excerpt: "state-level privacy regimes require named-account auditing"},
				{Kind: "best_practice", Reference: "NIST 800-53 AU-2: Audit Events"},
			},
		}},
		RelatedDecisionIDs: []string{"dec-auth"},
	}}})
	noIssues := `{"issues":[]}`

	scoutKeepOpen := scoutBriefJSON(t, ScoutBrief{
		DomainRead:         "campaign software",
		AxesOpen:           []OpenAxis{},
		NewNodes:           []NewSpecNode{},
		CritiqueDimensions: []CritiqueDimension{complianceDim},
		Converged:          false,
	})

	// Flip revision: elaborator picks the Auth0 counterproposal and
	// demotes the shared-account decision to alternatives.
	revFlip := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-auth", Title: "Adopt per-user Auth0 accounts with audit-log forwarding to S3",
		Rationale:  "Flipped per the compliance critic's counterproposal: GOALS §Compliance requires named-account auditing.",
		Confidence: 0.85,
		Alternatives: []spec.Alternative{
			{Name: "Per-user accounts via Auth0", Rationale: "individual auditing (preserved from prior)", RejectedBecause: "preserved",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://auth0.com", Excerpt: "Per-user accounts"}}},
			{Name: "Adopt shared service-account auth", Rationale: "single service account simplifies the dashboard's database connection model.", RejectedBecause: "violates GOALS §Compliance named-account requirement",
				Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "auth"}}},
		},
		Citations: []spec.Citation{
			{Kind: "goals", Reference: "GOALS.md", Excerpt: "state-level privacy regimes require named-account auditing"},
			{Kind: "best_practice", Reference: "NIST 800-53 AU-2: Audit Events"},
		},
		Axes:       []string{"auth-provider"},
		SurfacedBy: []string{"feat-organizing"},
	})

	scoutConverged := scoutBriefJSON(t, ScoutBrief{
		DomainRead:         "campaign software",
		AxesOpen:           []OpenAxis{},
		NewNodes:           []NewSpecNode{},
		CritiqueDimensions: []CritiqueDimension{complianceDim},
		Converged:          true,
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		// iter-1: first-author + narrative + reconcile + critique (1 dim) + tail scout.
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decAuth, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featOrganizing, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: complianceIssue, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
		// iter-2: revise fires; concern flips to addressed; scout converges (dimension stable).
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: revFlip, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutConverged, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(historian, 5)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Campaign software respecting state-level privacy regimes.",
	}, wf)
	require.NoError(t, err)
	require.NotNil(t, proposal)

	// dec-auth is the revised Auth0 chosen option.
	var auth *DecisionProposal
	for i := range proposal.Decisions {
		if proposal.Decisions[i].ID == "dec-auth" {
			auth = &proposal.Decisions[i]
		}
	}
	require.NotNil(t, auth)
	assert.Contains(t, auth.Title, "Auth0", "elaborator flipped per the compliance counterproposal")
}

// TestDJ129ProjectWithNoCostConcernRunsNoCostCritic — a fixture
// where GOALS.md does NOT imply a cost ceiling and the scout
// surfaces no cost dimension; assert zero cost-lens concerns in the
// final state. Validates the "no floor" design decision (#3): the
// workflow does not force a cost critic when the project doesn't
// have a cost concern.
func TestDJ129ProjectWithNoCostConcernRunsNoCostCritic(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)
	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	// Scout surfaces no critique dimensions at all — the test's
	// invariant is that the critique step skips entirely.
	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "research project; budget uncapped",
		AxesOpen: []OpenAxis{{
			ID: "compute-platform", Description: "Where does the analysis pipeline run?",
			SourceEvidence: []string{"GOALS §Analysis"}, SurfacedBy: []string{"feat-pipeline"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-pipeline", Title: "Analysis pipeline",
			Summary: "Process inputs.", Decisions: []string{},
		}},
		CritiqueDimensions: nil, // No dimensions: zero LLM critique calls.
		Converged:          false,
	})
	decCompute := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-compute", Title: "Adopt local Jupyter for the pipeline",
		Rationale:  "Researcher runs the analysis on a workstation.",
		Confidence: 0.8,
		Alternatives: []spec.Alternative{{
			Name: "Cloud VM", Rationale: "elastic", RejectedBecause: "researcher prefers local",
			Citations: []spec.Citation{{Kind: "best_practice", Reference: "Local-first research workflows"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "research"}},
		Axes:       []string{"compute-platform"},
		SurfacedBy: []string{"feat-pipeline"},
	})
	featPipeline := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-pipeline", Title: "Analysis pipeline",
		Description: "Process inputs.", Decisions: []string{"dec-compute"},
	})

	scoutConverged := scoutBriefJSON(t, ScoutBrief{
		DomainRead:         "research project",
		AxesOpen:           []OpenAxis{},
		NewNodes:           []NewSpecNode{},
		CritiqueDimensions: nil,
		Converged:          true,
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		// iter-1: first-author + narrative + reconcile + (no critique, scout surfaced no dimensions) + tail scout converges.
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decCompute, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featPipeline, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutConverged, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(historian, 5)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Research project; budget is uncapped; iterate freely.",
	}, wf)
	require.NoError(t, err)
	require.NotNil(t, proposal)

	// The test's structural invariant: MockExecutor was set up with
	// ZERO spec_critic_elaborator responses; if the workflow had
	// dispatched the critic-elaborator anyway, the mock would have
	// returned no-such-response and the workflow would have failed.
	// Reaching this point successfully IS the assertion. Belt-and-
	// suspenders: also assert the call log lacks the critic.
	for _, c := range mock.Calls() {
		assert.NotEqual(t, "spec_critic_elaborator", c.Def.ID,
			"the critique step must not have dispatched when scout surfaced no dimensions")
	}
}

// TestDJ129DimensionInstabilityBlocksConvergence — fixture where
// scout iter-2 surfaces a NEW dimension not present in iter-1; assert
// convergence does NOT fire that iteration even with Converged: true
// on the brief (the new dimension's critic-elaborator gets at least
// one chance to surface concerns).
func TestDJ129DimensionInstabilityBlocksConvergence(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)
	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	dimA := CritiqueDimension{
		ID: "dim-a", Lens: "architecture", FocusQuestion: "q", SourceEvidence: []string{"e"},
		Disciplines: []string{"freeform"}, SeverityFloor: "medium",
	}
	dimB := CritiqueDimension{
		ID: "dim-b", Lens: "cost", FocusQuestion: "q", SourceEvidence: []string{"e"},
		Disciplines: []string{"goals_grounded"}, SeverityFloor: "high",
	}

	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test project",
		AxesOpen: []OpenAxis{{
			ID: "axis-a", Description: "first axis",
			SourceEvidence: []string{"GOALS"}, SurfacedBy: []string{"feat-x"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-x", Title: "X", Summary: "x", Decisions: []string{},
		}},
		CritiqueDimensions: []CritiqueDimension{dimA},
		Converged:          false,
	})
	decX := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-x", Title: "X v0", Rationale: "r", Confidence: 0.7,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	})
	featX := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-x", Title: "X", Description: "x", Decisions: []string{"dec-x"},
	})
	noIssues := `{"issues":[]}`

	// iter-1 tail scout: claims Converged=true BUT surfaces a NEW
	// dimension dim-b. dimensionsAreStable returns false; loop spawns
	// iter-2 instead of exiting.
	scoutNewDim := scoutBriefJSON(t, ScoutBrief{
		DomainRead:         "test project",
		AxesOpen:           []OpenAxis{},
		NewNodes:           []NewSpecNode{},
		CritiqueDimensions: []CritiqueDimension{dimA, dimB},
		Converged:          true,
	})

	// iter-2 tail scout: dim-b now in the historical set; stability holds.
	scoutStable := scoutBriefJSON(t, ScoutBrief{
		DomainRead:         "test project",
		AxesOpen:           []OpenAxis{},
		NewNodes:           []NewSpecNode{},
		CritiqueDimensions: []CritiqueDimension{dimA, dimB},
		Converged:          true,
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		// iter-1
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decX, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featX, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: noIssues, Model: "m"}}, // dim-a
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutNewDim, Model: "m"}},          // surfaces dim-b NEW; convergence blocked
		// iter-2 (forced by instability)
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: noIssues, Model: "m"}}, // dim-a
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: noIssues, Model: "m"}}, // dim-b
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutStable, Model: "m"}},          // stable; converges
	)

	wf := NewSpecGenerationWorkflow(historian, 5)
	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Test project.",
	}, wf)
	require.NoError(t, err, "loop must run iter-2 after dimension instability at iter-1 tail")

	// Belt-and-suspenders: count the scout calls. Three scouts means
	// iter-0 + iter-1 tail + iter-2 tail — the instability check forced
	// the second tail iteration.
	scoutCalls := 0
	for _, c := range mock.Calls() {
		if c.Def.ID == "spec_scout" {
			scoutCalls++
		}
	}
	assert.Equal(t, 3, scoutCalls, "iter-2 should have fired its own tail scout after instability blocked iter-1's convergence")
}
