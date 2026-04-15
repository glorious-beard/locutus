package cmd

// TriageCmd evaluates an issue against GOALS.md.
type TriageCmd struct {
	Input string `help:"Path to markdown issue file." type:"existingfile"`
}

func (c *TriageCmd) Run(cli *CLI) error {
	return nil // implemented in Tier 2
}
