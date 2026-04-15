package frontmatter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type testFM struct {
	ID     string `yaml:"id"`
	Title  string `yaml:"title"`
	Status string `yaml:"status"`
}

func TestParseBasic(t *testing.T) {
	input := "---\nid: feat-1\ntitle: My Feature\nstatus: draft\n---\nThis is the body.\n"

	var fm testFM
	body, err := Parse([]byte(input), &fm)

	assert.NoError(t, err)
	assert.Equal(t, "feat-1", fm.ID)
	assert.Equal(t, "My Feature", fm.Title)
	assert.Equal(t, "draft", fm.Status)
	assert.Equal(t, "This is the body.\n", body)
}

func TestParseNoFrontmatter(t *testing.T) {
	input := "Just some plain markdown content.\nNo delimiters here.\n"

	var fm testFM
	body, err := Parse([]byte(input), &fm)

	assert.NoError(t, err)
	assert.Equal(t, testFM{}, fm)
	assert.Equal(t, input, body)
}

func TestParseEmptyFrontmatter(t *testing.T) {
	input := "---\n---\nBody here"

	var fm testFM
	body, err := Parse([]byte(input), &fm)

	assert.NoError(t, err)
	assert.Equal(t, testFM{}, fm)
	assert.Equal(t, "Body here", body)
}

func TestParseEmptyBody(t *testing.T) {
	input := "---\nid: feat-2\ntitle: No Body\nstatus: active\n---\n"

	var fm testFM
	body, err := Parse([]byte(input), &fm)

	assert.NoError(t, err)
	assert.Equal(t, "feat-2", fm.ID)
	assert.Equal(t, "No Body", fm.Title)
	assert.Equal(t, "active", fm.Status)
	assert.Equal(t, "", body)
}

func TestRenderBasic(t *testing.T) {
	fm := testFM{ID: "feat-3", Title: "Rendered", Status: "done"}
	bodyIn := "Some body text.\n"

	out, err := Render(fm, bodyIn)

	assert.NoError(t, err)

	expected := "---\nid: feat-3\ntitle: Rendered\nstatus: done\n---\nSome body text.\n"
	assert.Equal(t, expected, string(out))
}

func TestRoundTrip(t *testing.T) {
	original := testFM{ID: "feat-4", Title: "Round Trip", Status: "review"}
	originalBody := "Body survives the round trip.\n"

	rendered, err := Render(original, originalBody)
	assert.NoError(t, err)

	var parsed testFM
	parsedBody, err := Parse(rendered, &parsed)

	assert.NoError(t, err)
	assert.Equal(t, original, parsed)
	assert.Equal(t, originalBody, parsedBody)
}
