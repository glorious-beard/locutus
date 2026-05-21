package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/search"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// SpecProposal is the structured output of the spec-generation council.
// It mirrors the persisted spec graph but excludes computed fields
// (timestamps, status defaults) the persistence layer fills in. This
// keeps the architect agent's contract minimal — it proposes content,
// the persistence layer stamps the rest.
//
// Approaches are intentionally absent: per the council-resilience plan,
// approaches are synthesized at adopt time when real code context exists,
// not invented during spec generation. The architect produces features,
// strategies, and decisions; adopt fills in approaches per parent.
type SpecProposal struct {
	Features   []FeatureProposal  `json:"features,omitempty" jsonschema:"description=User-facing capabilities. Decisions referenced from Decisions[] are listed here by id under Feature.Decisions; not inlined."`
	Decisions  []DecisionProposal `json:"decisions,omitempty" jsonschema:"description=Canonical architectural decisions; deduped across the proposal by the reconciler. Referenced by id from Feature.Decisions and Strategy.Decisions."`
	Strategies []StrategyProposal `json:"strategies,omitempty" jsonschema:"description=Cross-cutting engineering commitments. Decisions referenced from Decisions[] are listed here by id."`
	// ConflictActions records reconciler conflict resolutions that
	// flipped a decision under one or more parent feature/strategy.
	// Populated by GenerateSpec after the workflow runs; consumed by
	// the persistence caller to fire cascade rewrites once the
	// affected parents are on disk. Omitted from JSON because it's
	// workflow telemetry, not part of the proposal's persisted shape.
	ConflictActions []AppliedAction `json:"-"`
}

// FeatureProposal is an LLM-friendly subset of spec.Feature.
type FeatureProposal struct {
	ID string `json:"id" jsonschema:"description=Stable slug starting with 'feat-'; lowercase; hyphen-separated (e.g. 'feat-realtime-dashboard'). Preserved verbatim from the source RawFeatureProposal through reconciliation."`
	// Summary: one-sentence "what" description, threaded from the
	// architect's RawFeatureProposal through reconciliation onto the
	// persisted Feature. Optional in the schema; the prereq fills any
	// gaps after-the-fact.
	Summary            string   `json:"summary,omitempty" jsonschema:"description=One-sentence what-the-feature-does; ending with a period. Read by scanning agents via spec_list_manifest; distinct from Description."`
	Title              string   `json:"title" jsonschema:"description=Concise human-readable title in sentence case. A noun phrase."`
	Description        string   `json:"description" jsonschema:"description=Full paragraph describing what the feature does — actor; trigger; outcome."`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty" jsonschema:"description=Testable assertions that gate the feature as shipped. Each entry in the form 'When X happens; Y is observable.'"`
	Decisions          []string `json:"decisions,omitempty" jsonschema:"description=IDs (starting 'dec-') of decisions this feature relies on. Empty when no canonical decisions are tied to the feature."`
}

// DecisionProposal is an LLM-friendly subset of spec.Decision. Citations
// and ArchitectRationale denormalize the decision's justification into
// the spec node itself per DJ-085, so the persisted Decision carries
// durable provenance independent of the .locutus/sessions/ transcript.
type DecisionProposal struct {
	ID string `json:"id" jsonschema:"description=Stable slug starting with 'dec-'; assigned canonically by the reconciler at apply time when inline decisions are deduped across features and strategies."`
	// Summary: see FeatureProposal.Summary.
	Summary            string             `json:"summary,omitempty" jsonschema:"description=One-sentence what-was-decided; ending with a period."`
	Title              string             `json:"title" jsonschema:"description=Concise human-readable title naming the decision. A noun phrase."`
	Rationale          string             `json:"rationale" jsonschema:"description=Multi-sentence prose explaining why this choice was made over the alternatives. Distinct from ArchitectRationale (one-sentence) and Summary (the what)."`
	Confidence         float64            `json:"confidence" jsonschema:"description=Confidence on a 0.0 to 1.0 scale. 1.0 = fully committed; 0.5 = leaning but reversible."`
	Alternatives       []spec.Alternative `json:"alternatives,omitempty" jsonschema:"description=The other options weighed; each with rationale and rejection reasoning."`
	Citations          []spec.Citation    `json:"citations,omitempty" jsonschema:"description=Sources backing the decision: GOALS.md clauses; vendor docs; prior decisions. Verbatim excerpts."`
	ArchitectRationale string             `json:"architect_rationale,omitempty" jsonschema:"description=One-sentence summary of why this choice fits the architecture; used by the reconciler when matching inline duplicates across features."`
	InfluencedBy       []string           `json:"influenced_by,omitempty" jsonschema:"description=IDs of other decisions whose outcome made this decision necessary or constrained the option set. Empty when the decision stands on its own."`
	// Locked propagates the DJ-128 cap-as-commit flag through the
	// build pipeline so persistence sees it on spec.Decision. Not in
	// the model-facing schema (omitted from the LLM example); set by
	// the workflow controller after the council terminates.
	Locked bool `json:"-"`
}

// StrategyProposal is an LLM-friendly subset of spec.Strategy. Body is
// the prose narrative persisted as the .md body alongside the JSON
// sidecar.
type StrategyProposal struct {
	ID string `json:"id" jsonschema:"description=Stable slug starting with 'strat-'; lowercase; hyphen-separated."`
	// Summary: see FeatureProposal.Summary.
	Summary   string   `json:"summary,omitempty" jsonschema:"description=One-sentence what-the-strategy-adopts; ending with a period."`
	Title     string   `json:"title" jsonschema:"description=Concise human-readable title in sentence case. A noun phrase."`
	Kind      string   `json:"kind" jsonschema:"description=Category of cross-cutting concern (foundational; derived; quality)."`
	Body      string   `json:"body" jsonschema:"description=The prose argument for the strategy: what is being adopted; why; what the system-wide consequences are. Multi-paragraph allowed."`
	Decisions []string `json:"decisions,omitempty" jsonschema:"description=IDs (starting 'dec-') of decisions this strategy relies on."`
}

