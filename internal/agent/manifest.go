// Package agent — DJ-125 in-flight manifest.
//
// The InFlightManifest is the council's working view of the spec graph.
// It promotes projection-by-blob (dumping state.RawProposal or
// state.ProposedSpec verbatim into every agent prompt) to projection-by-
// manifest: a compact structural overview of axes / decisions /
// features / strategies / concerns with per-item state markers. The
// specific working item each agent is acting on (the axis being
// decided, the feature being elaborated) still travels in the
// projection in full; only the surrounding context shrinks from blob
// to manifest.
//
// Builder: BuildManifest walks state.RawProposal (current iteration's
// in-flight proposal), state.Existing (persisted graph), state.AxesOpen
// (axes the scout has not yet seen settle), state.NewNodesFromScout
// (new feature/strategy nodes the current iteration's scout
// introduced), state.DecidedAxesByIter (axis → iter map for axis-state
// resolution), and state.Concerns. Returns a fully-populated manifest
// with per-item state markers computed.
//
// Renderer: RenderManifest produces a compact text rendering suitable
// for agent prompts. Sectioned by kind; one-line per item with state
// marker. Order is stable across calls for deterministic prompt bytes.

package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// ManifestAxisState marks where an axis stands in the council's
// working view of the spec graph.
//
//   - settled: a decision in the in-flight proposal or persisted graph
//     covers this axis. SettledByDecisionID names the decision.
//   - open: the scout's most recent brief left this axis in axes_open;
//     no decision answers it yet.
//   - under_evaluation: reserved for future per-iteration scout fanout
//     where an axis is being researched concurrently. Unused today.
type ManifestAxisState string

const (
	ManifestAxisStateSettled         ManifestAxisState = "settled"
	ManifestAxisStateOpen            ManifestAxisState = "open"
	ManifestAxisStateUnderEvaluation ManifestAxisState = "under_evaluation"
)

// ManifestDecisionState marks whether a decision is fresh this
// iteration or carried in from earlier work, and whether any open
// concern flags it.
//
//   - settled_prior: existed before this council run (in state.Existing
//     or in a prior iteration of the in-flight proposal).
//   - settled_this_iter: introduced by this iteration's decision-
//     elaborator. Visible to the scout as "what just landed".
//   - flagged: at least one open concern references this decision via
//     RelatedDecisionIDs. FlaggedConcernIDs surfaces the concerns'
//     positions in state.Concerns so the scout (and DJ-126) can
//     dispatch revision.
type ManifestDecisionState string

const (
	ManifestDecisionStateSettledPrior    ManifestDecisionState = "settled_prior"
	ManifestDecisionStateSettledThisIter ManifestDecisionState = "settled_this_iter"
	ManifestDecisionStateFlagged         ManifestDecisionState = "flagged"
)

// ManifestNodeState marks whether a feature/strategy has full content
// in the in-flight proposal or is awaiting elaboration.
//
//   - authored: a RawFeatureProposal / RawStrategyProposal exists in
//     the in-flight proposal with body content.
//   - pending_narrative: the scout introduced this node this iteration
//     but the narrative-elaborator hasn't fired yet.
//   - pending_decision_ref: the node exists but its Decisions[] slice
//     references an axis still in axes_open. Reserved; emitted when
//     the scout pre-populates a NewSpecNode whose covering decision
//     hasn't been minted yet.
type ManifestNodeState string

const (
	ManifestNodeStateAuthored           ManifestNodeState = "authored"
	ManifestNodeStatePendingNarrative   ManifestNodeState = "pending_narrative"
	ManifestNodeStatePendingDecisionRef ManifestNodeState = "pending_decision_ref"
)

