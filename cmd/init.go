package cmd

// InitCmd initializes a new spec-driven project.
type InitCmd struct {
	Name string `arg:"" optional:"" help:"Project name."`
}

func (c *InitCmd) Run(cli *CLI) error {
	return nil // implemented in Tier 2
}
