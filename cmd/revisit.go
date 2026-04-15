package cmd

// RevisitCmd updates a decision or strategy.
type RevisitCmd struct {
	ID string `arg:"" help:"Decision or strategy ID to revisit."`
}

func (c *RevisitCmd) Run(cli *CLI) error {
	return nil // implemented in Tier 7
}