// InFlightManifest is the council's structured working view of the
// spec graph for one iteration. Built by BuildManifest from
// PlanningState; rendered to text by RenderManifest for inclusion in
// agent prompts.
type InFlightManifest struct {
	Axes       []ManifestAxis     `json:"axes,omitempty" jsonschema:"description=Foundational axes the spec must commit to. State marks each as settled / open / under_evaluation."`
	Decisions  []ManifestDecision `json:"decisions,omitempty" jsonschema:"description=Decisions in the working view; sourced from the in-flight proposal and the persisted graph. State marks each as settled_prior / settled_this_iter / flagged."`
	Features   []ManifestNode     `json:"features,omitempty" jsonschema:"description=Features in the working view; sourced from the in-flight proposal and scout-introduced new nodes. State marks each as authored / pending_narrative / pending_decision_ref."`
	Strategies []ManifestNode     `json:"strategies,omitempty" jsonschema:"description=Strategies in the working view; same state taxonomy as features."`
	Concerns   []ManifestConcern  `json:"concerns,omitempty" jsonschema:"description=Concerns the council has raised; status communicates whether the concern currently blocks convergence."`
}

// ManifestAxis is one axis entry in the manifest. ID matches
// scout-emitted axis slugs.
type ManifestAxis struct {
	ID                  string            `json:"id" jsonschema:"description=Stable axis slug (lowercase / hyphen-separated). Matches the ID the scout emits in axes_open and the per-axis decision-elaborator answers."`
	State               ManifestAxisState `json:"state" jsonschema:"enum=settled,enum=open,enum=under_evaluation,description=Where this axis stands. settled: a decision covers it; open: the scout's most recent brief listed it in axes_open; under_evaluation: reserved."`
	Summary             string            `json:"summary,omitempty" jsonschema:"description=One-line description; sourced from OpenAxis.Description when available."`
	SettledByDecisionID string            `json:"settled_by_decision_id,omitempty" jsonschema:"description=Decision ID that covers this axis when State is settled; empty otherwise."`
}

// ManifestDecision is one decision entry in the manifest.
type ManifestDecision struct {
	ID                string                `json:"id" jsonschema:"description=Decision ID (starts with dec-)."`
	State             ManifestDecisionState `json:"state" jsonschema:"enum=settled_prior,enum=settled_this_iter,enum=flagged,description=When the decision landed and whether any open concern references it."`
	Title             string                `json:"title,omitempty" jsonschema:"description=Decision title. One-line; rendered into the manifest."`
	Summary           string                `json:"summary,omitempty" jsonschema:"description=One-line summary. Pulled from the decision's Summary field; falls back to the first line of Rationale."`
	Axes              []string              `json:"axes,omitempty" jsonschema:"description=Axis IDs this decision answers. Mirrors RawDecisionProposal.Axes."`
	FlaggedConcernIDs []string              `json:"flagged_concern_ids,omitempty" jsonschema:"description=Indices into the manifest's Concerns slice that reference this decision. Empty when State is not flagged."`
}

// ManifestNode is one feature or strategy entry in the manifest.
type ManifestNode struct {
	ID        string            `json:"id" jsonschema:"description=Node ID (feat- or strat- prefix)."`
	Kind      string            `json:"kind" jsonschema:"enum=feature,enum=strategy,description=Whether this node is a feature or a strategy."`
	State     ManifestNodeState `json:"state" jsonschema:"enum=authored,enum=pending_narrative,enum=pending_decision_ref,description=Whether the node has authored body content; is awaiting narrative elaboration; or is blocked on an axis decision."`
	Title     string            `json:"title,omitempty" jsonschema:"description=Node title. One-line."`
	Summary   string            `json:"summary,omitempty" jsonschema:"description=One-line summary. Authored Summary when present; truncated lead-in of Description / Body otherwise."`
	Decisions []string          `json:"decisions,omitempty" jsonschema:"description=Decision IDs this node depends on. Mirrors Raw{Feature,Strategy}Proposal.Decisions."`
}

