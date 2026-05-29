package mcp

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckRuntimeVersion_WarnsBelowFloor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	// checkRuntimeVersion loads the runtimepolicy registry and logs a
	// warning when the version is below floor. nil fsys → embedded defaults.
	checkRuntimeVersion(logger, nil, "claude-code", "2.1.100")
	assert.Contains(t, buf.String(), "below the minimum")
	assert.Contains(t, buf.String(), "2.1.154")
}

func TestCheckRuntimeVersion_SilentAtOrAboveFloor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	checkRuntimeVersion(logger, nil, "claude-code", "2.2.0")
	assert.Empty(t, buf.String())
}

func TestCheckRuntimeVersion_FailOpenOnUnparseable(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	checkRuntimeVersion(logger, nil, "claude-code", "weird")
	assert.Empty(t, buf.String())
}
