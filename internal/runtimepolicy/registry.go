// Package runtimepolicy implements DJ-144 §9's per-runtime minimum-
// version registry: a map from runtime id (claude-code / codex /
// gemini) to the lowest runtime version Locutus relies on for a
// version-gated feature. Below the floor, callers log a warn-and-
// proceed message; the registry never blocks dispatch.
//
// Lifecycle mirrors internal/activity.Registry: NewRegistry loads the
// embedded default, then layers .borg/runtimes.yaml from the project
// root on top (per-runtime replacement). Read-only after construction.
package runtimepolicy

import (
	"errors"
	"fmt"
	"io/fs"

	_ "embed"

	"github.com/glorious-beard/locutus/internal/dispatch/acp"
	"github.com/glorious-beard/locutus/internal/specio"
	"gopkg.in/yaml.v3"
)

//go:embed runtimes-default.yaml
var defaultRuntimesYAML []byte

const projectOverridePath = ".borg/runtimes.yaml"

type runtimesYAMLFile struct {
	Runtimes map[string]runtimeEntry `yaml:"runtimes"`
}

type runtimeEntry struct {
	MinVersion string `yaml:"min_version"`
}

// Registry is the merged runtime → min-version table.
type Registry struct {
	floors map[string]string // runtime → min_version (non-empty entries only)
}

func parseRuntimesYAML(data []byte) (map[string]string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("runtimes.yaml: empty input")
	}
	var file runtimesYAMLFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("runtimes.yaml: parse: %w", err)
	}
	out := map[string]string{}
	for name, raw := range file.Runtimes {
		out[name] = raw.MinVersion
	}
	return out, nil
}

// NewRegistry layers .borg/runtimes.yaml over the embedded default.
// A nil fsys loads only the defaults.
func NewRegistry(fsys specio.FS) (*Registry, error) {
	merged, err := parseRuntimesYAML(defaultRuntimesYAML)
	if err != nil {
		return nil, fmt.Errorf("runtimepolicy: parse embedded default: %w", err)
	}
	if fsys != nil {
		data, err := fsys.ReadFile(projectOverridePath)
		switch {
		case err == nil:
			override, perr := parseRuntimesYAML(data)
			if perr != nil {
				return nil, fmt.Errorf("runtimepolicy: %s: %w", projectOverridePath, perr)
			}
			for name, v := range override {
				merged[name] = v
			}
		case errors.Is(err, fs.ErrNotExist):
			// no override; defaults stand
		default:
			return nil, fmt.Errorf("runtimepolicy: read %s: %w", projectOverridePath, err)
		}
	}
	// Validate: every keyed runtime must be a real spawn target.
	floors := map[string]string{}
	for name, v := range merged {
		if _, ok := acp.AgentSpawns[name]; !ok {
			return nil, fmt.Errorf("runtimepolicy: runtime %q is not registered in acp.AgentSpawns", name)
		}
		if v != "" {
			floors[name] = v
		}
	}
	return &Registry{floors: floors}, nil
}

// MinVersion returns the declared floor for a runtime and whether one
// is declared (a non-empty min_version). Runtimes with empty/absent
// floors return ("", false) — no warning should fire for them.
func (r *Registry) MinVersion(runtime string) (string, bool) {
	v, ok := r.floors[runtime]
	return v, ok
}
