package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chetan/locutus/internal/check"
	"github.com/chetan/locutus/internal/reconcile"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
)

// AdoptCmd brings code into alignment with spec (the DJ-068 reconcile
// loop). It classifies every Approach into one of the lifecycle statuses,
// prints a reconciliation plan, optionally runs prereq checks, and — when
// not --dry-run — dispatches workstreams via the dispatcher.
//
// Phase C minimum viable: classification + plan preview + prereq gate +
// state store writes. Dispatch integration and cascade/pre-flight land in
// later Phase C rounds.
type AdoptCmd struct {
	Scope  string `arg:"" optional:"" help:"Limit adoption to spec nodes under this ID (default: all)."`
	DryRun bool   `help:"Classify and plan without dispatching."`
}

// AdoptReport is the structured result returned by RunAdopt, used by both
// the CLI and MCP handlers.
type AdoptReport struct {
	Scope           string                     `json:"scope,omitempty"`
	DryRun          bool                       `json:"dry_run"`
	Classifications []reconcile.Classification `json:"classifications"`
	PrereqResults   []check.Result             `json:"prereq_results,omitempty"`
	PrereqsOK       bool                       `json:"prereqs_ok"`
	Summary         AdoptSummary               `json:"summary"`
}

// AdoptSummary is a compact count of the reconciled statuses.
type AdoptSummary struct {
	Live       int `json:"live"`
	Drifted    int `json:"drifted"`
	OutOfSpec  int `json:"out_of_spec"`
	Unplanned  int `json:"unplanned"`
	Failed     int `json:"failed"`
	InProgress int `json:"in_progress"`
	Candidates int `json:"candidates"` // count that would be planned
}

func (c *AdoptCmd) Run(cli *CLI) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	report, err := RunAdopt(context.Background(), fsys, c.Scope, c.DryRun)
	if err != nil {
		return err
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(report)
	}

	renderAdoptReport(report)

	// Exit codes: 2 when prereqs failed, 1 when out_of_spec drift blocked
	// adoption, 0 otherwise. Dry-run always exits 0.
	if report.DryRun {
		return nil
	}
	if !report.PrereqsOK {
		os.Exit(2)
	}
	if report.Summary.OutOfSpec > 0 {
		os.Exit(1)
	}
	return nil
}

// RunAdopt is the shared implementation behind the CLI command and the MCP
// tool. It does NOT dispatch agents yet (Phase C round 2); it classifies,
// runs prereqs, and updates transient state store entries for unplanned
// Approaches to record that a reconciliation pass happened.
func RunAdopt(ctx context.Context, fsys specio.FS, scope string, dryRun bool) (*AdoptReport, error) {
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	bugs, _ := collectObjects[spec.Bug](fsys, ".borg/spec/bugs")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	approaches, _ := collectMarkdown[spec.Approach](fsys, ".borg/spec/approaches")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		_ = json.Unmarshal(data, &traces)
	}

	graph := spec.BuildGraph(features, bugs, decisions, strategies, approaches, traces)

	decMap := make(map[string]spec.Decision, len(decisions))
	for _, d := range decisions {
		decMap[d.ID] = d
	}

	store := state.NewFileStateStore(fsys, ".locutus/state")

	classifications, err := reconcile.Classify(fsys, graph, store, decMap)
	if err != nil {
		return nil, fmt.Errorf("classify: %w", err)
	}

	// Optional scope filter — keep only classifications whose Approach is in
	// the subtree of the scope node.
	if scope != "" {
		inScope := make(map[string]bool)
		for _, a := range graph.ApproachesUnder(scope) {
			inScope[a.ID] = true
		}
		filtered := make([]reconcile.Classification, 0, len(classifications))
		for _, c := range classifications {
			if inScope[c.Approach.ID] {
				filtered = append(filtered, c)
			}
		}
		classifications = filtered
	}

	report := &AdoptReport{
		Scope:           scope,
		DryRun:          dryRun,
		Classifications: classifications,
		Summary:         summariseClassifications(classifications),
	}

	// Prereq gate.
	prereqs, perr := check.CheckPrereqs(fsys)
	if perr != nil {
		return nil, fmt.Errorf("prereqs: %w", perr)
	}
	report.PrereqResults = prereqs
	report.PrereqsOK = !check.AnyFailed(prereqs)

	if dryRun {
		return report, nil
	}

	// Not dry-run: persist a "planned" transient status for every candidate
	// so the state store reflects that we observed them. Actual dispatch
	// comes next round (Phase C2). Live and out_of_spec entries are left
	// untouched.
	for _, c := range classifications {
		if c.Status == state.StatusLive || c.Status == state.StatusOutOfSpec {
			continue
		}
		entry := state.ReconciliationState{
			ApproachID:     c.Approach.ID,
			SpecHash:       c.CurrentHash,
			Artifacts:      c.StoredFiles, // preserve last known; dispatch rewrites
			Status:         state.StatusPlanned,
			Message:        "queued for adoption",
			LastReconciled: time.Now(),
		}
		if c.StateEntry != nil {
			entry.WorkstreamID = c.StateEntry.WorkstreamID
			entry.AssertionResults = c.StateEntry.AssertionResults
		}
		if err := store.Save(entry); err != nil {
			return report, fmt.Errorf("saving state for %s: %w", c.Approach.ID, err)
		}
	}

	return report, nil
}

