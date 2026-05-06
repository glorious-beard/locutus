package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnmarshalAgentOutput covers the defensive JSON extraction
// the prompt-only schema path depends on. Strict-mode adapters
// produce clean JSON and exercise only the fast path; the slow
// path matters when the model wraps its output (typical Gemini
// fallback when API-level schema enforcement is unavailable, e.g.
// schema + grounding).
func TestUnmarshalAgentOutput(t *testing.T) {
	type sample struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}

	cases := []struct {
		name    string
		input   string
		want    sample
		wantErr bool
	}{
		{
			name:  "clean json",
			input: `{"name":"a","tags":["x","y"]}`,
			want:  sample{Name: "a", Tags: []string{"x", "y"}},
		},
		{
			name:  "leading and trailing whitespace",
			input: "  \n  {\"name\":\"a\",\"tags\":[]}  \n",
			want:  sample{Name: "a", Tags: []string{}},
		},
		{
			name:  "json fenced block with language tag",
			input: "```json\n{\"name\":\"a\",\"tags\":[\"x\"]}\n```",
			want:  sample{Name: "a", Tags: []string{"x"}},
		},
		{
			name:  "fenced block without language tag",
			input: "```\n{\"name\":\"a\",\"tags\":[]}\n```",
			want:  sample{Name: "a", Tags: []string{}},
		},
		{
			name:  "prose preamble then json",
			input: "Here's the response:\n\n{\"name\":\"a\",\"tags\":[\"x\"]}\n",
			want:  sample{Name: "a", Tags: []string{"x"}},
		},
		{
			name:  "prose preamble then fenced json",
			input: "Sure, here you go:\n\n```json\n{\"name\":\"a\",\"tags\":[]}\n```\n\nLet me know if that helps.",
			want:  sample{Name: "a", Tags: []string{}},
		},
		{
			name:  "nested objects with strings containing braces",
			input: `{"name":"a {nested}","tags":["x{1}","y"]}`,
			want:  sample{Name: "a {nested}", Tags: []string{"x{1}", "y"}},
		},
		{
			name:    "no json at all",
			input:   "I cannot answer that.",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got sample
			err := unmarshalAgentOutput(tc.input, &got)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExtractJSON_ArrayResponse(t *testing.T) {
	input := "Here's the list:\n\n[1, 2, 3]"
	got, ok := extractJSON(input)
	require.True(t, ok)
	assert.Equal(t, "[1, 2, 3]", got)
}

func TestStripCodeFence_NotFenced(t *testing.T) {
	_, ok := stripCodeFence("plain text")
	assert.False(t, ok)
}
