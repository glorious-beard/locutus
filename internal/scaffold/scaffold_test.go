package scaffold_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecisionElaboratorPromptAllowsScoutBriefAndWebCitations locks
// in DJ-104 (re-scoped under DJ-124 Stage B): now that the per-axis
// decision-elaborator owns decision authoring — and therefore owns
// citations — the scout_brief allowance moves to that prompt. Phase
// 1 also extended the citation enum with `web` for grounded-research
// evidence, so the decision-elaborator must name both kinds. The
// narrative-elaborators no longer author citations at all; the test
// scope follows the responsibility.
func TestDecisionElaboratorPromptAllowsScoutBriefAndWebCitations(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_decision_elaborator.md")
	require.NoError(t, err, "read .borg/agents/spec_decision_elaborator.md")
	text := string(body)

	assert.Contains(t, text, "scout_brief",
		"spec_decision_elaborator.md must list scout_brief as an allowed citation kind (DJ-104 preserved on the agent that now authors citations)")
	assert.Contains(t, text, "web",
		"spec_decision_elaborator.md must list web as an allowed citation kind (DJ-124 Phase 1 extended the enum for grounded-research evidence)")
	assert.NotContains(t, text, "do not fabricate a citation kind for it",
		"spec_decision_elaborator.md must not retain the legacy anti-grounding rule that forced scout-derived facts into best_practice citations")
}

// TestNarrativeElaboratorPromptsConsumeDecisionsAsReferences locks in
// DJ-124 Stage B: the rewritten spec_feature_elaborator.md and
// spec_strategy_elaborator.md must describe the new
// decisions-by-reference role — the elaborators consume a
// pre-populated decision-ID list rather than authoring decisions
// inline. Without explicit prompt coverage of this responsibility
// the model can fall back to the pre-DJ-124 inline-authoring shape
// and emit decision objects the new schema rejects.
func TestNarrativeElaboratorPromptsConsumeDecisionsAsReferences(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	for _, file := range []string{
		".borg/agents/spec_feature_elaborator.md",
		".borg/agents/spec_strategy_elaborator.md",
	} {
		body, err := fsys.ReadFile(file)
		require.NoError(t, err, "read %s", file)
		text := string(body)

		assert.Contains(t, text, "decision-ID list",
			"%s must describe the input as a decision-ID list — the narrative-elaborator consumes IDs rather than authoring decision objects", file)
		assert.Contains(t, text, "pre-populated",
			"%s must describe the decision-ID list as pre-populated by the scout + workflow — the elaborator does not author the list", file)
		assert.Contains(t, text, "verbatim",
			"%s must say the pre-populated list is copied verbatim — invented or omitted IDs are the failure mode the new architecture eliminates", file)
		assert.Contains(t, text, "do not author",
			"%s must state the narrative-elaborator does not author decisions — the per-axis decision-elaborator owns that role under DJ-124", file)
	}
}

// TestElaboratorPromptsForbidDecisionsOmission locks in DJ-105: the
// feature and strategy elaborator prompts must NOT carry the legacy
// "or omit `decisions` entirely" escape hatch that contradicted the
// "every feature/strategy MUST have at least one inline decision"
// mandate four lines later. The contradiction (combined with the
// schema's omitempty on Decisions) gave preview-tier models like
// gemini-3.1-pro-preview a permission slip to emit decision-less
// outputs that passed strict-mode validation at the API layer but
// failed the spec-validator at the persist layer, killing
// `refine goals` runs.
func TestElaboratorPromptsForbidDecisionsOmission(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	for _, file := range []string{
		".borg/agents/spec_feature_elaborator.md",
		".borg/agents/spec_strategy_elaborator.md",
	} {
		body, err := fsys.ReadFile(file)
		require.NoError(t, err, "read %s", file)
		text := string(body)

		assert.NotContains(t, text, "or omit `decisions` entirely",
			"%s must not authorize omitting the decisions array; that contradicts the 'every feature/strategy MUST have at least one inline decision' mandate", file)
		assert.NotContains(t, text, "omit `decisions` entirely",
			"%s must not carry any phrasing of the decisions-omission escape hatch", file)
	}
}

