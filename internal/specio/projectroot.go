package specio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ProjectRootMarker is the file whose presence identifies a directory as
// a Locutus-managed project. Written by `locutus init` and persistent for
// the project's lifetime.
const ProjectRootMarker = ".borg/manifest.json"

// ErrNotInProject is returned by FindProjectRoot when no ancestor of the
// starting directory contains the project root marker.
var ErrNotInProject = errors.New("not inside a Locutus project (no .borg/manifest.json found in any parent directory)")

// FindProjectRoot walks up from startDir looking for a directory that
// contains the project root marker (.borg/manifest.json). Returns the
// absolute path of the first match. If the walk reaches the filesystem
// root without finding a marker, returns ErrNotInProject so callers can
// surface a friendly "are you in a Locutus project?" message.
//
// startDir may be relative; it is resolved against the process's current
// working directory before the walk begins.
func FindProjectRoot(startDir string) (string, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("resolve start dir: %w", err)
	}
	for {
		candidate := filepath.Join(abs, ProjectRootMarker)
		if _, err := os.Stat(candidate); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", ErrNotInProject
		}
		abs = parent
	}
}

// FindProjectRootFromCwd is a convenience wrapper around FindProjectRoot
// that uses the process working directory as the starting point.
func FindProjectRootFromCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return FindProjectRoot(cwd)
}
