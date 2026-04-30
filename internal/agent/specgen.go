package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// SpecProposal is the structured output of the spec-generation council.
// It mirrors the persisted spec graph but excludes computed fields
// (timestamps, status defaults) the persistence layer fills in. This
// keeps the architect agent's contract minimal — it proposes content,
// the persistence layer stamps the rest.
type SpecProposal struct {
	Features   []FeatureProposal  `json:"features,omitempty"`
	Decisions  []DecisionProposal `json:"decisions,omitempty"`
	Strategies []StrategyProposal `json:"strategies,omitempty"`
	Approaches []ApproachProposal `json:"approaches,omitempty"`
}

// FeatureProposal is an LLM-friendly subset of spec.Feature.
type FeatureProposal struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Decisions          []string `json:"decisions,omitempty"`
	Approaches         []string `json:"approaches,omitempty"`
}

// DecisionProposal is an LLM-friendly subset of spec.Decision. Citations
// and ArchitectRationale denormalize the decision's justification into
// the spec node itself per DJ-085, so the persisted Decision carries
// durable provenance independent of the .locutus/sessions/ transcript.
type DecisionProposal struct {
	ID                 string             `json:"id"`
	Title              string             `json:"title"`
	Rationale          string             `json:"rationale"`
	Confidence         float64            `json:"confidence"`
	Alternatives       []spec.Alternative `json:"alternatives,omitempty"`
	Citations          []spec.Citation    `json:"citations,omitempty"`
	ArchitectRationale string             `json:"architect_rationale,omitempty"`
	InfluencedBy       []string           `json:"influenced_by,omitempty"`
}

// StrategyProposal is an LLM-friendly subset of spec.Strategy. Body is
// the prose narrative persisted as the .md body alongside the JSON
// sidecar.
type StrategyProposal struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	Body       string   `json:"body"`
	Decisions  []string `json:"decisions,omitempty"`
	Approaches []string `json:"approaches,omitempty"`
}

// ApproachProposal is an LLM-friendly subset of spec.Approach.
type ApproachProposal struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ParentID string `json:"parent_id"`
	Body     string `json:"body"`
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
// tier, and the workflow YAML (.borg/workflows/spec_generation.yaml)
// declares the round shape. Edit those files to tune behavior.
type SpecGenRequest struct {
	GoalsBody    string
	DocumentBody string
	DocumentID   string
	Existing     *ExistingSpec

	// Capability, Model, CritiqueRounds — advisory; see type comment.
	Capability     CapabilityTier
	Model          string
	CritiqueRounds int

	// Sink, when non-nil, receives a WorkflowEvent for every agent
	// step (started/completed/error). Drives the CLI spinner UI and
	// MCP progress notifications. Nil sink is silent — same effect as
	// passing SilentSink{}.
	Sink EventSink
}

// ScoutBrief is the structured output of the spec_scout agent. The
// proposer reads this alongside GOALS.md and reacts to it — picking
// among the listed options and committing to specific values for each
// implicit assumption. Schema is registered in schemas.go so Genkit's
// structured-output path enforces it at the API layer.
type ScoutBrief struct {
	DomainRead          string   `json:"domain_read"`
	TechnologyOptions   []string `json:"technology_options"`
	ImplicitAssumptions []string `json:"implicit_assumptions"`
	WatchOuts           []string `json:"watch_outs"`
}

// CriticIssues is the structured output of every critic on the council
// (architect_critic, devops_critic, sre_critic, cost_critic). Each issue
// is one specific, actionable finding; the workflow's
// merge_as=critic_issues handler flattens them into PlanningState.Concerns
// for the revise step to address.
type CriticIssues struct {
	Issues []string `json:"issues"`
}

