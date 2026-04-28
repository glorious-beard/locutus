package cmd

import (
	"errors"
	"fmt"

	"github.com/chetan/locutus/internal/specio"
)

// projectFS returns an OSFS rooted at the current Locutus project's root
// (the nearest ancestor containing .borg/manifest.json). Returns a
// friendly error pointing at `locutus init` when the user is outside any
// project.
//
// Use this from every subcommand that operates on an existing spec —
// `init` is the lone exception (it bootstraps cwd into a project).
func projectFS() (specio.FS, string, error) {
	root, err := specio.FindProjectRootFromCwd()
	if err != nil {
		if errors.Is(err, specio.ErrNotInProject) {
			return nil, "", fmt.Errorf("%w — run `locutus init` here, or cd into an existing project", err)
		}
		return nil, "", err
	}
	return specio.NewOSFS(root), root, nil
}