// SpecGenRequest holds inputs for GenerateSpec. GoalsBody is required.
// DocumentBody is optional — when provided, the call is "elaborate the
// spec graph for THIS feature/document under the project's goals." The
// admitting flow (`import`) sets DocumentID to the id of the just-admitted
// feature so the LLM can extend that node rather than re-introducing it.
//
// Capability, Model, and CritiqueRounds are retained for backwards
// compatibility but are advisory in the workflow path: the council's
// agents (defined in .borg/agents/<role>.md) declare their own model
// tier; the workflow shape is defined in code as SpecGenerationWorkflow.
// Edit agent .md files to tune per-agent behavior.
type SpecGenRequest struct {
	GoalsBody    string
	DocumentBody string
	DocumentID   string
	Existing     *ExistingSpec

	// Imported is external content admitted into this generation pass
	// via `locutus import`. Empty for `locutus refine` runs. The scout
	// reads these alongside GOALS.md and the existing graph so its
	// gap analysis covers the new content's axes; the workflow handles
	// dispatch from there (DJ-124 Phase 6).
	Imported []ImportedContent

	// CritiqueRounds — advisory; see type comment.
	CritiqueRounds int

	// Sink, when non-nil, receives a WorkflowEvent for every agent
	// step (started/completed/error). Drives the CLI spinner UI and
	// MCP progress notifications. Nil sink is silent — same effect as
	// passing SilentSink{}.
	Sink EventSink
}

// ScoutBrief is the structured output of the spec_scout agent under
// DJ-124. The scout has three coupled responsibilities: domain survey
// (the original four fields), gap analysis (axes_open[] surfaces
// foundational axes no decision in the current graph covers), and
// decision-mapping (new_nodes[].decisions[] references existing
// decisions that already cover the new node's axes). The Converged
// flag drives the spec-council exit condition: true exactly when
// axes_open is empty AND the iteration carries no outstanding critic
// findings. Schema is registered in schemas.go so the structured-
// output path enforces it at the API layer.
type ScoutBrief struct {
	DomainRead           string               `json:"domain_read" jsonschema:"description=A paragraph or two describing what the scout believes the project is about based on GOALS.md and the (optional) feature/design document. Names the domain (e.g. 'campaign software for political organizing'); the user(s); and the central capability. Concrete; not generic — 'real-time collaboration on geospatial data' beats 'a web app'."`
	TechnologyOptions    []string             `json:"technology_options" jsonschema:"description=Specific candidate technology choices the decision-elaborator should weigh on a per-axis basis (compute platform; data store; frontend framework; etc.). Each entry names a real product/library ('Postgres with PostGIS'; 'Next.js App Router'); not a category ('a database'; 'a frontend'). Supporting content the scout surfaces during axis identification — the decision-elaborator picks among these per axis."`
	ImplicitAssumptions  []string             `json:"implicit_assumptions" jsonschema:"description=Assumptions GOALS.md makes implicitly that the scout has surfaced for the decision-elaborator (e.g. 'expected concurrent-user count'; 'data sensitivity classification'; 'team size and tenure'). Each entry names the assumption clearly enough that the elaborator can commit to a value when it picks per axis. Supporting context for axis identification; the load-bearing gap output is axes_open."`
	WatchOuts            []string             `json:"watch_outs" jsonschema:"description=Risks; gotchas; or non-obvious constraints the decision-elaborator should be aware of (e.g. 'election-cycle traffic seasonality: months of near-zero load followed by 6-week sprint'; 'PII handling regulations vary by state'). Each entry actionable; not generic."`
	AxesOpen             []OpenAxis           `json:"axes_open" jsonschema:"description=Foundational axes the scout identified that no decision in the current graph covers. Each entry is one axis the loop must resolve before convergence. Empty array exactly when Converged is true. The dispatcher in Phase 5 spawns one decision-elaborator per entry."`
	NewNodes             []NewSpecNode        `json:"new_nodes" jsonschema:"description=New feature or strategy nodes the scout identified from imported content or goal-shape analysis. Each entry pre-populates its decisions[] with existing-decision IDs that cover the node's axes; new decisions get appended at decision-creation time. Empty when no new nodes surface this iteration."`
	CritiqueDimensions   []CritiqueDimension  `json:"critique_dimensions" jsonschema:"description=Critique surfaces the scout has identified for the council's critic-elaborator to challenge this proposal on (DJ-129). Each dimension is one lens with a focus question and grounded source evidence. The workflow's critique step dispatches one critic-elaborator call per dimension. Empty array is valid when the proposal is too thin to critique yet (e.g. iter 0 with no decisions) or when GOALS explicitly suppresses categories the scout would otherwise surface."`
	ConcernDispositions  []ConcernDisposition `json:"concern_dispositions" jsonschema:"description=DJ-125 Phase 7: one entry per concern the scout grades from the open set this iteration. Concern IDs come from the manifest's c-N positions (the open concerns in the prompt's outstanding-findings section). Empty array when no concerns are open this iteration (the mechanical pre-pass already disposed them). Each entry's disposition tells the workflow whether the concern still blocks convergence; the justification field carries the scout's one-sentence rationale."`
	Converged            bool                 `json:"converged" jsonschema:"description=True only when AxesOpen is empty AND no concern's effective status is open (after applying the dispositions in ConcernDispositions). Loop exits as the queue drains. False otherwise; the loop continues with another iteration."`
}

// ConcernDisposition is one scout-graded verdict on an open concern.
// DJ-125 Phase 7: the scout receives the manifest with concerns
// marked open (after the mechanical pre-pass stales the easy cases)
// and grades each as addressed / wontfix / still_open with a
// one-sentence justification. The mechanical pre-pass owns the stale
// disposition so the scout never has to emit it.
type ConcernDisposition struct {
	ConcernID     string `json:"concern_id" jsonschema:"description=The concern's ID as rendered in the manifest's Concerns section (e.g. 'c-3'). Must match one of the concerns the prompt lists as open this iteration; unknown ids are skipped at merge time and the convergence judgment treats the concern as still open."`
	Disposition   string `json:"disposition" jsonschema:"enum=addressed,enum=wontfix,enum=still_open,description=addressed: the current proposal resolves the concern (justification names the resolving decision or design choice). wontfix: a real concern but representing a tradeoff that's acceptable (justification names the tradeoff being accepted). still_open: the concern is unaddressed and convergence cannot hold."`
	Justification string `json:"justification" jsonschema:"description=One-sentence rationale for the disposition. For addressed: name the specific decision or design choice that resolves it. For wontfix: name the tradeoff being accepted in plain terms. For still_open: name the specific gap the proposal still has. A complete sentence; not a one-word label."`
}