// TestDecisionElaboratorPromptDescribesGroundedCitations locks in
// DJ-124 Phase 3: the new spec_decision_elaborator.md must describe
// the grounded-research workflow — the `web` citation kind, the
// presence of search as the input the agent uses, and the literal-
// sentinel phrasing ported from justify_researcher.md for the two
// search-failure modes. Without this coverage the prompt could drift
// back to ungrounded training-data-recall and the per-call tool_calls
// audit would catch fabricated citations only after they reached
// downstream consumers.
func TestDecisionElaboratorPromptDescribesGroundedCitations(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_decision_elaborator.md")
	require.NoError(t, err, "read .borg/agents/spec_decision_elaborator.md")
	text := string(body)

	assert.Contains(t, text, "web",
		"spec_decision_elaborator.md must name the `web` citation kind — DJ-124 Phase 1 extended Citation.Kind to include it for grounded research evidence")
	assert.Contains(t, text, "search",
		"spec_decision_elaborator.md must describe search as the grounded-research input the agent uses to verify version numbers / pricing / rejection-reason claims")
	assert.Contains(t, text, "finding ungrounded",
		"spec_decision_elaborator.md must carry the literal-sentinel phrasing ported from justify_researcher.md so the audit tooling that greps for ungrounded findings continues to work")
}

// TestDecisionElaboratorPromptDescribesAxesAndSurfacedBy locks in
// DJ-124 Phase 3: the new spec_decision_elaborator.md must describe
// the per-axis scope of the agent and the back-reference fields on
// the output schema. The agent is dispatched once per OpenAxis; the
// axes[] and surfaced_by[] output fields mirror the input. Without
// prompt coverage the model can fall back to authoring sweep-of-the-
// project decisions ignoring the structural back-references the
// explain/justify verbs depend on.
func TestDecisionElaboratorPromptDescribesAxesAndSurfacedBy(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_decision_elaborator.md")
	require.NoError(t, err, "read .borg/agents/spec_decision_elaborator.md")
	text := string(body)

	assert.Contains(t, text, "axes",
		"spec_decision_elaborator.md must name the `axes` output field — it's the structural link from decision back to the foundational axis the scout dispatched on")
	assert.Contains(t, text, "surfaced_by",
		"spec_decision_elaborator.md must name the `surfaced_by` back-reference field — it mirrors the input surfacing-node IDs so explain/justify verbs can walk the graph in both directions")
	containsScope := strings.Contains(text, "one axis") ||
		strings.Contains(text, "per axis") ||
		strings.Contains(text, "the axis") ||
		strings.Contains(text, "ONE foundational axis")
	assert.True(t, containsScope,
		"spec_decision_elaborator.md must describe the agent's per-axis scope (the agent is dispatched once per OpenAxis; one decision per axis)")
}

// TestDecisionElaboratorPromptForbidsFabricatedRejection locks in
// DJ-124 Phase 3: the new spec_decision_elaborator.md must carry the
// structural-guardrail wording on alternative-rejection citations
// and must explicitly name the fabricated-rejection failure mode.
// Fabricated rejection reasoning (claims like "Auth0 was rejected
// because [made-up cost claim]") is the dominant failure surface
// this agent guards against; the prompt naming the failure mode
// alongside the schema's minItems=1 enforcement on alternative
// citations is the documented mitigation.
func TestDecisionElaboratorPromptForbidsFabricatedRejection(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_decision_elaborator.md")
	require.NoError(t, err, "read .borg/agents/spec_decision_elaborator.md")
	text := string(body)

	assert.Contains(t, text, "citations",
		"spec_decision_elaborator.md must mention citations as the structural guardrail on alternative rejection reasoning")
	// The prompt must use the alternatives-mandate framing to tie
	// citations to alternative rejection reasoning specifically.
	assert.Contains(t, text, "alternative",
		"spec_decision_elaborator.md must name alternatives in the mandate so the citation-on-rejection guardrail is anchored to the right field")
	containsFabricationLabel := strings.Contains(text, "fabricat") ||
		strings.Contains(text, "made-up")
	assert.True(t, containsFabricationLabel,
		"spec_decision_elaborator.md must explicitly name the fabricated-rejection failure mode so the model has direct guidance on what the alternative-citations guardrail exists to prevent")
}

