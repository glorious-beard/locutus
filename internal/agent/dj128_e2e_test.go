// DJ-128 Phase 7 — end-to-end convergence tests.
//
// These tests drive the entire DJ-128 shape with MockExecutor: critics
// emit counterproposal menus; the elaborator's revise pass produces a
// flip or reject revision that preserves alternatives; the convergence
// loop terminates correctly (clean exit on flip; cap-as-commit on
// intractable disagreement).

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dj128CounterproposalCloudWatch returns a single grounded
// counterproposal for the cost critic flagging Datadog. Pulled out
// because both the happy-path and cap tests reuse it.
func dj128CounterproposalCloudWatch() CriticCounterproposal {
	return CriticCounterproposal{
		Option:   "Switch from Datadog to CloudWatch + Sentry",
		Argument: "CloudWatch + Sentry combined land under $50/mo at the GOALS §3 traffic scale where Datadog Pro lands at ~$300/mo.",
		Citations: []spec.Citation{{
			Kind: "web", Reference: "https://aws.amazon.com/cloudwatch/pricing/",
			Excerpt: "CloudWatch Logs: $0.50 per GB ingested",
		}},
	}
}

// TestDJ128HappyPathConvergesViaFlipRevision — a single revision
// flips a prior chosen option to the critic's counterproposal; the
// demoted prior is in alternatives; concerns are addressed; loop
// converges in 3 iterations. Asserts the final proposal's
// alternatives[] carries the deliberation chronology.
func TestDJ128HappyPathConvergesViaFlipRevision(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)
	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "Observability product",
		AxesOpen: []OpenAxis{{
			ID: "observability-stack", Description: "Which metrics/logs platform backs the product?",
			SourceEvidence: []string{"GOALS §1 names observability"}, SurfacedBy: []string{"feat-monitoring"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-monitoring", Title: "Monitoring product",
			Summary: "Metrics + logs dashboard.", Decisions: []string{},
		}},
		Converged: false,
	})
	decDatadog := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-observability", Title: "Adopt Datadog",
		Rationale:  "Datadog provides the most polished UI.",
		Confidence: 0.8,
		Alternatives: []spec.Alternative{{
			Name: "CloudWatch", Rationale: "AWS-native", RejectedBecause: "less polish",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://aws.amazon.com/cloudwatch/", Excerpt: "AWS-native logging"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "observability"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	})
	featMonitoring := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-monitoring", Title: "Monitoring product",
		Description: "Metrics + logs.", Decisions: []string{"dec-observability"},
	})

	costIssue := mustJSON(t, CriticIssues{Issues: []CriticIssue{{
		Weakness: "The dec-observability rationale does not engage with the $150/mo cost ceiling in GOALS §3.",
		Evidence: "GOALS §3 caps infrastructure at $150/mo and Datadog Pro pricing exceeds that on a small fleet.",
		Counterproposals: []CriticCounterproposal{
			dj128CounterproposalCloudWatch(),
			{
				Option:   "Adopt self-hosted Prometheus + Grafana",
				Argument: "Self-hosted Prometheus + Grafana adds ops burden but eliminates per-host SaaS fees.",
				Citations: []spec.Citation{{Kind: "best_practice", Reference: "Prometheus operator pattern"}},
			},
		},
		RelatedDecisionIDs: []string{"dec-observability"},
	}}})
	noIssues := `{"issues":[]}`

	scoutKeepOpen := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "Observability product", AxesOpen: []OpenAxis{}, NewNodes: []NewSpecNode{},
		Converged: false,
	})

	// Flip revision: elaborator picks the cost critic's CloudWatch
	// counterproposal as the new chosen. Preserves prior alternative
	// CloudWatch and demotes "Adopt Datadog" to alternatives. Also
	// includes the rejected Prometheus counterproposal as alternative.
	revFlip := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-observability", Title: "Switch from Datadog to CloudWatch + Sentry",
		Rationale:  "Flipped per cost critic's counterproposal — CloudWatch + Sentry fit the GOALS §3 ceiling.",
		Confidence: 0.85,
		Alternatives: []spec.Alternative{
			{Name: "CloudWatch", Rationale: "AWS-native (preserved from prior)", RejectedBecause: "preserved",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://aws.amazon.com/cloudwatch/", Excerpt: "AWS-native logging"}}},
			{Name: "Adopt self-hosted Prometheus + Grafana", Rationale: "Self-hosted Prometheus + Grafana adds ops burden but eliminates per-host SaaS fees.", RejectedBecause: "ops overhead exceeds team's bandwidth",
				Citations: []spec.Citation{{Kind: "best_practice", Reference: "Prometheus operator pattern"}}},
			{Name: "Adopt Datadog", Rationale: "polished UI", RejectedBecause: "exceeds GOALS §3 cost ceiling",
				Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "$150/mo ceiling"}}},
		},
		Citations:  []spec.Citation{{Kind: "web", Reference: "https://aws.amazon.com/cloudwatch/pricing/", Excerpt: "CloudWatch Logs: $0.50 per GB ingested"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	})

	scoutConverged := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "Observability product", AxesOpen: []OpenAxis{}, NewNodes: []NewSpecNode{},
		Converged: true,
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		// iter-1: first-author + narrative + reconcile + critique + tail scout.
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decDatadog, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featMonitoring, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: costIssue, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
		// iter-2: revise fires; concern flips to addressed; tail scout converges.
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: revFlip, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutConverged, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(historian, 5)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Ship observability within a $150/mo budget.",
	}, wf)
	require.NoError(t, err)
	require.NotNil(t, proposal)

	// Final proposal carries the revised CloudWatch chosen option with
	// "Adopt Datadog" demoted into alternatives.
	var observability *DecisionProposal
	for i := range proposal.Decisions {
		if proposal.Decisions[i].ID == "dec-observability" {
			observability = &proposal.Decisions[i]
		}
	}
	require.NotNil(t, observability)
	assert.Equal(t, "Switch from Datadog to CloudWatch + Sentry", observability.Title)
	assert.False(t, observability.Locked,
		"happy-path convergence must NOT lock — cap-as-commit only fires on intractable disagreement")

	// Deliberation chronology in alternatives.
	var demoted *spec.Alternative
	for i := range observability.Alternatives {
		if observability.Alternatives[i].Name == "Adopt Datadog" {
			demoted = &observability.Alternatives[i]
		}
	}
	require.NotNil(t, demoted, "prior chosen Adopt Datadog must appear as a demoted alternative")
	assert.Greater(t, len(observability.Alternatives), 2,
		"alternatives carry the deliberation log: prior alt + rejected counterproposal + demoted chosen")
}