// OpenAxis names one foundational axis the current graph does not
// cover. The scout emits one entry per gap in ScoutBrief.AxesOpen;
// the Phase 5 dispatcher spawns one decision-elaborator per entry to
// research the option set and commit to a choice. Axis IDs are stable
// across iterations so the workflow can detect a cycle (the same axis
// re-opening after a decision was made).
type OpenAxis struct {
	ID             string   `json:"id" jsonschema:"description=Stable slug identifying the axis — lowercase / hyphen-separated / three to five words derived from what's being decided (e.g. 'auth-provider'; 'compute-platform'; 'rollout-cadence'). Stable across iterations so the loop can detect cycle behaviour (axis reopened after being decided)."`
	Description    string   `json:"description" jsonschema:"description=One-sentence statement of what needs to be decided on this axis — a noun phrase plus the deciding question (e.g. 'Auth provider: who owns the user identity store and how do clients authenticate against it?'). Concrete enough that the decision-elaborator knows what to research and pick."`
	SourceEvidence []string `json:"source_evidence" jsonschema:"description=Verbatim text excerpts from goals / features / strategies / imported content that surfaced this axis. Each entry is a span the elaborator can cite back to. At least one entry; empty means the axis was invented and the integrity check rejects it.,minItems=1"`
	SurfacedBy     []string `json:"surfaced_by" jsonschema:"description=Spec node IDs (goal / feature / strategy) whose content surfaced this axis. Mirrors Decision.SurfacedBy on the eventual decision. At least one entry.,minItems=1"`
}

// CritiqueDimension is one critique surface the scout has identified
// for the council's critic-elaborator to challenge (DJ-129). Each
// dimension carries a project-specific framing (focus_question +
// source_evidence) plus a bounded discipline-enum slice that drives
// the critic-elaborator prompt's grounding sections.
//
// Lens is a free-form grouping label that drives Concern.Kind for the
// revise projection but is not constrained by code — projects can
// declare any lens (e.g. "compliance", "election-cycle-traffic") and
// the workflow dispatches the same parametric critic-elaborator.
// Disciplines is bounded to keep the elaborator's prompt sections
// finite; the saturated set covers all grounding patterns the council
// supports.
type CritiqueDimension struct {
	ID             string   `json:"id" jsonschema:"description=Stable slug identifying this dimension across iterations — lowercase / hyphen-separated / three to five words derived from the focus (e.g. 'cost-ceiling-coverage'; 'voter-file-privacy'; 'election-cycle-traffic'). Stable across iterations so dimensionsAreStable can detect new-dimension additions."`
	Lens           string   `json:"lens" jsonschema:"description=Free-form grouping label naming the category of concern (e.g. 'cost'; 'sre'; 'compliance'; 'security'; 'vendor-portability'). Drives Concern.Kind for grouping in the revise projection; not constrained by code. Pick the most specific label that fits."`
	FocusQuestion  string   `json:"focus_question" jsonschema:"description=A complete-sentence question framing what the critic should challenge on this dimension (e.g. 'Does every paid SaaS or compute commitment engage with the $150/mo ceiling in GOALS §3?'). Concrete enough that the critic-elaborator can read it and immediately know what to look for."`
	SourceEvidence []string `json:"source_evidence" jsonschema:"description=Verbatim text excerpts from GOALS / spec nodes / imported content that surfaced this dimension. Each entry is a span the critic can cite back to. At least one entry; empty means the dimension was invented and the dispatcher should reject it.,minItems=1"`
	Disciplines    []string `json:"disciplines" jsonschema:"enum=web_grounded,enum=spec_node_grounded,enum=best_practice_grounded,enum=goals_grounded,enum=freeform,minItems=1,description=Bounded enum slice naming the grounding patterns the critic must apply. web_grounded=cite URLs with verbatim excerpts; spec_node_grounded=cite other spec nodes by id; best_practice_grounded=cite named principles; goals_grounded=cite GOALS.md clauses with verbatim excerpts; freeform=no specific grounding required. Multiple disciplines compose (e.g. a cost dimension may require both web_grounded for vendor pricing and goals_grounded for the budget clause)."`
	SeverityFloor  string   `json:"severity_floor" jsonschema:"enum=high,enum=medium,enum=low,description=Default severity for concerns surfaced on this dimension. high=blocks shipping; medium=worth addressing; low=polish-pass note. The critic-elaborator may emit higher-severity concerns than the floor when warranted."`
}

// NewSpecNode names a new feature or strategy the scout identified
// from imported content or goal-shape analysis. The Decisions slice
// is the scout's decision-mapper output: every entry is an existing
// decision ID that already covers an axis this node references.
// Newly-decided axes get appended to Decisions by the Phase 5
// workflow controller once their decision-elaborators land their
// outputs.
type NewSpecNode struct {
	Kind      string   `json:"kind" jsonschema:"enum=feature,enum=strategy,description=Whether this is a new feature (user-visible capability) or strategy (cross-cutting commitment)."`
	ID        string   `json:"id" jsonschema:"description=Stable slug for the node — starts with 'feat-' (features) or 'strat-' (strategies); lowercase; hyphen-separated; three to five words derived from the title."`
	Title     string   `json:"title" jsonschema:"description=Concise human-readable title in sentence case naming the node — a noun phrase rather than a sentence (e.g. 'Real-time dashboard'; 'Compute platform')."`
	Summary   string   `json:"summary" jsonschema:"description=One-sentence what-the-node-does (features) or what-the-node-adopts (strategies); ending with a period. The narrative-elaborator in Phase 4 picks this up as the seed for the full description/body."`
	Decisions []string `json:"decisions" jsonschema:"description=Decision IDs from the existing graph that already cover axes this node references. The scout pre-populates this list from its decision-mapper pass; the workflow appends newly-created decision IDs for axes that were in AxesOpen and got decided this iteration. Empty array is valid when no existing decision covers any axis this node references — in that case every axis the node depends on must appear in AxesOpen."`
}

// CriticIssues is the structured output of every critic on the council
// (architect_critic, devops_critic, sre_critic, cost_critic). Under
// DJ-128 each issue is a structured CriticIssue carrying weakness,
// evidence, and an enumerated counterproposal menu; the merge layer
// flattens them into PlanningState.Concerns for the revise step.
//
// Pre-DJ-128 the shape was `Issues []string` (free-form objection text);
// the new shape forces critics to enumerate concrete alternatives so the
// elaborator's revise pass can engage the menu rather than guess what
// the critic wanted, and so revisions land in the deliberation log
// (alternatives) with the critic's own argument + citations preserved
// verbatim.
type CriticIssues struct {
	Issues []CriticIssue `json:"issues" jsonschema:"description=The critic's structured findings. Each entry is one specific, actionable issue with a weakness statement, evidence supporting it, and an enumerated menu of counterproposals the elaborator can pick from. Empty issues array is the convergence signal (the critic found nothing this iteration)."`
}

