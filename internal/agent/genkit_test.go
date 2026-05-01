package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func clearProviderEnv(t *testing.T) {
	t.Helper()
	// t.Setenv restores the original after the test. Use an explicit empty
	// value so DetectProviders sees "not set."
	t.Setenv(EnvKeyAnthropicAPI, "")
	t.Setenv(EnvKeyGeminiAPI, "")
	t.Setenv(EnvKeyGoogleAPI, "")
	t.Setenv(EnvKeyLocutusModel, "")
}

func TestDetectProviders_None(t *testing.T) {
	clearProviderEnv(t)
	p := DetectProviders()
	assert.False(t, p.Any(), "no providers configured")
	assert.False(t, p.Anthropic)
	assert.False(t, p.GoogleAI)
	assert.Empty(t, p.Names())
}

func TestDetectProviders_AnthropicOnly(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(EnvKeyAnthropicAPI, "sk-ant-dummy")

	p := DetectProviders()
	assert.True(t, p.Anthropic)
	assert.False(t, p.GoogleAI)
	assert.Equal(t, []string{"anthropic"}, p.Names())
}

func TestDetectProviders_GeminiKey(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(EnvKeyGeminiAPI, "AIz-dummy")

	p := DetectProviders()
	assert.True(t, p.GoogleAI, "GEMINI_API_KEY alone should enable Google AI")
	assert.False(t, p.Anthropic)
	assert.Equal(t, []string{"googleai"}, p.Names())
}

func TestDetectProviders_GoogleAPIKeyFallback(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(EnvKeyGoogleAPI, "AIz-dummy")

	p := DetectProviders()
	assert.True(t, p.GoogleAI,
		"GOOGLE_API_KEY alone should enable Google AI — Genkit's plugin checks both env names")
}

func TestDetectProviders_Both(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(EnvKeyAnthropicAPI, "sk-ant-dummy")
	t.Setenv(EnvKeyGeminiAPI, "AIz-dummy")

	p := DetectProviders()
	assert.True(t, p.Anthropic)
	assert.True(t, p.GoogleAI)
	assert.Equal(t, []string{"anthropic", "googleai"}, p.Names(),
		"Names() should list in a stable order")
}

func TestLLMAvailable_MirrorsDetectProviders(t *testing.T) {
	clearProviderEnv(t)
	assert.False(t, LLMAvailable())

	t.Setenv(EnvKeyGeminiAPI, "x")
	assert.True(t, LLMAvailable())
}

func TestPickDefaultModel(t *testing.T) {
	// Expectations track the embedded models.yaml tier list. If you
	// reorder tier entries there, expect to re-sync these strings.
	// The balanced tier lists googleai/ first (cheap default when
	// both providers are configured); the strong tier flips the
	// order. This test covers only pickDefaultModel (balanced).
	cases := []struct {
		name     string
		p        DetectedProviders
		want     string
		contains string
	}{
		{"anthropic only", DetectedProviders{Anthropic: true}, "anthropic/claude-sonnet-4-6", "anthropic/"},
		{"google only", DetectedProviders{GoogleAI: true}, "googleai/gemini-2.5-flash", "googleai/"},
		{"both prefers google for balanced tier", DetectedProviders{Anthropic: true, GoogleAI: true}, "googleai/gemini-2.5-flash", "googleai/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pickDefaultModel(c.p)
			assert.Equal(t, c.want, got)
			assert.Contains(t, got, c.contains, "default should have the expected provider prefix")
		})
	}
}

func TestResolveModel(t *testing.T) {
	bothProviders := &GenKitLLM{
		defaultModel: "anthropic/claude-opus-4-7",
		providers:    DetectedProviders{Anthropic: true, GoogleAI: true},
	}
	googleOnly := &GenKitLLM{
		defaultModel: "googleai/gemini-2.5-flash",
		providers:    DetectedProviders{GoogleAI: true},
	}

	cases := []struct {
		name      string
		llm       *GenKitLLM
		requested string
		want      string
	}{
		{"empty uses default", bothProviders, "", "anthropic/claude-opus-4-7"},
		{"no prefix uses default", bothProviders, "gpt-4", "anthropic/claude-opus-4-7"},
		{"anthropic prefix passes through", bothProviders, "anthropic/claude-sonnet-4-6", "anthropic/claude-sonnet-4-6"},
		{"googleai prefix passes through", bothProviders, "googleai/gemini-2.5-pro", "googleai/gemini-2.5-pro"},
		{"anthropic prefix rewrites when only google registered", googleOnly, "anthropic/claude-opus-4-7", "googleai/gemini-2.5-flash"},
		{"unknown prefix uses default", googleOnly, "openai/gpt-4o", "googleai/gemini-2.5-flash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.llm.resolveModel(c.requested))
		})
	}
}

