package cmd

// ImportCmd creates a feature or bug from an issue.
type ImportCmd struct {
	Input string `help:"Path to markdown issue file." type:"existingfile"`
}

func (c *ImportCmd) Run(cli *CLI) error {
	return nil // implemented in Tier 2
}
