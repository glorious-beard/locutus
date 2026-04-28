package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

// GenerateInto runs an LLM call with provider-enforced JSON output and
// unmarshals the response into out. The schema is derived from out's type by
// the LLM implementation (see GenKitLLM, which uses ai.WithOutputType).
//
// out must be a non-nil pointer to a JSON-decodable value. Any caller-set
// req.OutputSchema is overwritten with out so the provider's structured-output
// mode and the unmarshal target stay in sync.
func GenerateInto(ctx context.Context, llm LLM, req GenerateRequest, out any) error {
	req.OutputSchema = out
	resp, err := llm.Generate(ctx, req)
	if err != nil {
		return err
	}
	return unmarshalLLMOutput(resp.Content, out)
}

// GenerateIntoWithRetry is GenerateInto layered over GenerateWithRetry, for
// callers that want exponential backoff on rate-limit / timeout errors.
func GenerateIntoWithRetry(ctx context.Context, llm LLM, req GenerateRequest, cfg RetryConfig, out any) error {
	req.OutputSchema = out
	resp, err := GenerateWithRetry(ctx, llm, req, cfg)
	if err != nil {
		return err
	}
	return unmarshalLLMOutput(resp.Content, out)
}

func unmarshalLLMOutput(content string, out any) error {
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("parse llm response: %w (content=%q)", err, content)
	}
	return nil
}
