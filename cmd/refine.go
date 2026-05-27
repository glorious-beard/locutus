package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
)

// RefineCmd dispatches the spec_refinement activity. Per DJ-135
// phase 5 the verb no longer drives the Go council in-process —
// instead it forks a coding-agent runtime (Claude Code / Codex /
// Gemini, picked by activity.Registry.Resolve) and hands it the
// spec_refinement playbook from .borg/plans/. The agent calls back
// into Locutus's MCP server (spec_* tools, spec://manifest resource)
// to mutate the graph.
//
// DJ-138 adds --with for the strong-bias cascade surface. With
// --with set, the verb routes to the spec_bias activity instead of
// spec_refinement; the target id becomes mandatory and is validated
// against the deliberation-layer kinds (Decision / Feature /
// Strategy). Goal / Approach / Bug are rejected under --with; in
// plain mode Approach and Bug are additionally rejected as
// targets entirely (DJ-138 resolved-question 6).
type RefineCmd struct {
	Target string `arg:"" optional:"" default:"goals" help:"Spec node id to focus on. Defaults to 'goals' (the root node — refines the whole graph against GOALS.md). Pass a specific id (e.g. 'dec-oltp-store') to scope the run to that subtree. Approach (app-) and Bug (bug-) ids are rejected — those layers have their own surfaces."`
	With   string `help:"Strong-bias cascade: apply the given natural-language bias to <target> and cascade implications through the spec graph in both directions. Requires <target> to be a Decision, Feature, or Strategy id (Goal / Approach / Bug rejected). Git is the rollback layer: commit before risky --with runs."`
}

func (c *RefineCmd) Run(ctx context.Context, cli *CLI) error {
	fsys, _, err := projectFS()
	if err != nil {
		return err
	}
	activityName, err := c.resolveActivity(fsys)
	if err != nil {
		return err
	}
	return runActivityVerb(ctx, cli, activityName, c.contextNote())
}

// resolveActivity validates the target / --with combination and
// returns the activity name to dispatch. Pulls fsys to perform the
// node-existence check; the kind-prefix validation is structural
// and could run without it, but the existence check provides early
// feedback on typos before the ACP subprocess spins up.
func (c *RefineCmd) resolveActivity(fsys specio.FS) (string, error) {
	target := c.Target
	if target == "" {
		target = "goals"
	}

	// Always-on validation: Approach and Bug ids are not refine
	// targets in either mode (DJ-138 resolved-question 6). The
	// `goals` default is exempt — it's the whole-graph default,
	// not a node-kind violation.
	if target != "goals" {
		if err := c.validateTargetKind(target); err != nil {
			return "", err
		}
	}

	if c.With == "" {
		// Plain mode: existence check happens inside the playbook
		// since plain refine can run with any deliberation-layer
		// id (or the `goals` default) — and the playbook already
		// reads the target. Returning here matches the pre-DJ-138
		// behavior.
		return "spec_refinement", nil
	}

	// --with mode: tighter validation.
	bias := strings.TrimSpace(c.With)
	if bias == "" {
		return "", fmt.Errorf("--with requires a non-empty bias string (got whitespace-only input)")
	}
	if target == "goals" {
		return "", fmt.Errorf("--with requires an explicit target id (Decision / Feature / Strategy); refused the default 'goals' because cascading from the root would over-blast the graph")
	}
	if err := c.validateWithTargetKind(target); err != nil {
		return "", err
	}
	if err := c.validateTargetExists(fsys, target); err != nil {
		return "", err
	}
	return "spec_bias", nil
}

// validateTargetKind rejects Approach (app-) and Bug (bug-) ids
// regardless of --with state. Decision / Feature / Strategy / Goal
// ids pass; an unknown prefix returns a clear error rather than
// silently routing.
func (c *RefineCmd) validateTargetKind(target string) error {
	kind := kindFromIDPrefix(target)
	switch kind {
	case spec.KindApproach:
		return fmt.Errorf("refine rejects Approach ids (got %q) — approaches are the synthesis layer; use `locutus adopt` or `locutus assimilate` to regenerate them", target)
	case spec.KindBug:
		return fmt.Errorf("refine rejects Bug ids (got %q) — the bug subgraph has its own surface, out of scope for refine", target)
	case "":
		// Unknown prefix: allow through so plain-mode `goals` is the
		// only "no recognized prefix" path that reaches the playbook.
		// `goals` was already handled by the caller; any other
		// non-prefixed id surfaces here as an explicit error.
		if target != "goals" {
			return fmt.Errorf("refine: target %q is not a recognized spec id (expected one of: dec-…, feat-…, strat-…, or the special token 'goals')", target)
		}
	}
	return nil
}

// validateWithTargetKind tightens validation for --with: only the
// mutable deliberation-layer kinds are valid (Decision / Feature /
// Strategy). Goal is rejected because cascading from the root is
// over-broad; Approach / Bug are already rejected by
// validateTargetKind.
func (c *RefineCmd) validateWithTargetKind(target string) error {
	kind := kindFromIDPrefix(target)
	switch kind {
	case spec.KindDecision, spec.KindFeature, spec.KindStrategy:
		return nil
	}
	return fmt.Errorf("--with target %q must be a Decision (dec-), Feature (feat-), or Strategy (strat-) id; got kind %q", target, kind)
}

// validateTargetExists confirms the target id resolves to a loaded
// spec node. Defends against typos before the ACP subprocess spins
// up — the playbook double-checks but this short-circuit saves the
// operator a round-trip.
func (c *RefineCmd) validateTargetExists(fsys specio.FS, target string) error {
	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		// LoadSpec failures are non-fatal for the existence check —
		// fall through and let the playbook surface the underlying
		// problem rather than hiding it behind a missing-id error.
		return nil
	}
	kind := kindFromIDPrefix(target)
	switch kind {
	case spec.KindDecision:
		if loaded.DecisionNodeByID(target) != nil {
			return nil
		}
	case spec.KindFeature:
		if loaded.FeatureNodeByID(target) != nil {
			return nil
		}
	case spec.KindStrategy:
		if loaded.StrategyNodeByID(target) != nil {
			return nil
		}
	}
	return fmt.Errorf("--with target %q does not resolve to a known spec node — run `locutus list` to find the right id", target)
}

// contextNote returns the per-invocation context appended to the
// playbook body before dispatch. Always carries Target; carries
// Bias when --with is set. The playbook reads both lines as
// load-bearing inputs.
func (c *RefineCmd) contextNote() string {
	target := c.Target
	if target == "" {
		target = "goals"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Target: %s", target)
	if bias := strings.TrimSpace(c.With); bias != "" {
		fmt.Fprintf(&b, "\nBias: %s", bias)
	}
	return b.String()
}
