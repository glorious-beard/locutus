package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHistoryFirstLine(t *testing.T) {
	assert.Equal(t, "first", firstLine("first\nsecond"))
	assert.Equal(t, "only", firstLine("only"))
	assert.Equal(t, "", firstLine(""))
}
