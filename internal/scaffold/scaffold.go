// Package scaffold creates the initial directory structure and seed files for a
// Locutus-managed project.
package scaffold

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// Embed only .md scaffold prompts. Test files (`*_test.go`) coexist
// in this directory for the agents_test package — go:embed would
// otherwise pull them into the binary and `locutus init` /
// `update --reset` would litter every user's .borg/agents/ with Go
// source. The loader only reads .md, so .go files would be silently
// ignored at runtime but persist on disk (the Reset cleanup pass
// only inspects .md). Narrowing the pattern keeps both this
// directory's dual-purpose layout and the user-facing scaffold clean.
//go:embed agents/*.md
var agentsFS embed.FS

// plansFS embeds the canonical activity-playbook content (DJ-135
// phase 5). Each .md is keyed by activity name (spec_refinement.md,
// feature_ingestion.md, etc.) and copied to .borg/plans/ at init
// time. The publisher (Phase 4) reads from .borg/plans/ to emit
// runtime-specific slash commands.
//
//go:embed plans/*.md
var plansFS embed.FS

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
	".borg/plans",
	".agents/skills",
	".borg/state",
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

	// 5b. Copy embedded activity playbooks (DJ-135 phase 5). Same
	// idempotent semantics as agent files — existing playbooks are
	// not overwritten on `init`; `update --reset` is the explicit
	// refresh path.
	if err := copyEmbedded(fsys, plansFS, "plans", ".borg/plans"); err != nil {
		return fmt.Errorf("copy plan files: %w", err)
	}

	// .borg/models.yaml retired in DJ-135 phase 5. Locutus no longer
	// makes LLM calls directly; the coding-agent runtime (Claude Code
	// etc.) handles model selection per its own configuration.

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

// ResetReport tells the caller what `update --reset` overwrote and
// removed. Surfaced so the CLI can print a per-action summary and so
// tests can assert exact behavior.
type ResetReport struct {
	AgentsReset   []string // FS-relative paths of agent files written
	AgentsRemoved []string // FS-relative paths of agent files deleted (no longer in the embedded scaffold)
	PlansReset    []string // FS-relative paths of activity-playbook files written (DJ-135 phase 5)
	ModelsReset   bool     // true if .borg/models.yaml was rewritten
}

// Reset overwrites scaffolded artifacts on fsys with the versions
// baked into the running binary, and removes agent files in
// .borg/agents/ that are no longer in the embedded scaffold. User-
// owned content is left untouched: GOALS.md, .borg/spec/,
// .borg/history/, .borg/manifest.json, the project's .locutus/
// runtime state, and .gitignore.
//
// Use this after upgrading the locutus binary to pick up new or
// changed agent definitions the upstream build ships. It does NOT
// download anything — the caller is expected to already have the
// desired binary running. Workflow topology is defined in code and
// rebuilt with the binary; nothing on disk to refresh for that.
//
// Removal semantics: any .md file under .borg/agents/ whose
// basename (without extension) doesn't match an embedded scaffold
// agent id is removed. This includes (a) agents that used to be in
// the scaffold and were dropped in a newer build, and (b) custom
// agent files a user wrote that aren't part of the locutus
// distribution. The latter is intentional — locutus only loads
// agents by known id, so a custom file with no matching id wasn't
// being used by anything anyway. If the user wants project-local
// agent overrides, they should write them to the same id as an
// embedded scaffold (which Reset will then overwrite from the
// embedded copy — manage these in source control alongside the
// project, not as files preserved across resets).
func Reset(fsys specio.FS) (*ResetReport, error) {
	report := &ResetReport{}

	// Build the set of embedded agent ids so we can identify orphan
	// files in the project copy below.
	embeddedIDs := map[string]struct{}{}
	if err := fs.WalkDir(agentsFS, "agents", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		base := filepath.Base(path)
		if !strings.HasSuffix(base, ".md") {
			return nil
		}
		embeddedIDs[strings.TrimSuffix(base, ".md")] = struct{}{}
		return nil
	}); err != nil {
		return report, fmt.Errorf("walk embedded agents: %w", err)
	}

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

	// Overwrite each embedded activity playbook (DJ-135 phase 5).
	// No orphan-removal pass for plans yet — the activity registry
	// drives which plans actually publish, so an extra .md sitting
	// in .borg/plans/ from an old binary is harmless until it
	// matches a registered activity name.
	if err := fs.WalkDir(plansFS, "plans", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel := path[len("plans"):]
			return fsys.MkdirAll(".borg/plans"+rel, 0o755)
		}
		rel := path[len("plans"):]
		target := ".borg/plans" + rel
		data, err := plansFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded plan %s: %w", path, err)
		}
		if err := fsys.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		report.PlansReset = append(report.PlansReset, target)
		return nil
	}); err != nil {
		return report, err
	}

	// Remove orphan agent .md files — files in the project's
	// .borg/agents/ whose id is no longer in the embedded scaffold.
	// ListDir returns FS-relative paths; we filter to .md and
	// compare basename against embeddedIDs.
	if entries, err := fsys.ListDir(".borg/agents"); err == nil {
		for _, p := range entries {
			base := filepath.Base(p)
			if !strings.HasSuffix(base, ".md") {
				continue
			}
			id := strings.TrimSuffix(base, ".md")
			if _, ok := embeddedIDs[id]; ok {
				continue
			}
			if err := fsys.Remove(p); err != nil {
				return report, fmt.Errorf("remove orphan agent %s: %w", p, err)
			}
			report.AgentsRemoved = append(report.AgentsRemoved, p)
		}
	}

	// models.yaml retired with the in-process LLM dispatch (see
	// Scaffold). No models.yaml is written by Reset; existing files
	// on legacy projects can be removed manually.

	return report, nil
}

// LoadAgent retired in DJ-135 phase 5 — the council-side agent.AgentDef
// type retired with the council. The publisher reads canonical agents
// directly via frontmatter into its own CanonicalAgent shape; nothing
// else loads agents through this path.

func parseAgentDef(data []byte) ([]byte, error) {
	body, err := frontmatter.Parse(data, &struct{}{})
	if err != nil {
		return nil, fmt.Errorf("parse agent frontmatter: %w", err)
	}
	return []byte(body), nil
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
