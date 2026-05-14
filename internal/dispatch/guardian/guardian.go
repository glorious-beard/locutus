// Package guardian implements the production permission policy that the
// supervisor uses to gate coding-agent tool calls under DJ-119. It ports
// the supervisor-side bridge logic that previously lived in
// `internal/dispatch/interaction.go` (deleted in Phase 3) into a
// `policy.Policy` implementation.
//
// Decision flow:
//
//  1. The agent calls `session/request_permission`. The acp client
//     translates this into a `policy.Request` and invokes Guardian.Decide.
//  2. Guardian builds a prompt describing the step and the requested
//     tool call, then asks the validator agent (via the LLM) whether to
//     allow or deny.
//  3. The validator's response is parsed: ALLOW selects the first
//     allow_once / allow_always option the agent offered; anything else
//     (including "DENY: reason") selects the first reject_once /
//     reject_always option, or cancels if none is offered.
//
// This is intentionally a port-verbatim of the pre-DJ-119 behaviour. A
// tighter typed-deny-by-default-with-whitelist policy is a future DJ.
package guardian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/dispatch/policy"
	"github.com/chetan/locutus/internal/spec"
)

// guardianRetry keeps validator-LLM calls cheap: two attempts max with
// short backoff. The agent waits synchronously for our decision, so long
// retries translate directly to user-visible latency.
var guardianRetry = agent.RetryConfig{
	MaxAttempts: 2,
	BaseDelay:   250 * time.Millisecond,
	MaxDelay:    1 * time.Second,
}

// Guardian implements policy.Policy by consulting the validator agent
// (LLM-backed) for each permission request. Under DJ-121, one Guardian
// instance is constructed per workstream — its Workstream field anchors
// the validator prompt to the right context.
type Guardian struct {
	// LLM is the validator-tier client. Required.
	LLM agent.AgentExecutor
	// Def is the validator agent definition (system prompt + retry policy).
	// When zero-valued, a permission-denying default is used and a clear
	// reason surfaces back to the agent (see Decide).
	Def agent.AgentDef
	// Workstream is the workstream currently being supervised. Anchors the
	// prompt in scope-of-this-workstream context.
	Workstream spec.Workstream
}

// Decide implements policy.Policy. Defensive defaults follow the pre-DJ-119
// `handleInteraction` behaviour:
//   - LLM nil → cancel (with a reason a future logging boundary can surface)
//   - validator agent def zero-valued → cancel
//   - LLM call errors → cancel with the wrapped error
//   - LLM returns "ALLOW" → pick the first allow_once / allow_always option
//   - anything else → pick the first reject_once / reject_always option,
//     or cancel if no reject option was offered
func (g *Guardian) Decide(ctx context.Context, req policy.Request) (policy.Decision, error) {
	if g.LLM == nil {
		return policy.Decision{}, errors.New("guardian: SupervisorConfig.LLM is nil; cannot consult validator")
	}
	if g.Def.ID == "" {
		// Defensive default: deny by cancelling. Matches the pre-DJ-119
		// behaviour of returning a DENY decision when no validator agent
		// is configured.
		return cancelOrReject(req.Options), nil
	}

	prompt := buildPrompt(g.Workstream, req)
	input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: prompt}}}
	resp, err := agent.RunWithRetry(ctx, g.LLM, g.Def, input, guardianRetry)
	if err != nil {
		// Surface the error to the supervisor (which will log it) but
		// also pick a deny option so the agent doesn't hang waiting.
		// The acp transport reads the error first; the Decision is the
		// fallback if some future layer ignores it.
		return cancelOrReject(req.Options), fmt.Errorf("guardian LLM: %w", err)
	}

	if verdictAllows(resp.Content) {
		return allowOrCancel(req.Options), nil
	}
	return cancelOrReject(req.Options), nil
}

// buildPrompt renders the validator-LLM prompt from the current
// workstream and the pending permission request. Mirrors the now-deleted
// `dispatch.buildInteractionPrompt` but anchored at workstream grain per
// DJ-121 (coarsening from step-level).
func buildPrompt(ws spec.Workstream, req policy.Request) string {
	inputJSON, _ := json.MarshalIndent(req.RawInput, "", "  ")

	var b strings.Builder
	fmt.Fprintf(&b, "Workstream: %s (strategy domain: %s)\n\n", ws.ID, ws.StrategyDomain)
	if files := expectedFilesForWorkstream(ws); len(files) > 0 {
		fmt.Fprintf(&b, "Files expected to change: %s\n\n", strings.Join(files, ", "))
	}
	fmt.Fprintf(&b, "The coding agent wants to invoke tool %q with input:\n%s\n\n", req.ToolName, string(inputJSON))
	if len(req.Locations) > 0 {
		paths := make([]string, 0, len(req.Locations))
		for _, l := range req.Locations {
			paths = append(paths, l.Path)
		}
		fmt.Fprintf(&b, "Locations the tool will touch: %s\n\n", strings.Join(paths, ", "))
	}
	b.WriteString("Decide whether to allow this tool call. Consider: is it in scope for this workstream? Is it safe? Could it write outside the expected files or leak data?\n\n")
	b.WriteString("Output exactly one of:\n  ALLOW\n  DENY: <one-line reason>\n")
	return b.String()
}

// expectedFilesForWorkstream returns the union of every step's ExpectedFiles
// in encountered order with duplicates removed. Phase 4 lifts the
// ExpectedFiles concept onto the workstream itself; this helper handles the
// transitional state where steps still carry it.
func expectedFilesForWorkstream(ws spec.Workstream) []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, s := range ws.Steps {
		for _, f := range s.ExpectedFiles {
			if seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// verdictAllows reports whether the validator's response begins with
// "ALLOW" (case-insensitive, leading whitespace tolerated). Anything
// else is treated as a denial — the safer default when the model output
// is unexpected. Mirrors the now-deleted `dispatch.parseVerdict`.
func verdictAllows(content string) bool {
	trimmed := strings.TrimSpace(content)
	upper := strings.ToUpper(trimmed)
	return strings.HasPrefix(upper, "ALLOW")
}

// allowOrCancel picks the first allow_once / allow_always option from
// the agent's menu. Falls back to cancelling when no allow option was
// offered — the agent should not be invoking a tool it didn't include
// an allow option for.
func allowOrCancel(opts []policy.Option) policy.Decision {
	for _, o := range opts {
		if o.Kind == policy.KindAllowOnce || o.Kind == policy.KindAllowAlways {
			return policy.Decision{OptionID: o.OptionID}
		}
	}
	return policy.Decision{}
}

// cancelOrReject picks the first reject_once / reject_always option, or
// cancels when no reject option was offered.
func cancelOrReject(opts []policy.Option) policy.Decision {
	for _, o := range opts {
		if o.Kind == policy.KindRejectOnce || o.Kind == policy.KindRejectAlways {
			return policy.Decision{OptionID: o.OptionID}
		}
	}
	return policy.Decision{}
}
