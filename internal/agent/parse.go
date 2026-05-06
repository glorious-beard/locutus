package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// unmarshalAgentOutput decodes content as JSON into out. When the
// model wrapped its output in a markdown code fence (```json … ```)
// or in surrounding prose despite a "no fences" prompt directive,
// the fast path fails — the slow path tries to extract a JSON
// object/array span and unmarshal that. This is belt-and-suspenders
// for prompt-only schema enforcement paths (e.g. Gemini's
// schema+grounding case where API-level strict mode is unavailable);
// strict-mode-enforced paths produce clean JSON and take the fast
// path on the first try.
func unmarshalAgentOutput(content string, out any) error {
	trimmed := strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(trimmed), out); err == nil {
		return nil
	}
	if extracted, ok := extractJSON(trimmed); ok {
		if err := json.Unmarshal([]byte(extracted), out); err == nil {
			return nil
		}
	}
	if err := json.Unmarshal([]byte(trimmed), out); err != nil {
		return fmt.Errorf("parse agent response: %w (content=%q)", err, content)
	}
	return nil
}

// extractJSON locates the first JSON object or array in s and
// returns its substring. Strips ```json … ``` fences when present;
// falls back to a brace/bracket-balanced scan from the first
// opening token. Returns ok=false when no plausible JSON span is
// found, in which case the caller should surface the original
// parse error.
func extractJSON(s string) (string, bool) {
	if fenced, ok := stripCodeFence(s); ok {
		return fenced, true
	}
	// Find first '{' or '[' and walk to its matching close. Naive
	// brace-counting that respects string literals is enough for
	// model output; we don't need full JSON tokenisation.
	start := -1
	open := byte(0)
	close := byte(0)
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			start, open, close = i, '{', '}'
			break
		}
		if s[i] == '[' {
			start, open, close = i, '[', ']'
			break
		}
	}
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if inString {
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == open {
			depth++
		} else if c == close {
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// stripCodeFence detects a markdown ```json … ``` (or unlabelled
// ``` … ```) block and returns its inner text. Tolerates leading /
// trailing whitespace and a label line. Returns ok=false when the
// input isn't fence-wrapped.
func stripCodeFence(s string) (string, bool) {
	if !strings.HasPrefix(s, "```") {
		return "", false
	}
	// Skip the opening fence and optional language tag (first line).
	rest := s[3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		return "", false
	}
	end := strings.LastIndex(rest, "```")
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:end]), true
}
