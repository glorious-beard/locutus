package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ProjectState builds the LLM messages for a specific agent role, drawing only
// the fields from the snapshot that are relevant to that agent's job. This keeps
// each agent's context window focused and avoids leaking irrelevant information.
//
// Projections are data-only. Rules of behavior — what schema to emit,
// how to handle empty buckets, what counts as actionable — live in the
// agent's .md system prompt, NOT in the user message generated here.
// This separation is structural: when system-prompt rules and
// user-message directives drift, the user message wins at inference
// time and silently overrides the system prompt. DJ-095's lossless
// triage broke for one run because the projection still carried a
// "non-actionable findings are simply omitted" tail after the system
// prompt was rewritten to mandate routing-completeness; the projection
// won and 22 of 32 findings got dropped. The fix is to never let
// projections carry directives — see DJ-097.
//
// Fanout steps (Phase 3) tag each call with a per-item suffix in the
// stepID — e.g. "elaborate_features (feat-dashboard)" — so progress
// sinks render one entry per item. We strip the suffix before routing
// to the projection switch so all per-item calls land in the same
// projection function.
func ProjectState(stepID string, snap StateSnapshot) []Message {
	base := stepID
	if i := strings.Index(base, " ("); i > 0 {
		base = base[:i]
	}
	switch base {
	case "propose":
		return projectPropose(snap)
	case "outline":
		return projectOutline(snap)
	case "elaborate_features":
		return projectElaborateFeature(snap)
	case "elaborate_strategies":
		return projectElaborateStrategy(snap)
	case "reconcile", "reconcile_revise":
		return projectReconcile(snap)
	case "challenge", "critique":
		return projectChallenge(snap)
	case "research":
		return projectResearch(snap)
	case "revise":
		// DJ-098: unified per-cluster fanout. Each fanout call hits
		// this branch (with a base step ID of "revise" after the
		// per-item suffix is stripped). The cluster-aware projection
		// dispatches off snap.FanoutItem (a FindingCluster JSON).
		// Legacy non-fanout "revise" calls (older workflows) fall
		// through to projectRevise via the empty-FanoutItem branch.
		if snap.FanoutItem != "" {
			return projectFindingCluster(snap)
		}
		return projectRevise(snap)
	case "cluster_findings":
		return projectClusterFindings(snap)
	case "record":
		return projectRecord(snap)
	default:
		// Fallback: provide the prompt and any existing spec.
		return projectDefault(snap)
	}
}

// projectOutline renders the outliner's user message: GOALS.md +
// scout brief in human-readable form. Same scout-brief formatting
// projectPropose uses, since the outliner has the same orientation
// (read GOALS, react to scout, list features and strategies).
func projectOutline(snap StateSnapshot) []Message {
	prompt := snap.Prompt
	if snap.ScoutBrief != "" {
		if formatted := formatScoutBrief(snap.ScoutBrief); formatted != "" {
			prompt = prompt + "\n\n## Scout brief\n\n" + formatted
		}
	}
	return []Message{{Role: "user", Content: prompt}}
}

// projectElaborateFeature builds the per-feature elaborator's user
// message: GOALS + scout + the full outline (so siblings are in
// situational context) + the specific feature being elaborated. The
// fanout dispatcher set snap.FanoutItem to the JSON of one
// OutlineFeature; we surface it as a labeled section the elaborator
// reads literally.
func projectElaborateFeature(snap StateSnapshot) []Message {
	return projectElaborateOne(snap, "feature")
}

// projectElaborateStrategy is the strategy counterpart.
func projectElaborateStrategy(snap StateSnapshot) []Message {
	return projectElaborateOne(snap, "strategy")
}

func projectElaborateOne(snap StateSnapshot, kind string) []Message {
	var b strings.Builder
	b.WriteString(snap.Prompt)
	if snap.ScoutBrief != "" {
		if formatted := formatScoutBrief(snap.ScoutBrief); formatted != "" {
			b.WriteString("\n\n## Scout brief\n\n")
			b.WriteString(formatted)
		}
	}
	if snap.Outline != "" {
		b.WriteString("\n\n## Outline (sibling features and strategies for situational context)\n\n")
		b.WriteString(formatOutlineForElaborator(snap.Outline))
	}
	b.WriteString(fmt.Sprintf("\n\n## %s to elaborate\n\n", kind))
	if snap.FanoutItem != "" {
		// Plain key-value, NOT raw JSON. The fanout item used to be
		// dumped as `{"id":...,"title":...}` directly above the
		// "Produce the full Raw...Proposal" instruction, which made
		// the input JSON structurally adjacent to the output JSON
		// shape. Models on schema-constrained output (Gemini Pro
		// Preview specifically) blurred the distinction between
		// "context I read" vs "shape I extend" and were observed
		// looping on field values they couldn't decide how to fill.
		// Plain-text presentation removes the ambiguity.
		b.WriteString(formatFanoutItem(snap.FanoutItem, kind))
	} else {
		b.WriteString("(missing — fanout did not populate FanoutItem)")
	}
	// Directive ("Produce the full Raw...Proposal") lives in the
	// elaborator's .md system prompt (Task section). Adding it here
	// would create the same drift surface that broke DJ-095's triage.
	return []Message{{Role: "user", Content: b.String()}}
}

