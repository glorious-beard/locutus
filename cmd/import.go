package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// ImportCmd dispatches the feature_ingestion activity. Per DJ-135
// phase 5 the verb forks a coding-agent runtime and hands it the
// feature_ingestion playbook, along with the to-be-admitted content
// as run context. The agent triages against GOALS.md and, on
// accept, proposes the new feature node via spec_propose_feature.
//
// Flags from the legacy verb (--type, --skip-triage, --no-plan,
// --dry-run) dropped in this rewrite. Triage discipline lives in
// the feature_ingestion playbook; explicit overrides return in a
// follow-up if needed.
type ImportCmd struct {
	Source string `arg:"" optional:"" help:"Path to a file containing the feature/bug content. Omitted reads from stdin."`
}

func (c *ImportCmd) Run(ctx context.Context, cli *CLI) error {
	content, err := c.readContent()
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("import: content is empty (read from %s)", c.sourceDescription())
	}
	contextNote := fmt.Sprintf("The supervisor is admitting the following content for triage:\n\n```\n%s\n```", content)
	return runActivityVerb(ctx, cli, "feature_ingestion", contextNote)
}

func (c *ImportCmd) readContent() (string, error) {
	if c.Source == "" {
		data, err := io.ReadAll(os.Stdin)
		return string(data), err
	}
	data, err := os.ReadFile(c.Source)
	return string(data), err
}

func (c *ImportCmd) sourceDescription() string {
	if c.Source == "" {
		return "stdin"
	}
	return c.Source
}
