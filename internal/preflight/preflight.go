// Package preflight implements the DJ-071 clarification protocol that runs
// between `planned` and `in_progress` in the reconcile lifecycle. For each
// Approach in a workstream, an LLM extracts ambiguities a coding agent
// would hit during implementation and resolves each one — either by
// locating the answer in the spec graph, or by proposing an assumption
// that lands as a new `assumed` Decision.
//
// New Decisions triggered by pre-flight cascade through the spec graph
// exactly like any other Decision revision (DJ-069): parent Feature /
// Strategy prose is rewritten, child Approaches are marked drifted.
// That cascade runs here in-process — callers get a single Report that
// covers ambiguity resolution, new Decision creation, and the resulting
// drift set.
//
// Protocol is bounded by maxRounds (default 3 per DJ-071). If unresolved
// ambiguities remain at the limit, the remaining ones are converted to
// `assumed` Decisions on the last round, per the DJ's "best-effort"
// clause.
package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
)

// DefaultMaxRounds matches the DJ-071 default.
const DefaultMaxRounds = 3

// ResolutionSource identifies how a pre-flight answer was obtained.
type ResolutionSource string

const (
	// SourceSpec means the answer was already in the spec graph (Feature
	// acceptance criteria, Decision rationale, Strategy constraints).
	SourceSpec ResolutionSource = "spec"
	// SourceAssumed means the pre-flight agent proposed an assumption and
	// Locutus persisted it as a new `assumed` Decision.
	SourceAssumed ResolutionSource = "assumed"
)

// Resolution pairs a question the coding agent would have hit with the
// answer pre-flight produced.
type Resolution struct {
	ApproachID  string           `json:"approach_id"`
	Question    string           `json:"question"`
	Source      ResolutionSource `json:"source"`
	Answer      string           `json:"answer"`
	SpecNodeID  string           `json:"spec_node_id,omitempty"` // set when Source == SourceSpec
	DecisionID  string           `json:"decision_id,omitempty"`  // set when Source == SourceAssumed
}

// Report is what Preflight returns. Callers consume it to bump the
// workstream record's PreFlightDone flag and to surface counts to the user.
type Report struct {
	Rounds            int           // number of rounds actually run
	RoundsRemaining   int           // rounds left unused (cap - Rounds)
	Resolutions       []Resolution  // every resolution across rounds
	AssumedDecisions  []spec.Decision // new Decisions created during pre-flight
	DriftedApproaches []string      // Approaches marked drifted by cascade of new Decisions
}

// proposedAssumption is the LLM's raw suggestion for a new Decision.
type proposedAssumption struct {
	Title      string  `json:"title"`
	Rationale  string  `json:"rationale"`
	Confidence float64 `json:"confidence"`
}

// agentResolution is the LLM's per-question response shape.
type agentResolution struct {
	Question         string               `json:"question"`
	Source           ResolutionSource     `json:"source"`
	SpecNodeID       string               `json:"spec_node_id,omitempty"`
	Answer           string               `json:"answer"`
	AssumedDecision  *proposedAssumption  `json:"assumed_decision,omitempty"`
}

// agentReport is the full JSON shape returned by the pre-flight agent.
type agentReport struct {
	Resolutions []agentResolution `json:"resolutions"`
}

// Preflight runs the DJ-071 clarification protocol over every Approach in
// a workstream. Returns a Report describing what was resolved, what was
// assumed, and which Approaches the cascade marked drifted. Approach
// bodies are mutated in place (new "## Pre-flight Resolutions" section
// appended) and resaved.
//
// graph and store must be the same handles the caller used for
// classification — pre-flight reads from and writes to both.
func Preflight(
	ctx context.Context,
	llm agent.LLM,
	fsys specio.FS,
	graph *spec.SpecGraph,
	store *state.FileStateStore,
	ws spec.Workstream,
	approachesByID map[string]spec.Approach,
	maxRounds int,
) (*Report, error) {
	if maxRounds <= 0 {
		maxRounds = DefaultMaxRounds
	}
	report := &Report{Rounds: 0, RoundsRemaining: maxRounds}
	hist := history.NewHistorian(fsys, ".borg/history")

	approachIDs := approachIDsFromWorkstream(ws)

	for round := 1; round <= maxRounds; round++ {
		report.Rounds = round
		report.RoundsRemaining = maxRounds - round

		progressThisRound := false

		for _, approachID := range approachIDs {
			approach, ok := approachesByID[approachID]
			if !ok {
				continue
			}
			ar, err := invokePreflight(ctx, llm, graph, approach, ws, report.RoundsRemaining)
			if err != nil {
				return report, fmt.Errorf("preflight invoke (approach %s): %w", approachID, err)
			}
			if len(ar.Resolutions) == 0 {
				continue
			}
			progressThisRound = true

			var renderedLines []string
			for _, res := range ar.Resolutions {
				resolution, decision, err := materialise(res, approachID)
				if err != nil {
					return report, err
				}
				report.Resolutions = append(report.Resolutions, resolution)

				if decision != nil {
					if err := persistAssumedDecision(fsys, *decision); err != nil {
						return report, err
					}
					report.AssumedDecisions = append(report.AssumedDecisions, *decision)
					// Add the new Decision to the in-memory graph so the
					// next cascade pass (and any subsequent round) sees it.
					graph = rebuildGraphWithDecision(graph, *decision)

					// Cascade the new Decision so any parent Feature /
					// Strategy that references it is refreshed, and
					// dependent Approaches are marked drifted.
					cascadeResult, err := cascade.Cascade(ctx, llm, fsys, graph, store, decision.ID)
					if err != nil {
						return report, fmt.Errorf("cascade new decision %s: %w", decision.ID, err)
					}
					report.DriftedApproaches = append(report.DriftedApproaches, cascadeResult.DriftedApproaches...)

					// Historian event for the new assumed decision.
					_ = hist.Record(history.Event{
						ID:        fmt.Sprintf("evt-preflight-%s-%d", decision.ID, time.Now().UnixNano()),
						Timestamp: time.Now(),
						Kind:      "decision_assumed",
						TargetID:  decision.ID,
						NewValue:  decision.Rationale,
						Rationale: fmt.Sprintf("Assumed during pre-flight of workstream %s, approach %s", ws.ID, approachID),
					})
				}

				renderedLines = append(renderedLines, renderResolution(resolution))
			}

			if len(renderedLines) > 0 {
				if err := appendResolutionsToApproach(fsys, &approach, renderedLines); err != nil {
					return report, err
				}
				approachesByID[approachID] = approach
			}
		}

		if !progressThisRound {
			// The agent found nothing new to clarify — we're done even if
			// we have rounds left. This is the happy-path exit.
			break
		}
	}

	return report, nil
}

