package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// HookValidateDecisionCmd is the subcommand external hook configs
// (Codex `.codex/config.toml`, Gemini `.gemini/settings.json`)
// invoke before the `mcp__locutus__spec_propose_decision` tool call
// fires. It reads the proposed decision input as JSON on stdin and
// either exits 0 (allow) or exits non-zero with a structured reason
// on stderr (deny).
//
// Decision invariants enforced today (DJ-133):
//
//   - id MUST start with the `dec-` prefix.
//   - the suffix after `dec-` MUST match the primary axis the decision
//     answers (carried in the `axes` field's first entry, when
//     present).
//
// More invariants land here as the spec graph's shape gains
// constraints; the hook is the lowest-level enforcement surface and
// catches violations mechanically rather than relying on the agent
// to honor a prose contract.
type HookValidateDecisionCmd struct{}

// Run reads JSON from stdin, validates the proposed decision, and
// exits accordingly. The Codex hook protocol passes tool input on
// stdin (Gemini's BeforeTool is the same shape). Exit 0 = allow;
// any non-zero exit = deny + the stderr message becomes the
// operator-visible reason.
//
// The function returns nil on success (Run exits with code 0) or a
// non-nil error otherwise — kong's runner translates the returned
// error into a non-zero exit, so we don't need an explicit os.Exit.
func (c *HookValidateDecisionCmd) Run(_ context.Context, _ *CLI) error {
	return validateDecisionFromReader(os.Stdin)
}

// validateDecisionFromReader is the testable kernel: reads + decodes
// the tool input, then runs the invariants. Separate from Run so the
// test suite can drive it with a bytes.Reader and assert on the
// error shape without going through os.Stdin.
func validateDecisionFromReader(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("hook-validate-decision: read input: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("hook-validate-decision: empty input — expected JSON tool call payload on stdin")
	}

	// The hook receives the runtime's tool-call wrapper; the
	// decision body lives at .tool_input (Codex) or .toolInput
	// (Gemini's BeforeTool). Try both — the schema is small enough
	// that probing keeps the hook portable across runtimes.
	var wrapper struct {
		ToolInput  *decisionInput `json:"tool_input,omitempty"`
		ToolInputC *decisionInput `json:"toolInput,omitempty"`
		// Some runtimes pass the input as the top-level object
		// (no wrapper). We fall back to unmarshaling the same bytes
		// directly into decisionInput when the wrapper has neither
		// child populated.
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return fmt.Errorf("hook-validate-decision: parse JSON: %w", err)
	}
	input := wrapper.ToolInput
	if input == nil {
		input = wrapper.ToolInputC
	}
	if input == nil {
		input = &decisionInput{}
		if err := json.Unmarshal(data, input); err != nil {
			return fmt.Errorf("hook-validate-decision: parse JSON (top-level fallback): %w", err)
		}
	}

	return validateDecision(input)
}

// decisionInput is the subset of the spec_propose_decision input
// we inspect. Fields beyond id / axes are ignored — adding new
// invariants means extending this struct, not parsing again.
type decisionInput struct {
	ID   string   `json:"id"`
	Axes []string `json:"axes,omitempty"`
}

// validateDecision runs the per-field invariants and returns an
// error naming the failing constraint. Multiple violations are
// reported one at a time; the hook only needs the first one to
// deny the call.
func validateDecision(d *decisionInput) error {
	if d == nil || strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("decision id is required (DJ-133: id is `dec-<axis-id>`)")
	}
	if !strings.HasPrefix(d.ID, "dec-") {
		return fmt.Errorf("decision id %q must start with `dec-` (DJ-133: id names the axis the decision answers)", d.ID)
	}
	if len(d.ID) <= len("dec-") {
		return fmt.Errorf("decision id %q has empty axis suffix — id format is `dec-<axis-id>`", d.ID)
	}
	// When axes is present, its first entry must match the suffix.
	// When axes is absent the MCP write tool backfills it from the
	// id; the hook doesn't reject that path because the backfill is
	// the canonical convergence-by-construction discipline.
	if len(d.Axes) > 0 {
		suffix := strings.TrimPrefix(d.ID, "dec-")
		if d.Axes[0] != suffix {
			return fmt.Errorf("decision id %q axis suffix %q does not match axes[0]=%q (DJ-133: id and primary axis must agree)",
				d.ID, suffix, d.Axes[0])
		}
	}
	return nil
}
