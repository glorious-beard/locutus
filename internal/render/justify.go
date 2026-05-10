package render

import (
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/agent"
)

// JustifyMarkdown renders the spec advocate's defense for one node.
// nodeID is the spec id; brief is the LLM output; sessionPath is the
// session directory under .locutus/sessions/ (empty when the run was
// not session-recorded).
func JustifyMarkdown(nodeID string, brief *agent.JustificationBrief, sessionPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Justifying `%s`\n\n", nodeID)

	b.WriteString("## Defense\n\n")
	b.WriteString(strings.TrimSpace(brief.Defense))
	b.WriteString("\n\n")

	if len(brief.GoalClausesCited) > 0 {
		b.WriteString("## Goals being served\n\n")
		for _, g := range brief.GoalClausesCited {
			fmt.Fprintf(&b, "- %s\n", g)
		}
		b.WriteString("\n")
	}

	if len(brief.ConditionsUnderWhichInvalid) > 0 {
		b.WriteString("## Conditions under which this should be revisited\n\n")
		for _, c := range brief.ConditionsUnderWhichInvalid {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString("\n")
	}

	if sessionPath != "" {
		fmt.Fprintf(&b, "---\n\n*Session: %s/*\n", sessionPath)
	}

	return b.String()
}

// JustifyAgainstMarkdown renders the adversarial dialogue: the
// challenge prompt, the challenger's concerns, the researcher's
// grounded findings, and the advocate's rebuttal. nodeID identifies
// the spec node; challenge is the user-supplied prompt; research may
// be nil when the researcher returned no findings; sessionPath is
// optional.
func JustifyAgainstMarkdown(nodeID, challenge string, ch *agent.ChallengeBrief, research *agent.ResearchBrief, def *agent.AdversarialDefense, sessionPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Justifying `%s`\n\n", nodeID)
	fmt.Fprintf(&b, "**Challenge:** %s\n\n", challenge)

	b.WriteString("## Challenger's concerns\n\n")
	for i, c := range ch.Concerns {
		fmt.Fprintf(&b, "### %d. %s\n\n", i+1, c.Weakness)
		if c.Evidence != "" {
			fmt.Fprintf(&b, "*Evidence:* %s\n\n", c.Evidence)
		}
		if c.Counterproposal != "" {
			fmt.Fprintf(&b, "*Counterproposal:* %s\n\n", c.Counterproposal)
		}
	}

	if research != nil && len(research.Findings) > 0 {
		b.WriteString("## Researcher's findings\n\n")
		for i, f := range research.Findings {
			fmt.Fprintf(&b, "### %d. %s\n\n", i+1, f.Query)
			if f.Result != "" {
				b.WriteString(strings.TrimSpace(f.Result))
				b.WriteString("\n\n")
			}
		}
	}

	b.WriteString("## Advocate's response\n\n")
	b.WriteString(strings.TrimSpace(def.Defense))
	b.WriteString("\n\n")

	if len(def.PointByPointAddressed) > 0 {
		b.WriteString("### Point-by-point\n\n")
		for i, ac := range def.PointByPointAddressed {
			fmt.Fprintf(&b, "**Concern %d (%s):**\n\n%s\n\n*Still stands:* %s\n\n",
				i+1, ac.ConcernSummary, strings.TrimSpace(ac.Response), formatStillStands(ac.StillStands))
		}
	}

	if len(def.GoalClausesCited) > 0 {
		b.WriteString("## Goals being served\n\n")
		for _, g := range def.GoalClausesCited {
			fmt.Fprintf(&b, "- %s\n", g)
		}
		b.WriteString("\n")
	}

	if len(def.ConditionsUnderWhichInvalid) > 0 {
		b.WriteString("## Conditions under which this should be revisited\n\n")
		for _, c := range def.ConditionsUnderWhichInvalid {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Verdict: %s\n\n", strings.ToUpper(strings.ReplaceAll(def.Verdict, "_", " ")))

	if len(def.BreakingPoints) > 0 {
		b.WriteString("**Breaking points:**\n\n")
		for _, bp := range def.BreakingPoints {
			fmt.Fprintf(&b, "- %s\n", bp)
		}
		b.WriteString("\n")

		b.WriteString("## Suggested next step\n\n")
		b.WriteString("```\n")
		fmt.Fprintf(&b, "%s\n", suggestedNextStep(nodeID, def.Verdict, def.BreakingPoints))
		b.WriteString("```\n\n")
	}

	if sessionPath != "" {
		fmt.Fprintf(&b, "---\n\n*Session: %s/*\n", sessionPath)
	}

	return b.String()
}

func formatStillStands(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// breakingPointsAsBrief joins the breaking-point list into a single
// brief string suitable as a refine --brief argument. Used in the
// suggested-next-step block of the adversarial dialogue.
func breakingPointsAsBrief(points []string) string {
	return "Address: " + strings.Join(points, "; ")
}

// suggestedNextStep returns the command string the operator should
// run to act on the verdict. Routes by verdict + node kind:
//
//   - BROKE DOWN against a Decision/Feature/Strategy → --supersede,
//     since the breaking points are sufficient to invalidate the
//     node's identity (alternatives missing, scope wrong, framing
//     stale). The supersede flow rewrites references and invalidates
//     affected approaches.
//   - BROKE DOWN against a Bug → --brief, since bugs use status
//     transitions plus a fresh filing for wrong-root-cause cases;
//     the supersede path doesn't accept bugs.
//   - held / partially_held_up / anything else → --brief, the
//     existing prose-cascade path.
//
// Bugs and approaches under BROKE DOWN still emit --brief so the
// operator at least gets a reasonable default; a smarter routing
// for bug-shaped verdicts is deferred.
func suggestedNextStep(nodeID, verdict string, breakingPoints []string) string {
	brief := breakingPointsAsBrief(breakingPoints)
	if verdict == "broke_down" && supersedeAcceptsKind(nodeID) {
		return fmt.Sprintf("locutus refine %s --supersede %q", nodeID, brief)
	}
	return fmt.Sprintf("locutus refine %s --brief %q", nodeID, brief)
}

// supersedeAcceptsKind reports whether the node id prefix matches a
// kind that `refine --supersede` operates on. Decisions, features,
// and strategies are in scope; bugs are explicitly excluded; other
// prefixes (approaches, goals) are out by construction.
func supersedeAcceptsKind(nodeID string) bool {
	return strings.HasPrefix(nodeID, "dec-") ||
		strings.HasPrefix(nodeID, "feat-") ||
		strings.HasPrefix(nodeID, "strat-")
}