// TestDJ128CapAsCommitShipsWithLockedDecisions — three oscillating
// revisions on the same axis; the cap fires; the workflow exits
// cleanly; the proposal carries the latest revision with Locked=true;
// the contested concerns are wontfix; the convergence rule fires
// naturally on the next scout pass.
func TestDJ128CapAsCommitShipsWithLockedDecisions(t *testing.T) {
	t.Setenv("LOCUTUS_DECISION_REVISION_CAP", "2")

	fs := setupSpecGenFixtureDJ124(t)
	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "Observability product",
		AxesOpen: []OpenAxis{{
			ID: "observability-stack", Description: "Which observability platform?",
			SourceEvidence: []string{"GOALS §1 names observability"}, SurfacedBy: []string{"feat-monitoring"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-monitoring", Title: "Monitoring",
			Summary: "Metrics + logs.", Decisions: []string{},
		}},
		Converged: false,
	})
	decFirst := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-observability", Title: "Adopt Datadog v0",
		Rationale:  "Initial choice.",
		Confidence: 0.7,
		Alternatives: []spec.Alternative{{
			Name: "CloudWatch", Rationale: "considered", RejectedBecause: "less polish",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://cw", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	})
	featForObs := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-monitoring", Title: "Monitoring",
		Description: "Metrics + logs.", Decisions: []string{"dec-observability"},
	})

	costPersists := mustJSON(t, CriticIssues{Issues: []CriticIssue{{
		Weakness: "The dec-observability rationale does not engage with the $150/mo ceiling.",
		Evidence: "GOALS §3 names the ceiling; the rationale doesn't cite cost at all.",
		Counterproposals: []CriticCounterproposal{
			dj128CounterproposalCloudWatch(),
		},
		RelatedDecisionIDs: []string{"dec-observability"},
	}}})
	noIssues := `{"issues":[]}`

	scoutKeepOpen := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "Observability product", AxesOpen: []OpenAxis{}, NewNodes: []NewSpecNode{},
		Converged: false,
	})

	// Iter-2 revise: elaborator emits a revision that satisfies
	// monotonicity (preserves prior CloudWatch + demotes Datadog v0).
	revV1 := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-observability", Title: "Adopt Datadog v1",
		Rationale:  "Tweaked v1.",
		Confidence: 0.75,
		Alternatives: []spec.Alternative{{
			Name: "CloudWatch", Rationale: "considered", RejectedBecause: "less polish",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://cw", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	})
	// Iter-3 revise: cap fires after this merge (count goes to 2).
	revV2 := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-observability", Title: "Adopt Datadog v2",
		Rationale:  "Tweaked v2.",
		Confidence: 0.78,
		Alternatives: []spec.Alternative{
			{Name: "CloudWatch", Rationale: "considered", RejectedBecause: "less polish",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://cw", Excerpt: "e"}}},
			{Name: "Adopt Datadog v0", Rationale: "first version", RejectedBecause: "revised at iter 2",
				Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}}},
			{Name: "Switch from Datadog to CloudWatch + Sentry", Rationale: "critic counterproposal preserved", RejectedBecause: "elaborator rejected",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://aws.amazon.com/cloudwatch/pricing/", Excerpt: "CloudWatch Logs: $0.50 per GB ingested"}}},
		},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		// iter-1
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decFirst, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featForObs, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: costPersists, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
		// iter-2
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: revV1, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: costPersists, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
		// iter-3 (cap=2 fires after this revise)
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: revV2, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: costPersists, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(historian, 10)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Ship observability within a $150/mo budget.",
	}, wf)
	require.NoError(t, err, "cap-as-commit means clean exit (no error)")
	require.NotNil(t, proposal)

	// The capped decision is in the proposal with Locked=true.
	var locked *DecisionProposal
	for i := range proposal.Decisions {
		if proposal.Decisions[i].ID == "dec-observability" {
			locked = &proposal.Decisions[i]
		}
	}
	require.NotNil(t, locked)
	assert.True(t, locked.Locked, "capped decision must be Locked=true")
	assert.Equal(t, "Adopt Datadog v2", locked.Title,
		"the LATEST revision is the committed snapshot")

	// DJ-103 events present.
	entries, readErr := os.ReadDir(filepath.Join(tmp, ".borg", "history"))
	require.NoError(t, readErr)
	var capEvent, lockEvent bool
	for _, e := range entries {
		switch {
		case strings.Contains(e.Name(), "convergence_revision_capped"):
			capEvent = true
		case strings.Contains(e.Name(), "decision_locked"):
			lockEvent = true
		}
	}
	assert.True(t, capEvent, "convergence_revision_capped DJ-103 event must be recorded")
	assert.True(t, lockEvent, "decision_locked DJ-103 event must be recorded per locked decision")
}