// GenerateSpec runs the spec-generation council to derive a spec graph
// from project goals and (optionally) a feature/design document.
//
// The council is defined declaratively:
//   - Agents in .borg/agents/spec_*.md and *_critic.md (loaded at runtime
//     from the project's FS, originally seeded from internal/scaffold/agents/
//     by `locutus init`).
//   - Workflow shape in .borg/workflows/spec_generation.yaml, defining
//     the rounds: survey → propose → critique (parallel) → revise.
//
// Editing those files tunes the council without rebuilding. Per-agent
// model tier comes from each agent's frontmatter, resolved against
// .borg/models.yaml at LLM-call time.
//
// The returned SpecProposal's referential integrity is best-effort —
// callers should run ValidateAndStrip before persistence to catch what
// the critic missed.
func GenerateSpec(ctx context.Context, llm LLM, fsys specio.FS, req SpecGenRequest) (*SpecProposal, error) {
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

	wf, err := LoadWorkflow(fsys, ".borg/workflows/spec_generation.yaml")
	if err != nil {
		return nil, fmt.Errorf("load spec-generation workflow: %w", err)
	}

	executor := &WorkflowExecutor{
		LLM:       llm,
		AgentDefs: agentDefs,
		Workflow:  wf,
	}

	// Bridge workflow events to the caller's sink. Buffered generously
	// since emitEvent now blocks on a full channel — a stuck consumer
	// would otherwise stall the council. 64 covers the worst case
	// (every agent fires started+completed in tight succession).
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
		sink.Close()
	}()

	prompt := buildSpecGenPrompt(req)

	results, err := executor.Run(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("spec-generation council: %w", err)
	}

	proposalJSON := lastProposalOutput(results)
	if proposalJSON == "" {
		return nil, fmt.Errorf("spec-generation council produced no proposer output")
	}

	var proposal SpecProposal
	if err := json.Unmarshal([]byte(proposalJSON), &proposal); err != nil {
		return nil, fmt.Errorf("parse spec proposal: %w (content=%q)", err, proposalJSON)
	}
	return &proposal, nil
}

// buildSpecGenPrompt assembles the seed prompt the workflow executor
// passes to every agent (each agent's projection function picks what it
// needs out of this).
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
		b.WriteString("\n\n## Existing spec (reuse these IDs when extending)\n\n")
		summarizeExistingSpec(&b, req.Existing)
	}
	return b.String()
}

// lastProposalOutput walks the executor results in reverse and returns
// the most recent SpecProposal-shaped output: revise wins over propose
// (the council's last word), propose wins when no revise fired (clean
// proposal didn't need revision).
func lastProposalOutput(results []RoundResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		r := results[i]
		if r.Err != nil || r.Output == "" {
			continue
		}
		if r.StepID == "revise" || r.StepID == "propose" {
			return r.Output
		}
	}
	return ""
}

func summarizeExistingSpec(b *strings.Builder, e *ExistingSpec) {
	if len(e.Features) > 0 {
		b.WriteString("Features:\n")
		for _, f := range e.Features {
			fmt.Fprintf(b, "- %s: %s\n", f.ID, f.Title)
		}
	}
	if len(e.Decisions) > 0 {
		b.WriteString("\nDecisions:\n")
		for _, d := range e.Decisions {
			fmt.Fprintf(b, "- %s: %s (confidence=%.2f)\n", d.ID, d.Title, d.Confidence)
		}
	}
	if len(e.Strategies) > 0 {
		b.WriteString("\nStrategies:\n")
		for _, s := range e.Strategies {
			fmt.Fprintf(b, "- %s: %s (kind=%s)\n", s.ID, s.Title, s.Kind)
		}
	}
	if len(e.Approaches) > 0 {
		b.WriteString("\nApproaches:\n")
		for _, a := range e.Approaches {
			fmt.Fprintf(b, "- %s: %s\n", a.ID, a.Title)
		}
	}
}

// IntegrityWarning describes a single referential-integrity violation
// detected in a SpecProposal: a node that references an id that doesn't
// exist in either the proposal itself or the existing spec. The
// reference is stripped from the proposal so persistence proceeds with
// only valid edges.
type IntegrityWarning struct {
	NodeKind  string // "feature", "strategy", "approach"
	NodeID    string // the node carrying the dangling reference
	Field     string // "decisions", "approaches", "parent_id"
	MissingID string // the id that wasn't found
}

func (w IntegrityWarning) String() string {
	return fmt.Sprintf("%s %s.%s references unknown id %q (stripped)", w.NodeKind, w.NodeID, w.Field, w.MissingID)
}

