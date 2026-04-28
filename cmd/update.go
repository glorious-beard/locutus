package cmd

import (
	"context"
	"fmt"
	"log/slog"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/chetan/locutus/internal/scaffold"
)

const updateRepo = "glorious-beard/locutus"

// UpdateCmd refreshes the locutus install. Default behavior checks GitHub
// for a newer binary release and downloads it if found — local project
// files are NOT touched, since the user may have edited
// agents/workflows/models.yaml and we shouldn't silently overwrite them.
//
// Two flags compose orthogonally:
//
//   - --reset overwrites the project's scaffolded artifacts
//     (.borg/agents/*.md, .borg/workflows/*.yaml, .borg/models.yaml)
//     with the running binary's embedded versions. User content
//     (GOALS.md, .borg/spec/, .borg/history/, .borg/manifest.json,
//     .locutus/) is never modified.
//
//   - --offline skips the GitHub release check and download.
//
// The four meaningful combinations:
//
//	update                  → check, download newer binary if available
//	update --reset          → check, download if newer (defers reset to
//	                           a follow-up run because the new binary's
//	                           embedded artifacts haven't loaded yet);
//	                           else reset using the current binary
//	update --offline        → no-op with a friendly message
//	update --offline --reset → reset only; no network
type UpdateCmd struct {
	Reset   bool `help:"Overwrite the project's scaffolded agents, workflows, and models.yaml with the running binary's embedded versions. Local edits to those files will be lost. Defaults to off so casual binary updates don't surprise users with overwritten edits."`
	Offline bool `help:"Skip the GitHub release check and download. Useful when working without network or paired with --reset to refresh local files from the current binary."`
}

func (c *UpdateCmd) Run(ctx context.Context, cli *CLI) error {
	// Bare --offline (no --reset) has nothing to do — be clear about
	// it rather than running a silent no-op the user might mistake for
	// a successful update.
	if c.Offline && !c.Reset {
		fmt.Println("Nothing to do: --offline skips the binary check, and --reset is not set.")
		fmt.Println("Pair --offline with --reset to refresh local files only, or run plain `update` to check for a new binary.")
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

	// 2. If --reset wasn't asked for, we're done.
	if !c.Reset {
		return nil
	}

	// 3. If we just downloaded a new binary, the running process still
	// has the OLD embedded artifacts. Resetting now would write the
	// old versions back to disk. Bail out and tell the user to re-run
	// with --offline --reset using the new binary.
	if binaryUpdated {
		fmt.Println("Skipping --reset: the new binary's embedded artifacts haven't loaded into this process.")
		fmt.Println("Run `locutus update --offline --reset` from your project to refresh agents/workflows/models.yaml from the new binary.")
		return nil
	}

	// 4. Refresh scaffolded artifacts from the running binary's
	// embed.FS. Requires a project FS — `--reset` outside a project
	// is an error.
	fsys, _, err := projectFS()
	if err != nil {
		return fmt.Errorf("update --reset: %w", err)
	}
	report, err := scaffold.Reset(fsys)
	if err != nil {
		return fmt.Errorf("update --reset: %w", err)
	}
	printResetReport(report)
	return nil
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
	fmt.Printf("Refreshed %d agent file(s), %d workflow file(s)",
		len(r.AgentsReset), len(r.WorkflowsReset))
	if r.ModelsReset {
		fmt.Print(", and models.yaml")
	}
	fmt.Println(".")
	if len(r.AgentsReset)+len(r.WorkflowsReset) > 0 {
		fmt.Println("Note: any local edits to those files have been overwritten. User content (GOALS.md, .borg/spec/, .borg/history/, .borg/manifest.json, .locutus/) was not touched.")
	}
}