// ManifestConcern is the manifest's rendering of one Concern. ID is
// the concern's index in state.Concerns (kept as a string so JSON
// rendering stays uniform across the manifest); Status mirrors the
// underlying Concern's Status; RelatedDecisionIDs and RelatedAxisIDs
// mirror the related-id fields.
type ManifestConcern struct {
	ID                 string        `json:"id" jsonschema:"description=Concern position in state.Concerns (zero-padded index). Stable within one iteration."`
	IterationRaised    int           `json:"iteration_raised,omitempty" jsonschema:"description=Iteration when this concern was first raised."`
	Status             ConcernStatus `json:"status" jsonschema:"enum=open,enum=addressed,enum=stale,enum=wontfix,description=Disposition. open blocks convergence; addressed/stale/wontfix do not."`
	Summary            string        `json:"summary,omitempty" jsonschema:"description=Truncated one-line rendering of the concern text for compact display."`
	AgentID            string        `json:"agent_id,omitempty" jsonschema:"description=Critic that raised the concern."`
	Kind               string        `json:"kind,omitempty" jsonschema:"description=Lens that produced the concern (architecture; devops; sre; cost; integrity)."`
	RelatedDecisionIDs []string      `json:"related_decision_ids,omitempty" jsonschema:"description=Decision IDs the concern references."`
	RelatedAxisIDs     []string      `json:"related_axis_ids,omitempty" jsonschema:"description=Axis IDs the concern references."`
	Justification      string        `json:"justification,omitempty" jsonschema:"description=Scout grading justification when Status is addressed or wontfix; empty otherwise."`
}

// manifestSummaryCap is the per-entry summary length cap. Matches
// spec_tools.summaryMaxRunes so manifest rendering stays uniform with
// the on-disk SpecManifest's summary truncation.
const manifestSummaryCap = 200

// manifestConcernSummaryCap is a tighter cap for concern summaries in
// the manifest — concerns can be longer paragraphs in the raw text and
// the manifest needs to stay scannable. The full text remains
// available on state.Concerns[i].Text.
const manifestConcernSummaryCap = 200

