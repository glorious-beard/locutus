package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
)

// Playbook resolution modes (DJ-140). The mode names which prompt
// body to select; loop-driving is orthogonal (the harness always
// outer-loops whatever it dispatches).
//
//   - ModeHeadless is the mode dispatched over ACP. It is the
//     default/fallback mode and has NO dedicated files today — a
//     headless request resolves via the provider overlay or the
//     canonical default, exactly like the pre-DJ-140 two-tier logic.
//   - ModeInteractive is the mode published as a slash command. It
//     activates the mode-specific tiers (<activity>.<runtime>.<mode>.md
//     and <activity>.<mode>.md) so an interactive overlay can replace
//     the headless body when published.
const (
	ModeHeadless    = "headless"
	ModeInteractive = "interactive"
)

// ResolvePlaybook reads the playbook for activityName from base,
// resolving the file naming <activity>[.<provider>][.<mode>].md with
// 4-tier specificity-descending precedence (DJ-140):
//
//	Tier 1: <dir>/<activity>.<runtime>.<mode>.md  (provider + mode)
//	Tier 2: <dir>/<activity>.<runtime>.md         (provider)
//	Tier 3: <dir>/<activity>.<mode>.md            (mode)
//	Tier 4: <dir>/<activity>.md                   (default; always)
//
// Provider outranks mode at equal specificity: tier 2 wins over tier 3.
// Headless is the default mode and has no dedicated tier — when
// mode == ModeHeadless (or mode == ""), tiers 1 and 3 are skipped, so
// headless resolution collapses to the pre-DJ-140 two-tier walk
// (provider overlay → default). This is what keeps the renamed
// interactive overlay (a later phase) from leaking into headless
// dispatch. An empty runtime skips the provider tiers (1 and 2).
//
// dir is the directory within base where playbooks live — "plans"
// for the embedded scaffold, ".borg/plans" for a project copy.
// runtime is the resolved runtime id (claude-code / codex / gemini)
// from internal/dispatch/acp.AgentSpawns.
//
// mode determines publish-vs-dispatch selection: the ACP dispatch path
// always passes ModeHeadless; the publisher passes ModeInteractive when
// emitting slash commands (DJ-140).
//
// The walk returns the first candidate that exists. A fs.ErrNotExist on
// a tier means "try the next tier"; any other read error returns
// immediately with a wrapped error naming that path. If no tier exists,
// the error names the canonical (tier-4) default path so operators can
// author or restore it.
//
// Returns (body, sourcePath, err). sourcePath is the path actually
// read — useful for operator-facing messages that want to show which
// file produced the playbook.
//
// Per DJ-136 resolved-question 3 the override is total: a matching
// overlay replaces the default in full, not a patch / partial-include.
func ResolvePlaybook(base fs.FS, dir, activityName, runtime, mode string) ([]byte, string, error) {
	modeActive := mode != "" && mode != ModeHeadless
	defaultPath := dir + "/" + activityName + ".md"

	var candidates []string
	if runtime != "" && modeActive {
		candidates = append(candidates, dir+"/"+activityName+"."+runtime+"."+mode+".md")
	}
	if runtime != "" {
		candidates = append(candidates, dir+"/"+activityName+"."+runtime+".md")
	}
	if modeActive {
		candidates = append(candidates, dir+"/"+activityName+"."+mode+".md")
	}
	candidates = append(candidates, defaultPath)

	for _, path := range candidates {
		data, err := fs.ReadFile(base, path)
		if err == nil {
			return data, path, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, path, fmt.Errorf("read %s: %w", path, err)
		}
	}

	return nil, defaultPath, fmt.Errorf("playbook %s missing — run `locutus update --offline --reset` to restore", defaultPath)
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
