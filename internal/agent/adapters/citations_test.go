package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

// TestExtractGeminiCitations_FromGroundingMetadata verifies that
// per-chunk web sources surfaced by GoogleSearch grounding land as
// Citations on the round, deduped on URL.
func TestExtractGeminiCitations_FromGroundingMetadata(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			GroundingMetadata: &genai.GroundingMetadata{
				GroundingChunks: []*genai.GroundingChunk{
					{Web: &genai.GroundingChunkWeb{URI: "https://a.example/x", Title: "A"}},
					{Web: &genai.GroundingChunkWeb{URI: "https://b.example/y", Title: "B"}},
					{Web: &genai.GroundingChunkWeb{URI: "https://a.example/x", Title: "A duplicate"}},
				},
			},
		}},
	}

	got := extractGeminiCitations(resp)

	assert.Equal(t, []Citation{
		{URL: "https://a.example/x", Title: "A"},
		{URL: "https://b.example/y", Title: "B"},
	}, got)
}

func TestExtractGeminiCitations_NoGrounding(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{}},
	}
	assert.Empty(t, extractGeminiCitations(resp))
}

// TestExtractOpenAICitations verifies url_citation annotations on
// output content items are flattened into Citations, deduped on URL.
// The fixture mirrors what the Responses API emits in resp.Output —
// the same JSON shape that lands in Round.Message.
func TestExtractOpenAICitations(t *testing.T) {
	raw := []byte(`[
	  {"type": "message", "content": [
	    {"type": "output_text", "text": "first", "annotations": [
	      {"type": "url_citation", "url": "https://x.example/a", "title": "X"},
	      {"type": "url_citation", "url": "https://y.example/b", "title": "Y"}
	    ]},
	    {"type": "output_text", "text": "second", "annotations": [
	      {"type": "url_citation", "url": "https://x.example/a", "title": "X again"}
	    ]}
	  ]}
	]`)

	got := extractOpenAICitations(raw)

	assert.Equal(t, []Citation{
		{URL: "https://x.example/a", Title: "X"},
		{URL: "https://y.example/b", Title: "Y"},
	}, got)
}

func TestExtractOpenAICitations_None(t *testing.T) {
	assert.Empty(t, extractOpenAICitations(nil))
}

// TestExtractAnthropicCitations covers two surfaces the Messages API
// exposes when grounded with web_search_20250305:
//  1. WebSearchToolResultBlock items with the actual search results.
//  2. text-block citations referencing those results by url+title.
//
// The extractor flattens both into a single deduped Citations list.
// The fixture mirrors the JSON shape captured in Round.Message.
func TestExtractAnthropicCitations(t *testing.T) {
	raw := []byte(`[
	  {
	    "type": "web_search_tool_result",
	    "tool_use_id": "tu_1",
	    "content": [
	      {"type": "web_search_result", "url": "https://w.example/a", "title": "W", "encrypted_content": "enc1"},
	      {"type": "web_search_result", "url": "https://w.example/b", "title": "W2", "encrypted_content": "enc2"}
	    ]
	  },
	  {
	    "type": "text",
	    "text": "Per the source, the answer is...",
	    "citations": [
	      {"type": "web_search_result_location", "url": "https://w.example/a", "title": "W", "cited_text": "the source says X"}
	    ]
	  }
	]`)

	got := extractAnthropicCitations(raw)

	// Both block sources resolve to two unique URLs; the first
	// occurrence (in the tool-result block) wins on title/snippet.
	assert.Equal(t, []Citation{
		{URL: "https://w.example/a", Title: "W"},
		{URL: "https://w.example/b", Title: "W2"},
	}, got)
}

func TestExtractAnthropicCitations_None(t *testing.T) {
	assert.Empty(t, extractAnthropicCitations(nil))
}