// CriticIssue is one structured critic finding (DJ-128). The shape
// mirrors AdversarialConcern's discipline (Weakness + Evidence) and
// adds an enumerated counterproposal menu so the elaborator's revise
// pass picks from concrete alternatives the critic committed to,
// rather than guessing what the critic wanted.
//
// RelatedDecisionIDs is the critic's own structured surfacing of which
// decisions the issue targets. Merged with the regex-extracted set in
// mergeCriticIssues (critic-provided wins on conflict).
type CriticIssue struct {
	Weakness          string                   `json:"weakness" jsonschema:"description=A complete sentence naming the specific weakness in the current proposal. Concrete enough that a reader who hasn't seen the proposal can tell what's wrong without re-reading the rationale. Cites the spec node id or GOALS.md clause when relevant. Not a generic complaint like 'not enough error handling'."`
	Evidence          string                   `json:"evidence" jsonschema:"description=A complete sentence with concrete support for the weakness — draws from the proposal's own rationale, GOALS.md clauses, named engineering practices, or current vendor/library behaviour. Names the source the elaborator should engage with."`
	Counterproposals  []CriticCounterproposal  `json:"counterproposals" jsonschema:"description=The enumerated menu of concrete alternatives the critic would accept in place of the current proposal. List every option you would accept on this dimension — do not pick one arbitrarily and do not omit candidates. The elaborator's revise pass evaluates the full menu and either picks one as the new chosen option or rejects all of them coherently. Each counterproposal becomes an alternative entry in the deliberation log with its Argument and Citations preserved verbatim.,minItems=1"`
	RelatedDecisionIDs []string                `json:"related_decision_ids,omitempty" jsonschema:"description=Decision IDs (starting 'dec-') this issue targets — the critic's structured surfacing of which decisions need revision. Merged with the regex-extracted set in mergeCriticIssues; the critic's list wins on conflict. Empty when the issue spans the whole proposal rather than a specific decision."`
}

// CriticCounterproposal is one entry in a CriticIssue's enumerated
// counterproposal menu (DJ-128). Each counterproposal is a concrete
// alternative the critic commits to: a specific vendor, configuration,
// architectural pattern, or behavior — not "use something else."
//
// All three fields are required content. The validator
// (degenerateCriticIssueValidator) rejects placeholder Options like
// "dummy" / "TBD" / one-word answers, empty Citations on non-sentinel
// Options, and Arguments shorter than a sentence. The exception is the
// literal "needs investigation" sentinel: when a critic sees a real
// problem but cannot name a concrete alternative, emitting a single
// counterproposal with Option == "needs investigation" + empty Citations
// is acceptable — the concern surfaces to the user as advisory-only
// (the merge marks it Advisory, hasReviseableConcerns skips it).
type CriticCounterproposal struct {
	Option    string          `json:"option" jsonschema:"description=The concrete alternative the critic commits to — a specific vendor; configuration; architectural pattern; or behavior the elaborator could pick from (e.g. 'Aurora Serverless v2 for the OLTP store'; 'lower the availability SLO from 99.9% to 99.5%'; 'switch from Datadog to CloudWatch + Sentry'). Names a specific product or pattern that the elaborator's revise pass can engage with point-by-point. The one exception is the literal sentinel 'needs investigation' — emit that exactly when you see a real problem but genuinely cannot name a specific alternative; the concern then surfaces as advisory-only."`
	Argument  string          `json:"argument" jsonschema:"description=A complete sentence stating positively why this option is superior to the current decision on the dimension the Weakness names. Argues with the prior chosen path's rationale; does not just restate the weakness. The elaborator's revise pass folds this Argument verbatim into the alternative's Rationale field when the counterproposal lands in the deliberation log."`
	Citations []spec.Citation `json:"citations" jsonschema:"description=Sources grounding the Argument — GOALS.md clauses; vendor docs; web-fetched research; named best practices; other spec nodes. minItems=1 unless Option is the literal sentinel 'needs investigation' (which permits empty citations). Citation kinds and reference format match the canonical Citation discipline used elsewhere in the spec."`
}

// GenerateSpec runs the spec-generation council to derive a spec graph
// from project goals and (optionally) a feature/design document.
//
// The council is defined declaratively:
//   - Agents in .borg/agents/spec_*.md and *_critic.md (loaded at runtime
//     from the project's FS, originally seeded from internal/scaffold/agents/
//     by `locutus init`). Editable per-project.
//   - Workflow shape in SpecGenerationWorkflow (this package), defining
//     the rounds: survey → outline → elaborate → reconcile → critique →
//     cluster_findings → revise → reconcile_revise.
//
// Per-agent model tier comes from each agent's frontmatter, resolved
// against .borg/models.yaml at LLM-call time.
//
// The returned proposal is guaranteed to be referentially clean —
// every id referenced in features[].decisions, strategies[].decisions,
// etc. resolves to either a node in the proposal itself or a node in
// req.Existing. This is enforced by an integrity-revise loop: when
// the council's output has dangling refs, the architect agent is
// invoked one more time with the violations as concerns. After
// MaxIntegrityRetries failed attempts, GenerateSpec returns an
// IntegrityViolationError instead of producing a degraded proposal —
// silent stripping would mask a council failure the user cares about.
func GenerateSpec(ctx context.Context, exec AgentExecutor, fsys specio.FS, req SpecGenRequest) (*SpecProposal, error) {
	budget := readSpecGateBudget()
	var historian *history.Historian
	if fsys != nil {
		historian = history.NewHistorian(fsys, ".borg/history")
	}
	wf := NewSpecGenerationWorkflow(historian, budget)
	return generateSpecWithWorkflow(ctx, exec, fsys, req, wf)
}

// logSpecSearchAggregate emits the per-council spec_search summary
// (DJ-123 Phase 5). Called once from the deferred council-teardown
// path. Pulls totals + empty-result rate off the collector and lands
// them on a single slog.Info line so the DJ-123 reversal criterion (a)
// threshold (>25% empty) can be measured from operator-side logs.
//
// The summary is intentionally a single log line rather than a new
// session-trace file: the per-call records live on the collector for
// in-process consumers (tests, future operator surfaces); the
// reversal-criteria threshold reads only the aggregate, which is what
// surfaces here.
//
// Per-agent breakdown is deliberately absent: the calling-agent
// identity isn't plumbed into the spec_search handler, and the
// aggregate rate is what feeds the reversal criterion regardless of
// which elaborator issued the queries.
func logSpecSearchAggregate(m *SpecSearchMetrics) {
	if m == nil {
		return
	}
	agg := m.Aggregate()
	if agg.TotalCalls == 0 {
		// No spec_search activity this run — emit a minimal note so
		// the audit trail still shows the council was instrumented,
		// just nothing fired. Cheaper than a structured zero-row.
		slog.Info("spec_search metrics: no calls this run", "dj", "DJ-123")
		return
	}
	slog.Info("spec_search metrics for council run",
		"dj", "DJ-123",
		"total_calls", agg.TotalCalls,
		"completed_calls", agg.CompletedCalls,
		"empty_calls", agg.EmptyCalls,
		"error_calls", agg.ErrorCalls,
		"empty_rate", agg.EmptyRate,
		"reversal_threshold", SpecSearchEmptyRateThreshold,
		"above_threshold", agg.EmptyRate > SpecSearchEmptyRateThreshold,
	)
}