// approachIDsFromWorkstream extracts the unique set of Approach IDs
// referenced by the workstream's PlanSteps. Each Approach is pre-flighted
// once per round even if multiple steps target it.
func approachIDsFromWorkstream(ws spec.Workstream) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, step := range ws.Steps {
		if step.ApproachID == "" {
			continue
		}
		if _, ok := seen[step.ApproachID]; ok {
			continue
		}
		seen[step.ApproachID] = struct{}{}
		out = append(out, step.ApproachID)
	}
	return out
}

// invokePreflight builds the prompt, calls the LLM, and parses the JSON.
func invokePreflight(
	ctx context.Context,
	llm agent.LLM,
	g *spec.SpecGraph,
	approach spec.Approach,
	ws spec.Workstream,
	roundsRemaining int,
) (*agentReport, error) {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "## Approach\n\nID: %s\nTitle: %s\nParent: %s\n\n### Body\n%s\n\n",
		approach.ID, approach.Title, approach.ParentID, approach.Body)

	prompt.WriteString("### Artifact paths\n")
	for _, p := range approach.ArtifactPaths {
		fmt.Fprintf(&prompt, "- %s\n", p)
	}

	prompt.WriteString("\n## PlanSteps\n")
	for _, step := range ws.Steps {
		if step.ApproachID != approach.ID {
			continue
		}
		fmt.Fprintf(&prompt, "- %s (order=%d): %s\n", step.ID, step.Order, step.Description)
	}

	if parent := resolveParentProse(g, approach.ParentID); parent != "" {
		fmt.Fprintf(&prompt, "\n## Parent prose\n%s\n", parent)
	}

	if len(approach.Decisions) > 0 {
		prompt.WriteString("\n## Applicable Decisions\n")
		for _, id := range approach.Decisions {
			if d := g.Decision(id); d != nil {
				fmt.Fprintf(&prompt, "- %s (%s, confidence=%.2f): %s — %s\n", d.ID, d.Status, d.Confidence, d.Title, d.Rationale)
			}
		}
	}

	fmt.Fprintf(&prompt, "\n## Rounds remaining after this one\n%d\n", roundsRemaining)

	req := agent.GenerateRequest{
		Messages: []agent.Message{
			{Role: "system", Content: "You are the pre-flight clarifier. Respond with valid JSON matching the PreflightReport schema."},
			{Role: "user", Content: prompt.String()},
		},
	}
	resp, err := llm.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	var out agentReport
	if err := json.Unmarshal([]byte(resp.Content), &out); err != nil {
		return nil, fmt.Errorf("preflight parse: %w", err)
	}
	return &out, nil
}