// TestDecisionElaboratorReviseModeSectionPresent locks in DJ-126
// Phase 1: spec_decision_elaborator.md gains a second mode — revise an
// existing decision in response to a critic finding. The prompt must
// name the revise-mode section and the two input blocks (Prior
// decision; Critic finding) the workflow projects when dispatching
// the agent for revision. Without explicit prompt coverage the model
// falls back to first-author behaviour and authors a fresh decision
// ignoring the prior commitment.
func TestDecisionElaboratorReviseModeSectionPresent(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_decision_elaborator.md")
	require.NoError(t, err, "read .borg/agents/spec_decision_elaborator.md")
	text := string(body)

	assert.Contains(t, text, "Revise mode",
		"spec_decision_elaborator.md must carry a Revise mode section heading — DJ-126 Phase 1 adds the second mode to handle critic-driven decision revision")
	assert.Contains(t, text, "Prior decision",
		"spec_decision_elaborator.md must describe the Prior decision input block the revise projection injects (the existing decision being revised)")
	assert.Contains(t, text, "Critic finding",
		"spec_decision_elaborator.md must describe the Critic finding input block the revise projection injects (the concern driving the revision)")
}

// TestDecisionElaboratorPromptPreservesAxesInRevise locks in DJ-126
// Phase 1: the revise-mode section must mandate axis-ID preservation
// because axes are the dispatch key mergeDecisions's replace-by-axis-ID
// logic uses to recognize the output as a revision of the prior
// decision. A revise output that changes the axes is treated as a
// first-author decision on a new axis, leaving the prior unrevised
// and the critic finding unresolved.
func TestDecisionElaboratorPromptPreservesAxesInRevise(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_decision_elaborator.md")
	require.NoError(t, err, "read .borg/agents/spec_decision_elaborator.md")
	text := string(body)

	reviseIdx := strings.Index(text, "Revise mode")
	require.Positive(t, reviseIdx, "Revise mode section heading must be present (TestDecisionElaboratorReviseModeSectionPresent guards this; this test sequences on it)")
	after := text[reviseIdx:]

	assert.Contains(t, after, "verbatim",
		"Revise mode section must say the axes are preserved verbatim from the prior decision — anything else would break mergeDecisions's replace-by-axis-ID match")
	assert.Contains(t, after, "axes",
		"Revise mode section must name the `axes` field as the field whose IDs the agent preserves")
}

// TestScoutPromptDescribesAxesAndConvergence locks in DJ-124 Phase 2:
// the rewritten spec_scout.md must walk the new ScoutBrief shape —
// axes_open[] as the gap output, the decision-mapper pass, and the
// convergence judge. Without explicit prompt coverage of these
// responsibilities the model can fall back to the pre-DJ-124
// senior-engineer-brief framing and ignore the structural fields the
// workflow controller drives off.
func TestScoutPromptDescribesAxesAndConvergence(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_scout.md")
	require.NoError(t, err, "read .borg/agents/spec_scout.md")
	text := string(body)

	assert.Contains(t, text, "axes_open",
		"spec_scout.md must mention the axes_open field by name — it's the structural gap output the workflow dispatches on")
	assert.Contains(t, text, "decision-mapper",
		"spec_scout.md must describe the decision-mapper pass that pre-populates new_nodes[].decisions[] from existing covered axes")
	assert.Contains(t, text, "converged",
		"spec_scout.md must describe the convergence judge — the converged flag drives the loop's exit condition")
}