// specSearchSwap returns the SwappableSpecSearch wired on the
// underlying AgentExecutor, or nil when none was wired (CLI path that
// failed to open the on-disk index; mocks that don't opt into the swap
// surface). Returning nil keeps the council path additive — when the
// wiring is absent, GenerateSpec runs without the in-flight index,
// identical to the pre-DJ-123 behaviour.
//
// Detection is via a small structural interface rather than a concrete
// *Executor assertion: both production *Executor and a test-extended
// MockExecutor satisfy it, so the swap-and-restore path can be driven
// end-to-end from tests without spinning up real adapters.
func specSearchSwap(exec AgentExecutor) *SwappableSpecSearch {
	p, ok := exec.(interface {
		SpecSearch() *SwappableSpecSearch
	})
	if !ok || p == nil {
		return nil
	}
	return p.SpecSearch()
}

// specListManifestSwap mirrors specSearchSwap for the DJ-125
// spec_list_manifest swappable. Returns nil when the executor doesn't
// expose one (mocks that don't opt in; CLI paths that failed to wire
// the registry); the council degrades to the on-disk default in that
// case, identical to pre-DJ-125 behaviour.
func specListManifestSwap(exec AgentExecutor) *SwappableSpecListManifest {
	p, ok := exec.(interface {
		SpecListManifest() *SwappableSpecListManifest
	})
	if !ok || p == nil {
		return nil
	}
	return p.SpecListManifest()
}

// specGetSwap is the spec_get counterpart.
func specGetSwap(exec AgentExecutor) *SwappableSpecGet {
	p, ok := exec.(interface {
		SpecGet() *SwappableSpecGet
	})
	if !ok || p == nil {
		return nil
	}
	return p.SpecGet()
}

