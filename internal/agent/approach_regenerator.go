package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// RegenerateApproachResult is the JSON shape the approach-regenerator
// agent returns. The agent rewrites the Body to cover both the new
// spec (forward) and the cleanup of prior artifacts (backward) but
// leaves the structured fields — ArtifactPaths, Decisions, Skills,
// Prerequisites, Assertions — untouched. Those carry forward from
// the prior approach via the caller; the cascade has already rewritten
// any id references the supersede affected.
type RegenerateApproachResult struct {
	RevisedBody string `json:"revised_body"`
	Rationale   string `json:"rationale"`
}

func init() {
	RegisterSchema("RegenerateApproachResult", RegenerateApproachResult{
		RevisedBody: "the revised approach body covering forward (new spec) and backward (cleanup) directions",
		Rationale:   "one-line summary of how the regeneration addresses the supersession",
	})
}

// RegenerateApproachContext carries the inputs the regenerator agent
// needs to address both the new spec and the cleanup of prior
// artifacts.
type RegenerateApproachContext struct {
	// PriorApproach is the invalidated approach as it sits on disk —
	// id-references already cascade-rewritten, but the Body still
	// describes the old spec and ArtifactPaths still lists what the
	// coding agent built last time.
	PriorApproach spec.Approach
	// CurrentParent is the live parent (Feature or Strategy) that
	// the approach is owned by, after any cascade rewrites.
	ParentKind  spec.NodeKind
	ParentID    string
	ParentTitle string
	ParentProse string

	// CurrentDecisions is the set of Decisions the parent currently
	// references, in their post-cascade state.
	CurrentDecisions []spec.Decision

	// SupersedeMotivation is the user's authoritative directive
	// from the supersede operation that invalidated this approach.
	SupersedeMotivation string
	// SupersededID and ReplacementID describe what was replaced.
	// Same value when the supersede was an in-place revision.
	SupersededID  string
	ReplacementID string
}

// InvokeApproachRegenerator runs the approach-regenerator agent and
// returns the parsed result. The caller is responsible for writing
// the result to disk (clearing InvalidatedByEventID) and refreshing
// CreatedAt/UpdatedAt.
func InvokeApproachRegenerator(ctx context.Context, llm AgentExecutor, def AgentDef, rctx RegenerateApproachContext) (*RegenerateApproachResult, error) {
	user := buildRegenerateApproachPrompt(rctx)
	out, err := llm.Run(ctx, def, AgentInput{Messages: []Message{{Role: "user", Content: user}}})
	if err != nil {
		return nil, fmt.Errorf("invoke approach regenerator: %w", err)
	}
	var result RegenerateApproachResult
	if err := json.Unmarshal([]byte(out.Content), &result); err != nil {
		return nil, fmt.Errorf("invoke approach regenerator: parse output: %w", err)
	}
	if strings.TrimSpace(result.RevisedBody) == "" {
		return nil, fmt.Errorf("invoke approach regenerator: agent returned empty body")
	}
	return &result, nil
}

func buildRegenerateApproachPrompt(rctx RegenerateApproachContext) string {
	var b strings.Builder
	b.WriteString("# Regeneration context\n\n")

	fmt.Fprintf(&b, "**Approach to regenerate:** `%s` (parent: `%s` / %s)\n\n",
		rctx.PriorApproach.ID, rctx.ParentID, rctx.ParentKind)

	b.WriteString("**Current parent:**\n\n")
	fmt.Fprintf(&b, "- Title: %s\n- Kind: %s\n", rctx.ParentTitle, rctx.ParentKind)
	if rctx.ParentProse != "" {
		b.WriteString("- Prose:\n\n  ")
		b.WriteString(strings.ReplaceAll(rctx.ParentProse, "\n", "\n  "))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if len(rctx.CurrentDecisions) > 0 {
		b.WriteString("**Current decisions (post-cascade):**\n\n")
		for _, d := range rctx.CurrentDecisions {
			fmt.Fprintf(&b, "- `%s` — %s (status: %s, confidence: %.2f)\n  Rationale: %s\n",
				d.ID, d.Title, d.Status, d.Confidence, d.Rationale)
		}
		b.WriteString("\n")
	}

	b.WriteString("**Supersession that invalidated this approach:**\n\n")
	if rctx.SupersededID == rctx.ReplacementID {
		fmt.Fprintf(&b, "- In-place revision of `%s` (content changed materially; structured fields revised)\n", rctx.SupersededID)
	} else {
		fmt.Fprintf(&b, "- `%s` superseded by `%s`\n", rctx.SupersededID, rctx.ReplacementID)
	}
	if rctx.SupersedeMotivation != "" {
		b.WriteString("- Motivation: ")
		b.WriteString(rctx.SupersedeMotivation)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("**Prior approach body (the brief the coding agent ran against last time):**\n\n")
	if rctx.PriorApproach.Body == "" {
		b.WriteString("(empty)\n\n")
	} else {
		b.WriteString("```markdown\n")
		b.WriteString(rctx.PriorApproach.Body)
		b.WriteString("\n```\n\n")
	}

	if len(rctx.PriorApproach.ArtifactPaths) > 0 {
		b.WriteString("**Prior artifact paths (files the previous coding-agent run produced):**\n\n")
		for _, p := range rctx.PriorApproach.ArtifactPaths {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
		b.WriteString("\n")
	}

	b.WriteString("# Your task\n\n")
	b.WriteString("Emit a revised Approach body (markdown prose) that covers both directions:\n\n")
	b.WriteString("1. **Forward.** Describe what the coding agent must build to satisfy the current spec.\n")
	b.WriteString("2. **Backward.** Describe what to do with each existing artifact from the prior list — keep, modify, replace, or delete — to align with the current spec. Be specific about each file: a coding agent reading this should know exactly which files are obsolete and which are still load-bearing.\n\n")
	b.WriteString("Match the voice and structure of the prior body where the prior content remains correct under the new spec. Do not invent new acceptance criteria; the parent's structured fields are authoritative.\n")
	return b.String()
}