// TestDecisionElaboratorPromptDescribesCandidateListSection locks in
// DJ-132 Phase 3: spec_decision_elaborator.md gains an "Initial
// dispatch with candidate list" section describing how to engage
// with the survey output. Without explicit prompt coverage of the
// new input block the elaborator falls back to its prior commit-mode
// enumeration discipline and the survey's effect doesn't materialize.
//
// The test asserts on the load-bearing pieces of the new section:
//
//   - the section heading exists,
//   - the elaborator engages every unpicked surveyed candidate as an
//     alternative (the structural condition for the pre-populated
//     alternatives slice DJ-132 was designed to produce),
//   - the elaborator may surface candidates beyond the survey
//     (anti-anchoring; reversal criterion (c) on DJ-132 is the
//     failure mode this guidance addresses).
// TestSpecToolPromptsDescribeUnifiedView locks in the corrected
// wording across the council-aware agent prompts. The prior text
// claimed `spec_list_manifest` and `spec_get` "continue to read the
// persisted spec graph on disk; only spec_search is redirected to
// the in-flight surface during the council" — false since DJ-125
// Phase 3 swapped all three. The mis-information caused models to
// discount valid spec_get results during the trace pathology that
// motivated the May 2026 follow-up fixes, and contributed to tool-
// loop exhaustion on Gemini 3.5 Flash.
//
// The corrected wording must:
//
//   - state that all three tools return a unified view during a
//     council run (in-flight + persisted),
//   - describe the per-entry origin / working fields the manifest
//     now carries,
//   - tell the model what to do when spec_get returns not-found
//     (pick from the inlined id list rather than guessing variants).
func TestSpecToolPromptsDescribeUnifiedView(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	for _, agent := range []string{
		"spec_decision_elaborator",
		"spec_feature_elaborator",
		"spec_strategy_elaborator",
		"spec_candidate_survey",
	} {
		t.Run(agent, func(t *testing.T) {
			body, err := fsys.ReadFile(".borg/agents/" + agent + ".md")
			require.NoError(t, err)
			text := string(body)

			// Negative: the stale wording is gone.
			assert.NotContains(t, text, "continue to read the persisted spec graph on disk",
				"%s must not carry the stale claim that spec_list_manifest and spec_get only read the persisted graph — DJ-125 Phase 3 swapped all three onto the in-flight overlay", agent)
			assert.NotContains(t, text, "only `spec_search` is redirected",
				"%s must not carry the stale 'only spec_search is redirected' claim", agent)

			// Positive: the corrected unified-view language.
			containsUnifiedClaim := strings.Contains(text, "all three tools") ||
				strings.Contains(text, "all three return a view") ||
				strings.Contains(text, "unified view") ||
				strings.Contains(text, "unified spec graph")
			assert.True(t, containsUnifiedClaim,
				"%s must state that all three tools return a unified view of in-flight + persisted during a council run", agent)
			assert.Contains(t, text, "origin",
				"%s must describe the per-entry origin field (settled / proposed)", agent)
			assert.Contains(t, text, "working",
				"%s must describe the per-entry working flag", agent)
		})
	}
}

// TestCouncilAwareAgentsHaveNoGreenfieldEmptyPriming locks in the
// follow-up fix to the May 2026 trace pathology: the model on Gemini
// 3.5 Flash misread populated tool results as "empty manifest"
// because several agent prompts primed it to expect emptiness on
// "greenfield" runs. The priming was incorrect — `locutus refine` can
// run against a project with prior persisted commitments at any time,
// and even on a brand-new project the council's iter-1 commits land
// in the in-flight overlay that subsequent iterations see.
//
// The corrected wording describes the tools as returning a UNIFIED
// view (in-flight + persisted) with each entry carrying an `origin`
// field. The negative assertions below guarantee the stale priming
// language doesn't creep back across any council-aware agent.
//
// Companion to TestSpecToolPromptsDescribeUnifiedView — that test
// asserts the load-bearing positive language across the four agents
// with full tool-section templates; this test asserts the broader
// negative across every council-aware agent, including the four
// without the full template (reconciler, scout, architect, outliner).
func TestCouncilAwareAgentsHaveNoGreenfieldEmptyPriming(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	// Every council-aware agent — runs inside the spec-generation
	// council (not the adopt-time scout / gap_analyst, which operate
	// on persisted-only context and where "greenfield → empty"
	// remains accurate).
	for _, agent := range []string{
		"spec_decision_elaborator",
		"spec_feature_elaborator",
		"spec_strategy_elaborator",
		"spec_candidate_survey",
		"spec_reconciler",
		"spec_scout",
		"spec_architect",
		"spec_outliner",
	} {
		t.Run(agent, func(t *testing.T) {
			body, err := fsys.ReadFile(".borg/agents/" + agent + ".md")
			require.NoError(t, err)
			text := string(body)

			// Negative: stale "greenfield → empty" priming is gone.
			// These specific phrasings primed Gemini 3.5 Flash to
			// discount populated manifest results in the May 2026
			// trace; if they creep back any of three failure modes
			// resurface (tool-loop spirals; manifest-discount reasoning;
			// the model citing "the existing spec is empty" when the
			// in-flight overlay has just-committed work).
			for _, stale := range []string{
				"manifest will be empty",
				"Greenfield runs need no lookups",
				"greenfield runs have nothing to find",
				"snapshot of the existing spec",
				"the project is greenfield and the tools return empty",
				"the project is greenfield and no lookups will return anything",
				"Greenfield runs (no existing-spec flag) need no lookups",
			} {
				assert.NotContains(t, text, stale,
					"%s must not carry the stale '%s' priming — it tells the model to expect empty manifests when the unified in-flight + persisted view can be populated by prior commitments or by sibling council steps", agent, stale)
			}

			// Positive (low bar): some mention of the current spec
			// graph being a unified or per-entry-tagged view, so the
			// model knows the tools are useful even on initial-refine
			// runs once iter-1 commits land.
			containsUnifiedHint := strings.Contains(text, "unified view") ||
				strings.Contains(text, "all three tools") ||
				strings.Contains(text, "current spec graph") ||
				strings.Contains(text, "origin")
			assert.True(t, containsUnifiedHint,
				"%s must mention the unified-view shape (the tools return both settled and proposed nodes) so the model doesn't fall back to 'tools return empty on greenfield' priors", agent)
		})
	}
}