// materialise converts the agent's raw resolution into the typed form
// Preflight returns and, for assumed resolutions, constructs the new
// Decision. It enforces the invariants the DJ-071 schema requires
// (spec/assumed exclusivity; confidence bounds; non-empty answer).
func materialise(res agentResolution, approachID string) (Resolution, *spec.Decision, error) {
	if res.Question == "" || res.Answer == "" {
		return Resolution{}, nil, fmt.Errorf("preflight: agent returned resolution with empty question or answer")
	}
	out := Resolution{
		ApproachID: approachID,
		Question:   res.Question,
		Source:     res.Source,
		Answer:     res.Answer,
	}

	switch res.Source {
	case SourceSpec:
		if res.SpecNodeID == "" {
			return Resolution{}, nil, fmt.Errorf("preflight: spec resolution without spec_node_id (q=%q)", res.Question)
		}
		if res.AssumedDecision != nil {
			return Resolution{}, nil, fmt.Errorf("preflight: spec resolution must not carry assumed_decision (q=%q)", res.Question)
		}
		out.SpecNodeID = res.SpecNodeID
		return out, nil, nil

	case SourceAssumed:
		if res.AssumedDecision == nil {
			return Resolution{}, nil, fmt.Errorf("preflight: assumed resolution without assumed_decision (q=%q)", res.Question)
		}
		if res.SpecNodeID != "" {
			return Resolution{}, nil, fmt.Errorf("preflight: assumed resolution must not carry spec_node_id (q=%q)", res.Question)
		}
		dec := res.AssumedDecision
		if dec.Title == "" || dec.Rationale == "" {
			return Resolution{}, nil, fmt.Errorf("preflight: assumed_decision missing title or rationale")
		}
		if dec.Confidence <= 0 || dec.Confidence >= 1 {
			return Resolution{}, nil, fmt.Errorf("preflight: assumed_decision confidence must be in (0,1), got %.2f", dec.Confidence)
		}
		now := time.Now()
		decisionID := spec.UniqueID(dec.Title, now)
		decision := spec.Decision{
			ID:         decisionID,
			Title:      dec.Title,
			Status:     spec.DecisionStatusAssumed,
			Confidence: dec.Confidence,
			Rationale:  dec.Rationale,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		out.DecisionID = decisionID
		return out, &decision, nil

	default:
		return Resolution{}, nil, fmt.Errorf("preflight: unknown resolution source %q", res.Source)
	}
}

// persistAssumedDecision writes a new Decision pair to the spec store.
func persistAssumedDecision(fsys specio.FS, d spec.Decision) error {
	body := fmt.Sprintf("# %s\n\n**Status**: assumed (pre-flight)\n\n%s\n", d.Title, d.Rationale)
	return specio.SavePair(fsys, ".borg/spec/decisions/"+d.ID, d, body)
}

// rebuildGraphWithDecision returns a new SpecGraph that includes the new
// Decision alongside all existing nodes. Cheap in practice because Build is
// O(n) over the current node set; pre-flight runs rarely (per workstream
// dispatch) and assumed Decisions are expected to be few.
func rebuildGraphWithDecision(g *spec.SpecGraph, d spec.Decision) *spec.SpecGraph {
	var features []spec.Feature
	var bugs []spec.Bug
	var decisions []spec.Decision
	var strategies []spec.Strategy
	var approaches []spec.Approach

	for id, node := range g.Nodes() {
		switch node.Kind {
		case spec.KindFeature:
			if f := g.Feature(id); f != nil {
				features = append(features, *f)
			}
		case spec.KindBug:
			if b := g.Bug(id); b != nil {
				bugs = append(bugs, *b)
			}
		case spec.KindDecision:
			if existing := g.Decision(id); existing != nil {
				decisions = append(decisions, *existing)
			}
		case spec.KindStrategy:
			if s := g.Strategy(id); s != nil {
				strategies = append(strategies, *s)
			}
		case spec.KindApproach:
			if a := g.Approach(id); a != nil {
				approaches = append(approaches, *a)
			}
		}
	}

	decisions = append(decisions, d)
	return spec.BuildGraph(features, bugs, decisions, strategies, approaches, spec.TraceabilityIndex{})
}

// resolveParentProse returns the current prose of a parent Feature or
// Strategy by ID. Returns "" if not found.
func resolveParentProse(g *spec.SpecGraph, parentID string) string {
	if f := g.Feature(parentID); f != nil {
		return f.Description
	}
	// Strategies don't carry inline prose; skip.
	return ""
}

// appendResolutionsToApproach adds a "## Pre-flight Resolutions" section
// with the lines rendered this round, bumps UpdatedAt, and re-saves the
// Approach. Per DJ-071 step 4, the coding agent sees the resolutions
// inline in the brief.
func appendResolutionsToApproach(fsys specio.FS, a *spec.Approach, lines []string) error {
	var b strings.Builder
	b.WriteString(a.Body)
	if !strings.HasSuffix(a.Body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\n## Pre-flight Resolutions\n")
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	a.Body = b.String()
	a.UpdatedAt = time.Now()
	return specio.SaveMarkdown(fsys, ".borg/spec/approaches/"+a.ID+".md", *a, a.Body)
}

// renderResolution formats a single resolution for inclusion in the
// Approach body. Terse on purpose — full context lives in the source spec
// node or new assumed Decision the resolution points at.
func renderResolution(r Resolution) string {
	switch r.Source {
	case SourceSpec:
		return fmt.Sprintf("- **Q:** %s\n  **A:** %s (spec: %s)", r.Question, r.Answer, r.SpecNodeID)
	case SourceAssumed:
		return fmt.Sprintf("- **Q:** %s\n  **A:** %s (assumed — Decision %s)", r.Question, r.Answer, r.DecisionID)
	default:
		return fmt.Sprintf("- **Q:** %s\n  **A:** %s", r.Question, r.Answer)
	}
}
