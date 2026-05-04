package cmd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupAdoptSynthFixture builds a project FS where one feature has no
// approach attached. Adopt should synthesize one on demand at run time.
func setupAdoptSynthFixture(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/state", 0o755))

	feat := spec.Feature{
		ID: "feat-bare", Title: "Bare feature",
		Status:    spec.FeatureStatusActive,
		Decisions: []string{"dec-x"},
		// No Approaches — adopt is responsible for synthesizing one.
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-bare", feat, "Bare feature body."))
	return fs
}

func TestAdoptSynthesizesMissingApproachOnDryRun(t *testing.T) {
	fs := setupAdoptSynthFixture(t)

	// Mock a synthesizer reply matching the cascade.RewriteResult schema.
	synthBody, _ := json.Marshal(map[string]any{
		"revised_body": "## Approach\n\nImplementation sketch.",
		"changed":      true,
		"rationale":    "synthesized from feature prose",
	})
	llm := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: string(synthBody)},
	})

	report, err := RunAdoptWithConfig(context.Background(), AdoptConfig{
		FS:     fs,
		LLM:    llm,
		DryRun: true,
	})
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, []string{"app-feat-bare"}, report.SynthesizedApproaches,
		"adopt must synthesize one approach per parent that has none")
	assert.Equal(t, 1, llm.CallCount(),
		"one synthesizer call per missing approach")

	// Dry-run drops writes — the approach .md must not exist on disk.
	_, err = fs.ReadFile(".borg/spec/approaches/app-feat-bare.md")
	assert.Error(t, err, "dry-run must not persist synthesized approach")
}

func TestAdoptSynthesizesMissingApproachAndPersists(t *testing.T) {
	fs := setupAdoptSynthFixture(t)

	synthBody, _ := json.Marshal(map[string]any{
		"revised_body": "## Approach\n\nImplementation sketch.",
		"changed":      true,
		"rationale":    "synthesized",
	})
	llm := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: string(synthBody)},
	})

	report, err := RunAdoptWithConfig(context.Background(), AdoptConfig{
		FS:     fs,
		LLM:    llm,
		DryRun: false,
	})
	require.NoError(t, err)
	require.NotNil(t, report)

	require.Equal(t, []string{"app-feat-bare"}, report.SynthesizedApproaches)

	// Approach .md should be persisted.
	body, err := fs.ReadFile(".borg/spec/approaches/app-feat-bare.md")
	require.NoError(t, err, "synthesized approach must be persisted to disk")
	assert.Contains(t, string(body), "Implementation sketch")

	// Parent feature should now reference the new approach.
	updated, _, err := specio.LoadPair[spec.Feature](fs, ".borg/spec/features/feat-bare")
	require.NoError(t, err)
	assert.Equal(t, []string{"app-feat-bare"}, updated.Approaches,
		"parent must carry the new approach in its approaches[]")

	// Classification should now show the new approach as unplanned.
	require.Len(t, report.Classifications, 1)
	assert.Equal(t, "app-feat-bare", report.Classifications[0].Approach.ID)
}

func TestAdoptSkipsSynthesisWhenParentAlreadyHasApproach(t *testing.T) {
	// Pre-existing approach: synthesis should NOT fire.
	fs := setupAdoptFixture(t)

	llm := agent.NewMockLLM() // no responses scripted; any call would fail

	report, err := RunAdoptWithConfig(context.Background(), AdoptConfig{
		FS:     fs,
		LLM:    llm,
		DryRun: true,
	})
	require.NoError(t, err)
	assert.Empty(t, report.SynthesizedApproaches,
		"parents with existing approaches must not trigger synthesis")
	assert.Equal(t, 0, llm.CallCount(),
		"no LLM calls when nothing needs synthesizing")
}

func TestAdoptSynthesizeRespectsScope(t *testing.T) {
	// Two bare features; scope to one. Only that feature gets a
	// synthesized approach.
	fs := setupAdoptSynthFixture(t)
	other := spec.Feature{
		ID: "feat-other", Title: "Other", Status: spec.FeatureStatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-other", other, "Other body."))

	synthBody, _ := json.Marshal(map[string]any{
		"revised_body": "sketch",
		"changed":      true,
		"rationale":    "x",
	})
	llm := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: string(synthBody)},
	})

	report, err := RunAdoptWithConfig(context.Background(), AdoptConfig{
		FS:     fs,
		LLM:    llm,
		Scope:  "feat-bare",
		DryRun: true,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"app-feat-bare"}, report.SynthesizedApproaches,
		"only the in-scope parent should synthesize")
	assert.Equal(t, 1, llm.CallCount())
}
