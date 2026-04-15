package cmd

// McpCmd starts the MCP server.
type McpCmd struct {
	HTTP string `help:"Start HTTP transport on the given address (e.g. :8080)." optional:""`
}

func (c *McpCmd) Run(cli *CLI) error {
	return nil // implemented in Tier 8
}
