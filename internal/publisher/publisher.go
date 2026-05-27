package publisher

import (
	"fmt"
	"os"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/specio"
)

// osExecutable aliases os.Executable so tests can stub the
// `executable` package-level seam without depending on the stdlib
// symbol directly.
var osExecutable = os.Executable

// RuntimePublisher is the per-runtime emission contract. Implementations
// know the runtime's expected directory layout, frontmatter shape, and
// MCP-server config location. Phase 4 ships three: Claude Code, Codex,
// Gemini.
//
// projectFS is rooted at the project root and writes go OUTSIDE .borg/
// (to .claude/, .codex/, .gemini/, .mcp.json, etc.) — the publisher is
// the only Locutus surface that writes runtime-private files. Tests
// inspect the resulting layout to confirm the file contracts.
type RuntimePublisher interface {
	// Name returns the runtime id (matches activity.Registry runtime
	// preferences — claude-code, codex, gemini).
	Name() string
	// EnsureMCPConfig writes the runtime's MCP-server descriptor so
	// published subagents can call back into Locutus. Idempotent —
	// the publisher runs on every locutus update --reset, and the
	// config must converge to the same file regardless of how many
	// times it's run.
	EnsureMCPConfig(projectFS specio.FS) error
	// PublishAgent writes one canonical agent as a runtime-shaped
	// subagent. Overwrites the prior emitted file (re-emission is
	// the operational model — locutus update --reset wipes and
	// rewrites).
	PublishAgent(agent CanonicalAgent, projectFS specio.FS) error
	// PublishActivity writes one canonical activity as a runtime-
	// shaped slash command. Skipped at the orchestrator layer when
	// HasPlan is false (no plan body, no slash command).
	PublishActivity(act CanonicalActivity, projectFS specio.FS) error
	// EnsureHooks writes any mechanical-enforcement hooks the
	// runtime supports for the registered activity set (DJ-136
	// phase 5+). Codex and Gemini emit fragments here; Claude Code
	// is a no-op because DJ-136 uses /goal for enforcement rather
	// than hooks on that runtime. Called once per Publish() after
	// per-activity slash commands have landed.
	EnsureHooks(activities []CanonicalActivity, projectFS specio.FS) error
}

// publishers is the runtime-id → implementation registry. Populated
// at package init by each runtime file (claudecode.go, codex.go,
// gemini.go) so adding a runtime means dropping a file in this
// package, not threading registration through Publish().
var publishers = map[string]RuntimePublisher{}

func registerRuntime(p RuntimePublisher) {
	publishers[p.Name()] = p
}

// Publish runs every runtime mentioned in any activity's runtimes
// preference list. For each runtime: ensures the MCP config, walks
// every canonical agent, and walks every activity whose plan file
// exists.
//
// The fsys argument is rooted at the project root. Phase 4's design
// keeps reads (.borg/) and writes (.claude/, .codex/, etc.) on the
// same FS rather than threading two; the only writes that escape
// .borg/ are the publisher's outputs.
func Publish(fsys specio.FS, reg *activity.Registry) error {
	canonical, err := LoadCanonical(fsys, reg)
	if err != nil {
		return err
	}
	for _, rt := range canonical.Runtimes {
		pub, ok := publishers[rt]
		if !ok {
			return fmt.Errorf("publisher: no implementation registered for runtime %q (known: %v)", rt, registeredRuntimes())
		}
		if err := pub.EnsureMCPConfig(fsys); err != nil {
			return fmt.Errorf("publisher %s: mcp config: %w", rt, err)
		}
		for _, agent := range canonical.Agents {
			if err := pub.PublishAgent(agent, fsys); err != nil {
				return fmt.Errorf("publisher %s: agent %s: %w", rt, agent.ID, err)
			}
		}
		for _, act := range canonical.Activities {
			if !act.HasPlan {
				continue
			}
			if err := pub.PublishActivity(act, fsys); err != nil {
				return fmt.Errorf("publisher %s: activity %s: %w", rt, act.Name, err)
			}
		}
		if err := pub.EnsureHooks(canonical.Activities, fsys); err != nil {
			return fmt.Errorf("publisher %s: hooks: %w", rt, err)
		}
	}
	return nil
}

func registeredRuntimes() []string {
	names := make([]string, 0, len(publishers))
	for n := range publishers {
		names = append(names, n)
	}
	return names
}

// deriveDescription synthesizes a one-line description for the
// published subagent when the canonical agent doesn't carry a
// dedicated description field. Used by every per-runtime publisher,
// so it lives in the shared package rather than being duplicated
// three times.
//
// Heuristic: "<id> — Locutus <role> agent" when role is set, else
// "<id> — Locutus agent". Stable across releases so subagent UIs
// (Claude Code's `/agents` listing) don't churn their descriptions
// on each republish.
func deriveDescription(agent CanonicalAgent) string {
	if agent.Role != "" {
		return fmt.Sprintf("%s — Locutus %s agent", agent.ID, agent.Role)
	}
	return fmt.Sprintf("%s — Locutus agent", agent.ID)
}

// locutusCommand returns the absolute path of the running locutus
// binary, used by every per-runtime publisher when emitting the
// runtime's MCP-server config. Coding-agent runtimes (Claude Code's
// .mcp.json launcher, Codex's mcp_servers table, Gemini's extension
// manifest) spawn the named command as a subprocess; using a bare
// "locutus" string requires the user to install the binary on $PATH
// globally, which is fragile when the user runs a project-local
// `./locutus` build. os.Executable returns the path of the binary
// that's currently running — typically the one the user invoked
// init/update with — so .mcp.json gets a stable absolute path that
// works regardless of $PATH state.
//
// Operators who upgrade locutus by replacing the binary in place
// keep the same .mcp.json path. Operators who switch from a project-
// local build to a globally-installed one (or vice versa) need to
// re-run `locutus update --reset` from the new binary so the per-
// runtime configs pick up the new path. Documented in
// docs/activities.md under "Lifecycle."
//
// Falls back to "locutus" (bare name; PATH lookup) when
// os.Executable fails — a defensive case that shouldn't fire on
// any supported platform, but failing the publish is worse than
// emitting a config that requires PATH.
var locutusCommand = func() string {
	path, err := executable()
	if err != nil {
		return "locutus"
	}
	return path
}

// executable is the package-level seam over os.Executable. Tests
// stub it to avoid coupling the emitted MCP config to wherever the
// test binary lives.
var executable = func() (string, error) {
	return osExecutable()
}
