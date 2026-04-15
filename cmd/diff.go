package cmd

// DiffCmd previews blast radius of a spec change.
type DiffCmd struct {
	ID string `arg:"" help:"Feature, decision, or strategy ID."`
}

func (c *DiffCmd) Run(cli *CLI) error {
	return nil // implemented in Tier 3
}
