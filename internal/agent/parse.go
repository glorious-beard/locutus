package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

// RunInto runs an agent call and unmarshals the response Content
// into out. out must be a non-nil pointer to a JSON-decodable value.
// Suitable for ad-hoc agent dispatches where the caller has the
// AgentDef in hand and wants the response decoded directly.
func RunInto(ctx context.Context, exec AgentExecutor, def AgentDef, input AgentInput, out any) error {
	resp, err := exec.Run(ctx, def, input)
	if err != nil {
		return err
	}
	return unmarshalAgentOutput(resp.Content, out)
}

// RunIntoWithRetry layers RunInto over RunWithRetry, for callers
// that want exponential backoff on rate-limit / timeout errors.
func RunIntoWithRetry(ctx context.Context, exec AgentExecutor, def AgentDef, input AgentInput, cfg RetryConfig, out any) error {
	resp, err := RunWithRetry(ctx, exec, def, input, cfg)
	if err != nil {
		return err
	}
	return unmarshalAgentOutput(resp.Content, out)
}

func unmarshalAgentOutput(content string, out any) error {
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("parse agent response: %w (content=%q)", err, content)
	}
	return nil
}