func TestDecisionElaboratorPromptDescribesCandidateListSection(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_decision_elaborator.md")
	require.NoError(t, err, "read .borg/agents/spec_decision_elaborator.md")
	text := string(body)

	assert.Contains(t, text, "Candidate list",
		"spec_decision_elaborator.md must describe the Candidate list input block the DJ-132 projection injects on initial dispatch")
	assert.Contains(t, text, "Initial dispatch with candidate list",
		"spec_decision_elaborator.md must carry the Initial dispatch with candidate list section heading — the section is where DJ-132's elaborator-side discipline lives")

	// Every unpicked surveyed candidate becomes an alternative — the
	// load-bearing structural condition for the pre-populated
	// alternatives slice DJ-132 is designed to produce.
	assert.Contains(t, text, "unpicked",
		"spec_decision_elaborator.md must say every unpicked surveyed candidate becomes an alternative entry")

	// Anti-anchoring: the elaborator may surface candidates beyond
	// the survey when the axis warrants. Reversal criterion (c) is
	// the failure mode this guidance addresses (elaborator anchors
	// so hard on the survey it stops considering missed candidates).
	containsAntiAnchoring := strings.Contains(text, "beyond the survey") ||
		strings.Contains(text, "additional candidates") ||
		strings.Contains(text, "candidates the survey missed")
	assert.True(t, containsAntiAnchoring,
		"spec_decision_elaborator.md must explicitly permit the elaborator to add candidates beyond the survey (DJ-132 anti-anchoring; reversal criterion (c))")

	// The candidate-list-absent fallthrough must be documented so
	// revise dispatches and survey-misfire axes don't leave the
	// elaborator without instructions.
	assert.Contains(t, text, "absent",
		"spec_decision_elaborator.md must describe what to do when the Candidate list section is absent (revise dispatches; survey misfires)")
}

// TestCandidateSurveyAgentScaffoldedWithFastTierGroundedFrontmatter
// locks in DJ-132 Phase 1: spec_candidate_survey.md must ship with the
// scaffold, declare fast-tier providers across the three deployers,
// keep grounding on (load-bearing for currency + hallucination
// prevention), declare thinking off (enumeration is not a reasoning
// task), and bind to output_schema: CandidateList. The frontmatter
// contract is structurally part of DJ-132's mechanism: a survey that
// runs on the strong tier wastes budget; one without grounding
// regresses to training-data-only enumeration with hallucinated /
// stale candidates; one with the wrong schema bypasses the elaborator's
// downstream projection wiring.
func TestCandidateSurveyAgentScaffoldedWithFastTierGroundedFrontmatter(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	body, err := fsys.ReadFile(".borg/agents/spec_candidate_survey.md")
	require.NoError(t, err, "read .borg/agents/spec_candidate_survey.md")
	text := string(body)

	assert.Contains(t, text, "id: spec_candidate_survey",
		"frontmatter must declare id: spec_candidate_survey")
	assert.Contains(t, text, "output_schema: CandidateList",
		"frontmatter must bind output_schema: CandidateList — the survey emits the registered CandidateList shape")
	assert.Contains(t, text, "tier: fast",
		"frontmatter must declare fast-tier model preferences across providers (enumeration is discovery work; spending strong tier is wasted budget)")
	assert.Contains(t, text, "grounding: true",
		"frontmatter must keep grounding on — DJ-132 documents grounding as load-bearing for currency + hallucination prevention; training-data-only enumeration produces stale / invented candidates")
	assert.Contains(t, text, "thinking: off",
		"frontmatter must declare thinking off — enumeration is not a deep reasoning task; thinking on doubles cost without improving quality")

	// The prompt must frame enumeration-vs-judgment explicitly so the
	// task framing is unambiguous; this is the structural condition
	// DJ-132 exists to enforce, not a stylistic note.
	assert.Contains(t, text, "enumerate",
		"prompt must explicitly frame the task as enumeration (DJ-132 cognitive-task separation)")
	assert.Contains(t, text, "not judge",
		"prompt must explicitly tell the survey not to judge — judgment is the elaborator's job, mixing it in re-creates the task conflation the survey exists to break")
}

