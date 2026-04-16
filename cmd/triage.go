package cmd

import (
	"fmt"
	"os"
	"path/filepath"
)

// TriageCmd evaluates an issue against GOALS.md.
type TriageCmd struct {
	Input string `help:"Path to markdown issue file." type:"existingfile"`
}

func (c *TriageCmd) Run(cli *CLI) error {
	if c.Input == "" {
		return fmt.Errorf("--input is required")
	}

	// Read the input file.
	inputData, err := os.ReadFile(c.Input)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}
	_ = inputData

	// Check for GOALS.md.
	goalsPath := filepath.Join(".borg", "GOALS.md")
	if _, err := os.Stat(goalsPath); os.IsNotExist(err) {
		return fmt.Errorf("GOALS.md not found at %s — run 'locutus init' first", goalsPath)
	}

	// LLM provider not yet wired up; the agent.EvaluateAgainstGoals function
	// is fully implemented and tested via the agent package.
	return fmt.Errorf("LLM not configured — triage evaluation requires an LLM provider")
}
