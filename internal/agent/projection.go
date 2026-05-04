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
		return projectRevise(snap)
	case "triage":
		return projectTriage(snap)
	case "revise_features":
		return projectReviseNode(snap, "feature")
	case "revise_strategies":
		return projectReviseNode(snap, "strategy")
	case "revise_feature_additions":
		return projectAdditionElaborate(snap, "feature")
	case "revise_strategy_additions":
		return projectAdditionElaborate(snap, "strategy")
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
	b.WriteString("\n\nProduce the full Raw")
	if kind == "feature" {
		b.WriteString("FeatureProposal")
	} else {
		b.WriteString("StrategyProposal")
	}
	b.WriteString(" for the item above. Preserve its id and title verbatim. Decisions are inline; the reconciler downstream dedupes across siblings.")
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

// projectTriage builds the spec_revision_triager's user message:
// the list of node ids in the current proposal + the critic findings
// (one per concern, grouped by kind for readability). The triager
// emits a RevisionPlan routing each finding to a feature/strategy
// revision, an addition, or discarded.
//
// The triager does NOT see the full proposal content — only the node
// id list. Routing is a pure mapping from finding text → node id, not
// an authoring task.
func projectTriage(snap StateSnapshot) []Message {
	var b strings.Builder
	b.WriteString("## Existing nodes in the proposal\n\n")
	features, strategies := proposalNodeIDs(snap.RawProposal)
	if len(features) == 0 && len(strategies) == 0 {
		b.WriteString("(none — the proposal is empty; route every finding to additions[] or discarded[])\n")
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

	if len(snap.Concerns) > 0 {
		b.WriteString("## Critic findings to route\n\n")
		// Group by kind so the triager sees lens labels next to each
		// finding — useful when judging whether a finding is a node
		// targeting one or a proposed addition.
		byKind := make(map[string][]Concern)
		for _, c := range snap.Concerns {
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

	b.WriteString("Emit a RevisionPlan routing each actionable finding above to feature_revisions, strategy_revisions, or additions. Findings you judge non-actionable (off-topic, already addressed, pure observations) are simply omitted. Use the verbatim finding text in the concerns/additions arrays — do not paraphrase.")
	return []Message{{Role: "user", Content: b.String()}}
}

// projectReviseNode builds the per-node revise elaborator's user
// message. The fanout dispatcher set snap.FanoutItem to the JSON of
// one NodeRevision; we look up the prior RawFeatureProposal /
// RawStrategyProposal from snap.OriginalRawProposal and present both
// the prior content and the targeted concerns to the elaborator.
//
// This routes through the same elaborator agent (spec_feature_elaborator
// / spec_strategy_elaborator) that handled the initial elaborate
// fanout — the agent's job is identical (produce a Raw*Proposal for
// one node) and the projection just supplies different context.
func projectReviseNode(snap StateSnapshot, kind string) []Message {
	var b strings.Builder
	b.WriteString(snap.Prompt)
	if snap.ScoutBrief != "" {
		if formatted := formatScoutBrief(snap.ScoutBrief); formatted != "" {
			b.WriteString("\n\n## Scout brief\n\n")
			b.WriteString(formatted)
		}
	}

	var rev NodeRevision
	if snap.FanoutItem != "" {
		_ = json.Unmarshal([]byte(snap.FanoutItem), &rev)
	}

	fmt.Fprintf(&b, "\n\n## %s to revise\n\n", kind)
	if rev.NodeID == "" {
		b.WriteString("(missing — fanout did not populate a NodeRevision)\n")
		return []Message{{Role: "user", Content: b.String()}}
	}
	fmt.Fprintf(&b, "- **Node ID:** `%s`\n\n", rev.NodeID)

	b.WriteString("## Prior content (rejected — re-emit a corrected version)\n\n")
	switch kind {
	case "feature":
		if prior, ok := findRawFeature(snap.OriginalRawProposal, rev.NodeID); ok {
			data, err := json.MarshalIndent(prior, "", "  ")
			if err == nil {
				b.WriteString("```json\n")
				b.Write(data)
				b.WriteString("\n```\n")
			}
		} else {
			fmt.Fprintf(&b, "(prior feature %q not found in the original proposal)\n", rev.NodeID)
		}
	case "strategy":
		if prior, ok := findRawStrategy(snap.OriginalRawProposal, rev.NodeID); ok {
			data, err := json.MarshalIndent(prior, "", "  ")
			if err == nil {
				b.WriteString("```json\n")
				b.Write(data)
				b.WriteString("\n```\n")
			}
		} else {
			fmt.Fprintf(&b, "(prior strategy %q not found in the original proposal)\n", rev.NodeID)
		}
	}

	b.WriteString("\n## Concerns targeting this node\n\n")
	if len(rev.Concerns) == 0 {
		b.WriteString("(none — fanout was spawned without concerns; treat as a no-op re-emission of the prior content)\n")
	} else {
		for _, c := range rev.Concerns {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}

	fmt.Fprintf(&b, "\nProduce the corrected Raw%sProposal for the node above. Address every concern. Preserve the id verbatim. Decisions are inline; the reconciler downstream dedupes across siblings. Re-emit the FULL node — do not emit a delta.\n", titleize(kind))

	return []Message{{Role: "user", Content: b.String()}}
}

// projectAdditionElaborate builds the per-finding addition
// elaborator's user message. The fanout dispatcher set
// snap.FanoutItem to the JSON of one AddedNode; the elaborator
// invents one new RawFeatureProposal or RawStrategyProposal
// addressing the source_concern verbatim, picking its own id and
// title from the concern's subject. Existing nodes are listed
// explicitly as "do NOT re-emit" to prevent the elaborator from
// re-introducing already-present features/strategies.
//
// Phase 4 promotes additions from a single architect call (DJ-092)
// to per-finding fanout (DJ-095), eliminating the multi-node
// authoring pressure that was the same anti-pattern Phase 1 fixed
// for the main revise step.
func projectAdditionElaborate(snap StateSnapshot, kind string) []Message {
	var b strings.Builder
	b.WriteString(snap.Prompt)
	if snap.ScoutBrief != "" {
		if formatted := formatScoutBrief(snap.ScoutBrief); formatted != "" {
			b.WriteString("\n\n## Scout brief\n\n")
			b.WriteString(formatted)
		}
	}

	var added AddedNode
	if snap.FanoutItem != "" {
		_ = json.Unmarshal([]byte(snap.FanoutItem), &added)
	}

	features, strategies := proposalNodeIDs(snap.OriginalRawProposal)
	b.WriteString("\n\n## Existing nodes (do NOT re-emit)\n\n")
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

	fmt.Fprintf(&b, "## %s to propose (addition)\n\n", titleize(kind))
	if added.SourceConcern == "" {
		b.WriteString("(missing — fanout did not populate an AddedNode source_concern)\n")
		return []Message{{Role: "user", Content: b.String()}}
	}
	b.WriteString("**Critic finding driving this addition (verbatim):**\n\n")
	fmt.Fprintf(&b, "> %s\n\n", added.SourceConcern)

	fmt.Fprintf(&b, "Produce one Raw%sProposal that addresses the finding above. Invent the id (slug-derived from the subject of the finding, prefix `%s-`), the title, the body/description, and the inline decisions that justify the architectural shape. Do NOT re-emit any node from the existing-nodes list — pick a fresh id that doesn't collide.\n",
		titleize(kind), kindIDPrefix(kind))

	return []Message{{Role: "user", Content: b.String()}}
}

// kindIDPrefix returns the id prefix the addition elaborator should
// use when minting a new node id. Defensive default: empty kind →
// strategy prefix, matching the AddedNode contract.
func kindIDPrefix(kind string) string {
	if kind == "feature" {
		return "feat"
	}
	return "strat"
}

func titleize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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
	b.WriteString("\n\nEmit a ReconciliationVerdict naming the clusters that need dedupe / resolve_conflict / reuse_existing. Inline decisions you do not mention are kept as separate canonical decisions.")
	if snap.Existing != nil && !snap.Existing.IsEmpty() {
		b.WriteString("\n\nThe project has an existing persisted spec. Use the `spec_list_manifest` tool to see what's already there, then `spec_get(id)` to fetch a specific node when you suspect an inline decision matches one already on disk (and should be a `reuse_existing` action). Do not call the tools for greenfield runs — they will return empty.")
	}
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
