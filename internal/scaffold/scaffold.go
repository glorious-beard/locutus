// Package scaffold creates the initial directory structure and seed files for a
// Locutus-managed project.
package scaffold

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// directories is the set of directories created by Scaffold.
var directories = []string{
	".borg",
	".borg/spec/features",
	".borg/spec/bugs",
	".borg/spec/decisions",
	".borg/spec/strategies",
	".borg/spec/entities",
	".borg/history",
	".borg/council/agents",
	".agents/skills",
}

const defaultWorkflow = `rounds:
  - id: propose
    agent: planner
    parallel: false
  - id: challenge
    agents: [critic, stakeholder]
    parallel: true
    depends_on: [propose]
  - id: research
    agent: researcher
    parallel: false
    depends_on: [challenge]
    conditional: open_questions
  - id: revise
    agent: planner
    parallel: false
    depends_on: [research]
  - id: record
    agent: historian
    parallel: false
    depends_on: [revise]
max_rounds: 5
`

// agentDef holds the content for a council agent definition file.
type agentDef struct {
	filename string
	content  string
}

var agentDefs = []agentDef{
	{"planner.md", `---
id: planner
role: planning
---
You are the Planner agent. Your job is to decompose goals into features, decisions, and strategies. Produce clear, actionable execution plans.
`},
	{"critic.md", `---
id: critic
role: review
---
You are the Critic agent. Challenge assumptions, identify risks, and find gaps in proposed plans. Be constructive but thorough.
`},
	{"researcher.md", `---
id: researcher
role: research
---
You are the Researcher agent. Investigate open questions, gather evidence, and provide factual answers to support decision-making.
`},
	{"stakeholder.md", `---
id: stakeholder
role: advocacy
---
You are the Stakeholder agent. Represent end-user and business interests. Ensure plans align with project goals and user needs.
`},
	{"historian.md", `---
id: historian
role: record-keeping
---
You are the Historian agent. Record decisions, rationale, and outcomes. Maintain the decision journal and ensure institutional memory.
`},
	{"convergence.md", `---
id: convergence
role: synthesis
---
You are the Convergence agent. Synthesize diverse viewpoints into coherent decisions. Identify consensus and resolve conflicts.
`},
}

// Scaffold creates the full project scaffold on the given FS. It is idempotent:
// existing files are not overwritten.
func Scaffold(fsys specio.FS, projectName string) error {
	// 1. Create directories.
	for _, dir := range directories {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// 2. Write manifest.
	if err := writeIfMissing(fsys, ".borg/manifest.json", func() ([]byte, error) {
		m := spec.Manifest{
			ProjectName: projectName,
			Version:     "0.1.0",
			CreatedAt:   time.Now(),
		}
		return json.MarshalIndent(m, "", "  ")
	}); err != nil {
		return err
	}

	// 3. Write traceability index.
	if err := writeIfMissing(fsys, ".borg/spec/traces.json", func() ([]byte, error) {
		idx := spec.TraceabilityIndex{
			Entries: map[string]spec.TraceEntry{},
		}
		return json.MarshalIndent(idx, "", "  ")
	}); err != nil {
		return err
	}

	// 4. Write GOALS.md.
	if err := writeIfMissing(fsys, "GOALS.md", func() ([]byte, error) {
		content := fmt.Sprintf("# %s\n\n## In Scope\n\n## Out of Scope\n", projectName)
		return []byte(content), nil
	}); err != nil {
		return err
	}

	// 5. Write council workflow.
	if err := writeIfMissing(fsys, ".borg/council/workflow.yaml", func() ([]byte, error) {
		return []byte(defaultWorkflow), nil
	}); err != nil {
		return err
	}

	// 6. Write council agent definitions.
	for _, agent := range agentDefs {
		path := ".borg/council/agents/" + agent.filename
		content := agent.content
		if err := writeIfMissing(fsys, path, func() ([]byte, error) {
			return []byte(content), nil
		}); err != nil {
			return err
		}
	}

	return nil
}

// writeIfMissing writes a file only if it does not already exist (idempotency).
func writeIfMissing(fsys specio.FS, path string, generate func() ([]byte, error)) error {
	if _, err := fsys.Stat(path); err == nil {
		return nil // already exists, skip
	}
	data, err := generate()
	if err != nil {
		return fmt.Errorf("generate %s: %w", path, err)
	}
	if err := fsys.WriteFile(path, data, os.FileMode(0o644)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
