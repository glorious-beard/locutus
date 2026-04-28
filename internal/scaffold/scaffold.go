// Package scaffold creates the initial directory structure and seed files for a
// Locutus-managed project.
package scaffold

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

//go:embed agents
var agentsFS embed.FS

//go:embed workflows/planning.yaml
var planningWorkflow []byte

//go:embed workflows/assimilation.yaml
var assimilationWorkflow []byte

//go:embed workflows/spec_generation.yaml
var specGenerationWorkflow []byte

// directories is the set of directories created by Scaffold.
var directories = []string{
	".borg",
	".borg/spec/features",
	".borg/spec/bugs",
	".borg/spec/decisions",
	".borg/spec/strategies",
	".borg/spec/approaches",
	".borg/spec/entities",
	".borg/history",
	".borg/agents",
	".borg/workflows",
	".agents/skills",
	".locutus/state",
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

	// 5. Copy embedded agent definitions.
	if err := copyEmbedded(fsys, agentsFS, "agents", ".borg/agents"); err != nil {
		return fmt.Errorf("copy agent files: %w", err)
	}

	// 6. Write workflow files.
	if err := writeIfMissing(fsys, ".borg/workflows/planning.yaml", func() ([]byte, error) {
		return planningWorkflow, nil
	}); err != nil {
		return err
	}
	if err := writeIfMissing(fsys, ".borg/workflows/assimilation.yaml", func() ([]byte, error) {
		return assimilationWorkflow, nil
	}); err != nil {
		return err
	}
	if err := writeIfMissing(fsys, ".borg/workflows/spec_generation.yaml", func() ([]byte, error) {
		return specGenerationWorkflow, nil
	}); err != nil {
		return err
	}

	// 7. Seed .borg/models.yaml from the embedded defaults so users can
	// edit per-project model preferences (provider order, tier candidates)
	// without rebuilding or setting LOCUTUS_MODELS_CONFIG. The runtime
	// reads from this path on every invocation; absent file falls back
	// to the embedded bytes.
	if err := writeIfMissing(fsys, ".borg/models.yaml", func() ([]byte, error) {
		return agent.EmbeddedModelsYAML(), nil
	}); err != nil {
		return err
	}

	return nil
}

// copyEmbedded walks an embedded FS and copies all files to the target FS,
// preserving directory structure. Files are only written if missing (idempotent).
func copyEmbedded(fsys specio.FS, embedded embed.FS, root, targetPrefix string) error {
	return fs.WalkDir(embedded, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath := path[len(root):]
		targetPath := targetPrefix + relPath

		if d.IsDir() {
			return fsys.MkdirAll(targetPath, 0o755)
		}

		return writeIfMissing(fsys, targetPath, func() ([]byte, error) {
			return embedded.ReadFile(path)
		})
	})
}

// ResetReport tells the caller what `update --reset` overwrote. Surfaced
// so the CLI can print "refreshed N agents, M workflows, models.yaml" or
// the like — and so tests can assert exact behavior.
type ResetReport struct {
	AgentsReset    []string // FS-relative paths of agent files written
	WorkflowsReset []string // FS-relative paths of workflow files written
	ModelsReset    bool     // true if .borg/models.yaml was rewritten
}

// Reset overwrites scaffolded artifacts on fsys with the versions baked
// into the running binary. User-owned content is left untouched:
// GOALS.md, .borg/spec/, .borg/history/, .borg/manifest.json, the
// project's .locutus/ runtime state, and .gitignore.
//
// Use this after upgrading the locutus binary to pick up new or
// changed agent definitions and workflow shapes the upstream build
// ships. It does NOT download anything — the caller is expected to
// already have the desired binary running.
//
// Custom agent files the user added under .borg/agents/ that aren't in
// the embedded set are not touched. Embedded agents that have been
// removed in this build are also left alone — Reset overwrites; it
// never deletes. A future "prune" mode could surface stale files, but
// that's a separate decision than reset semantics.
func Reset(fsys specio.FS) (*ResetReport, error) {
	report := &ResetReport{}

	// Overwrite each embedded agent file. fs.WalkDir gives us every file
	// under the agents/ embed root; we rewrite the corresponding
	// .borg/agents/<rel>.md path on fsys.
	if err := fs.WalkDir(agentsFS, "agents", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel := path[len("agents"):]
			return fsys.MkdirAll(".borg/agents"+rel, 0o755)
		}
		rel := path[len("agents"):]
		target := ".borg/agents" + rel
		data, err := agentsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded agent %s: %w", path, err)
		}
		if err := fsys.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		report.AgentsReset = append(report.AgentsReset, target)
		return nil
	}); err != nil {
		return report, err
	}

	// Overwrite the three embedded workflow YAMLs.
	workflows := []struct {
		path string
		data []byte
	}{
		{".borg/workflows/planning.yaml", planningWorkflow},
		{".borg/workflows/assimilation.yaml", assimilationWorkflow},
		{".borg/workflows/spec_generation.yaml", specGenerationWorkflow},
	}
	if err := fsys.MkdirAll(".borg/workflows", 0o755); err != nil {
		return report, err
	}
	for _, wf := range workflows {
		if err := fsys.WriteFile(wf.path, wf.data, 0o644); err != nil {
			return report, fmt.Errorf("write %s: %w", wf.path, err)
		}
		report.WorkflowsReset = append(report.WorkflowsReset, wf.path)
	}

	// Overwrite models.yaml.
	if err := fsys.MkdirAll(".borg", 0o755); err != nil {
		return report, err
	}
	if err := fsys.WriteFile(".borg/models.yaml", agent.EmbeddedModelsYAML(), 0o644); err != nil {
		return report, fmt.Errorf("write .borg/models.yaml: %w", err)
	}
	report.ModelsReset = true

	return report, nil
}

// writeIfMissing writes a file only if it does not already exist (idempotency).
func writeIfMissing(fsys specio.FS, path string, generate func() ([]byte, error)) error {
	if _, err := fsys.Stat(path); err == nil {
		return nil
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