func summariseClassifications(cs []reconcile.Classification) AdoptSummary {
	var s AdoptSummary
	for _, c := range cs {
		switch c.Status {
		case state.StatusLive:
			s.Live++
		case state.StatusDrifted:
			s.Drifted++
			s.Candidates++
		case state.StatusOutOfSpec:
			s.OutOfSpec++
		case state.StatusUnplanned:
			s.Unplanned++
			s.Candidates++
		case state.StatusFailed:
			s.Failed++
			s.Candidates++
		case state.StatusInProgress, state.StatusPlanned, state.StatusPreFlight:
			s.InProgress++
			s.Candidates++
		}
	}
	return s
}

func renderAdoptReport(r *AdoptReport) {
	heading := "Adoption plan"
	if r.DryRun {
		heading = "Adoption plan (dry-run)"
	}
	if r.Scope != "" {
		heading += fmt.Sprintf(" — scope %s", r.Scope)
	}
	fmt.Println(heading)
	fmt.Println()

	if len(r.Classifications) == 0 {
		fmt.Println("  No Approach nodes found. Nothing to reconcile.")
		return
	}

	for _, c := range r.Classifications {
		fmt.Printf("  %-12s  %s\n", c.Status, c.Approach.ID)
	}
	fmt.Println()
	fmt.Printf("Summary: %d live, %d drifted, %d out_of_spec, %d unplanned, %d failed, %d in_progress. %d candidate(s).\n",
		r.Summary.Live, r.Summary.Drifted, r.Summary.OutOfSpec,
		r.Summary.Unplanned, r.Summary.Failed, r.Summary.InProgress, r.Summary.Candidates)

	if len(r.PrereqResults) > 0 {
		fmt.Println()
		status := "ok"
		if !r.PrereqsOK {
			status = "FAILED — adoption will abort"
		}
		fmt.Printf("Prerequisites: %s\n", status)
		for _, p := range r.PrereqResults {
			for _, pr := range p.Passed {
				fmt.Printf("  ok    %s — %s\n", p.StrategyID, pr)
			}
			for _, f := range p.Failed {
				fmt.Printf("  FAIL  %s — %s (%s)\n", p.StrategyID, f.Prerequisite, f.Err)
			}
		}
	}

	if r.Summary.OutOfSpec > 0 {
		fmt.Println()
		fmt.Println("One or more Approaches have out_of_spec drift (artifacts changed outside Locutus).")
		fmt.Println("Resolve each by running `locutus refine <id>` to update spec to match, or by")
		fmt.Println("reverting the artifact and re-running `locutus adopt`.")
	}
}