// BuildManifest assembles an InFlightManifest from PlanningState. Pure
// computation; no I/O. Safe to call from any code path that has a
// PlanningState reference.
//
// Axis-state resolution:
//   - settled when DecidedAxesByIter contains the axis ID OR a decision
//     in the in-flight proposal / state.Existing carries it in its
//     Axes[] slice. SettledByDecisionID picks the decision that names
//     the axis (in-flight decisions win over existing ones when both
//     match).
//   - open when the axis appears in state.AxesOpen and is not settled.
//
// Decision-state resolution:
//   - settled_this_iter when DecidedAxesByIter records any of the
//     decision's axes at the most-recent iteration the map contains.
//   - settled_prior otherwise (including persisted decisions from
//     state.Existing).
//   - flagged overrides the prior two when at least one open concern
//     references the decision via RelatedDecisionIDs.
//
// Node-state resolution:
//   - authored when the in-flight proposal has a full
//     RawFeatureProposal / RawStrategyProposal entry.
//   - pending_narrative when only state.NewNodesFromScout carries the
//     node (the scout introduced it this iteration; narrative elab not
//     yet fired).
//   - pending_decision_ref reserved for future use; not emitted today
//     since the workflow doesn't surface that case yet.
func BuildManifest(state *PlanningState) (*InFlightManifest, error) {
	if state == nil {
		return &InFlightManifest{}, nil
	}

	manifest := &InFlightManifest{}

	// Parse the in-flight RawProposal once for downstream lookups.
	var inflight RawSpecProposal
	if strings.TrimSpace(state.RawProposal) != "" {
		if err := json.Unmarshal([]byte(state.RawProposal), &inflight); err != nil {
			return nil, fmt.Errorf("BuildManifest: parse in-flight proposal: %w", err)
		}
	}

	// --- Concerns -------------------------------------------------
	//
	// Concerns are emitted first so the decision-flag pass can index
	// into manifest.Concerns by position. Status defaults to
	// ConcernStatusOpen when the underlying Concern carries the zero
	// value (legacy pre-DJ-125 entries).
	manifest.Concerns = make([]ManifestConcern, 0, len(state.Concerns))
	for i, c := range state.Concerns {
		status := c.Status
		if status == "" {
			status = ConcernStatusOpen
		}
		manifest.Concerns = append(manifest.Concerns, ManifestConcern{
			ID:                 fmt.Sprintf("c-%d", i),
			IterationRaised:    c.IterationRaised,
			Status:             status,
			Summary:            truncate(c.Text, manifestConcernSummaryCap),
			AgentID:            c.AgentID,
			Kind:               c.Kind,
			RelatedDecisionIDs: append([]string(nil), c.RelatedDecisionIDs...),
			RelatedAxisIDs:     append([]string(nil), c.RelatedAxisIDs...),
			Justification:      c.Justification,
		})
	}

	// --- Axis state map -------------------------------------------
	//
	// settledAxisToDecision maps axis ID → decision ID that covers it.
	// In-flight decisions win over existing graph entries when both
	// match (in-flight is "what the loop just committed"); within a
	// source the first decision wins on ties.
	settledAxisToDecision := make(map[string]string)
	for _, d := range inflight.Decisions {
		for _, axisID := range d.Axes {
			axisID = strings.TrimSpace(axisID)
			if axisID == "" {
				continue
			}
			if _, claimed := settledAxisToDecision[axisID]; !claimed {
				settledAxisToDecision[axisID] = d.ID
			}
		}
	}
	if state.Existing != nil {
		for _, d := range state.Existing.Decisions {
			for _, axisID := range axesFromExistingDecision(d) {
				axisID = strings.TrimSpace(axisID)
				if axisID == "" {
					continue
				}
				if _, claimed := settledAxisToDecision[axisID]; !claimed {
					settledAxisToDecision[axisID] = d.ID
				}
			}
		}
	}

	// --- Open-concern indexing per decision -----------------------
	//
	// Walk concerns once and bucket their manifest positions under each
	// referenced decision ID. The decision pass uses these to mark
	// "flagged" state without re-scanning concerns per decision.
	openConcernsByDecisionID := make(map[string][]string)
	for i, mc := range manifest.Concerns {
		if mc.Status != ConcernStatusOpen {
			continue
		}
		for _, decID := range mc.RelatedDecisionIDs {
			decID = strings.TrimSpace(decID)
			if decID == "" {
				continue
			}
			openConcernsByDecisionID[decID] = append(
				openConcernsByDecisionID[decID],
				fmt.Sprintf("c-%d", i),
			)
		}
	}

	// --- Decisions ------------------------------------------------
	//
	// Walk persisted decisions first so their "settled_prior" state
	// stays stable when an in-flight decision happens to share an ID
	// (defensive — should not occur under DJ-124's id minting).
	seenDecisions := make(map[string]struct{})
	if state.Existing != nil {
		for _, d := range state.Existing.Decisions {
			if _, dup := seenDecisions[d.ID]; dup {
				continue
			}
			seenDecisions[d.ID] = struct{}{}
			state := ManifestDecisionStateSettledPrior
			flagged := openConcernsByDecisionID[d.ID]
			if len(flagged) > 0 {
				state = ManifestDecisionStateFlagged
			}
			manifest.Decisions = append(manifest.Decisions, ManifestDecision{
				ID:                d.ID,
				State:             state,
				Title:             d.Title,
				Summary:           decisionSummaryFromExisting(d),
				Axes:              axesFromExistingDecision(d),
				FlaggedConcernIDs: flagged,
			})
		}
	}
	// Determine which iter is "this iteration" for settled_this_iter
	// tagging: the highest iteration index recorded in DecidedAxesByIter.
	maxIter := -1
	for _, iter := range state.DecidedAxesByIter {
		if iter > maxIter {
			maxIter = iter
		}
	}
	for _, d := range inflight.Decisions {
		if _, dup := seenDecisions[d.ID]; dup {
			continue
		}
		seenDecisions[d.ID] = struct{}{}
		decisionState := ManifestDecisionStateSettledPrior
		// Decision is this iter's if any of its axes was recorded at
		// the max iter we've seen. If DecidedAxesByIter is empty (e.g.
		// pre-existing in-flight content with no iter mapping yet),
		// fall back to settled_this_iter — the in-flight proposal
		// being non-empty without iter tracking is itself a "fresh
		// from the current run" signal.
		if maxIter >= 0 {
			for _, axisID := range d.Axes {
				if iter, ok := state.DecidedAxesByIter[strings.TrimSpace(axisID)]; ok && iter == maxIter {
					decisionState = ManifestDecisionStateSettledThisIter
					break
				}
			}
		} else {
			decisionState = ManifestDecisionStateSettledThisIter
		}
		flagged := openConcernsByDecisionID[d.ID]
		if len(flagged) > 0 {
			decisionState = ManifestDecisionStateFlagged
		}
		manifest.Decisions = append(manifest.Decisions, ManifestDecision{
			ID:                d.ID,
			State:             decisionState,
			Title:             d.Title,
			Summary:           decisionSummaryFromRaw(d),
			Axes:              append([]string(nil), d.Axes...),
			FlaggedConcernIDs: flagged,
		})
	}

	// --- Axes -----------------------------------------------------
	//
	// Walk the union of (settled axes, open axes). Stable iteration
	// order: settled IDs sorted alphabetically; open axes in the order
	// state.AxesOpen presents them (the scout's emit order).
	seenAxes := make(map[string]struct{})
	settledIDs := make([]string, 0, len(settledAxisToDecision))
	for id := range settledAxisToDecision {
		settledIDs = append(settledIDs, id)
	}
	sort.Strings(settledIDs)
	for _, id := range settledIDs {
		seenAxes[id] = struct{}{}
		manifest.Axes = append(manifest.Axes, ManifestAxis{
			ID:                  id,
			State:               ManifestAxisStateSettled,
			SettledByDecisionID: settledAxisToDecision[id],
		})
	}
	for _, axis := range state.AxesOpen {
		id := strings.TrimSpace(axis.ID)
		if id == "" {
			continue
		}
		if _, dup := seenAxes[id]; dup {
			continue
		}
		seenAxes[id] = struct{}{}
		manifest.Axes = append(manifest.Axes, ManifestAxis{
			ID:      id,
			State:   ManifestAxisStateOpen,
			Summary: axis.Description,
		})
	}

	// --- Features and strategies ----------------------------------
	//
	// Existing graph entries are emitted in the order WalkPairs returns
	// them upstream; in-flight nodes follow. NewNodesFromScout entries
	// without a matching in-flight body get "pending_narrative". The
	// scout pre-populates Decisions[] for new nodes, so the slice is
	// surfaced verbatim onto ManifestNode.
	seenFeatureIDs := make(map[string]struct{})
	seenStrategyIDs := make(map[string]struct{})
	if state.Existing != nil {
		for _, f := range state.Existing.Features {
			if _, dup := seenFeatureIDs[f.ID]; dup {
				continue
			}
			seenFeatureIDs[f.ID] = struct{}{}
			manifest.Features = append(manifest.Features, ManifestNode{
				ID:        f.ID,
				Kind:      "feature",
				State:     ManifestNodeStateAuthored,
				Title:     f.Title,
				Summary:   featureSummaryFromExisting(f),
				Decisions: append([]string(nil), f.Decisions...),
			})
		}
		for _, s := range state.Existing.Strategies {
			if _, dup := seenStrategyIDs[s.ID]; dup {
				continue
			}
			seenStrategyIDs[s.ID] = struct{}{}
			manifest.Strategies = append(manifest.Strategies, ManifestNode{
				ID:        s.ID,
				Kind:      "strategy",
				State:     ManifestNodeStateAuthored,
				Title:     s.Title,
				Summary:   strategySummaryFromExisting(s),
				Decisions: append([]string(nil), s.Decisions...),
			})
		}
	}
	for _, f := range inflight.Features {
		if _, dup := seenFeatureIDs[f.ID]; dup {
			continue
		}
		seenFeatureIDs[f.ID] = struct{}{}
		manifest.Features = append(manifest.Features, ManifestNode{
			ID:        f.ID,
			Kind:      "feature",
			State:     ManifestNodeStateAuthored,
			Title:     f.Title,
			Summary:   featureSummaryFromRaw(f),
			Decisions: append([]string(nil), f.Decisions...),
		})
	}
	for _, s := range inflight.Strategies {
		if _, dup := seenStrategyIDs[s.ID]; dup {
			continue
		}
		seenStrategyIDs[s.ID] = struct{}{}
		manifest.Strategies = append(manifest.Strategies, ManifestNode{
			ID:        s.ID,
			Kind:      "strategy",
			State:     ManifestNodeStateAuthored,
			Title:     s.Title,
			Summary:   strategySummaryFromRaw(s),
			Decisions: append([]string(nil), s.Decisions...),
		})
	}
	for _, n := range state.NewNodesFromScout {
		kind := strings.ToLower(strings.TrimSpace(n.Kind))
		if kind == "" {
			kind = inferKindFromID(n.ID)
		}
		switch kind {
		case "feature":
			if _, dup := seenFeatureIDs[n.ID]; dup {
				continue
			}
			seenFeatureIDs[n.ID] = struct{}{}
			manifest.Features = append(manifest.Features, ManifestNode{
				ID:        n.ID,
				Kind:      "feature",
				State:     ManifestNodeStatePendingNarrative,
				Title:     n.Title,
				Summary:   truncate(n.Summary, manifestSummaryCap),
				Decisions: append([]string(nil), n.Decisions...),
			})
		case "strategy":
			if _, dup := seenStrategyIDs[n.ID]; dup {
				continue
			}
			seenStrategyIDs[n.ID] = struct{}{}
			manifest.Strategies = append(manifest.Strategies, ManifestNode{
				ID:        n.ID,
				Kind:      "strategy",
				State:     ManifestNodeStatePendingNarrative,
				Title:     n.Title,
				Summary:   truncate(n.Summary, manifestSummaryCap),
				Decisions: append([]string(nil), n.Decisions...),
			})
		}
	}

	return manifest, nil
}

