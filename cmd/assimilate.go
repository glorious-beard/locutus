package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/remediate"
	"github.com/chetan/locutus/internal/specio"
)

// AssimilateCmd analyzes an existing codebase and produces a spec graph.
//
// Default behavior is to run remediation (DJ-045 + DJ-046) after the
// inference pass — the gap_analyst's findings are converted into
// assumed Decisions, new Strategies, and updated Features in a single
// atomic write. `--no-remediate` opts out: the report still includes
// the Gaps list but no remediation writes happen.
type AssimilateCmd struct {
	DryRun      bool `help:"Run the pipeline but do not write spec files."`
	NoRemediate bool `help:"Skip the remediation pass; report gaps but do not auto-fill them with assumed Decisions/Strategies/Features."`
}

func (c *AssimilateCmd) Run(ctx context.Context, cli *CLI) error {
	llm, err := getLLM()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	// Dry-run: wrap fsys so writes are discarded while reads still hit the
	// real filesystem. The pipeline runs to completion and we report what it
	// would have written.
	effective := specio.FS(fsys)
	if c.DryRun {
		effective = newReadOnlyFS(fsys)
	}

	result, err := RunAssimilate(ctx, llm, effective, !c.NoRemediate)
	if err != nil {
		return err
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	verb := "Assimilation complete"
	if c.DryRun {
		verb = "Assimilation preview (dry-run)"
	}
	fmt.Printf("%s: %d decisions, %d entities, %d features, %d gaps.\n",
		verb, len(result.Decisions), len(result.Entities), len(result.Features), len(result.Gaps))
	return nil
}

// RunAssimilate executes the assimilation analysis pipeline: walks the
// file inventory, loads the current spec snapshot so the LLM can
// distinguish new from existing nodes, delegates inference to
// agent.Analyze, optionally runs the remediation pass to fill gaps
// autonomously (DJ-045 + DJ-046), and finally writes the merged spec
// back to `.borg/spec/` via a per-file atomic pass. A crash-safety
// sentinel at `.borg/spec/.assimilating` is present for the duration
// of the write loop so a crashed prior run is visible to the next
// invocation.
//
// Dry-run is handled by the caller wrapping fsys with readOnlyFS — all
// writes (including the sentinel) are silently dropped by the wrapper,
// which preserves the preview semantics without extra branching here.
//
// remediate=true runs the gap-filling pass; remediate=false leaves
// inference output untouched and reports gaps without acting on them.
func RunAssimilate(ctx context.Context, llm agent.LLM, fsys specio.FS, runRemediate bool) (*agent.AssimilationResult, error) {
	inventory, err := agent.WalkInventory(fsys)
	if err != nil {
		return nil, fmt.Errorf("walking inventory: %w", err)
	}

	existing := loadExistingSpec(fsys)
	req := agent.AssimilationRequest{
		Inventory:    inventory,
		ExistingSpec: existing,
	}
	result, err := agent.Analyze(ctx, llm, fsys, req)
	if err != nil {
		return nil, fmt.Errorf("assimilation analysis: %w", err)
	}

	if runRemediate && len(result.Gaps) > 0 {
		remResult, err := remediate.Remediate(ctx, llm, result.Gaps, existing)
		if err != nil {
			return result, fmt.Errorf("assimilation remediate: %w", err)
		}
		remediate.ApplyToAssimilation(remResult.Plan, result, existing)
		recordRemediationRun(fsys, len(result.Gaps), remResult)
	}

	if err := persistAssimilationResult(fsys, result); err != nil {
		return result, fmt.Errorf("persist assimilation result: %w", err)
	}

	return result, nil
}

// recordRemediationRun emits a single history event summarising one
// remediation pass. Errors are swallowed: history is best-effort and a
// readOnlyFS wrapper (dry-run) silently drops the write.
func recordRemediationRun(fsys specio.FS, gapCount int, r *remediate.Result) {
	if r == nil {
		return
	}
	hist := history.NewHistorian(fsys, ".borg/history")
	now := time.Now()
	_ = hist.Record(history.Event{
		ID:        fmt.Sprintf("evt-remediation-%d", now.UnixNano()),
		Timestamp: now,
		Kind:      "remediation_run",
		Rationale: fmt.Sprintf("Remediated %d gap(s): %d decisions, %d strategies, %d features created; %d features updated.",
			gapCount, r.DecisionsCreated, r.StrategiesCreated, r.FeaturesCreated, r.FeaturesUpdated),
	})
}
