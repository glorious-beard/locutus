package agent

import (
	"context"
	"fmt"
	"strings"
)

// ConvergenceVerdict is the LLM-driven assessment of council convergence.
type ConvergenceVerdict struct {
	Converged     bool     `json:"converged"`
	Reasoning     string   `json:"reasoning"`
	OpenIssues    []string `json:"open_issues,omitempty"`
	ForcedAfter   int      `json:"forced_after,omitempty"` // round at which decision was forced
}

// CheckConvergence calls the convergence monitor agent to assess whether the
// council has reached agreement. It uses a fast/cheap model (configured in the
// convergence agent def) and evaluates the full planning state.
func CheckConvergence(ctx context.Context, exec AgentExecutor, monitorDef AgentDef, state *PlanningState) (*ConvergenceVerdict, error) {
	snap := state.Snapshot()

	prompt := buildConvergencePrompt(snap)
	input := AgentInput{Messages: []Message{{Role: "user", Content: prompt}}}

	resp, err := RunWithRetry(ctx, exec, monitorDef, input, executionRetryConfig())
	if err != nil {
		return nil, fmt.Errorf("convergence check: %w", err)
	}

	return parseConvergenceResponse(resp.Content, state.Round), nil
}

// buildConvergencePrompt constructs the prompt for the convergence monitor.
func buildConvergencePrompt(snap StateSnapshot) string {
	var b strings.Builder

	// Data sections only — the convergence agent's system prompt (convergence.md)
	// contains the evaluation criteria and response format instructions.
	if snap.ProposedSpec != "" {
		b.WriteString("## Current Proposal\n")
		b.WriteString(compactContext(snap.ProposedSpec, defaultMaxChars))
		b.WriteString("\n\n")
	}

	if len(snap.Concerns) > 0 {
		b.WriteString("## Concerns Raised\n")
		for _, c := range snap.Concerns {
			fmt.Fprintf(&b, "- [%s/%s] %s\n", c.AgentID, c.Severity, c.Text)
		}
		b.WriteString("\n")
	}

	if len(snap.ResearchResults) > 0 {
		b.WriteString("## Research Findings\n")
		for _, f := range snap.ResearchResults {
			fmt.Fprintf(&b, "- Q: %s\n  A: %s\n", f.Query, f.Result)
		}
		b.WriteString("\n")
	}

	if snap.Revisions != "" {
		b.WriteString("## Revised Proposal\n")
		b.WriteString(compactContext(snap.Revisions, defaultMaxChars))
		b.WriteString("\n\n")
	}

	return b.String()
}

// parseConvergenceResponse interprets the convergence monitor's free-text
// output into a structured verdict. This is intentionally lenient — the
// monitor is an LLM and may not follow the exact format.
func parseConvergenceResponse(content string, round int) *ConvergenceVerdict {
	upper := strings.ToUpper(content)

	verdict := &ConvergenceVerdict{
		Reasoning: content,
	}

	switch {
	case strings.Contains(upper, "CONVERGED") && !strings.Contains(upper, "NOT_CONVERGED"):
		verdict.Converged = true
	case strings.Contains(upper, "CYCLING"):
		// Cycling = force convergence to break the loop.
		verdict.Converged = true
		verdict.ForcedAfter = round
		verdict.Reasoning = "Forced convergence: council is cycling. " + content
	default:
		// NOT_CONVERGED or ambiguous — extract open issues from lines starting with "-".
		verdict.Converged = false
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") {
				verdict.OpenIssues = append(verdict.OpenIssues, strings.TrimPrefix(line, "- "))
			}
		}
	}

	return verdict
}

// CheckReadiness runs the readiness gate: critic and stakeholder each get a
// final approval call. Returns true only if both approve.
func CheckReadiness(ctx context.Context, exec AgentExecutor, agentDefs map[string]AgentDef, state *PlanningState) (bool, error) {
	snap := state.Snapshot()

	approvers := []string{"critic", "stakeholder"}
	for _, id := range approvers {
		def, ok := agentDefs[id]
		if !ok {
			continue // if agent not defined, skip (don't block)
		}

		prompt := fmt.Sprintf(
			"The council has converged on a proposal. Review and respond APPROVED or BLOCKED with reason.\n\nProposal:\n%s\n\nRevisions:\n%s",
			snap.ProposedSpec, snap.Revisions,
		)

		input := AgentInput{Messages: []Message{{Role: "user", Content: prompt}}}
		resp, err := RunWithRetry(ctx, exec, def, input, executionRetryConfig())
		if err != nil {
			return false, fmt.Errorf("readiness check (%s): %w", id, err)
		}

		if strings.Contains(strings.ToUpper(resp.Content), "BLOCKED") {
			return false, nil
		}
	}

	return true, nil
}