func TestBuildProviderConfig(t *testing.T) {
	t.Run("googleai always sets MaxOutputTokens to default", func(t *testing.T) {
		// Gemini's API default is too small for the spec-generation
		// architect once the proposal has multiple deliverables; we
		// always supply a default cap so structured-output validation
		// doesn't reject truncated JSON.
		got := buildProviderConfig("googleai/gemini-2.5-flash", GenerateRequest{})
		cfg, ok := got.(*genai.GenerateContentConfig)
		require.True(t, ok, "expected *genai.GenerateContentConfig, got %T", got)
		assert.Equal(t, int32(defaultGoogleAIMaxOutputTokens), cfg.MaxOutputTokens)
	})

	t.Run("googleai populates GenerateContentConfig", func(t *testing.T) {
		got := buildProviderConfig("googleai/gemini-2.5-flash", GenerateRequest{
			Temperature: 0.5,
			MaxTokens:   1024,
		})
		cfg, ok := got.(*genai.GenerateContentConfig)
		require.True(t, ok, "expected *genai.GenerateContentConfig, got %T", got)
		require.NotNil(t, cfg.Temperature)
		assert.InDelta(t, 0.5, *cfg.Temperature, 1e-6)
		assert.Equal(t, int32(1024), cfg.MaxOutputTokens)
	})

	t.Run("anthropic always sets MaxTokens to satisfy plugin", func(t *testing.T) {
		got := buildProviderConfig("anthropic/claude-sonnet-4-6", GenerateRequest{})
		cfg, ok := got.(*anthropicsdk.MessageNewParams)
		require.True(t, ok, "expected *anthropicsdk.MessageNewParams, got %T", got)
		assert.Equal(t, int64(defaultAnthropicMaxTokens), cfg.MaxTokens,
			"plugin rejects MaxTokens == 0; default must kick in when caller omits it")
	})

	t.Run("anthropic respects explicit MaxTokens", func(t *testing.T) {
		got := buildProviderConfig("anthropic/claude-sonnet-4-6", GenerateRequest{
			MaxTokens: 2048,
		})
		cfg, ok := got.(*anthropicsdk.MessageNewParams)
		require.True(t, ok)
		assert.Equal(t, int64(2048), cfg.MaxTokens)
	})

	t.Run("unknown provider returns nil", func(t *testing.T) {
		got := buildProviderConfig("openai/gpt-4o", GenerateRequest{Temperature: 0.5})
		assert.Nil(t, got)
	})
}

func TestLLMCallTimeout(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		t.Setenv(EnvKeyLocutusLLMTimeout, "")
		assert.Equal(t, DefaultLLMCallTimeout, llmCallTimeout())
	})
	t.Run("env override", func(t *testing.T) {
		t.Setenv(EnvKeyLocutusLLMTimeout, "90s")
		assert.Equal(t, 90*time.Second, llmCallTimeout())
	})
	t.Run("zero disables", func(t *testing.T) {
		t.Setenv(EnvKeyLocutusLLMTimeout, "0")
		assert.Equal(t, time.Duration(0), llmCallTimeout(),
			"0 should pass through so callers can opt out of the cap")
	})
	t.Run("invalid falls back to default", func(t *testing.T) {
		t.Setenv(EnvKeyLocutusLLMTimeout, "garbage")
		assert.Equal(t, DefaultLLMCallTimeout, llmCallTimeout())
	})
}

func TestToGenkitRole(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"user", "user"},
		{"system", "system"},
		{"assistant", "model"},
		{"model", "model"},
		{"", "user"},
		{"unknown", "user"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, string(toGenkitRole(c.in)), "role %q", c.in)
	}
}

// loadRepoEnv walks up from the test's working directory looking for
// .env and loads it into the environment if found. `go test` doesn't
// run main.go, so the CLI's godotenv.Load() never fires — without this
// helper, the live smoke test would skip when keys live only in .env.
// Never overrides values already present in the env.
func loadRepoEnv(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			if err := godotenv.Load(candidate); err != nil {
				t.Logf("loadRepoEnv: %s: %v", candidate, err)
			}
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

// TestGenKitLLM_LiveSmoke is an opt-in end-to-end call against whichever
// provider is configured in .env. Gated behind LOCUTUS_INTEGRATION_TEST=1
// so unit runs don't require network or burn tokens. Also gated behind
// a present API key — no point running this if the user hasn't set one
// up yet.
func TestGenKitLLM_LiveSmoke(t *testing.T) {
	if os.Getenv("LOCUTUS_INTEGRATION_TEST") != "1" {
		t.Skip("set LOCUTUS_INTEGRATION_TEST=1 to run this live LLM call")
	}
	loadRepoEnv(t)
	if !LLMAvailable() {
		t.Skip("no LLM provider configured; set GEMINI_API_KEY or ANTHROPIC_API_KEY in .env")
	}

	llm, err := NewGenKitLLM()
	require.NoError(t, err, "NewGenKitLLM should succeed when at least one provider is configured")
	require.NotNil(t, llm)
	t.Logf("providers=%v default_model=%s", llm.Providers().Names(), llm.DefaultModel())

	ctx := context.Background()
	resp, err := llm.Generate(ctx, GenerateRequest{
		Messages: []Message{
			{Role: "user", Content: "Reply with exactly the word 'pong' and nothing else."},
		},
		Temperature: 0.0,
	})
	require.NoError(t, err, "live Generate call")
	require.NotNil(t, resp)
	t.Logf("response model=%q content=%q", resp.Model, resp.Content)
	assert.NotEmpty(t, resp.Content)
	// Most models will produce "pong" possibly surrounded by whitespace or
	// trivial punctuation. Lowercase substring match is lenient but still
	// validates the round-trip actually produced a relevant answer.
	assert.Contains(t, stringsToLower(resp.Content), "pong",
		"expected response to contain 'pong' (case-insensitive); got %q", resp.Content)
}

// stringsToLower is a tiny wrapper so the live test doesn't reach into
// the strings package just for one call.
func stringsToLower(s string) string {
	// Avoid importing "strings" just for this one spot (import keeps the
	// test file tight). Pragma: ASCII-only models for now.
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
