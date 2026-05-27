package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
)

// ResolvePlaybook reads the playbook for activityName from base,
// preferring the runtime-specific overlay <activity>.<runtime>.md
// when present and falling back to the canonical <activity>.md.
//
// dir is the directory within base where playbooks live — "plans"
// for the embedded scaffold, ".borg/plans" for a project copy.
// runtime is the resolved runtime id (claude-code / codex / gemini)
// from internal/dispatch/acp.AgentSpawns. An empty runtime skips
// the overlay attempt and reads the default directly.
//
// Returns (body, sourcePath, err). sourcePath is the path actually
// read — useful for operator-facing messages that want to show
// which file produced the playbook. On a missing default the error
// mentions the canonical path so operators can author or restore it.
//
// Per DJ-136 resolved-question 3 the override is total: overlay
// presence replaces the default in full for that runtime, not a
// patch / partial-include.
func ResolvePlaybook(base fs.FS, dir, activityName, runtime string) ([]byte, string, error) {
	if runtime != "" {
		overlayPath := dir + "/" + activityName + "." + runtime + ".md"
		data, err := fs.ReadFile(base, overlayPath)
		if err == nil {
			return data, overlayPath, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, overlayPath, fmt.Errorf("read overlay %s: %w", overlayPath, err)
		}
	}
	defaultPath := dir + "/" + activityName + ".md"
	data, err := fs.ReadFile(base, defaultPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, defaultPath, fmt.Errorf("playbook %s missing — run `locutus update --offline --reset` to restore", defaultPath)
		}
		return nil, defaultPath, fmt.Errorf("read %s: %w", defaultPath, err)
	}
	return data, defaultPath, nil
}

// EmbeddedPlansFS returns the embedded scaffold plans FS so
// out-of-package tests (and any future readers) can walk the
// canonical playbook set without re-embedding the same files.
//
// The returned FS is rooted such that paths are "plans/<file>"
// (matching the embed directive in scaffold.go). Callers that want
// a "plans/"-stripped view should pass dir="plans" to ResolvePlaybook.
func EmbeddedPlansFS() fs.FS {
	return plansFS
}