// TestDJ128RejectRevisionAddsCriticCounterproposalAsAlternative —
// a single revision rejects the critic's counterproposal; the
// counterproposal becomes a new alternative with elaborator-side
// rejection reasoning; the chosen option stays; loop converges.
func TestDJ128RejectRevisionAddsCriticCounterproposalAsAlternative(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)
	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "Observability product",
		AxesOpen: []OpenAxis{{
			ID: "observability-stack", Description: "Which observability platform?",
			SourceEvidence: []string{"GOALS §1"}, SurfacedBy: []string{"feat-monitoring"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-monitoring", Title: "Monitoring",
			Summary: "Metrics + logs.", Decisions: []string{},
		}},
		Converged: false,
	})
	decDatadog := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-observability", Title: "Adopt Datadog",
		Rationale:  "Datadog has best APM.",
		Confidence: 0.8,
		Alternatives: []spec.Alternative{{
			Name: "CloudWatch", Rationale: "AWS-native", RejectedBecause: "less polish",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://cw", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	})
	featForObs := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-monitoring", Title: "Monitoring",
		Description: "Metrics + logs.", Decisions: []string{"dec-observability"},
	})

	costIssue := mustJSON(t, CriticIssues{Issues: []CriticIssue{{
		Weakness: "dec-observability rationale does not engage with the cost ceiling.",
		Evidence: "GOALS §3 names a $150/mo ceiling.",
		Counterproposals: []CriticCounterproposal{
			dj128CounterproposalCloudWatch(),
		},
		RelatedDecisionIDs: []string{"dec-observability"},
	}}})
	noIssues := `{"issues":[]}`

	scoutKeepOpen := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "Observability product", AxesOpen: []OpenAxis{}, NewNodes: []NewSpecNode{},
		Converged: false,
	})

	// Reject revision: elaborator KEEPS "Adopt Datadog" as the chosen
	// option, adds the critic's counterproposal as an alternative
	// with rejection reasoning that engages the critic's argument.
	revReject := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-observability", Title: "Adopt Datadog",
		Rationale:  "Reviewed cost critic's CloudWatch counterproposal; the team's existing Datadog runbook investment offsets the SaaS premium per GOALS §4's 'minimize ops bandwidth' clause.",
		Confidence: 0.85,
		Alternatives: []spec.Alternative{
			{Name: "CloudWatch", Rationale: "AWS-native (preserved from prior)", RejectedBecause: "less polish (preserved)",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://cw", Excerpt: "e"}}},
			{Name: "Switch from Datadog to CloudWatch + Sentry",
				Rationale:       "CloudWatch + Sentry combined land under $50/mo at the GOALS §3 traffic scale where Datadog Pro lands at ~$300/mo.",
				RejectedBecause: "Elaborator rejected: GOALS §4 ops-bandwidth clause favors team's existing Datadog runbook investment.",
				Citations:       []spec.Citation{{Kind: "web", Reference: "https://aws.amazon.com/cloudwatch/pricing/", Excerpt: "CloudWatch Logs: $0.50 per GB ingested"}}},
		},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	})

	scoutConverged := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "Observability product", AxesOpen: []OpenAxis{}, NewNodes: []NewSpecNode{},
		Converged: true,
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decDatadog, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featForObs, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: costIssue, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: revReject, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutConverged, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(historian, 5)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Ship observability.",
	}, wf)
	require.NoError(t, err)
	require.NotNil(t, proposal)

	var observability *DecisionProposal
	for i := range proposal.Decisions {
		if proposal.Decisions[i].ID == "dec-observability" {
			observability = &proposal.Decisions[i]
		}
	}
	require.NotNil(t, observability)
	assert.Equal(t, "Adopt Datadog", observability.Title,
		"Reject revision keeps the prior chosen option")

	// The critic's counterproposal must appear as an alternative
	// carrying the critic's Argument verbatim in Rationale.
	var counterAsAlt *spec.Alternative
	for i := range observability.Alternatives {
		if strings.Contains(observability.Alternatives[i].Name, "CloudWatch + Sentry") {
			counterAsAlt = &observability.Alternatives[i]
		}
	}
	require.NotNil(t, counterAsAlt, "rejected counterproposal must appear as an alternative")
	assert.Contains(t, counterAsAlt.Rationale, "CloudWatch + Sentry combined",
		"alternative's Rationale carries the critic's Argument verbatim")
	assert.Contains(t, counterAsAlt.RejectedBecause, "Elaborator rejected",
		"RejectedBecause names the elaborator's reasoning for rejecting the counterproposal")
}

// dj128SearchHistoryEventByKind returns true if any DJ-103 event
// file under dir has Kind == wantKind. Used by the end-to-end tests
// to assert events landed in the history directory.
func dj128SearchHistoryEventByKind(t *testing.T, dir, wantKind string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		var evt history.Event
		if err := json.Unmarshal(body, &evt); err != nil {
			continue
		}
		if evt.Kind == wantKind {
			return true
		}
	}
	return false
}
