// Package policy declares the provider-neutral permission policy types
// that flow between the dispatcher's supervisor and the ACP transport.
//
// This package exists to break the import cycle that would otherwise close
// between `dispatch` (which needs to reference Policy in PromptConn) and
// `dispatch/acp` (which already imports dispatch for AgentEvent). Both
// packages import `dispatch/policy` instead; neither imports the other for
// policy types.
//
// The acp package adapts at its `client.RequestPermission` boundary —
// translating acpsdk's `ToolCallUpdate` + `[]PermissionOption` payload
// into the `Request` declared here, calling Policy.Decide, and translating
// the returned `Decision.OptionID` back into the SDK's
// `RequestPermissionResponse` shape. The Policy implementation itself
// (e.g. the Guardian under internal/dispatch/guardian/) never sees an
// acpsdk type and so can be exercised without spinning up an ACP connection.
package policy

import "context"

// Policy decides what to do when the coding agent requests permission to
// invoke a tool. Implementations receive a provider-neutral Request and
// return a Decision naming one of the offered Options — or an empty
// OptionID, which the ACP transport translates into a `cancelled` outcome.
//
// Decide MUST NOT mutate Request (which the transport reuses across late-
// delivery edge cases). Implementations are free to log, take time, or
// invoke other agents (e.g. the validator LLM in the Guardian impl).
type Policy interface {
	Decide(ctx context.Context, req Request) (Decision, error)
}

// Request is the provider-neutral form of an agent permission request,
// translated from the ACP `session/request_permission` method call by the
// `acp` package's `client.RequestPermission` handler. The fields cover
// what every implemented Policy needs today; richer adapters can fold
// additional context onto a future field without breaking implementations.
type Request struct {
	// ToolCallID is the agent's identifier for this particular tool call.
	// Stable across retries within one prompt; useful for correlation in
	// logs and traces but otherwise opaque to the Policy.
	ToolCallID string

	// ToolName is the human-readable name the agent gave for the tool
	// (acpsdk.ToolCallUpdate.Title). For Claude Code tools this is e.g.
	// "Edit", "Bash", "Write"; for Codex/Gemini-defined tools it varies.
	ToolName string

	// Kind categorizes the tool call (acpsdk.ToolKind). Common values:
	// "edit", "read", "execute", "search", "other". Empty when the agent
	// did not specify a kind.
	Kind string

	// RawInput is the agent's input payload to the tool — typically a
	// map[string]any (decoded JSON object) but the SDK types it as `any`
	// so non-object inputs are also possible. Treat as untrusted data.
	RawInput any

	// Locations are file-system locations the tool will read or write.
	// Empty for tools with no obvious file affinity (Bash, network calls,
	// etc.). Useful for guardian policies that gate on path scoping.
	Locations []Location

	// Options are the responses the agent has offered to accept. Decide
	// MUST pick one of these OptionIDs (or return Decision{} to cancel).
	// Order is the agent's preferred order — first is typically the
	// "default allow" if the policy has no opinion.
	Options []Option
}

// Location names a file the tool intends to touch. Line is nil when the
// agent gave no line hint; callers that gate on line scope should fall
// back to whole-file scope in that case.
type Location struct {
	Path string
	Line *int
}

// Option is one of the choices the agent offered. The Policy returns an
// OptionID matching one of these.
type Option struct {
	// OptionID is the agent's identifier. Pass this back in Decision.OptionID
	// to select it.
	OptionID string

	// Kind categorises the option for policies that don't want to pattern-
	// match on names. Canonical values (from acpsdk.PermissionOptionKind):
	//   - "allow_once"      — permit just this call
	//   - "allow_always"    — permit this and structurally-similar calls
	//   - "reject_once"     — refuse just this call
	//   - "reject_always"   — refuse this and structurally-similar calls
	// Empty when the agent didn't supply a kind; policies should fall
	// back to inspecting Name in that case.
	Kind string

	// Name is the human-readable label the agent suggested for this
	// option (e.g. "Allow", "Reject"). Useful for logs.
	Name string
}

// Decision is the Policy's response. An empty OptionID means cancel — the
// ACP transport will translate that into `outcome: cancelled` on the wire
// and the agent will surface a refusal back to its caller. A non-empty
// OptionID MUST match one of the Request.Options[*].OptionID values; the
// transport does not validate this, and an unknown OptionID will surface
// as an agent-side error.
type Decision struct {
	OptionID string
}

// Kind values for Option.Kind. Mirrors acpsdk.PermissionOptionKind but
// kept here to avoid Policy implementations needing to import the SDK.
const (
	KindAllowOnce    = "allow_once"
	KindAllowAlways  = "allow_always"
	KindRejectOnce   = "reject_once"
	KindRejectAlways = "reject_always"
)

// AllowOncePolicy is a minimal Policy useful for tests and for early-phase
// integration before a production Policy is wired in. It picks the first
// allow_once / allow_always option from the agent's menu and cancels when
// no allow option is offered.
//
// This is the Phase-3 placeholder behaviour. Production deployments under
// DJ-119 use the Guardian Policy (internal/dispatch/guardian) which calls
// the validator LLM.
type AllowOncePolicy struct{}

// Decide implements Policy.
func (AllowOncePolicy) Decide(_ context.Context, req Request) (Decision, error) {
	for _, o := range req.Options {
		if o.Kind == KindAllowOnce || o.Kind == KindAllowAlways {
			return Decision{OptionID: o.OptionID}, nil
		}
	}
	return Decision{}, nil
}
