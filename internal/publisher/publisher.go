package publisher

import (
	"fmt"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/specio"
)

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
