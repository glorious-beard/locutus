package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/prereqs"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/specio"
)

const updateRepo = "glorious-beard/locutus"

// UpdateCmd refreshes the locutus install. Default behavior checks GitHub
// for a newer binary release and downloads it if found — local project
// files are NOT touched, since the user may have edited
// agents/models.yaml and we shouldn't silently overwrite them.
//
// Three flags compose orthogonally:
//
//   - --reset overwrites the project's scaffolded artifacts
//     (.borg/agents/*.md and .borg/models.yaml) with the running
//     binary's embedded versions. User content (GOALS.md, .borg/spec/,
//     .borg/history/, .borg/manifest.json, .locutus/) is never modified.
//     Workflow topology lives in code and rebuilds with the binary;
//     nothing on disk to refresh for that.
//
//   - --offline skips the GitHub release check and download. Implicitly
//     also skips prereq checks (no LLM calls) unless --check-pre-reqs
//     is supplied explicitly.
//
//   - --check-pre-reqs runs every prereq function (currently:
//     SummariesPresent — fill missing spec summaries via the
//     spec_summarizer agent). Implicit when --offline is not set;
//     opt-in when --offline IS set so the dev compile-and-run loop can
//     still satisfy prereqs without going over the network for the
//     binary check.
//
// Meaningful combinations:
//
//	update                            → binary check + prereqs
//	update --reset                    → binary check + reset + prereqs
//	update --offline                  → friendly no-op message
//	update --offline --reset          → reset only; no network, no prereqs
//	update --offline --check-pre-reqs → prereqs only; no binary check, no reset
//	update --check-pre-reqs           → equivalent to bare `update` for prereqs purposes
type UpdateCmd struct {
	Reset        bool `help:"Overwrite the project's scaffolded agents and models.yaml with the running binary's embedded versions. Local edits to those files will be lost. Defaults to off so casual binary updates don't surprise users with overwritten edits."`
	Offline      bool `help:"Skip the GitHub release check and download. Useful when working without network or paired with --reset to refresh local files from the current binary."`
	CheckPreReqs bool `name:"check-pre-reqs" help:"Run prerequisite checks (fill missing spec summaries, etc.) using LLM calls. Implicit when --offline is not set; opt-in with this flag when --offline IS set so a dev compile-and-run loop can still satisfy prereqs without going over the network for the binary check."`
}

func (c *UpdateCmd) Run(ctx context.Context, cli *CLI) error {
	// Bare --offline (no --reset, no --check-pre-reqs) has nothing to
	// do — be clear about it rather than running a silent no-op the
	// user might mistake for a successful update.
	if c.Offline && !c.Reset && !c.CheckPreReqs {
		fmt.Println("Nothing to do: --offline skips the binary check, --reset is not set, and --check-pre-reqs is not set.")
		fmt.Println("Pair --offline with --reset (refresh local files) or --check-pre-reqs (run prereq checks) — or run plain `update` to do everything.")
		return nil
	}

	// 1. Optional: check + download a newer binary.
	binaryUpdated := false
	if !c.Offline {
		updated, err := c.runBinaryUpdate(ctx)
		if err != nil {
			return err
		}
		binaryUpdated = updated
	}

	// 2. If we just downloaded a new binary, the running process still
	// has the OLD embedded artifacts. Resetting or running prereqs now
	// would use stale embedded scaffolds (the new spec_summarizer might
	// have a different prompt). Bail out and tell the user to re-run
	// with --offline using the new binary.
	if binaryUpdated && (c.Reset || c.CheckPreReqs) {
		fmt.Println("Skipping --reset / --check-pre-reqs: the new binary's embedded artifacts haven't loaded into this process.")
		fmt.Println("Run `locutus update --offline --reset --check-pre-reqs` from your project to refresh scaffolds and run prereqs from the new binary.")
		return nil
	}

	// 3. Optional: refresh scaffolded artifacts from the running
	// binary's embed.FS. Requires a project FS. Run before prereqs so
	// the prereq layer sees the freshest embedded agent definitions
	// (the spec_summarizer prompt may have changed in this binary).
	if c.Reset {
		fsys, _, err := projectFS()
		if err != nil {
			return fmt.Errorf("update --reset: %w", err)
		}
		report, err := scaffold.Reset(fsys)
		if err != nil {
			return fmt.Errorf("update --reset: %w", err)
		}
		printResetReport(report)
	}

	// 4. Run prerequisite checks. Implicit when --offline is not set;
	// opt-in via --check-pre-reqs when --offline is set (the dev
	// compile-and-run loop).
	if c.shouldRunPrereqs() {
		if err := c.runPrereqs(ctx); err != nil {
			return err
		}
	}

	return nil
}