func TestScaffoldCreatesDirectories(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	dirs := []string{
		".borg",
		".borg/spec/features",
		".borg/spec/bugs",
		".borg/spec/decisions",
		".borg/spec/strategies",
		".borg/spec/entities",
		".borg/history",
		".borg/agents",
		".agents/skills",
	}
	for _, dir := range dirs {
		info, statErr := fsys.Stat(dir)
		assert.NoError(t, statErr, "directory should exist: %s", dir)
		if info != nil {
			assert.True(t, info.IsDir(), "should be a directory: %s", dir)
		}
	}
}

func TestScaffoldCreatesManifest(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "my-project")
	assert.NoError(t, err)

	data, err := fsys.ReadFile(".borg/manifest.json")
	assert.NoError(t, err)

	var m spec.Manifest
	err = json.Unmarshal(data, &m)
	assert.NoError(t, err)

	assert.Equal(t, "my-project", m.ProjectName)
	assert.Equal(t, "0.1.0", m.Version)
	assert.False(t, m.CreatedAt.IsZero(), "created_at should be set")
}

func TestScaffoldCreatesTraces(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	data, err := fsys.ReadFile(".borg/spec/traces.json")
	assert.NoError(t, err)

	var idx spec.TraceabilityIndex
	err = json.Unmarshal(data, &idx)
	assert.NoError(t, err)

	assert.NotNil(t, idx.Entries, "entries should be initialized, not nil")
	assert.Empty(t, idx.Entries, "entries should be empty")
}

func TestScaffoldCreatesGoals(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "my-app")
	assert.NoError(t, err)

	data, err := fsys.ReadFile("GOALS.md")
	assert.NoError(t, err)

	assert.Contains(t, string(data), "my-app")
}

func TestScaffoldCreatesAgents(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	// All 15 agent definition files should exist under .borg/agents/.
	agents := []string{
		".borg/agents/planner.md",
		".borg/agents/critic.md",
		".borg/agents/researcher.md",
		".borg/agents/stakeholder.md",
		".borg/agents/historian.md",
		".borg/agents/convergence.md",
		".borg/agents/scout.md",
		".borg/agents/backend_analyzer.md",
		".borg/agents/frontend_analyzer.md",
		".borg/agents/infra_analyzer.md",
		".borg/agents/gap_analyst.md",
		".borg/agents/remediator.md",
		".borg/agents/validator.md",
		".borg/agents/guide.md",
		".borg/agents/reviewer.md",
	}
	for _, agent := range agents {
		_, statErr := fsys.Stat(agent)
		assert.NoError(t, statErr, "agent file should exist: %s", agent)
	}
}

func TestScaffoldSeedsModelsYAML(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	data, err := fsys.ReadFile(".borg/models.yaml")
	assert.NoError(t, err, ".borg/models.yaml should be seeded by init so users can edit per-project model preferences")
	assert.NotEmpty(t, data)
	// The seeded content must match the embedded source of truth byte-for-byte.
	assert.Equal(t, agent.EmbeddedModelsYAML(), data)
}

func TestResetOverwritesEmbeddedArtifacts(t *testing.T) {
	// User has scaffolded a project, then edited an agent .md file.
	// `update --reset` should overwrite that edit with the binary's
	// embedded version of the same agent.
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	// Locally modify spec_architect.md so we can detect overwrite.
	const localEdit = "# LOCAL EDIT — should be overwritten by Reset\n"
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_architect.md", []byte(localEdit), 0o644))
	got, err := fsys.ReadFile(".borg/agents/spec_architect.md")
	require.NoError(t, err)
	require.Equal(t, localEdit, string(got), "precondition: local edit was written")

	report, err := scaffold.Reset(fsys)
	require.NoError(t, err)

	got, err = fsys.ReadFile(".borg/agents/spec_architect.md")
	require.NoError(t, err)
	assert.NotEqual(t, localEdit, string(got),
		"Reset should have overwritten the local edit with the embedded version")
	assert.Contains(t, string(got), "spec_architect",
		"the new content should be the embedded spec_architect.md (frontmatter mentions its id)")

	// Report should list the reset agent files and the models.yaml flag.
	assert.NotEmpty(t, report.AgentsReset, "report should record reset agent files")
	assert.Contains(t, report.AgentsReset, ".borg/agents/spec_architect.md")
	assert.True(t, report.ModelsReset, "report should record models.yaml refresh")
}

