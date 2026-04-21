package cmd

import (
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveNodeKindFromFeature(t *testing.T) {
	fs := setupDiffFS(t)
	kind, err := resolveNodeKind(fs, "feat-auth")
	require.NoError(t, err)
	assert.Equal(t, spec.KindFeature, kind)
}

func TestResolveNodeKindFromDecision(t *testing.T) {
	fs := setupDiffFS(t)
	kind, err := resolveNodeKind(fs, "dec-lang")
	require.NoError(t, err)
	assert.Equal(t, spec.KindDecision, kind)
}

func TestResolveNodeKindFromApproach(t *testing.T) {
	fs := setupDiffFS(t)
	kind, err := resolveNodeKind(fs, "app-auth")
	require.NoError(t, err)
	assert.Equal(t, spec.KindApproach, kind)
}

func TestResolveNodeKindUnknown(t *testing.T) {
	fs := setupDiffFS(t)
	_, err := resolveNodeKind(fs, "nonexistent")
	assert.Error(t, err)
}

func TestRefineDryRunRendersBlastRadius(t *testing.T) {
	fs := setupDiffFS(t)
	// Smoke: renderRefineDryRun prints to stdout; just confirm no error for a
	// known Decision target.
	out := captureStdout(func() {
		err := renderRefineDryRun(fs, "dec-lang", spec.KindDecision)
		require.NoError(t, err)
	})
	assert.Contains(t, out, "cascade preview")
}