// readSpecGateBudget returns the iteration cap for the spec-council
// convergence gate. LOCUTUS_SPEC_GEN_MAX_ITERATIONS overrides the
// default when set to a positive integer. Invalid or zero values are
// ignored — the workflow constructor falls back to the default.
func readSpecGateBudget() int {
	raw := strings.TrimSpace(os.Getenv("LOCUTUS_SPEC_GEN_MAX_ITERATIONS"))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// defaultDecisionRevisionCap is the per-axis cap on revise replacements
// when LOCUTUS_DECISION_REVISION_CAP is unset or invalid. Three is the
// plan-validated threshold: a revision dispatch that gets revised
// itself a third time is oscillating, not converging, and the loop
// should force-terminate so the operator can intervene rather than
// burning budget against a moving target.
const defaultDecisionRevisionCap = 3

// readDecisionRevisionCap returns the per-axis revision-count cap.
// LOCUTUS_DECISION_REVISION_CAP overrides defaultDecisionRevisionCap
// when set to a positive integer; invalid / zero / negative values
// fall back to the default. Mirrors readSpecGateBudget's pattern.
func readDecisionRevisionCap() int {
	raw := strings.TrimSpace(os.Getenv("LOCUTUS_DECISION_REVISION_CAP"))
	if raw == "" {
		return defaultDecisionRevisionCap
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultDecisionRevisionCap
	}
	return n
}

// generateSpecWithWorkflow runs the spec-generation council with the
// given workflow. Production callers go through GenerateSpec, which
// wires SpecGenerationWorkflow. Tests use this entry point to inject a
// simpler workflow that focuses the assertion surface.
func generateSpecWithWorkflow(ctx context.Context, exec AgentExecutor, fsys specio.FS, req SpecGenRequest, wf *Workflow[PlanningState]) (*SpecProposal, error) {
	if strings.TrimSpace(req.GoalsBody) == "" {
		return nil, fmt.Errorf("GenerateSpec: GoalsBody is required")
	}
	if fsys == nil {
		return nil, fmt.Errorf("GenerateSpec: fsys is required (load agents from .borg/agents/)")
	}

	defs, err := LoadAgentDefs(fsys, ".borg/agents")
	if err != nil {
		return nil, fmt.Errorf("load council agents: %w", err)
	}
	agentDefs := make(map[string]AgentDef, len(defs))
	for _, d := range defs {
		agentDefs[d.ID] = d
	}

	executor := &WorkflowExecutor[PlanningState]{
		Executor:  exec,
		AgentDefs: agentDefs,
		Workflow:  wf,
	}

	// Bridge workflow events to the caller's sink. Buffered generously
	// since emitEvent now blocks on a full channel — a stuck consumer
	// would otherwise stall the council. 64 covers the worst case
	// (every agent fires started+completed in tight succession).
	//
	// Sink lifecycle: the caller owns Close(). GenerateSpec drains and
	// closes only the bridge channel, not the sink itself, so the cmd
	// layer can keep using the same sink for direct LLM calls that
	// happen after the spec-generation pass returns (rewriter,
	// synthesizer, advocate, etc.). Closing the sink here would tear
	// down the pterm MultiPrinter mid-run and silently drop those
	// events.
	sink := req.Sink
	if sink == nil {
		sink = SilentSink{}
	}
	events := make(chan WorkflowEvent, 64)
	executor.Events = events
	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		for ev := range events {
			sink.OnEvent(ev)
		}
	}()
	defer func() {
		close(events)
		<-bridgeDone
	}()

	prompt := buildSpecGenPrompt(req)

	state := &PlanningState{Prompt: prompt, Round: 1, Existing: req.Existing}
	// DJ-124 Phase 6: thread imported content through to PlanningState
	// so the scout's projection (projectScout) can render it as scoping
	// input for gap analysis. Empty for refine runs; populated by
	// `locutus import`'s post-admission planning pass.
	if len(req.Imported) > 0 {
		state.Imported = make([]ImportedContent, len(req.Imported))
		copy(state.Imported, req.Imported)
	}

	// DJ-125 Phase 3: wire the in-flight overlay for the RAG list/get
	// tools. The InFlightSpecStore carries the current RawProposal +
	// the loaded state.Existing snapshot; the council's merge helpers
	// call inflightStore.Update on every RawProposal mutation so the
	// next agent's spec_list_manifest / spec_get call sees the freshest
	// proposal. Outside the council the swappables fall back to the
	// fsys-backed default provider (wired at registration in cmd/llm.go).
	//
	// Swappables come from the same executor the spec_search swap pulls
	// from; mock executors that don't expose them no-op without breaking
	// the council.
	var inflightStore *InFlightSpecStore
	if listSwap := specListManifestSwap(exec); listSwap != nil {
		inflightStore = NewInFlightSpecStore()
		inflightStore.SetState("", req.Existing)
		prevList := listSwap.Swap(inflightStore)
		state.InFlightSpecStore = inflightStore
		defer listSwap.Swap(prevList)
	}
	if getSwap := specGetSwap(exec); getSwap != nil {
		if inflightStore == nil {
			inflightStore = NewInFlightSpecStore()
			inflightStore.SetState("", req.Existing)
			state.InFlightSpecStore = inflightStore
		}
		prevGet := getSwap.Swap(inflightStore)
		defer getSwap.Swap(prevGet)
	}

	// DJ-123 Phase 3: wire a council-scoped in-flight Bluge index over
	// RawProposal so spec_search calls from council agents return hits
	// against the emerging proposal (not the persisted .borg/spec/
	// graph). The on-disk index is restored on completion via the
	// deferred Swap below. Swappable comes from the production
	// *Executor; mock executors in tests don't provide it, and the
	// council degrades gracefully to no in-flight search (existing
	// behaviour).
	if swap := specSearchSwap(exec); swap != nil {
		inflight, err := search.NewInFlightIndex()
		if err != nil {
			slog.Warn("in-flight spec_search: index init failed; council proceeds without it",
				"error", err)
		} else {
			prev := swap.Swap(inflight)
			state.InFlightIndex = inflight
			// DJ-123 Phase 5: install a council-scoped SpecSearchMetrics
			// collector for the duration of the run. The spec_search
			// tool handler records one entry per call into it; the
			// deferred aggregate log at council end surfaces total
			// calls + empty-result rate, which feeds the DJ-123
			// reversal criterion (a) threshold (>25% empty means
			// BM25-only is no longer viable on the in-flight surface).
			metrics := &SpecSearchMetrics{}
			swap.SetMetrics(metrics)
			defer func() {
				// Restore the disk backend at function exit so
				// subsequent CLI verbs (and any caller that reuses this
				// Executor) get the production corpus back. The
				// integrity-revise loop below runs BEFORE this defer
				// fires, so the architect's repair retries still see the
				// in-flight index — intentional, since the repair
				// operates on the proposal that's still in flight.
				swap.Swap(prev)
				swap.SetMetrics(nil)
				_ = inflight.Close()
				logSpecSearchAggregate(metrics)
			}()
		}
	}

	if _, err := RunCouncil(ctx, executor, state); err != nil {
		return nil, fmt.Errorf("spec-generation council: %w", err)
	}

	// Phase 2: the canonical SpecProposal is the post-reconcile output
	// stored on PlanningState.ProposedSpec. Read it directly off the
	// state pointer rather than walking RoundResults — RoundResult.Output
	// holds the raw agent text (verdict JSON for reconcile, raw proposal
	// JSON for propose/revise), neither of which is the canonical shape
	// downstream callers expect.
	//
	// DJ-124: a converged-at-iter-0 scout produces no RawProposal /
	// ProposedSpec because no downstream steps fired. That's a valid
	// "spec is already complete relative to GOALS.md" outcome — return
	// an empty SpecProposal rather than erroring out, so the caller can
	// treat it as a successful no-op refine.
	proposalJSON := state.ProposedSpec
	if proposalJSON == "" {
		if state.RawProposal == "" {
			return &SpecProposal{}, nil
		}
		return nil, fmt.Errorf("spec-generation council produced no proposer output")
	}

	var proposal SpecProposal
	if err := json.Unmarshal([]byte(proposalJSON), &proposal); err != nil {
		return nil, fmt.Errorf("parse spec proposal: %w (content=%q)", err, proposalJSON)
	}
	proposal.ConflictActions = state.ConflictActions
	// DJ-128: propagate the cap-as-commit Locked flag from PlanningState
	// onto each affected DecisionProposal so ToAssimilationResult
	// projects it onto the persisted spec.Decision.
	if len(state.LockedDecisionIDs) > 0 {
		for i := range proposal.Decisions {
			if _, locked := state.LockedDecisionIDs[proposal.Decisions[i].ID]; locked {
				proposal.Decisions[i].Locked = true
			}
		}
	}

	// Integrity gate. If the proposal references node IDs it didn't
	// emit, ask the architect to repair the proposal rather than
	// silently dropping the dangling refs. After Phase 2 the architect's
	// output is reconciler-built and structurally clean; this loop is
	// a backstop for rare failures (e.g. a malformed reconciler verdict
	// that surfaced as a parse error). The cap is small because a model
	// that fails twice in a row is unlikely to comply on the third try.
	//
	// Events are bridged through the live workflow events channel so
	// the CLI spinner reflects integrity-revise activity in real time
	// — without this the user sees a stalled prompt for tens of seconds
	// while the architect retries.
	for attempt := 0; attempt < MaxIntegrityRetries; attempt++ {
		warnings := proposal.Validate(req.Existing)
		if len(warnings) == 0 {
			return &proposal, nil
		}
		archDef, ok := agentDefs["spec_architect"]
		if !ok {
			// No architect to repair with — surface the violations.
			return nil, &IntegrityViolationError{
				Warnings: warnings,
				Proposal: &proposal,
				Attempts: attempt,
			}
		}
		stepID := fmt.Sprintf("integrity-revise (%d/%d)", attempt+1, MaxIntegrityRetries)
		events <- WorkflowEvent{
			StepID:    stepID,
			AgentID:   "spec_architect",
			Status:    "started",
			Message:   fmt.Sprintf("repairing %d dangling reference(s)", len(warnings)),
			Timestamp: time.Now(),
		}
		repaired, err := reviseForIntegrity(ctx, exec, archDef, prompt, &proposal, warnings)
		if err != nil {
			events <- WorkflowEvent{
				StepID:    stepID,
				AgentID:   "spec_architect",
				Status:    "error",
				Message:   err.Error(),
				Timestamp: time.Now(),
			}
			return nil, fmt.Errorf("integrity-revise attempt %d: %w", attempt+1, err)
		}
		events <- WorkflowEvent{
			StepID:    stepID,
			AgentID:   "spec_architect",
			Status:    "completed",
			Timestamp: time.Now(),
		}
		proposal = *repaired
	}

	// Final check after the retry budget. Returning the violations as
	// a typed error lets callers format them clearly and lets users
	// decide whether to re-run, switch model, or hand-edit.
	if final := proposal.Validate(req.Existing); len(final) > 0 {
		return nil, &IntegrityViolationError{
			Warnings: final,
			Proposal: &proposal,
			Attempts: MaxIntegrityRetries,
		}
	}
	return &proposal, nil
}

