package cmd

import (
	"fmt"

	"github.com/chetan/locutus/internal/agent"
)

// getLLM returns a Genkit-backed LLM for commands that need it. Returns a
// typed error when no provider is configured so callers can render a friendly
// message; production commands should guard against this case and point the
// user at the env vars Genkit reads.
func getLLM() (agent.LLM, error) {
	if !agent.LLMAvailable() {
		return nil, fmt.Errorf(
			"no LLM provider configured: set %s, %s, or %s",
			agent.EnvKeyAnthropicAPI, agent.EnvKeyGeminiAPI, agent.EnvKeyGoogleAPI,
		)
	}
	return agent.NewGenKitLLM()
}
