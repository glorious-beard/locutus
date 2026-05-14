package acp

import (
	"os/exec"
	"sort"
)

// PreflightResult is one row per known agent — whether its ACP-server binary
// is reachable on $PATH and, when missing, the install hint a user should
// follow to get it.
//
// Shape is deliberately minimal: only the fields the `locutus init` UX
// renders today are present. Adding richer fields (e.g. version probe output)
// without a UX consumer would be aspirational; revisit if version-skew
// surfaces as a real failure mode.
type PreflightResult struct {
	AgentID     string // matches AgentSpawns key
	Binary      string // the Cmd this agent spawns
	Found       bool   // exec.LookPath succeeded
	ResolvedAt  string // absolute path returned by LookPath (when Found)
	InstallHint string // empty when Found; populated install incantation otherwise
}

// installHints maps each agent-id to the install instruction the preflight
// surfaces when its binary is missing. Kept adjacent to AgentSpawns so the
// two stay in sync; an agent without an explicit hint falls back to
// "binary not found on $PATH".
//
// Sourcing notes (Phase 0 of DJ-119 verification, see .claude/plans/acp-migration.md):
//   - claude-code: npm package at @agentclientprotocol/claude-agent-acp. Pinned
//     version 0.33.1 is the Phase 0 known-good build.
//   - codex: GitHub release tarballs only. Critically NOT on crates.io —
//     `cargo install codex-acp` fails. Phase 0 verified against v0.14.0.
//   - gemini: the official Google Gemini CLI ships ACP behind `--acp`.
//     Canonical install per the upstream README is the npm package
//     @google/gemini-cli.
var installHints = map[string]string{
	"claude-code": "npm i -g @agentclientprotocol/claude-agent-acp@0.33.1 (requires Node + npm)",
	"codex":       "download the latest release tarball from https://github.com/zed-industries/codex-acp/releases and place `codex-acp` on $PATH (note: NOT on crates.io — `cargo install codex-acp` does not work)",
	"gemini":      "npm i -g @google/gemini-cli (see https://github.com/google-gemini/gemini-cli for alternatives)",
}

// lookPath is the indirection that lets tests stub binary lookups without
// depending on what's actually installed on the CI host. Production calls
// flow through exec.LookPath.
var lookPath = exec.LookPath

// Preflight checks every agent declared in AgentSpawns and returns one
// PreflightResult per agent, sorted by AgentID for stable output. The
// check is read-only — it does not invoke the binary, only resolves it
// on $PATH.
//
// This is a check-and-report function, not check-and-fail: callers
// (specifically `locutus init`) inspect the results and decide whether
// to surface a warning, an error, or silent success. The init UX prints
// found vs missing and lets the user proceed regardless; the user can
// install the missing agents at their leisure and re-run any verb that
// needs them.
func Preflight() []PreflightResult {
	ids := make([]string, 0, len(AgentSpawns))
	for id := range AgentSpawns {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]PreflightResult, 0, len(ids))
	for _, id := range ids {
		spawn := AgentSpawns[id]
		res := PreflightResult{AgentID: id, Binary: spawn.Cmd}
		if path, err := lookPath(spawn.Cmd); err == nil {
			res.Found = true
			res.ResolvedAt = path
		} else {
			res.InstallHint = installHints[id]
		}
		out = append(out, res)
	}
	return out
}

// MissingCount returns the number of agents whose binaries were not found.
// Used by callers that want a single-int summary for exit-code logic or UX
// gating without re-scanning the slice.
func MissingCount(results []PreflightResult) int {
	n := 0
	for _, r := range results {
		if !r.Found {
			n++
		}
	}
	return n
}