// MaxIntegrityRetries caps the number of architect re-roll attempts
// triggered by the post-workflow integrity gate. Two is the sweet
// spot: enough to give a stochastic model a second chance, not so
// many that a stubbornly broken model burns minutes for no value.
const MaxIntegrityRetries = 2

// IntegrityViolationError is returned by GenerateSpec when the
// architect produces a proposal with dangling references and the
// retry budget is exhausted. Callers can format the warning list to
// guide the user (re-run, switch model, or hand-edit). Proposal is
// the last attempt's output, retained so callers can inspect what
// the architect produced even when it wasn't usable.
type IntegrityViolationError struct {
	Warnings []IntegrityWarning
	Proposal *SpecProposal
	Attempts int
}

func (e *IntegrityViolationError) Error() string {
	if e == nil {
		return "spec integrity violation"
	}
	return fmt.Sprintf("spec integrity violation: %d dangling reference(s) after %d revise attempt(s)",
		len(e.Warnings), e.Attempts)
}

// reviseForIntegrity asks the architect agent for one corrected
// SpecProposal given a list of integrity violations. Single LLM call
// — the violations are mechanical so we don't need critics to
// re-discover them, just the architect to fix them.
func reviseForIntegrity(ctx context.Context, exec AgentExecutor, archDef AgentDef, originalPrompt string, prev *SpecProposal, warnings []IntegrityWarning) (*SpecProposal, error) {
	prevJSON, err := json.MarshalIndent(prev, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal previous proposal: %w", err)
	}
	var b strings.Builder
	b.WriteString("STOP. Your previous SpecProposal is rejected because it references node IDs that you did not define.\n\n")
	b.WriteString("This is not a stylistic note. Every id in features[].decisions and strategies[].decisions ")
	b.WriteString("MUST resolve to a node that exists in this proposal or in the existing spec snapshot. ")
	b.WriteString("If it does not, the proposal is invalid and will be rejected again.\n\n")
	b.WriteString("## Specific violations\n\n")
	for _, w := range warnings {
		fmt.Fprintf(&b, "- %s\n", w.String())
	}
	b.WriteString("\nFor each violation above, you must take exactly one of these two actions:\n\n")
	b.WriteString("  1. ADD the missing node to the proposal. Decisions need id, title, rationale, ")
	b.WriteString("confidence, alternatives, citations. Strategies need id, title, kind, body.\n")
	b.WriteString("  2. REMOVE the dangling reference from the node that emitted it.\n\n")
	b.WriteString("Do not paraphrase the violations. Do not acknowledge them in prose. ")
	b.WriteString("Do not re-emit the same broken structure with cosmetic edits. ")
	b.WriteString("Address every listed violation directly in the JSON output below.\n\n")
	b.WriteString("## Original prompt\n\n")
	b.WriteString(originalPrompt)
	b.WriteString("\n\n## Your previous (rejected) proposal\n\n```json\n")
	b.Write(prevJSON)
	b.WriteString("\n```\n\nRe-emit the COMPLETE corrected SpecProposal as a single JSON object. No diff. No partial object. No prose.")

	input := AgentInput{Messages: []Message{{Role: "user", Content: b.String()}}}
	resp, err := exec.Run(WithRole(ctx, "integrity_revise"), archDef, input)
	if err != nil {
		return nil, err
	}
	var out SpecProposal
	if err := json.Unmarshal([]byte(resp.Content), &out); err != nil {
		return nil, fmt.Errorf("parse integrity-revise response: %w", err)
	}
	return &out, nil
}

// buildSpecGenPrompt assembles the seed prompt the workflow executor
// passes to every agent (each agent's projection function picks what it
// needs out of this).
//
// Existing-spec context is delivered via the spec_list_manifest /
// spec_get tools (DJ-094, DJ-115), not inlined. We emit a one-line
// data-state flag when an existing spec is present so the agent knows
// the tools will return non-empty results; on greenfield runs the flag
// is omitted entirely so agents don't burn turns on lookups that would
// return empty. Mirrors projectReconcile's shape.
func buildSpecGenPrompt(req SpecGenRequest) string {
	var b strings.Builder
	b.WriteString("## GOALS.md\n\n")
	b.WriteString(req.GoalsBody)
	if strings.TrimSpace(req.DocumentBody) != "" {
		b.WriteString("\n\n## Feature document\n\n")
		if req.DocumentID != "" {
			fmt.Fprintf(&b, "(This document corresponds to feature id %q — extend it rather than introducing a new feature.)\n\n", req.DocumentID)
		}
		b.WriteString(req.DocumentBody)
	}
	if req.Existing != nil && !req.Existing.IsEmpty() {
		b.WriteString("\n\n## Existing spec is present\n\nA persisted spec snapshot exists at `.borg/spec/`; the `spec_list_manifest` and `spec_get` tools will return non-empty results. Call `spec_list_manifest` first to scan ids + summaries; call `spec_get(id)` only to fetch the full content of a node you need to inspect. Reuse existing ids when extending; mint new ones only for genuinely new concepts. (On greenfield runs this section is omitted.)")
	}
	return b.String()
}

// IntegrityWarning describes a structural defect in a SpecProposal —
// either a dangling reference (a node references an id that doesn't
// exist in either the proposal itself or the existing spec) or a
// missing required field (a feature with no decisions, etc.).
//
// MissingID is populated for dangling-ref warnings; Reason is populated
// for shape violations (no decisions, etc.). String() formats both.
type IntegrityWarning struct {
	NodeKind  string // "feature", "strategy"
	NodeID    string // the node carrying the violation
	Field     string // "decisions"
	MissingID string // the id that wasn't found (dangling-ref warnings)
	Reason    string // describes the violation when MissingID is empty
}

