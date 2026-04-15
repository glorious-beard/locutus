package cmd

import (
	"encoding/json"
	"fmt"
	"os"
)

// VersionCmd prints the current version.
type VersionCmd struct {
	Version string `kong:"-"`
}

type versionOutput struct {
	Version string `json:"version"`
}

func (c *VersionCmd) Run(cli *CLI) error {
	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(versionOutput{Version: c.Version})
	}
	fmt.Printf("locutus %s\n", c.Version)
	return nil
}