func TestResetLeavesUserContentAlone(t *testing.T) {
	// Reset must NOT touch GOALS.md, .borg/spec/, .borg/history/,
	// .borg/manifest.json, .locutus/. User content survives the
	// refresh untouched.
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	// Plant user content under each preserved location.
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/history", 0o755))
	require.NoError(t, fsys.MkdirAll(".locutus/sessions", 0o755))
	require.NoError(t, fsys.WriteFile("GOALS.md", []byte("user goals"), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-x.json", []byte(`{"id":"dec-x"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/history/some-event.json", []byte(`{}`), 0o644))
	require.NoError(t, fsys.WriteFile(".locutus/sessions/session.yaml", []byte(`session_id: x`), 0o644))

	manifestBefore, err := fsys.ReadFile(".borg/manifest.json")
	require.NoError(t, err)

	_, err = scaffold.Reset(fsys)
	require.NoError(t, err)

	got, err := fsys.ReadFile("GOALS.md")
	require.NoError(t, err)
	assert.Equal(t, "user goals", string(got), "GOALS.md must survive Reset untouched")

	got, err = fsys.ReadFile(".borg/spec/decisions/dec-x.json")
	require.NoError(t, err)
	assert.Equal(t, `{"id":"dec-x"}`, string(got), ".borg/spec/ must survive Reset untouched")

	got, err = fsys.ReadFile(".borg/history/some-event.json")
	require.NoError(t, err)
	assert.Equal(t, `{}`, string(got), ".borg/history/ must survive Reset untouched")

	got, err = fsys.ReadFile(".locutus/sessions/session.yaml")
	require.NoError(t, err)
	assert.Equal(t, `session_id: x`, string(got), ".locutus/ runtime state must survive Reset untouched")

	manifestAfter, err := fsys.ReadFile(".borg/manifest.json")
	require.NoError(t, err)
	assert.Equal(t, manifestBefore, manifestAfter, ".borg/manifest.json must survive Reset untouched")
}

func TestResetRemovesOrphanAgents(t *testing.T) {
	// If the user has an agent .md under .borg/agents/ whose id
	// isn't in the binary's embedded scaffold (either a renamed/
	// removed scaffold agent from a prior version or a custom
	// file), Reset removes it. Locutus only loads agents by known
	// id, so orphan files weren't being used by anything anyway —
	// removing them on reset keeps the project tree clean as the
	// scaffold evolves.
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))
	require.NoError(t, fsys.WriteFile(".borg/agents/my_custom_agent.md", []byte("custom"), 0o644))

	report, err := scaffold.Reset(fsys)
	require.NoError(t, err)

	_, err = fsys.ReadFile(".borg/agents/my_custom_agent.md")
	assert.Error(t, err, "orphan agent file must be removed by Reset")
	assert.Contains(t, report.AgentsRemoved, ".borg/agents/my_custom_agent.md",
		"removed-agents list must record what was deleted so the CLI printer can surface it")
}

func TestResetLeavesEmbeddedAgentsAfterRemoval(t *testing.T) {
	// Companion to the orphan-removal test: confirm Reset doesn't
	// remove agents that ARE in the embedded scaffold (i.e. an
	// override file with a matching id is overwritten, not deleted).
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	// Write a project override with a real scaffold id; Reset
	// should overwrite (not remove) it.
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_advocate.md",
		[]byte("# overridden by user; scaffold should overwrite"), 0o644))

	report, err := scaffold.Reset(fsys)
	require.NoError(t, err)

	got, err := fsys.ReadFile(".borg/agents/spec_advocate.md")
	require.NoError(t, err, "overridden scaffold agent must still exist after Reset")
	assert.NotContains(t, string(got), "overridden by user",
		"override content must be replaced with embedded scaffold")
	assert.NotContains(t, report.AgentsRemoved, ".borg/agents/spec_advocate.md",
		"a file whose id is in the embedded scaffold must not appear in AgentsRemoved")
}

func TestScaffoldIdempotent(t *testing.T) {
	fsys := specio.NewMemFS()

	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	err = scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err, "second run of Scaffold should not error")
}
