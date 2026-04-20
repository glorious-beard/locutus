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

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

//go:embed agents
var agentsFS embed.FS

//go:embed workflows/planning.yaml
var planningWorkflow []byte

//go:embed workflows/assimilation.yaml
var assimilationWorkflow []byte

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