// axesFromExistingDecision pulls the Axes slice off a persisted
// spec.Decision when the field exists. Defensive against schema drift:
// persisted decisions on disk may predate the axes back-reference
// field; returning nil for those keeps axis-state resolution from
// claiming false-settled.
func axesFromExistingDecision(d spec.Decision) []string {
	// spec.Decision carries Axes on the typed shape; mirrored from the
	// elaborator-authored RawDecisionProposal.Axes. Returning a copy
	// keeps the manifest immune to upstream mutation.
	return append([]string(nil), d.Axes...)
}

func decisionSummaryFromExisting(d spec.Decision) string {
	if s := strings.TrimSpace(d.Summary); s != "" {
		return truncate(s, manifestSummaryCap)
	}
	return truncate(d.Rationale, manifestSummaryCap)
}

func decisionSummaryFromRaw(d RawDecisionProposal) string {
	if s := strings.TrimSpace(d.Summary); s != "" {
		return truncate(s, manifestSummaryCap)
	}
	return truncate(d.Rationale, manifestSummaryCap)
}

func featureSummaryFromExisting(f spec.Feature) string {
	if s := strings.TrimSpace(f.Summary); s != "" {
		return truncate(s, manifestSummaryCap)
	}
	return truncate(f.Description, manifestSummaryCap)
}

