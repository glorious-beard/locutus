package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// JustifyCmd defends a spec node with the advocate agent. Without
// --against, runs the advocate solo and emits a JustificationBrief.
// With --against, runs the challenger first and the advocate second
// to produce an AdversarialDefense.
type JustifyCmd struct {
	ID      string `arg:"" help:"Spec node id (dec-…, feat-…, strat-…, app-…, bug-…)."`
	Against string `help:"Free-form challenge to defend against." optional:""`
	Format  string `help:"Output format: markdown or json." enum:"markdown,json" default:"markdown"`
}

// JustifyResult is the JSON-shaped output. Exactly one of Brief or
// Adversarial is populated.
type JustifyResult struct {
	ID          string                     `json:"id"`
	Challenge   string                     `json:"challenge,omitempty"`
	Brief       *agent.JustificationBrief  `json:"brief,omitempty"`
	Challenger  *agent.ChallengeBrief      `json:"challenger,omitempty"`
	Adversarial *agent.AdversarialDefense  `json:"adversarial,omitempty"`
	Markdown    string                     `json:"markdown"`
	SessionPath string                     `json:"session_path,omitempty"`
}

func (c *JustifyCmd) Run(ctx context.Context, cli *CLI) error {
	fsys, root, err := projectFS()
	if err != nil {
		return err
	}

	challenge := c.Against
	llm, rec, err := recordingLLM(fsys, root, justifyCommandLabel(c.ID, challenge))
	if err != nil {
		return err
	}

	result, err := RunJustifyCommand(ctx, llm, fsys, c.ID, challenge)
	if err != nil {
		return err
	}
	if rec != nil {
		_ = rec.Close()
		result.SessionPath = rec.Path()
		// Re-render markdown with session path in the footer.
		result.Markdown = renderJustifyMarkdown(result)
	}

	format := c.Format
	if cli.JSON && format == "markdown" {
		format = "json"
	}
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		fmt.Print(result.Markdown)
		if rec != nil {
			fmt.Printf("\nSession: %s/\n", rec.Path())
		}
		return nil
	}
}

// RunJustifyCommand is the shared implementation backing the CLI and
// MCP handlers. challenge is empty for the solo defense path; non-
// empty triggers the adversarial dialogue.
func RunJustifyCommand(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, id, challenge string) (*JustifyResult, error) {
	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return nil, err
	}
	stages := spec.DeriveStages(loaded, fsys)
	nodeMD, err := render.ExplainNode(loaded, stages, id)
	if err != nil {
		return nil, err
	}
	goalsBody, _ := readGoals(fsys)

	in := agent.JustifyInputs{
		NodeID:       id,
		NodeMarkdown: nodeMD,
		GoalsBody:    goalsBody,
		Challenge:    challenge,
	}

	result := &JustifyResult{ID: id, Challenge: challenge}

	if challenge == "" {
		brief, err := agent.RunJustify(ctx, llm, in)
		if err != nil {
			return nil, err
		}
		result.Brief = brief
	} else {
		ch, def, err := agent.RunJustifyAgainst(ctx, llm, in)
		if err != nil {
			return nil, err
		}
		result.Challenger = ch
		result.Adversarial = def
	}

	result.Markdown = renderJustifyMarkdown(result)
	return result, nil
}

func renderJustifyMarkdown(r *JustifyResult) string {
	if r.Adversarial != nil {
		return render.JustifyAgainstMarkdown(r.ID, r.Challenge, r.Challenger, r.Adversarial, r.SessionPath)
	}
	if r.Brief != nil {
		return render.JustifyMarkdown(r.ID, r.Brief, r.SessionPath)
	}
	return ""
}

func justifyCommandLabel(id, challenge string) string {
	if challenge == "" {
		return "justify " + id
	}
	return "justify " + id + " --against"
}

