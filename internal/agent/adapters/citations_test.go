package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestExtractGeminiCitations_SnippetsFromGroundingSupports verifies
// the v1.52+ Snippet enrichment: each GroundingSupport's Segment.Text
// (the model's own response excerpt that cites this chunk) lands as
// the Citation.Snippet for the chunks listed in
// GroundingChunkIndices. Multiple supports citing the same chunk
// aggregate their texts joined by " | ". This gives trace forensics
// "what the model said about this URL" instead of an empty Snippet
// (the pre-DJ-132-followup behaviour).
//
// The Go SDK doesn't yet expose the source-side rendered_parts text
// array on GroundingMetadata; once it does, the enrichment swaps
// from segment.text to the underlying source excerpts without
// changing the Citation surface or this test's structure.
func TestExtractGeminiCitations_SnippetsFromGroundingSupports(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			GroundingMetadata: &genai.GroundingMetadata{
				GroundingChunks: []*genai.GroundingChunk{
					{Web: &genai.GroundingChunkWeb{URI: "https://a.example/x", Title: "Source A"}},
					{Web: &genai.GroundingChunkWeb{URI: "https://b.example/y", Title: "Source B"}},
				},
				GroundingSupports: []*genai.GroundingSupport{
					// One assertion cites chunk 0 only.
					{
						Segment:               &genai.Segment{Text: "Postgres 16 ships with native JSONB."},
						GroundingChunkIndices: []int32{0},
					},
					// Another assertion cites both chunks.
					{
						Segment:               &genai.Segment{Text: "Aurora Serverless v2 is now Postgres 16 compatible."},
						GroundingChunkIndices: []int32{0, 1},
					},
					// A third assertion cites chunk 1 only.
					{
						Segment:               &genai.Segment{Text: "Aurora's per-ACU pricing starts at $0.12/hour."},
						GroundingChunkIndices: []int32{1},
					},
				},
			},
		}},
	}

	got := extractGeminiCitations(resp)

	assert.Equal(t, []Citation{
		{
			URL:     "https://a.example/x",
			Title:   "Source A",
			Snippet: "Postgres 16 ships with native JSONB. | Aurora Serverless v2 is now Postgres 16 compatible.",
		},
		{
			URL:     "https://b.example/y",
			Title:   "Source B",
			Snippet: "Aurora Serverless v2 is now Postgres 16 compatible. | Aurora's per-ACU pricing starts at $0.12/hour.",
		},
	}, got)
}

// TestExtractGeminiCitations_SnippetsWithNoSupports verifies the
// pre-enrichment behaviour remains unchanged when the response
// carries chunks but no GroundingSupports. Older preview models and
// some grounding paths emit chunks without supports — Snippet stays
// empty in that case.
func TestExtractGeminiCitations_SnippetsWithNoSupports(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			GroundingMetadata: &genai.GroundingMetadata{
				GroundingChunks: []*genai.GroundingChunk{
					{Web: &genai.GroundingChunkWeb{URI: "https://a.example/x", Title: "Source A"}},
				},
				// No GroundingSupports.
			},
		}},
	}

	got := extractGeminiCitations(resp)

	assert.Equal(t, []Citation{
		{URL: "https://a.example/x", Title: "Source A"},
	}, got)
}

// TestExtractGeminiCitations_DedupeOnURLAggregatesAcrossCandidates
// verifies that when the same URL surfaces across multiple
// candidates, the snippet aggregation folds new texts into the
// first occurrence — and skips duplicates. Same-text-twice does not
// produce a stuttering "X | X" snippet.
func TestExtractGeminiCitations_DedupeOnURLAggregatesAcrossCandidates(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			GroundingMetadata: &genai.GroundingMetadata{
				GroundingChunks: []*genai.GroundingChunk{
					{Web: &genai.GroundingChunkWeb{URI: "https://a.example/x", Title: "Source A"}},
				},
				GroundingSupports: []*genai.GroundingSupport{
					{Segment: &genai.Segment{Text: "Postgres ships with JSONB."}, GroundingChunkIndices: []int32{0}},
				},
			},
		}, {
			GroundingMetadata: &genai.GroundingMetadata{
				GroundingChunks: []*genai.GroundingChunk{
					{Web: &genai.GroundingChunkWeb{URI: "https://a.example/x", Title: "Source A again"}},
				},
				GroundingSupports: []*genai.GroundingSupport{
					{Segment: &genai.Segment{Text: "Postgres ships with JSONB."}, GroundingChunkIndices: []int32{0}}, // identical text
					{Segment: &genai.Segment{Text: "JSONB supports GIN indexes."}, GroundingChunkIndices: []int32{0}},
				},
			},
		}},
	}

	got := extractGeminiCitations(resp)
	require.Len(t, got, 1, "URL appearing across candidates must dedupe to one Citation")
	assert.Equal(t, "https://a.example/x", got[0].URL)
	assert.Equal(t, "Source A", got[0].Title, "first occurrence's Title wins")
	assert.Equal(t, "Postgres ships with JSONB. | JSONB supports GIN indexes.", got[0].Snippet,
		"redundant snippet text is dropped; net-new text from later candidates folds in")
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

