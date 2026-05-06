package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
)

// handleInteraction processes a bridge-originated permission or
// clarifying-question event. It asks the guardian agent (validator slot
// for now) whether to allow or deny, then routes the decision back to
// the bridge which forwards it to Claude as an MCP tool_result.
//
// Defensive defaults:
//   - No bridge attached: return an error. The event shouldn't have fired
//     without a bridge, so this is a programmer-error signal rather than
//     silent drop.
//   - No validator agent configured: deny. Refusing-by-default is the
//     safer posture than pass-through, and the deny message tells the
//     operator what to fix.
func (s *Supervisor) handleInteraction(ctx context.Context, step spec.PlanStep, evt AgentEvent) error {
	if s.permBridge == nil {
		return fmt.Errorf("handleInteraction: no permission bridge attached (event id=%q)", evt.InteractionID)
	}
	if evt.InteractionID == "" {
		return fmt.Errorf("handleInteraction: event missing InteractionID (kind=%s)", evt.Kind)
	}

	def, ok := s.cfg.AgentDefs["validator"]
	if !ok || def.ID == "" {
		return s.permBridge.Respond(evt.InteractionID, PermDecision{
			Behavior: "deny",
			Message:  "no validator agent configured; denying permission by default",
		})
	}
	if s.cfg.LLM == nil {
		return s.permBridge.Respond(evt.InteractionID, PermDecision{
			Behavior: "deny",
			Message:  "validator agent configured but SupervisorConfig.LLM is nil; denying by default",
		})
	}

	prompt := buildInteractionPrompt(step, evt)
	input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: prompt}}}
	resp, err := agent.RunWithRetry(ctx, s.cfg.LLM, def, input, fastMonitorRetry)
	if err != nil {
		// LLM call failed — the supervisor must still respond to the
		// bridge so Claude doesn't hang. Default to deny with the
		// error as the message.
		_ = s.permBridge.Respond(evt.InteractionID, PermDecision{
			Behavior: "deny",
			Message:  fmt.Sprintf("guardian LLM unreachable: %v", err),
		})
		return fmt.Errorf("guardian LLM: %w", err)
	}

	return s.permBridge.Respond(evt.InteractionID, parseVerdict(resp.Content))
}

// buildInteractionPrompt renders the guardian prompt from the current
// plan step and the pending permission request. Kept minimal — the
// agent definition carries the role/criteria; this function only
// assembles the per-request context.
func buildInteractionPrompt(step spec.PlanStep, evt AgentEvent) string {
	inputJSON, _ := json.MarshalIndent(evt.ToolInput, "", "  ")

	var b strings.Builder
	fmt.Fprintf(&b, "Plan step: %s\n\n", step.Description)
	if len(step.ExpectedFiles) > 0 {
		fmt.Fprintf(&b, "Files expected to change: %s\n\n", strings.Join(step.ExpectedFiles, ", "))
	}
	switch evt.Kind {
	case EventPermissionRequest:
		fmt.Fprintf(&b, "The coding agent wants to invoke tool %q with input:\n%s\n\n", evt.ToolName, string(inputJSON))
		b.WriteString("Decide whether to allow this tool call. Consider: is it in scope for this step? Is it safe? Could it write outside the expected files or leak data?\n\n")
	case EventClarifyQuestion:
		fmt.Fprintf(&b, "The coding agent is asking a clarifying question:\n%s\n\n", evt.Text)
		b.WriteString("Decide whether to answer the question directly (allow with the answer as message) or deny (agent should proceed without interaction).\n\n")
	}
	b.WriteString("Output exactly one of:\n  ALLOW\n  DENY: <one-line reason>\n")
	return b.String()
}

// parseVerdict translates the guardian's text response into a
// PermDecision. The prompt instructs the model to emit either "ALLOW"
// or "DENY: <reason>". Anything that doesn't start with ALLOW is
// treated as a denial — the safer default when the model output is
// unexpected.
func parseVerdict(content string) PermDecision {
	trimmed := strings.TrimSpace(content)
	upper := strings.ToUpper(trimmed)

	if strings.HasPrefix(upper, "ALLOW") {
		return PermDecision{Behavior: "allow"}
	}

	// Strip a leading "DENY:" or "DENY" so the message is just the reason.
	msg := trimmed
	switch {
	case strings.HasPrefix(upper, "DENY:"):
		msg = strings.TrimSpace(trimmed[len("DENY:"):])
	case strings.HasPrefix(upper, "DENY"):
		msg = strings.TrimSpace(trimmed[len("DENY"):])
	}
	return PermDecision{Behavior: "deny", Message: msg}
}
