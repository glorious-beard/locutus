package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/workstream"
)

// StatusCmd shows spec summary, and optionally in-flight plans.
type StatusCmd struct {
	InFlight bool `help:"Only print in-flight plans (leftover .locutus/workstreams/ subdirectories from interrupted adopt runs)."`
}

func (c *StatusCmd) Run(cli *CLI) error {
	fsys := specio.NewOSFS(".")

	if c.InFlight {
		report, err := GatherInFlight(fsys)
		if err != nil {
			return err
		}
		if cli.JSON {
			return json.NewEncoder(os.Stdout).Encode(report)
		}
		printInFlight(report)
		return nil
	}

	sd := GatherStatus(fsys)
	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(sd)
	}
	fmt.Print(render.StatusSummary(sd))
	return nil
}

// GatherStatus reads the spec from the FS and returns a StatusData struct
// summarizing the current state of the spec graph.
func GatherStatus(fsys specio.FS) render.StatusData {
	var sd render.StatusData

	// Check if GOALS.md exists.
	if _, err := fsys.Stat(".borg/GOALS.md"); err == nil {
		sd.GoalsPresent = true
	}

	// Count features.
	if pairs, err := specio.WalkPairs[spec.Feature](fsys, ".borg/spec/features"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				sd.FeatureCount++
			}
		}
	}

	// Count decisions.
	if pairs, err := specio.WalkPairs[spec.Decision](fsys, ".borg/spec/decisions"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				sd.DecisionCount++
			}
		}
	}

	// Count strategies.
	if pairs, err := specio.WalkPairs[spec.Strategy](fsys, ".borg/spec/strategies"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				sd.StrategyCount++
			}
		}
	}

	// Count bugs.
	if pairs, err := specio.WalkPairs[spec.Bug](fsys, ".borg/spec/bugs"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				sd.BugCount++
			}
		}
	}

	return sd
}

// InFlightPlan summarises one leftover plan from .locutus/workstreams/ —
// what adopt would see as a resume candidate on its next invocation.
type InFlightPlan struct {
	PlanID        string               `json:"plan_id"`
	PlanPrompt    string               `json:"plan_prompt,omitempty"`
	Workstreams   []InFlightWorkstream `json:"workstreams"`
	CreatedAt     string               `json:"created_at"`
	UpdatedAt     string               `json:"updated_at"`
}

// InFlightWorkstream records the per-workstream step progress of a
// leftover plan. It's the data a resume implementation would consume to
// decide where to pick up.
type InFlightWorkstream struct {
	WorkstreamID   string `json:"workstream_id"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	PreFlightDone  bool   `json:"pre_flight_done"`
	ApproachIDs    []string `json:"approach_ids"`
	StepTotal      int      `json:"step_total"`
	StepsComplete  int      `json:"steps_complete"`
	StepsFailed    int      `json:"steps_failed"`
	StepsPending   int      `json:"steps_pending"`
	NextStepID     string   `json:"next_step_id,omitempty"` // first step not marked complete
}

// InFlightReport is the JSON shape emitted by `status --in-flight`.
type InFlightReport struct {
	BaseDir string         `json:"base_dir"`
	Plans   []InFlightPlan `json:"plans"`
}

// GatherInFlight walks .locutus/workstreams/ and returns a summary of
// every leftover plan. No LLM calls, no network — just reads the
// persisted records. Intended as the discovery surface for interrupted
// adopt runs (DJ-073).
func GatherInFlight(fsys specio.FS) (*InFlightReport, error) {
	report := &InFlightReport{BaseDir: workstreamsDir}
	planIDs, err := workstream.ListActivePlans(fsys, workstreamsDir)
	if err != nil {
		return report, err
	}
	for _, id := range planIDs {
		store := workstream.NewFileStore(fsys, workstreamsDir, id)
		plan, err := store.LoadPlan()
		if err != nil {
			continue
		}
		entry := InFlightPlan{
			PlanID:     id,
			PlanPrompt: plan.Plan.Prompt,
			CreatedAt:  plan.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt:  plan.UpdatedAt.Format("2006-01-02 15:04:05"),
		}
		workstreams, err := store.Walk()
		if err != nil {
			continue
		}
		for _, ws := range workstreams {
			entry.Workstreams = append(entry.Workstreams, summariseInFlightWorkstream(ws))
		}
		report.Plans = append(report.Plans, entry)
	}
	return report, nil
}

func summariseInFlightWorkstream(ws workstream.ActiveWorkstream) InFlightWorkstream {
	out := InFlightWorkstream{
		WorkstreamID:   ws.WorkstreamID,
		AgentID:        ws.Plan.AgentID,
		AgentSessionID: ws.AgentSessionID,
		PreFlightDone:  ws.PreFlightDone,
		ApproachIDs:    append([]string(nil), ws.ApproachIDs...),
		StepTotal:      len(ws.Plan.Steps),
	}

	completedByStep := make(map[string]workstream.StepExecutionStatus, len(ws.StepStatus))
	for _, p := range ws.StepStatus {
		completedByStep[p.StepID] = p.Status
	}
	for _, step := range ws.Plan.Steps {
		status := completedByStep[step.ID]
		switch status {
		case workstream.StepComplete:
			out.StepsComplete++
		case workstream.StepFailed:
			out.StepsFailed++
		default:
			out.StepsPending++
			if out.NextStepID == "" {
				out.NextStepID = step.ID
			}
		}
	}
	return out
}

func printInFlight(r *InFlightReport) {
	if len(r.Plans) == 0 {
		fmt.Println("No in-flight plans. `.locutus/workstreams/` is empty.")
		return
	}
	fmt.Printf("%d in-flight plan(s) under %s:\n", len(r.Plans), r.BaseDir)
	for _, p := range r.Plans {
		fmt.Println()
		fmt.Printf("  plan %s  (created %s, last updated %s)\n", p.PlanID, p.CreatedAt, p.UpdatedAt)
		if p.PlanPrompt != "" {
			fmt.Printf("    prompt: %s\n", p.PlanPrompt)
		}
		for _, ws := range p.Workstreams {
			sessionNote := ""
			if ws.AgentSessionID != "" {
				sessionNote = fmt.Sprintf("  session=%s", ws.AgentSessionID)
			}
			preflightNote := ""
			if ws.PreFlightDone {
				preflightNote = "  preflight=done"
			}
			fmt.Printf("    ws %s  agent=%s  steps=%d/%d complete (%d failed, %d pending)%s%s\n",
				ws.WorkstreamID, ws.AgentID,
				ws.StepsComplete, ws.StepTotal,
				ws.StepsFailed, ws.StepsPending,
				sessionNote, preflightNote,
			)
			if ws.NextStepID != "" {
				fmt.Printf("      next step: %s\n", ws.NextStepID)
			}
			if len(ws.ApproachIDs) > 0 {
				fmt.Printf("      approaches: %v\n", ws.ApproachIDs)
			}
		}
	}
	fmt.Println()
	fmt.Println("Note: running `locutus adopt` currently invalidates these and replans.")
	fmt.Println("True per-session resume (DJ-074 proposed) is not yet implemented.")
}