// formatFanoutItem renders a single OutlineFeature or OutlineStrategy
// JSON as plain key-value lines. Falls back to the raw JSON only if
// parsing fails — shouldn't happen in production wiring (the same
// JSON came from the outliner via extractFanoutItems).
func formatFanoutItem(raw, kind string) string {
	if kind == "feature" {
		var f OutlineFeature
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return raw
		}
		return fmt.Sprintf("- **ID:** `%s`\n- **Title:** %s\n- **Summary:** %s", f.ID, f.Title, f.Summary)
	}
	var s OutlineStrategy
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return raw
	}
	return fmt.Sprintf("- **ID:** `%s`\n- **Title:** %s\n- **Kind:** %s\n- **Summary:** %s", s.ID, s.Title, s.Kind, s.Summary)
}

// formatOutlineForElaborator renders the Outline JSON as a compact
// human-readable list — feeding the elaborator the raw JSON would
// inflate prompts unnecessarily.
func formatOutlineForElaborator(raw string) string {
	var outline Outline
	if err := json.Unmarshal([]byte(raw), &outline); err != nil {
		return raw
	}
	var b strings.Builder
	if len(outline.Features) > 0 {
		b.WriteString("**Features:**\n")
		for _, f := range outline.Features {
			fmt.Fprintf(&b, "- %s — %s: %s\n", f.ID, f.Title, f.Summary)
		}
	}
	if len(outline.Strategies) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("**Strategies:**\n")
		for _, s := range outline.Strategies {
			fmt.Fprintf(&b, "- %s (%s) — %s: %s\n", s.ID, s.Kind, s.Title, s.Summary)
		}
	}
	return strings.TrimSpace(b.String())
}