// ValidateAndStrip removes references in the proposal that don't resolve
// to a node in either the proposal itself or the supplied existing spec
// snapshot. Returns the list of warnings (one per stripped reference) so
// the caller can surface them. The proposal is mutated in place.
//
// Rules:
//   - Feature.Decisions, Feature.Approaches: each id must resolve.
//   - Strategy.Decisions, Strategy.Approaches: same.
//   - Approach.ParentID: must resolve to a feature or strategy. Approaches
//     with no resolvable parent are removed entirely (an orphan approach
//     has no anchor in the graph).
func (p *SpecProposal) ValidateAndStrip(existing *ExistingSpec) []IntegrityWarning {
	if p == nil {
		return nil
	}

	known := indexKnownIDs(p, existing)
	var warnings []IntegrityWarning

	for i := range p.Features {
		p.Features[i].Decisions, warnings = filterRefs(p.Features[i].Decisions, known.decisions, warnings, "feature", p.Features[i].ID, "decisions")
		p.Features[i].Approaches, warnings = filterRefs(p.Features[i].Approaches, known.approaches, warnings, "feature", p.Features[i].ID, "approaches")
	}
	for i := range p.Strategies {
		p.Strategies[i].Decisions, warnings = filterRefs(p.Strategies[i].Decisions, known.decisions, warnings, "strategy", p.Strategies[i].ID, "decisions")
		p.Strategies[i].Approaches, warnings = filterRefs(p.Strategies[i].Approaches, known.approaches, warnings, "strategy", p.Strategies[i].ID, "approaches")
	}

	// Approaches with unresolvable parents have no anchor — drop them.
	kept := p.Approaches[:0]
	for _, ap := range p.Approaches {
		if _, ok := known.parentable[ap.ParentID]; !ok {
			warnings = append(warnings, IntegrityWarning{
				NodeKind:  "approach",
				NodeID:    ap.ID,
				Field:     "parent_id",
				MissingID: ap.ParentID,
			})
			continue
		}
		kept = append(kept, ap)
	}
	p.Approaches = kept
	return warnings
}

type knownIDs struct {
	decisions  map[string]struct{}
	approaches map[string]struct{}
	// parentable holds ids that an Approach.ParentID is allowed to point
	// at — features and strategies, since approaches hang off either.
	parentable map[string]struct{}
}

func indexKnownIDs(p *SpecProposal, existing *ExistingSpec) knownIDs {
	k := knownIDs{
		decisions:  map[string]struct{}{},
		approaches: map[string]struct{}{},
		parentable: map[string]struct{}{},
	}
	for _, d := range p.Decisions {
		k.decisions[d.ID] = struct{}{}
	}
	for _, a := range p.Approaches {
		k.approaches[a.ID] = struct{}{}
	}
	for _, f := range p.Features {
		k.parentable[f.ID] = struct{}{}
	}
	for _, s := range p.Strategies {
		k.parentable[s.ID] = struct{}{}
	}
	if existing != nil {
		for _, d := range existing.Decisions {
			k.decisions[d.ID] = struct{}{}
		}
		for _, a := range existing.Approaches {
			k.approaches[a.ID] = struct{}{}
		}
		for _, f := range existing.Features {
			k.parentable[f.ID] = struct{}{}
		}
		for _, s := range existing.Strategies {
			k.parentable[s.ID] = struct{}{}
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
			Title:              fp.Title,
			Status:             spec.FeatureStatusProposed,
			Description:        fp.Description,
			AcceptanceCriteria: fp.AcceptanceCriteria,
			Decisions:          fp.Decisions,
			Approaches:         fp.Approaches,
		})
	}
	for _, dp := range p.Decisions {
		d := spec.Decision{
			ID:           dp.ID,
			Title:        dp.Title,
			Status:       spec.DecisionStatusProposed,
			Rationale:    dp.Rationale,
			Confidence:   dp.Confidence,
			Alternatives: dp.Alternatives,
			InfluencedBy: dp.InfluencedBy,
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
			ID:         sp.ID,
			Title:      sp.Title,
			Kind:       spec.StrategyKind(sp.Kind),
			Status:     "proposed",
			Decisions:  sp.Decisions,
			Approaches: sp.Approaches,
		})
	}
	for _, ap := range p.Approaches {
		r.Approaches = append(r.Approaches, spec.Approach{
			ID:       ap.ID,
			Title:    ap.Title,
			ParentID: ap.ParentID,
			Body:     ap.Body,
		})
	}
	return r
}
