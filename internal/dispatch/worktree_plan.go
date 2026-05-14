package dispatch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// LocutusWorktreeDir is the path (relative to a workstream worktree root)
// where Locutus writes its worktree-resident artifacts: the workstream's
// plan (written once by Locutus before the agent's first prompt) and the
// agent-maintained checklist. The `_` prefix signals the directory is
// internal to the workstream's tooling and not part of the project's own
// source tree; agents are instructed not to commit it (a gitignore line is
// also added by the worktree setup).
const LocutusWorktreeDir = "_locutus"

const (
	worktreePlanFile      = "plan.md"
	worktreeChecklistFile = "checklist.md"
)

// WriteWorkstreamPlan writes _locutus/plan.md into the workstream's worktree.
// The plan is the agent's authoritative briefing for what to build, the
// acceptance criteria the supervisor will grade against, and the checklist
// it must maintain at _locutus/checklist.md as it works (per DJ-121).
//
// This is the Phase 2 additive form — PlanSteps still exist in the spec
// model, so the plan renders both workstream-level assertions and the
// per-step decomposition the planner produced. Phase 4 removes the
// per-step section once `spec.PlanStep` goes away; the surrounding
// scaffolding (plan path, checklist instruction, the rest of the prose)
// remains.
func WriteWorkstreamPlan(worktreeDir string, ws spec.Workstream) error {
	if worktreeDir == "" {
		return fmt.Errorf("WriteWorkstreamPlan: empty worktreeDir")
	}
	dir := filepath.Join(worktreeDir, LocutusWorktreeDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	body := renderWorkstreamPlan(ws)
	path := filepath.Join(dir, worktreePlanFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// renderWorkstreamPlan produces the markdown body of plan.md for ws. Pure
// function — separated from WriteWorkstreamPlan so it's unit-testable
// without touching the filesystem.
//
// The section ordering reflects DJ-121's emphasis: workstream-level
// acceptance criteria are the primary contract and appear first. The
// per-step decomposition the planner produced (if any) appears below as
// a hint the agent may adopt or supersede. Once the planner is migrated
// (Phase 6) the per-step section will typically be empty; the renderer
// still handles both shapes for transition compatibility.
func renderWorkstreamPlan(ws spec.Workstream) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Workstream %s\n\n", ws.ID)
	fmt.Fprintf(&b, "**Strategy domain:** %s\n", ws.StrategyDomain)
	if ws.AgentID != "" {
		fmt.Fprintf(&b, "**Assigned agent:** %s\n", ws.AgentID)
	}
	fmt.Fprintln(&b)

	// Workstream-level acceptance criteria — the primary contract under
	// DJ-121. The validator grades against these at workstream completion.
	// When ws.Assertions is populated, the agent should treat this as the
	// authoritative success definition. When empty, the step-level
	// assertions further down are the transitional fallback.
	if len(ws.Assertions) > 0 {
		fmt.Fprintln(&b, "## Acceptance criteria (workstream-level)")
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "_The validator will grade your work against these criteria. Treat them as the authoritative success definition for this workstream._")
		fmt.Fprintln(&b)
		for _, a := range ws.Assertions {
			fmt.Fprintf(&b, "- %s\n", renderAssertion(a))
		}
		fmt.Fprintln(&b)
	}

	// Approaches touched — lifted from steps for traceability into the spec
	// DAG. Each unique ApproachID gets one line.
	if approaches := uniqueApproaches(ws); len(approaches) > 0 {
		fmt.Fprintln(&b, "## Approaches in scope")
		fmt.Fprintln(&b)
		for _, a := range approaches {
			fmt.Fprintf(&b, "- %s\n", a)
		}
		fmt.Fprintln(&b)
	}

	// Expected files — lifted from steps. Helps the agent understand the
	// blast radius before reading the codebase.
	if files := uniqueExpectedFiles(ws); len(files) > 0 {
		fmt.Fprintln(&b, "## Expected files")
		fmt.Fprintln(&b)
		for _, f := range files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		fmt.Fprintln(&b)
	}

	// Per-step decomposition — hint, not contract. Per DJ-121, the agent
	// owns its own decomposition; this section reflects whatever the
	// planner produced as a starting point. When ws.Assertions is
	// populated, the per-step assertions here are subordinate to it. When
	// ws.Assertions is empty (the transitional fallback the validator
	// also handles), per-step assertions are the only acceptance criteria
	// available, so the agent should treat them as binding.
	if len(ws.Steps) > 0 {
		header := "## Suggested decomposition (hint — adopt or supersede as needed)"
		if len(ws.Assertions) == 0 {
			header = "## Suggested decomposition (transitional — step assertions are the only acceptance criteria for this workstream until the planner is migrated)"
		}
		fmt.Fprintln(&b, header)
		fmt.Fprintln(&b)
		for _, step := range ws.Steps {
			fmt.Fprintf(&b, "### Step %d: %s\n\n", step.Order, step.ID)
			if step.Description != "" {
				fmt.Fprintf(&b, "%s\n\n", step.Description)
			}
			if len(step.Assertions) > 0 {
				fmt.Fprintln(&b, "**Assertions:**")
				for _, a := range step.Assertions {
					fmt.Fprintf(&b, "- %s\n", renderAssertion(a))
				}
				fmt.Fprintln(&b)
			}
		}
	}

	// The checklist instruction. This is the load-bearing DJ-121 piece:
	// the agent owns step decomposition via this file, and Locutus's resume
	// contract narrows because the agent's own continuity comes from here.
	fmt.Fprintln(&b, "## Your checklist")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Maintain `%s/%s` as you work. Use it to:\n\n", LocutusWorktreeDir, worktreeChecklistFile)
	fmt.Fprintln(&b, "- Plan your own decomposition of this workstream into concrete steps before you start coding.")
	fmt.Fprintln(&b, "- Mark each step as you complete it, with a brief note about what landed.")
	fmt.Fprintln(&b, "- Record any decisions you made that aren't already captured in the spec DAG — Locutus's supervisor will read your checklist on resume if this workstream is interrupted.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "The checklist is your working memory, not Locutus's. Keep it concise and current; stale entries hurt resume more than missing entries.")

	return b.String()
}

// uniqueApproaches collects the distinct ApproachIDs across ws.Steps,
// in first-encounter order. Empty when ws has no steps.
func uniqueApproaches(ws spec.Workstream) []string {
	seen := make(map[string]bool, len(ws.Steps))
	out := make([]string, 0, len(ws.Steps))
	for _, s := range ws.Steps {
		if s.ApproachID == "" || seen[s.ApproachID] {
			continue
		}
		seen[s.ApproachID] = true
		out = append(out, s.ApproachID)
	}
	return out
}

// uniqueExpectedFiles collects the distinct paths across ws.Steps'
// ExpectedFiles, in sorted order. Sorting (rather than first-encounter)
// keeps the rendered output stable across plan regenerations.
func uniqueExpectedFiles(ws spec.Workstream) []string {
	seen := make(map[string]bool)
	for _, s := range ws.Steps {
		for _, f := range s.ExpectedFiles {
			seen[f] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// renderAssertion produces a one-line markdown description of an assertion.
// Kept compact so a workstream with many assertions doesn't bury the rest
// of the plan.
func renderAssertion(a spec.Assertion) string {
	parts := []string{string(a.Kind)}
	if a.Target != "" {
		parts = append(parts, fmt.Sprintf("target=`%s`", a.Target))
	}
	if a.Pattern != "" {
		parts = append(parts, fmt.Sprintf("pattern=`%s`", a.Pattern))
	}
	if a.Prompt != "" {
		parts = append(parts, fmt.Sprintf("prompt=%q", a.Prompt))
	}
	out := strings.Join(parts, " ")
	if a.Message != "" {
		out += " — " + a.Message
	}
	return out
}
