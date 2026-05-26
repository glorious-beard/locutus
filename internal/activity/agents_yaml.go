package activity

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/glorious-beard/locutus/internal/specio"
	"gopkg.in/yaml.v3"
)

// agentsYAMLFile is the wire shape: top-level `activities` map keyed
// by activity name. We keep the on-disk schema flat — no version
// envelope, no metadata block — because the consumer (Registry) is
// internal and we control all readers. Adding a version field
// before a real compatibility break would be aspirational shape
// (see [[feedback-no-aspirational-fields]]).
type agentsYAMLFile struct {
	Activities map[string]agentsYAMLActivity `yaml:"activities"`
}

type agentsYAMLActivity struct {
	Runtimes []string `yaml:"runtimes"`
}

// parseAgentsYAML decodes the wire shape into the in-process Activity
// map. Validation of runtime ids happens at Registry assembly time
// (validateRegistry) so embedded-default and project-override go
// through the same checks.
func parseAgentsYAML(data []byte) (map[string]Activity, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("agents.yaml: empty input")
	}
	var file agentsYAMLFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("agents.yaml: parse: %w", err)
	}
	if len(file.Activities) == 0 {
		return nil, fmt.Errorf("agents.yaml: no activities defined (top-level 'activities' map is required and must be non-empty)")
	}

	out := make(map[string]Activity, len(file.Activities))
	for name, raw := range file.Activities {
		if name == "" {
			return nil, fmt.Errorf("agents.yaml: empty activity name")
		}
		out[name] = Activity{Name: name, Runtimes: raw.Runtimes}
	}
	return out, nil
}

// loadProjectOverride reads .borg/agents.yaml from fsys if present.
// A missing file is not an error — the default content remains in
// effect. Other errors (malformed YAML, unreadable file) propagate
// so the operator sees the misconfiguration at startup rather than
// at first-dispatch.
func loadProjectOverride(fsys specio.FS) (map[string]Activity, error) {
	data, err := fsys.ReadFile(projectOverridePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("activity: read %s: %w", projectOverridePath, err)
	}
	parsed, err := parseAgentsYAML(data)
	if err != nil {
		return nil, fmt.Errorf("activity: %s: %w", projectOverridePath, err)
	}
	return parsed, nil
}
