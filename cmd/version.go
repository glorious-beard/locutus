package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

// VersionCmd prints the current version.
type VersionCmd struct {
	Version string `kong:"-"`
}

type versionOutput struct {
	Version         string `json:"version"`
	UpdateAvailable string `json:"update_available,omitempty"`
}

func (c *VersionCmd) Run(cli *CLI) error {
	// Fire off a non-blocking update check while we print the version.
	type updateResult struct {
		version string
	}
	ch := make(chan updateResult, 1)

	if c.Version != "dev" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
			if err != nil {
				ch <- updateResult{}
				return
			}
			updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
			if err != nil {
				ch <- updateResult{}
				return
			}
			latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(updateRepo))
			if err != nil || !found || latest.LessOrEqual(c.Version) {
				ch <- updateResult{}
				return
			}
			ch <- updateResult{version: latest.Version()}
		}()
	} else {
		ch <- updateResult{}
	}

	// Print version immediately.
	if cli.JSON {
		out := versionOutput{Version: c.Version}
		// Wait for update check (bounded by 500ms timeout).
		if res := <-ch; res.version != "" {
			out.UpdateAvailable = res.version
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}

	fmt.Printf("locutus %s\n", c.Version)

	// Wait for update check (bounded by 500ms timeout).
	if res := <-ch; res.version != "" {
		fmt.Fprintf(os.Stderr, "Update available: v%s → v%s (run locutus update)\n", c.Version, res.version)
	}

	return nil
}