// projectClusterFindings builds the spec_finding_clusterer's user
// message (DJ-098). The clusterer receives the unmatched-findings
// list (findings that didn't name an existing node id) and groups
// them by topic. Decision dimensions are minimal: which cluster a
// finding belongs to, and what kind (feature/strategy) the cluster
// is. No node-id matching, no revise-vs-add intent — the elaborator
// downstream decides those locally per cluster.
func projectClusterFindings(snap StateSnapshot) []Message {
	var b strings.Builder
	b.WriteString("## Existing nodes in the proposal (for kind-classification context)\n\n")
	features, strategies := proposalNodeIDs(snap.RawProposal)
	if len(features) == 0 && len(strategies) == 0 {
		b.WriteString("(none — the proposal is empty)\n\n")
	} else {
		if len(features) > 0 {
			b.WriteString("**Features:**\n")
			for _, f := range features {
				fmt.Fprintf(&b, "- %s\n", f)
			}
			b.WriteString("\n")
		}
		if len(strategies) > 0 {
			b.WriteString("**Strategies:**\n")
			for _, s := range strategies {
				fmt.Fprintf(&b, "- %s\n", s)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Findings to cluster (verbatim — every entry must end up in exactly one cluster)\n\n")
	if len(snap.UnmatchedFindings) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, f := range snap.UnmatchedFindings {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}

	// Lossless-grouping mandate, kind-defaulting rule, and example
	// shape live in spec_finding_clusterer.md. Projection is data-only
	// per DJ-097.
	return []Message{{Role: "user", Content: b.String()}}
}

// projectFindingCluster builds the per-cluster elaborator's user
// message (DJ-098). The fanout dispatcher set snap.FanoutItem to the
// JSON of one FindingCluster; the elaborator emits one
// RawFeatureProposal or RawStrategyProposal that addresses every
// finding in the cluster.
//
// This unifies what used to be projectReviseNode + projectAdditionElaborate.
// Discrimination between revise and add is implicit in cluster.NodeID:
// when set, the elaborator preserves that id and the projection
// includes the prior node content; when empty, the elaborator picks
// a fresh id and the projection includes the existing-nodes list as
// an id-collision-avoidance reference.
func projectFindingCluster(snap StateSnapshot) []Message {
	var b strings.Builder
	b.WriteString(snap.Prompt)
	if snap.ScoutBrief != "" {
		if formatted := formatScoutBrief(snap.ScoutBrief); formatted != "" {
			b.WriteString("\n\n## Scout brief\n\n")
			b.WriteString(formatted)
		}
	}

	var cluster FindingCluster
	if snap.FanoutItem != "" {
		_ = json.Unmarshal([]byte(snap.FanoutItem), &cluster)
	}

	// Always show the existing-nodes list. For revise mode this is
	// situational awareness; for add mode it's the id-collision-
	// avoidance reference.
	features, strategies := proposalNodeIDs(snap.OriginalRawProposal)
	b.WriteString("\n\n## Existing nodes\n\n")
	if len(features) == 0 && len(strategies) == 0 {
		b.WriteString("(none)\n")
	} else {
		if len(features) > 0 {
			b.WriteString("**Features:**\n")
			for _, f := range features {
				fmt.Fprintf(&b, "- %s\n", f)
			}
			b.WriteString("\n")
		}
		if len(strategies) > 0 {
			b.WriteString("**Strategies:**\n")
			for _, s := range strategies {
				fmt.Fprintf(&b, "- %s\n", s)
			}
			b.WriteString("\n")
		}
	}

	fmt.Fprintf(&b, "## Cluster topic\n\n%s\n\n", strings.TrimSpace(cluster.Topic))

	if cluster.NodeID != "" {
		fmt.Fprintf(&b, "## Targeted node\n\n- **Node ID:** `%s`\n\n", cluster.NodeID)
		b.WriteString("## Prior content\n\n")
		// Sniff prefix to decide which Raw*Proposal type to look up.
		switch {
		case strings.HasPrefix(cluster.NodeID, "feat-"):
			if prior, ok := findRawFeature(snap.OriginalRawProposal, cluster.NodeID); ok {
				data, err := json.MarshalIndent(prior, "", "  ")
				if err == nil {
					b.WriteString("```json\n")
					b.Write(data)
					b.WriteString("\n```\n\n")
				}
			} else {
				fmt.Fprintf(&b, "(prior feature %q not found in the original proposal)\n\n", cluster.NodeID)
			}
		case strings.HasPrefix(cluster.NodeID, "strat-"):
			if prior, ok := findRawStrategy(snap.OriginalRawProposal, cluster.NodeID); ok {
				data, err := json.MarshalIndent(prior, "", "  ")
				if err == nil {
					b.WriteString("```json\n")
					b.Write(data)
					b.WriteString("\n```\n\n")
				}
			} else {
				fmt.Fprintf(&b, "(prior strategy %q not found in the original proposal)\n\n", cluster.NodeID)
			}
		}
	}

	b.WriteString("## Findings to address (verbatim)\n\n")
	if len(cluster.Findings) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, f := range cluster.Findings {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}

	// Directive ("If a Targeted node is named, preserve its id and
	// re-emit a corrected version. Otherwise pick a fresh id and
	// invent a new node addressing the findings.") lives in the
	// elaborator's .md system prompt. Projection is data-only per
	// DJ-097.
	return []Message{{Role: "user", Content: b.String()}}
}

// projectReconcile builds the reconciler's user message: the raw
// proposal from the upstream propose/revise step. The existing spec
// is no longer inlined — the reconciler agent navigates it lazily via
// the spec_list_manifest / spec_get tools (registered against the
// Genkit runtime in cmd/llm.go). Inlining the snapshot was an
// O(N)-prompt-size scaling problem that motivated DJ-094.
func projectReconcile(snap StateSnapshot) []Message {
	var b strings.Builder
	b.WriteString("## Raw proposal (inline decisions, no IDs)\n\n")
	if snap.RawProposal != "" {
		b.WriteString(snap.RawProposal)
	} else {
		// Defensive: if the upstream propose merge didn't populate
		// RawProposal, fall back to ProposedSpec so the reconciler
		// gets *something* to work on. This shouldn't happen in
		// production wiring; kept as a soft fallback for tests.
		b.WriteString(snap.ProposedSpec)
	}
	// Data-conditional flag (NOT a directive) — the reconciler's .md
	// system prompt covers tool usage in general; this tells the
	// agent the data state of *this* call ("an existing spec is
	// present"). Greenfield runs omit the flag entirely so the agent
	// doesn't burn turns on tool calls that would return empty.
	if snap.Existing != nil && !snap.Existing.IsEmpty() {
		b.WriteString("\n\n## Existing spec is present\n\nA persisted spec snapshot exists at `.borg/spec/`; the spec_list_manifest and spec_get tools will return non-empty results. (On greenfield runs this section is omitted.)")
	}
	// Directive ("Emit a ReconciliationVerdict... inline decisions
	// you do not mention are kept as separate canonical decisions")
	// lives in spec_reconciler.md. Do not re-state here — see DJ-097.
	return []Message{{Role: "user", Content: b.String()}}
}

func projectPropose(snap StateSnapshot) []Message {
	prompt := snap.Prompt
	// If a scout brief was produced upstream (spec-generation council),
	// fold its formatted form into the proposer's user message so the
	// proposer reads the senior-engineer survey alongside GOALS.md
	// rather than working from the goals body alone.
	if snap.ScoutBrief != "" {
		if formatted := formatScoutBrief(snap.ScoutBrief); formatted != "" {
			prompt = prompt + "\n\n## Scout brief\n\n" + formatted
		}
	}
	msgs := []Message{{Role: "user", Content: prompt}}

	// On revision rounds, include open concerns to address.
	if len(snap.OpenConcerns) > 0 {
		msgs = append(msgs, Message{
			Role:    "user",
			Content: fmt.Sprintf("Address these open concerns:\n%s", strings.Join(snap.OpenConcerns, "\n")),
		})
	}
	return msgs
}

// formatScoutBrief unmarshals the raw ScoutBrief JSON the scout step
// produced and renders it as human-readable markdown for the proposer's
// user message. Falls back to the raw JSON on parse failure so the
// proposer still receives the upstream content.
func formatScoutBrief(raw string) string {
	var brief ScoutBrief
	if err := json.Unmarshal([]byte(raw), &brief); err != nil {
		return raw
	}
	var b strings.Builder
	if brief.DomainRead != "" {
		fmt.Fprintf(&b, "**Domain read:** %s\n\n", brief.DomainRead)
	}
	if len(brief.TechnologyOptions) > 0 {
		b.WriteString("**Technology options to choose among:**\n")
		for _, o := range brief.TechnologyOptions {
			fmt.Fprintf(&b, "- %s\n", o)
		}
		b.WriteString("\n")
	}
	if len(brief.ImplicitAssumptions) > 0 {
		b.WriteString("**Implicit assumptions you MUST commit to (one strategy + one decision each):**\n")
		for _, a := range brief.ImplicitAssumptions {
			fmt.Fprintf(&b, "- %s\n", a)
		}
		b.WriteString("\n")
	}
	if len(brief.WatchOuts) > 0 {
		b.WriteString("**Watch-outs:**\n")
		for _, w := range brief.WatchOuts {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return strings.TrimSpace(b.String())
}

func projectChallenge(snap StateSnapshot) []Message {
	msgs := []Message{
		{Role: "user", Content: snap.Prompt},
	}
	if snap.ProposedSpec != "" {
		msgs = append(msgs, Message{
			Role:    "assistant",
			Content: compactContext(snap.ProposedSpec, defaultMaxChars),
		})
		msgs = append(msgs, Message{
			Role:    "user",
			Content: "Review the proposal above.",
		})
	}
	return msgs
}

func projectResearch(snap StateSnapshot) []Message {
	msgs := []Message{
		{Role: "user", Content: snap.Prompt},
	}

	// Researcher sees the concerns that need investigation.
	if len(snap.Concerns) > 0 {
		var lines []string
		for _, c := range snap.Concerns {
			lines = append(lines, fmt.Sprintf("- [%s] %s", c.Severity, c.Text))
		}
		msgs = append(msgs, Message{
			Role:    "user",
			Content: fmt.Sprintf("Investigate these concerns:\n%s", strings.Join(lines, "\n")),
		})
	}
	return msgs
}

func projectRevise(snap StateSnapshot) []Message {
	msgs := []Message{
		{Role: "user", Content: snap.Prompt},
	}

	// Show the architect its prior raw proposal (what it actually
	// produced) as the assistant message, so the rejection language
	// is unambiguous: "your prior proposal is rejected; here are the
	// findings; emit a corrected one." Falls back to the canonical
	// ProposedSpec for legacy paths where RawProposal isn't populated.
	prior := snap.RawProposal
	if prior == "" {
		prior = snap.ProposedSpec
	}
	if prior != "" {
		msgs = append(msgs, Message{
			Role:    "assistant",
			Content: prior,
		})
	}

	if len(snap.Concerns) > 0 || len(snap.ResearchResults) > 0 {
		msgs = append(msgs, Message{
			Role:    "user",
			Content: buildRevisePrompt(snap.Concerns, snap.ResearchResults),
		})
	}
	return msgs
}

// buildRevisePrompt assembles the directive-shape rejection message used
// in revise rounds. Matches the reviseForIntegrity prompt's structure
// (`78da6b5`): explicit rejection, enumerated findings grouped by kind,
// prescriptive actions, explicit don'ts. Critic findings emitted as
// `- {text}` under per-kind headings — no agent_id/severity noise that
// the architect has to filter out.
func buildRevisePrompt(concerns []Concern, research []Finding) string {
	var b strings.Builder
	b.WriteString("STOP. Your previous RawSpecProposal is rejected. The council critics flagged issues that must be addressed before this proposal can be accepted.\n\n")
	b.WriteString("This is not a stylistic note. Every finding below describes a specific defect. Address every one in your revised RawSpecProposal.\n\n")

	if len(concerns) > 0 {
		b.WriteString("## Specific findings\n\n")
		// Group concerns by Kind so the architect addresses each lens
		// (integrity / architecture / devops / sre / cost) explicitly.
		byKind := make(map[string][]Concern)
		for _, c := range concerns {
			k := c.Kind
			if k == "" {
				k = "review"
			}
			byKind[k] = append(byKind[k], c)
		}
		kinds := make([]string, 0, len(byKind))
		for k := range byKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			fmt.Fprintf(&b, "### %s (%d)\n", k, len(byKind[k]))
			for _, c := range byKind[k] {
				fmt.Fprintf(&b, "- %s\n", c.Text)
			}
			b.WriteString("\n")
		}
	}

	if len(research) > 0 {
		b.WriteString("## Research findings\n\n")
		for _, f := range research {
			fmt.Fprintf(&b, "- Q: %s\n  A: %s\n", f.Query, f.Result)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Required actions\n\n")
	b.WriteString("For each finding above, take one of these actions in your revised RawSpecProposal:\n\n")
	b.WriteString("1. **integrity** findings: add the missing inline decision under the relevant feature/strategy, OR remove the offending reference. The reconciler rebuilds canonical IDs from your output, so you do not need to invent IDs.\n")
	b.WriteString("2. **architecture / devops / sre / cost** findings: revise the affected feature, strategy, or inline decision content to address the critic's specific concern. If a decision in your prior proposal contradicted GOALS.md or a scout-brief mandate, emit a corrected inline decision (with rationale, alternatives, and citations) under the same parent.\n\n")
	b.WriteString("Do not paraphrase the findings. Do not acknowledge them in prose. Do not re-emit the same broken structure with cosmetic edits.\n\n")
	b.WriteString("Re-emit the COMPLETE corrected RawSpecProposal as a single JSON object. No diff. No partial object. No prose.")
	return b.String()
}

func projectRecord(snap StateSnapshot) []Message {
	msgs := []Message{
		{Role: "user", Content: snap.Prompt},
	}
	// Historian sees everything — the full journey.
	if snap.ProposedSpec != "" {
		msgs = append(msgs, Message{
			Role: "user",
			Content: fmt.Sprintf("Original proposal:\n%s", snap.ProposedSpec),
		})
	}
	if len(snap.Concerns) > 0 {
		var lines []string
		for _, c := range snap.Concerns {
			lines = append(lines, fmt.Sprintf("- [%s/%s] %s", c.AgentID, c.Severity, c.Text))
		}
		msgs = append(msgs, Message{
			Role: "user",
			Content: fmt.Sprintf("Concerns:\n%s", strings.Join(lines, "\n")),
		})
	}
	if snap.Revisions != "" {
		msgs = append(msgs, Message{
			Role: "user",
			Content: fmt.Sprintf("Revised proposal:\n%s", snap.Revisions),
		})
	}
	msgs = append(msgs, Message{
		Role:    "user",
		Content: "Record the council session above.",
	})
	return msgs
}

func projectDefault(snap StateSnapshot) []Message {
	msgs := []Message{{Role: "user", Content: snap.Prompt}}
	if snap.ProposedSpec != "" {
		msgs = append(msgs, Message{Role: "assistant", Content: snap.ProposedSpec})
	}
	return msgs
}
