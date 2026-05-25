// Package activity implements DJ-135's activity registry: a map from
// activity name (spec_refinement, planning, implementation, etc.) to
// a prioritized list of coding-agent runtimes capable of executing
// that activity. Locutus's CLI verbs and MCP prompts both resolve
// against this registry to pick a runtime at dispatch time.
//
// An activity is "what we're trying to do" (deliberate on the spec
// graph, plan a feature decomposition, implement an approach). A
// runtime is "which coding agent we send the work to" (Claude Code,
// Codex, Gemini CLI). The registry decouples the two: a project that
// prefers Codex for planning but Claude Code for everything else
// overrides per-activity in .borg/agents.yaml without touching CLI
// or prompt code.
//
// Lifecycle: NewRegistry loads the embedded default, then layers any
// .borg/agents.yaml from the project root on top. Activities defined
// in both sources merge with project override winning per-key
// (runtime list replacement, not concatenation — partial overrides
// are explicit). The merged registry is read-only at dispatch time.
package activity

import (
	"fmt"
	"sort"

	_ "embed"

	"github.com/glorious-beard/locutus/internal/dispatch/acp"
	"github.com/glorious-beard/locutus/internal/specio"
)

// defaultAgentsYAML is the embedded shipping default. Lives next to
// the loader so the embed path is stable as the package moves; the
// content is the same YAML committed at internal/activity/agents-default.yaml.
//
//go:embed agents-default.yaml
var defaultAgentsYAML []byte

// projectOverridePath is the well-known per-project override location.
// Constant so callers can show it in operator-facing messages
// ("override at .borg/agents.yaml") without string drift.
const projectOverridePath = ".borg/agents.yaml"

// Activity describes one named unit of coding work and the runtimes
// capable of executing it, ordered by preference.
//
// Per resolved-question 3, the registry is activity-driven: routing
// by capability rather than by per-call configuration. The same
// activity dispatches to different runtimes on different machines
// based purely on what's installed and what the project overrides
// prefer.
type Activity struct {
	// Name is the activity id (snake_case; matches MCP prompt names
	// in Phase 5). Stable across releases — used as a map key in
	// agents.yaml and as the lookup key here.
	Name string

	// Runtimes is the project-preferred runtime list, ordered
	// most-preferred first. Each entry must be a key registered in
	// internal/dispatch/acp/registry.go AgentSpawns. The Resolve
	// helper walks this list in order and returns the first runtime
	// whose ACP-server binary is detectable on $PATH.
	Runtimes []string
}

// Registry is the merged activity → preference table. Constructed
// via NewRegistry; treat the returned value as read-only.
type Registry struct {
	activities map[string]Activity
}

// NewRegistry layers the per-project .borg/agents.yaml override (if
// present) on top of the embedded default. fsys is rooted at the
// project; pass specio.NewOSFS(projectRoot) in production, MemFS in
// tests. A nil fsys loads only the defaults — useful for tests that
// don't care about overrides.
//
// Override merging: any activity defined in the project file
// replaces the default entirely for that activity name. There is no
// partial override at the per-runtime level; an override that lists
// `[gemini]` discards the default's full runtime list and ships with
// only `[gemini]`. Explicit is better than implicit when the user
// is overriding routing.
func NewRegistry(fsys specio.FS) (*Registry, error) {
	defaults, err := parseAgentsYAML(defaultAgentsYAML)
	if err != nil {
		return nil, fmt.Errorf("activity: parse embedded default: %w", err)
	}

	merged := map[string]Activity{}
	for name, a := range defaults {
		merged[name] = a
	}

	if fsys != nil {
		override, err := loadProjectOverride(fsys)
		if err != nil {
			return nil, err
		}
		for name, a := range override {
			merged[name] = a
		}
	}

	if err := validateRegistry(merged); err != nil {
		return nil, err
	}
	return &Registry{activities: merged}, nil
}

// Lookup returns the activity with the given name, or false when
// unknown. Names are case-sensitive; the convention is snake_case
// matching the MCP prompt naming Phase 5 introduces.
func (r *Registry) Lookup(name string) (Activity, bool) {
	a, ok := r.activities[name]
	return a, ok
}

// Names returns every registered activity name in lexical order.
// Used by diagnostic surfaces (and the bootstrap-time validation
// pass) — not load-bearing for dispatch.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.activities))
	for name := range r.activities {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// validateRegistry rejects entries that reference unknown runtimes.
// Catches typos in agents.yaml before dispatch time — a runtime
// listed in agents.yaml but missing from acp.AgentSpawns means we
// could never spawn it anyway.
func validateRegistry(activities map[string]Activity) error {
	for name, a := range activities {
		if len(a.Runtimes) == 0 {
			return fmt.Errorf("activity %q: runtimes list is empty", name)
		}
		for _, rt := range a.Runtimes {
			if _, ok := acp.AgentSpawns[rt]; !ok {
				return fmt.Errorf("activity %q: runtime %q is not registered in acp.AgentSpawns", name, rt)
			}
		}
	}
	return nil
}
