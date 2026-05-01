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
func ProjectState(stepID string, snap StateSnapshot) []Message {
	switch stepID {
	case "propose":
		return projectPropose(snap)
	case "reconcile", "reconcile_revise":
		return projectReconcile(snap)
	case "challenge", "critique":
		return projectChallenge(snap)
	case "research":
		return projectResearch(snap)
	case "revise":
		return projectRevise(snap)
	case "record":
		return projectRecord(snap)
	default:
		// Fallback: provide the prompt and any existing spec.
		return projectDefault(snap)
	}
}

// projectReconcile builds the reconciler's user message: the raw proposal
// from the upstream propose/revise step plus the existing-spec snapshot
// (when present) so the agent can mark clusters for ID reuse rather than
// minting new IDs.
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
	if snap.Existing != nil && !snap.Existing.IsEmpty() {
		b.WriteString("\n\n## Existing spec snapshot (decisions you may reuse via reuse_existing)\n\n")
		formatExistingDecisions(&b, snap.Existing)
	}
	b.WriteString("\n\nEmit a ReconciliationVerdict naming the clusters that need dedupe / resolve_conflict / reuse_existing. Inline decisions you do not mention are kept as separate canonical decisions.")
	return []Message{{Role: "user", Content: b.String()}}
}

func formatExistingDecisions(b *strings.Builder, e *ExistingSpec) {
	for _, d := range e.Decisions {
		fmt.Fprintf(b, "- %s — %s (%s, confidence=%.2f)\n", d.ID, d.Title, d.Status, d.Confidence)
		if d.Rationale != "" {
			fmt.Fprintf(b, "  rationale: %s\n", d.Rationale)
		}
	}
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
