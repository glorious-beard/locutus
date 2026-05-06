package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// ExplainCmd renders a single spec node — its rationale, alternatives,
// citations, lineage, and back-references — without invoking the LLM.
// Pure read of `.borg/` plus the `.locutus/workstreams/` activity used
// for stage classification.
//
// Output formats: markdown (default) or json. JSON emits the
// SnapshotData-shaped per-node block plus the rendered Markdown so
// downstream tooling can consume either form.
type ExplainCmd struct {
	ID     string `arg:"" help:"Spec node id (dec-…, feat-…, strat-…, app-…, bug-…)."`
	Format string `help:"Output format: markdown or json." enum:"markdown,json" default:"markdown"`
}

// ExplainResult is the JSON shape emitted with --format=json. Carries
// both the structured node data and the rendered Markdown so
// downstream consumers can pick either without re-running the load.
type ExplainResult struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Stage    string `json:"stage,omitempty"`
	Markdown string `json:"markdown"`
}

func (c *ExplainCmd) Run(cli *CLI) error {
	fsys, _, err := projectFS()
	if err != nil {
		return err
	}
	result, err := RunExplain(fsys, c.ID)
	if err != nil {
		return err
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
		return nil
	}
}

// RunExplain loads the spec graph, derives stages, and renders the
// requested node. Shared between the CLI handler and the MCP tool.
func RunExplain(fsys specio.FS, id string) (*ExplainResult, error) {
	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return nil, err
	}
	stages := spec.DeriveStages(loaded, fsys)

	md, err := render.ExplainNode(loaded, stages, id)
	if err != nil {
		return nil, err
	}

	return &ExplainResult{
		ID:       id,
		Kind:     string(kindFromIDPrefix(id)),
		Stage:    string(stages[id]),
		Markdown: md,
	}, nil
}

// kindFromIDPrefix maps the id prefix to a NodeKind for the JSON
// output. Unknown prefix → empty kind; the explain renderer will
// have already returned an error in that case so this is defensive.
func kindFromIDPrefix(id string) spec.NodeKind {
	switch {
	case len(id) >= 4 && id[:4] == "dec-":
		return spec.KindDecision
	case len(id) >= 5 && id[:5] == "feat-":
		return spec.KindFeature
	case len(id) >= 6 && id[:6] == "strat-":
		return spec.KindStrategy
	case len(id) >= 4 && id[:4] == "app-":
		return spec.KindApproach
	case len(id) >= 4 && id[:4] == "bug-":
		return spec.KindBug
	}
	return ""
}
