package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/specio"
)

// InitCmd initializes a new spec-driven project.
type InitCmd struct {
	Name string `arg:"" optional:"" help:"Project name."`
}

func (c *InitCmd) Run(cli *CLI) error {
	name := c.Name
	if name == "" {
		name = filepath.Base(".")
	}

	fsys := specio.NewOSFS(".")
	if err := scaffold.Scaffold(fsys, name); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	// Detect git and either warn or wire up .gitignore. The spec is meant
	// to live in version control alongside the code; .locutus/ holds
	// runtime state and should be excluded.
	if isGitRepo(".") {
		if err := ensureGitignoreEntry(".gitignore", ".locutus/"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "warning: not inside a git repository — `.borg/` spec files are intended to be tracked in git; run `git init` before continuing.")
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "ok", "project": name})
	}
	fmt.Print(render.StatusSummary(GatherStatus(fsys)))
	return nil
}

// isGitRepo reports whether dir (or any of its ancestors) contains a `.git`
// entry. `.git` may be a directory (normal repo), a file (worktrees,
// submodules), so a Stat is enough — we don't care which.
func isGitRepo(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false
		}
		abs = parent
	}
}

// ensureGitignoreEntry appends entry to path if it isn't already present. The
// match is line-exact to avoid spurious duplicates on re-runs of `locutus
// init` while still letting the user keep their own variants (e.g.
// `.locutus`).
func ensureGitignoreEntry(path, entry string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range bytes.Split(existing, []byte("\n")) {
		if bytes.Equal(bytes.TrimSpace(line), []byte(entry)) {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(entry + "\n")
	return err
}
