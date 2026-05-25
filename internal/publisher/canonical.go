// Package publisher implements DJ-135 phase 4: reads the canonical
// agent + activity-plan content from .borg/ and emits per-runtime
// copies in the format each coding agent expects (Claude Code,
// Codex, Gemini CLI). Copies are tailored per resolved-question 6:
// no symlinks, no shared source-of-truth at the runtime layer.
//
// Per resolved-question 7, namespacing uses subdirectories where the
// runtime supports them (Claude Code → .claude/agents/locutus/<name>.md)
// and filename prefixes elsewhere (Codex → .codex/agents/locutus-<name>.toml,
// Gemini → .gemini/extensions/locutus/agents/locutus-<name>.md).
//
// Per resolved-question 4 the MCP server is a per-project singleton
// reached over a Unix socket; the publisher injects each runtime's
// MCP-server config so published subagents know to call back into
// `locutus mcp` (which the Phase 1 bridge routes to the daemon).
//
// What Phase 4 explicitly does NOT do:
//
//   - Verify the runtime actually accepts the emitted file shape.
//     The Codex TOML schema and Gemini extension schema are subject
//     to upstream churn; the formats here match what the plan
//     specifies and what was documented at design time. Phase 5's
//     empirical verification (running `locutus refine goals` against
//     a real Claude Code session) is the first end-to-end check;
//     adjustments to the per-runtime formats land alongside that
//     verification, not in Phase 4.
package publisher

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/frontmatter"
	"github.com/glorious-beard/locutus/internal/specio"
)

// CanonicalAgent is one Locutus agent loaded from .borg/agents/<id>.md.
// Frontmatter carries the Locutus-internal schema (id, role, models,
// thinking, etc.); per-runtime publishers translate the subset each
// runtime cares about into its own frontmatter shape.
type CanonicalAgent struct {
	// ID is the agent's hyphenated identifier (DJ-135 phase 2),
	// matching the file's basename sans .md extension.
	ID string
	// Role is the short "what does this agent do" label from the
	// canonical frontmatter (e.g. "survey", "planning", "critique").
	// Per-runtime publishers use it to derive a one-line description
	// when the canonical doesn't carry a dedicated description field.
	Role string
	// Frontmatter is the raw decoded YAML map. Reserved for future
	// per-runtime translation logic — Phase 4 only reads ID + Role.
	Frontmatter map[string]any
	// Body is the prompt content (everything after the frontmatter
	// delimiters). Published verbatim as the subagent's system
	// prompt.
	Body string
}

// CanonicalActivity is one activity entry from the registry, plus
// the matching plan file body if one exists at .borg/plans/<name>.md.
// HasPlan distinguishes "no slash command yet" (activity registered
// but plan not authored) from "plan file unreadable" (which surfaces
// as an error at load time).
type CanonicalActivity struct {
	Name     string
	Runtimes []string
	PlanBody string
	HasPlan  bool
}

// Canonical bundles the inputs for one publisher run: the agent set
// loaded from disk, the activity-plan set from the registry, and the
// distinct list of runtimes mentioned across all activities.
type Canonical struct {
	Agents     []CanonicalAgent
	Activities []CanonicalActivity
	Runtimes   []string
}

// LoadCanonical walks .borg/agents/*.md, parses every file's
// frontmatter, and pairs the activity registry against .borg/plans/.
// Returns an error when an agent file is malformed (better to surface
// at startup than at publish time) but treats a missing .borg/plans/
// directory as "no plans yet" — Phase 5 lands those.
func LoadCanonical(fsys specio.FS, reg *activity.Registry) (Canonical, error) {
	if fsys == nil {
		return Canonical{}, fmt.Errorf("publisher: fsys is required")
	}
	if reg == nil {
		return Canonical{}, fmt.Errorf("publisher: registry is required")
	}

	agents, err := loadAgents(fsys)
	if err != nil {
		return Canonical{}, err
	}

	activities := make([]CanonicalActivity, 0, len(reg.Names()))
	runtimes := map[string]struct{}{}
	for _, name := range reg.Names() {
		act, _ := reg.Lookup(name)
		canonical := CanonicalActivity{Name: name, Runtimes: act.Runtimes}
		body, ok, err := loadPlan(fsys, name)
		if err != nil {
			return Canonical{}, err
		}
		canonical.PlanBody = body
		canonical.HasPlan = ok
		activities = append(activities, canonical)
		for _, rt := range act.Runtimes {
			runtimes[rt] = struct{}{}
		}
	}

	rtList := make([]string, 0, len(runtimes))
	for rt := range runtimes {
		rtList = append(rtList, rt)
	}
	sort.Strings(rtList)

	return Canonical{Agents: agents, Activities: activities, Runtimes: rtList}, nil
}

// loadAgents reads every .borg/agents/*.md file, parses frontmatter,
// and returns sorted-by-id CanonicalAgent entries. Files lacking an
// id field are rejected — the publisher can't emit a runtime file
// without a name.
func loadAgents(fsys specio.FS) ([]CanonicalAgent, error) {
	const dir = ".borg/agents"
	paths, err := fsys.ListDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("publisher: list %s: %w", dir, err)
	}

	type rawFrontmatter struct {
		ID   string `yaml:"id"`
		Role string `yaml:"role"`
	}

	// ListDir returns full paths (matching the OSFS / MemFS contract);
	// no need to re-prepend the dir.
	out := make([]CanonicalAgent, 0, len(paths))
	for _, path := range paths {
		if !strings.HasSuffix(path, ".md") {
			continue
		}
		data, err := fsys.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("publisher: read %s: %w", path, err)
		}
		var fm rawFrontmatter
		body, err := frontmatter.Parse(data, &fm)
		if err != nil {
			return nil, fmt.Errorf("publisher: parse %s frontmatter: %w", path, err)
		}
		if fm.ID == "" {
			return nil, fmt.Errorf("publisher: %s missing required id: field", path)
		}
		out = append(out, CanonicalAgent{
			ID:   fm.ID,
			Role: fm.Role,
			Body: body,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// loadPlan tries to read .borg/plans/<name>.md. Returns (body, true,
// nil) when present; (zero-value, false, nil) when missing (not an
// error — Phase 5 fills these); (zero-value, false, err) on actual
// read failures.
func loadPlan(fsys specio.FS, activityName string) (string, bool, error) {
	path := ".borg/plans/" + activityName + ".md"
	data, err := fsys.ReadFile(path)
	if err != nil {
		if isNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("publisher: read %s: %w", path, err)
	}
	return string(data), true, nil
}

// isNotExist matches both fs.ErrNotExist (OSFS via *PathError) and
// the MemFS "file does not exist" sentinel that doesn't unwrap to
// the stdlib sentinel.
func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return strings.Contains(err.Error(), "file does not exist")
}