// shouldRunPrereqs implements the flag matrix the design pins down:
//
//	update                         → run
//	update --reset                 → run
//	update --offline               → skip
//	update --offline --reset       → skip
//	update --check-pre-reqs        → run
//	update --offline --check-pre-reqs → run
func (c *UpdateCmd) shouldRunPrereqs() bool {
	return !c.Offline || c.CheckPreReqs
}

// runPrereqs invokes every prereq check in turn with regen=true. New
// prereqs added here as the surface grows; today there's just the
// SummariesPresent check. When the list grows past two or three, the
// hardcoded sequence becomes a slice or config struct.
func (c *UpdateCmd) runPrereqs(ctx context.Context) error {
	fsys, root, err := projectFS()
	if err != nil {
		return fmt.Errorf("update --check-pre-reqs: %w", err)
	}

	sctx, closeFn, err := buildPrereqsContext(fsys, root)
	if err != nil {
		return fmt.Errorf("update --check-pre-reqs: %w", err)
	}
	defer closeFn()

	if err := prereqs.EnsureSpecsContainSummaries(ctx, sctx, true); err != nil {
		var sErr *prereqs.SummariesError
		if errors.As(err, &sErr) {
			// SummariesError carries a friendly message; surface it
			// directly without wrapping noise.
			return fmt.Errorf("prereqs: %s", sErr.Error())
		}
		return fmt.Errorf("prereqs: %w", err)
	}
	fmt.Println("Prereqs satisfied: every spec node has a Summary.")
	return nil
}

// buildPrereqsContext constructs a SummariesContext with an LLM
// executor + dispatcher pair. The dispatcher is registered against the
// project filesystem so the spec_summarizer's spec_list_manifest /
// spec_get tools (DJ-094) bind to the same files the rest of the
// command operates on. Returns a close function the caller defers.
func buildPrereqsContext(fsys specio.FS, root string) (prereqs.SummariesContext, func(), error) {
	llm, rec, err := recordingLLM(fsys, root, "update --check-pre-reqs")
	if err != nil {
		return prereqs.SummariesContext{}, func() {}, err
	}
	closeFn := func() {
		if rec != nil {
			_ = rec.Close()
		}
	}
	return prereqs.SummariesContext{
		FSys:       fsys,
		Executor:   llm,
		Dispatcher: agent.NewDispatcher(llm),
	}, closeFn, nil
}

// runBinaryUpdate runs the GitHub release check and downloads a newer
// binary if available. Returns true when the binary on disk was
// replaced (the running process is still the old version).
func (c *UpdateCmd) runBinaryUpdate(ctx context.Context) (bool, error) {
	if buildVersion == "dev" {
		fmt.Println("Self-update is not available in dev builds. Install a release build to enable.")
		return false, nil
	}

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return false, fmt.Errorf("update: %w", err)
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
	if err != nil {
		return false, fmt.Errorf("update: %w", err)
	}

	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(updateRepo))
	if err != nil {
		return false, fmt.Errorf("update: checking for latest release: %w", err)
	}
	if !found {
		fmt.Println("No releases found.")
		return false, nil
	}

	if latest.LessOrEqual(buildVersion) {
		fmt.Printf("Already up to date (v%s).\n", buildVersion)
		return false, nil
	}

	slog.Info("updating", "from", buildVersion, "to", latest.Version())

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return false, fmt.Errorf("update: locating executable: %w", err)
	}

	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return false, fmt.Errorf("update: applying update: %w", err)
	}

	fmt.Printf("Updated to v%s.\n", latest.Version())
	return true, nil
}

func printResetReport(r *scaffold.ResetReport) {
	if r == nil {
		return
	}
	fmt.Printf("Refreshed %d agent file(s)", len(r.AgentsReset))
	if r.ModelsReset {
		fmt.Print(" and models.yaml")
	}
	fmt.Println(".")
	if n := len(r.AgentsRemoved); n > 0 {
		fmt.Printf("Removed %d orphan agent file(s) (no longer in the embedded scaffold):\n", n)
		for _, p := range r.AgentsRemoved {
			fmt.Printf("  - %s\n", p)
		}
	}
	if len(r.AgentsReset) > 0 || r.ModelsReset || len(r.AgentsRemoved) > 0 {
		fmt.Println("Note: local edits to refreshed files have been overwritten, and orphan agent files have been deleted. User content (GOALS.md, .borg/spec/, .borg/history/, .borg/manifest.json, .locutus/) was not touched.")
	}
}