// TestExtractAnthropicToolCalls_MixedSuccessAndError replays the shape
// observed in the live winplan justify session that motivated the
// grounding-failure backstop: 5 server_tool_use queries, some with
// successful wrapper-level result blocks, some with only error blocks.
// The function must pair queries to outcomes by tool_use_id and
// classify each as success/error correctly.
func TestExtractAnthropicToolCalls_MixedSuccessAndError(t *testing.T) {
	raw := []byte(`[
	  {"type": "thinking", "thinking": "let me search"},
	  {"type": "server_tool_use", "id": "tu_a", "name": "web_search", "input": {"query": "TanStack Start production ready stable release 2025"}},
	  {"type": "web_search_tool_result_error", "tool_use_id": "tu_a", "error_code": "unavailable"},
	  {"type": "server_tool_use", "id": "tu_b", "name": "web_search", "input": {"query": "Next.js App Router cold start GCP Cloud Run performance 2024 2025"}},
	  {"type": "web_search_tool_result", "tool_use_id": "tu_b", "content": [
	    {"type": "web_search_result", "url": "https://x.example/1", "title": "x"}
	  ]},
	  {"type": "server_tool_use", "id": "tu_c", "name": "web_search", "input": {"query": "Next.js App Router RSC learning curve"}},
	  {"type": "web_search_tool_result", "tool_use_id": "tu_c", "content": [
	    {"type": "web_search_result", "url": "https://y.example/1", "title": "y"}
	  ]},
	  {"type": "server_tool_use", "id": "tu_d", "name": "web_search", "input": {"query": "Next.js standalone output cold start time"}},
	  {"type": "web_search_tool_result_error", "tool_use_id": "tu_d", "error_code": ""},
	  {"type": "server_tool_use", "id": "tu_e", "name": "web_search", "input": {"query": "React Server Components security benefits vs API auth layer"}},
	  {"type": "web_search_tool_result", "tool_use_id": "tu_e", "content": [
	    {"type": "web_search_result", "url": "https://z.example/1", "title": "z"}
	  ]}
	]`)

	got := extractAnthropicToolCalls(raw)

	assert.Equal(t, []ToolCall{
		{Name: "web_search", Query: "TanStack Start production ready stable release 2025", Status: "error", ErrorCode: "unavailable"},
		{Name: "web_search", Query: "Next.js App Router cold start GCP Cloud Run performance 2024 2025", Status: "success"},
		{Name: "web_search", Query: "Next.js App Router RSC learning curve", Status: "success"},
		{Name: "web_search", Query: "Next.js standalone output cold start time", Status: "error"},
		{Name: "web_search", Query: "React Server Components security benefits vs API auth layer", Status: "success"},
	}, got)
}

// TestExtractAnthropicToolCalls_PartialFetchErrorsStillSuccess —
// Anthropic's web_search emits one wrapper-level result block per
// query plus per-URL fetch errors when individual URLs fail.
// Per-URL errors should NOT degrade a query whose wrapper block
// returned at least one usable result; those queries still produced
// retrievable evidence.
func TestExtractAnthropicToolCalls_PartialFetchErrorsStillSuccess(t *testing.T) {
	raw := []byte(`[
	  {"type": "server_tool_use", "id": "tu_x", "name": "web_search", "input": {"query": "q"}},
	  {"type": "web_search_tool_result_error", "tool_use_id": "tu_x", "content": [
	    {"type": "web_search_result", "url": "https://broken.example/page"}
	  ], "error_code": ""},
	  {"type": "web_search_tool_result", "tool_use_id": "tu_x", "content": [
	    {"type": "web_search_result", "url": "https://ok.example/page", "title": "OK"}
	  ]}
	]`)

	got := extractAnthropicToolCalls(raw)
	assert.Equal(t, []ToolCall{
		{Name: "web_search", Query: "q", Status: "success"},
	}, got)
}

// TestExtractAnthropicToolCalls_NoToolUse returns no entries when
// the call didn't invoke any server tools.
func TestExtractAnthropicToolCalls_NoToolUse(t *testing.T) {
	raw := []byte(`[{"type": "text", "text": "hello"}]`)
	assert.Empty(t, extractAnthropicToolCalls(raw))
}

// TestExtractAnthropicToolCalls_None handles nil/empty input
// without panicking.
func TestExtractAnthropicToolCalls_None(t *testing.T) {
	assert.Empty(t, extractAnthropicToolCalls(nil))
	assert.Empty(t, extractAnthropicToolCalls([]byte{}))
}
