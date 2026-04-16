package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegisterAndRoute(t *testing.T) {
	reg := NewRegistry()

	goAgent := AgentCapability{
		ID:            "agent-go",
		Name:          "Go Coder",
		Languages:     []string{"go"},
		Frameworks:    []string{"stdlib"},
		MaxConcurrent: 2,
	}
	tsAgent := AgentCapability{
		ID:            "agent-ts",
		Name:          "TS Coder",
		Languages:     []string{"typescript"},
		Frameworks:    []string{"react"},
		MaxConcurrent: 3,
	}

	reg.Register(goAgent)
	reg.Register(tsAgent)

	got, err := reg.Route("go")
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, "agent-go", got.ID)
	assert.Equal(t, "Go Coder", got.Name)

	got, err = reg.Route("typescript")
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, "agent-ts", got.ID)
	assert.Equal(t, "TS Coder", got.Name)
}

func TestRouteUnknown(t *testing.T) {
	reg := NewRegistry()

	goAgent := AgentCapability{
		ID:        "agent-go",
		Name:      "Go Coder",
		Languages: []string{"go"},
	}
	reg.Register(goAgent)

	got, err := reg.Route("rust")
	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "rust")
}

func TestRouteDefaultFallback(t *testing.T) {
	reg := NewRegistry()

	specificAgent := AgentCapability{
		ID:        "agent-go",
		Name:      "Go Coder",
		Languages: []string{"go"},
	}
	fallbackAgent := AgentCapability{
		ID:            "agent-any",
		Name:          "Universal Coder",
		Languages:     []string{"*"},
		MaxConcurrent: 1,
	}

	reg.Register(specificAgent)
	reg.Register(fallbackAgent)

	// Specific match still wins over wildcard.
	got, err := reg.Route("go")
	assert.NoError(t, err)
	assert.Equal(t, "agent-go", got.ID)

	// Unknown domain falls back to wildcard agent.
	got, err = reg.Route("anything")
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, "agent-any", got.ID)
	assert.Equal(t, "Universal Coder", got.Name)

	// Another unknown domain also falls back.
	got, err = reg.Route("haskell")
	assert.NoError(t, err)
	assert.Equal(t, "agent-any", got.ID)
}