func featureSummaryFromRaw(f RawFeatureProposal) string {
	if s := strings.TrimSpace(f.Summary); s != "" {
		return truncate(s, manifestSummaryCap)
	}
	return truncate(f.Description, manifestSummaryCap)
}

func strategySummaryFromExisting(s spec.Strategy) string {
	if v := strings.TrimSpace(s.Summary); v != "" {
		return truncate(v, manifestSummaryCap)
	}
	return ""
}

func strategySummaryFromRaw(s RawStrategyProposal) string {
	if v := strings.TrimSpace(s.Summary); v != "" {
		return truncate(v, manifestSummaryCap)
	}
	return truncate(s.Body, manifestSummaryCap)
}

// RenderManifest produces a compact text rendering of the manifest
// suitable for agent prompts. Order matches the manifest's slice order
// (which BuildManifest fixes deterministically), so the same input
// produces identical bytes — important for prompt-cache hits on
// repeated calls within a fanout.
//
// Layout: sectioned by kind, one line per item, state marker rendered
// after the ID. Empty sections are omitted to keep small manifests
// from carrying empty-section noise.
func RenderManifest(m *InFlightManifest) string {
	if m == nil {
		return ""
	}
	var b strings.Builder

	if len(m.Axes) > 0 {
		b.WriteString("### Axes\n")
		for _, a := range m.Axes {
			switch a.State {
			case ManifestAxisStateSettled:
				fmt.Fprintf(&b, "- `%s` [settled by %s]", a.ID, a.SettledByDecisionID)
			case ManifestAxisStateOpen:
				fmt.Fprintf(&b, "- `%s` [open]", a.ID)
			default:
				fmt.Fprintf(&b, "- `%s` [%s]", a.ID, string(a.State))
			}
			if s := strings.TrimSpace(a.Summary); s != "" {
				b.WriteString(" — ")
				b.WriteString(s)
			}
			b.WriteString("\n")
		}
	}

	if len(m.Decisions) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("### Decisions\n")
		for _, d := range m.Decisions {
			fmt.Fprintf(&b, "- `%s` [%s]", d.ID, string(d.State))
			if t := strings.TrimSpace(d.Title); t != "" {
				b.WriteString(" ")
				b.WriteString(t)
			}
			if s := strings.TrimSpace(d.Summary); s != "" {
				b.WriteString(" — ")
				b.WriteString(s)
			}
			if len(d.FlaggedConcernIDs) > 0 {
				fmt.Fprintf(&b, " (flagged by %s)", strings.Join(d.FlaggedConcernIDs, ", "))
			}
			b.WriteString("\n")
		}
	}

	if len(m.Features) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("### Features\n")
		for _, n := range m.Features {
			renderManifestNode(&b, n)
		}
	}

	if len(m.Strategies) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("### Strategies\n")
		for _, n := range m.Strategies {
			renderManifestNode(&b, n)
		}
	}

	if len(m.Concerns) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("### Concerns\n")
		for _, c := range m.Concerns {
			fmt.Fprintf(&b, "- `%s` [%s]", c.ID, string(c.Status))
			if c.AgentID != "" {
				fmt.Fprintf(&b, " %s", c.AgentID)
			}
			if c.IterationRaised > 0 {
				fmt.Fprintf(&b, " (iter %d)", c.IterationRaised)
			}
			if s := strings.TrimSpace(c.Summary); s != "" {
				b.WriteString(" — ")
				b.WriteString(s)
			}
			if c.Justification != "" {
				fmt.Fprintf(&b, " (%s)", c.Justification)
			}
			b.WriteString("\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// renderManifestForProjection builds the manifest for a PlanningState
// and renders it as text. Returns "" when state has no content worth
// rendering (greenfield zero-iter calls). Swallows build errors with a
// truncated diagnostic — projections are best-effort context; an
// internal build failure shouldn't fail the agent call. The fallback
// keeps the agent moving (it can still read its specific working
// item) instead of erroring the whole step.
func renderManifestForProjection(state *PlanningState) string {
	m, err := BuildManifest(state)
	if err != nil {
		return fmt.Sprintf("(manifest unavailable: %s)", truncate(err.Error(), 200))
	}
	return RenderManifest(m)
}

func renderManifestNode(b *strings.Builder, n ManifestNode) {
	fmt.Fprintf(b, "- `%s` [%s]", n.ID, string(n.State))
	if t := strings.TrimSpace(n.Title); t != "" {
		b.WriteString(" ")
		b.WriteString(t)
	}
	if s := strings.TrimSpace(n.Summary); s != "" {
		b.WriteString(" — ")
		b.WriteString(s)
	}
	if len(n.Decisions) > 0 {
		fmt.Fprintf(b, " (decisions: %s)", strings.Join(n.Decisions, ", "))
	}
	b.WriteString("\n")
}
