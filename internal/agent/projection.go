package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ProjectState builds the LLM messages for a specific agent role, drawing only
// the fields from the snapshot that are relevant to that agent's job. This keeps
// each agent's context window focused and avoids leaking irrelevant information.
func ProjectState(stepID string, snap StateSnapshot) []Message {
	switch stepID {
	case "propose":
		return projectPropose(snap)
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
	if snap.ProposedSpec != "" {
		msgs = append(msgs, Message{
			Role:    "assistant",
			Content: snap.ProposedSpec,
		})
	}

	// Include concerns and research for the planner to address.
	if len(snap.Concerns) > 0 {
		var lines []string
		for _, c := range snap.Concerns {
			lines = append(lines, fmt.Sprintf("- [%s/%s] %s", c.AgentID, c.Severity, c.Text))
		}
		msgs = append(msgs, Message{
			Role:    "user",
			Content: fmt.Sprintf("Concerns raised:\n%s", strings.Join(lines, "\n")),
		})
	}
	if len(snap.ResearchResults) > 0 {
		var lines []string
		for _, f := range snap.ResearchResults {
			lines = append(lines, fmt.Sprintf("Q: %s\nA: %s", f.Query, f.Result))
		}
		msgs = append(msgs, Message{
			Role:    "user",
			Content: fmt.Sprintf("Research findings:\n%s", strings.Join(lines, "\n---\n")),
		})
	}

	msgs = append(msgs, Message{
		Role:    "user",
		Content: "Address the concerns and findings above.",
	})
	return msgs
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