// String renders a warning without claiming what was done about it.
// Callers that strip the dangling ref append their own suffix (e.g.
// " (stripped)"); callers that surface the warning as a hard failure
// leave the fact-statement standing on its own.
func (w IntegrityWarning) String() string {
	if w.MissingID != "" {
		return fmt.Sprintf("%s %s.%s references unknown id %q", w.NodeKind, w.NodeID, w.Field, w.MissingID)
	}
	if w.Reason != "" {
		return fmt.Sprintf("%s %s.%s: %s", w.NodeKind, w.NodeID, w.Field, w.Reason)
	}
	return fmt.Sprintf("%s %s.%s violation", w.NodeKind, w.NodeID, w.Field)
}

// Validate detects structural defects in the proposal without mutating
// it. Returns one IntegrityWarning per violation. Pure check — callers
// use this to decide whether to send the proposal back to the architect
// for repair instead of silently dropping data.
//
// Rules:
//   - Feature.Decisions: each id must resolve.
//   - Strategy.Decisions: each id must resolve.
//   - Feature: must have at least one decision (architect contract;
//     a feature with no decisions has no architectural commitment).
func (p *SpecProposal) Validate(existing *ExistingSpec) []IntegrityWarning {
	if p == nil {
		return nil
	}
	known := indexKnownIDs(p, existing)
	var warnings []IntegrityWarning

	for _, f := range p.Features {
		warnings = appendMissingRefs(warnings, f.Decisions, known.decisions, "feature", f.ID, "decisions")
		if len(f.Decisions) == 0 {
			warnings = append(warnings, IntegrityWarning{
				NodeKind: "feature", NodeID: f.ID, Field: "decisions",
				Reason: "feature has no decisions; every feature must commit to at least one architectural choice",
			})
		}
	}
	for _, s := range p.Strategies {
		warnings = appendMissingRefs(warnings, s.Decisions, known.decisions, "strategy", s.ID, "decisions")
	}
	return warnings
}

// Strip removes references in the proposal that don't resolve to any
// known node. Mutates the proposal in place and returns the warnings
// for the dropped refs.
//
// Strip is the destructive fallback — preferred behaviour is to call
// Validate first and ask the architect to repair. Reach for Strip
// only when the architect has been given a chance to fix the issue
// and refused, or when the caller has explicitly opted into
// best-effort persistence over a hard failure.
func (p *SpecProposal) Strip(existing *ExistingSpec) []IntegrityWarning {
	if p == nil {
		return nil
	}

	known := indexKnownIDs(p, existing)
	var warnings []IntegrityWarning

	for i := range p.Features {
		p.Features[i].Decisions, warnings = filterRefs(p.Features[i].Decisions, known.decisions, warnings, "feature", p.Features[i].ID, "decisions")
	}
	for i := range p.Strategies {
		p.Strategies[i].Decisions, warnings = filterRefs(p.Strategies[i].Decisions, known.decisions, warnings, "strategy", p.Strategies[i].ID, "decisions")
	}
	return warnings
}

// appendMissingRefs records a warning for every id in refs that does
// not resolve in known. Used by Validate; equivalent to filterRefs's
// detection half but without the destructive filtering.
func appendMissingRefs(warnings []IntegrityWarning, refs []string, known map[string]struct{}, nodeKind, nodeID, field string) []IntegrityWarning {
	for _, r := range refs {
		if _, ok := known[r]; ok {
			continue
		}
		warnings = append(warnings, IntegrityWarning{
			NodeKind:  nodeKind,
			NodeID:    nodeID,
			Field:     field,
			MissingID: r,
		})
	}
	return warnings
}

type knownIDs struct {
	decisions map[string]struct{}
}

func indexKnownIDs(p *SpecProposal, existing *ExistingSpec) knownIDs {
	k := knownIDs{
		decisions: map[string]struct{}{},
	}
	for _, d := range p.Decisions {
		k.decisions[d.ID] = struct{}{}
	}
	if existing != nil {
		for _, d := range existing.Decisions {
			k.decisions[d.ID] = struct{}{}
		}
	}
	return k
}

func filterRefs(refs []string, known map[string]struct{}, warnings []IntegrityWarning, nodeKind, nodeID, field string) ([]string, []IntegrityWarning) {
	if len(refs) == 0 {
		return refs, warnings
	}
	kept := refs[:0]
	for _, r := range refs {
		if _, ok := known[r]; ok {
			kept = append(kept, r)
			continue
		}
		warnings = append(warnings, IntegrityWarning{
			NodeKind:  nodeKind,
			NodeID:    nodeID,
			Field:     field,
			MissingID: r,
		})
	}
	return kept, warnings
}

// ToAssimilationResult converts a SpecProposal into an AssimilationResult
// suitable for the existing persistence layer. Strategy bodies are not
// representable on spec.Strategy directly (they live in the .md sidecar),
// so the caller persists them via a parallel path; this conversion
// preserves only the JSON-side fields.
func (p *SpecProposal) ToAssimilationResult() *AssimilationResult {
	if p == nil {
		return nil
	}
	r := &AssimilationResult{}
	for _, fp := range p.Features {
		r.Features = append(r.Features, spec.Feature{
			ID:                 fp.ID,
			Summary:            fp.Summary,
			Title:              fp.Title,
			Status:             spec.FeatureStatusProposed,
			Description:        fp.Description,
			AcceptanceCriteria: fp.AcceptanceCriteria,
			Decisions:          fp.Decisions,
		})
	}
	for _, dp := range p.Decisions {
		d := spec.Decision{
			ID:           dp.ID,
			Summary:      dp.Summary,
			Title:        dp.Title,
			Status:       spec.DecisionStatusProposed,
			Rationale:    dp.Rationale,
			Confidence:   dp.Confidence,
			Alternatives: dp.Alternatives,
			InfluencedBy: dp.InfluencedBy,
			Locked:       dp.Locked,
		}
		// Denormalize provenance onto the decision per DJ-085. We populate
		// only when the architect supplied citations or a summary —
		// otherwise we leave Provenance nil so the spec node looks like
		// what hand-authored or assimilated decisions look like.
		if len(dp.Citations) > 0 || strings.TrimSpace(dp.ArchitectRationale) != "" {
			d.Provenance = &spec.DecisionProvenance{
				Citations:          dp.Citations,
				ArchitectRationale: dp.ArchitectRationale,
			}
		}
		r.Decisions = append(r.Decisions, d)
	}
	for _, sp := range p.Strategies {
		r.Strategies = append(r.Strategies, spec.Strategy{
			ID:        sp.ID,
			Summary:   sp.Summary,
			Title:     sp.Title,
			Kind:      spec.StrategyKind(sp.Kind),
			Status:    "proposed",
			Decisions: sp.Decisions,
		})
	}
	return r
}
