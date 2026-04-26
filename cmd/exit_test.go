package cmd_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/chetan/locutus/cmd"
	"github.com/stretchr/testify/assert"
)

func TestExitCodeImplementsError(t *testing.T) {
	var err error = cmd.ExitCode(2)
	assert.EqualError(t, err, "exit 2")
}

func TestIsExitCodeUnwrapsThroughErrorsWrap(t *testing.T) {
	wrapped := fmt.Errorf("adopt: %w", cmd.ExitCode(2))
	code, ok := cmd.IsExitCode(wrapped)
	assert.True(t, ok)
	assert.Equal(t, 2, code)
}

func TestIsExitCodeReturnsFalseForOtherErrors(t *testing.T) {
	code, ok := cmd.IsExitCode(errors.New("plain"))
	assert.False(t, ok)
	assert.Equal(t, 0, code)
}

func TestIsExitCodeReturnsFalseForNil(t *testing.T) {
	code, ok := cmd.IsExitCode(nil)
	assert.False(t, ok)
	assert.Equal(t, 0, code)
}

func TestIsExitCodePreservesZero(t *testing.T) {
	code, ok := cmd.IsExitCode(cmd.ExitCode(0))
	assert.True(t, ok)
	assert.Equal(t, 0, code)
}
