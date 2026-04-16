package agent

import (
	"context"
	"fmt"
	"os"
)

// EnvKeyAnthropicAPI is the environment variable for the Anthropic API key.
const EnvKeyAnthropicAPI = "ANTHROPIC_API_KEY"

// GenKitLLM implements the LLM interface using the Genkit Go SDK.
// Currently a placeholder that validates configuration; the actual Genkit
// wiring is added when the genkit-ai/genkit dependency is integrated.
//
// For now, this provides the env-var-based configuration and model resolution
// that the MCP server and CLI commands use to determine LLM availability.
type GenKitLLM struct {
	APIKey string
	Model  string
}

// NewGenKitLLM creates a GenKitLLM from environment variables.
// Returns an error if ANTHROPIC_API_KEY is not set.
func NewGenKitLLM() (*GenKitLLM, error) {
	key := os.Getenv(EnvKeyAnthropicAPI)
	if key == "" {
		return nil, fmt.Errorf("%s not set — configure an Anthropic API key to enable LLM features", EnvKeyAnthropicAPI)
	}

	model := os.Getenv("LOCUTUS_MODEL")
	if model == "" {
		model = DefaultModel
	}

	return &GenKitLLM{
		APIKey: key,
		Model:  model,
	}, nil
}

// Generate calls the LLM provider. This is a placeholder that returns an error
// until the Genkit SDK is wired in. The MCP server and CLI commands check for
// LLM availability before calling this.
func (g *GenKitLLM) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	// TODO: Wire to genkit-ai/genkit/go with Anthropic plugin.
	// For now, this validates that the configuration is correct.
	return nil, fmt.Errorf("GenKit LLM provider not yet wired — API key is configured but genkit-ai/genkit integration is pending")
}

// LLMAvailable returns true if an LLM provider can be configured from
// the current environment. Does not validate the key — just checks presence.
func LLMAvailable() bool {
	return os.Getenv(EnvKeyAnthropicAPI) != ""
}
