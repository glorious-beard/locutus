package cmd

import (
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

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "ok", "project": name})
	}
	fmt.Print(render.StatusSummary(GatherStatus(fsys)))
	return nil
}
