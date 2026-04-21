package cmd

import (
	"context"
	"fmt"
	"log/slog"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

const updateRepo = "glorious-beard/locutus"

// UpdateCmd self-updates to the latest release.
type UpdateCmd struct{}

func (c *UpdateCmd) Run(cli *CLI) error {
	if buildVersion == "dev" {
		fmt.Println("Self-update is not available in dev builds. Install a release build to enable.")
		return nil
	}

	ctx := context.Background()

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(updateRepo))
	if err != nil {
		return fmt.Errorf("update: checking for latest release: %w", err)
	}
	if !found {
		fmt.Println("No releases found.")
		return nil
	}

	if latest.LessOrEqual(buildVersion) {
		fmt.Printf("Already up to date (v%s).\n", buildVersion)
		return nil
	}

	slog.Info("updating", "from", buildVersion, "to", latest.Version())

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("update: locating executable: %w", err)
	}

	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return fmt.Errorf("update: applying update: %w", err)
	}

	fmt.Printf("Updated to v%s.\n", latest.Version())
	return nil
}
