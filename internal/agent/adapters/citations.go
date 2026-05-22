package adapters

import (
	"encoding/json"
	"strings"

	"google.golang.org/genai"
)

// mergeCitations appends cs into dst, skipping any URLs already
// present in dst. Used by adapters to roll up per-round citations
// into Response.Citations across a multi-round tool-use loop.
func mergeCitations(dst, cs []Citation) []Citation {
	if len(cs) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, c := range dst {
		seen[c.URL] = struct{}{}
	}
	for _, c := range cs {
		if c.URL == "" {
			continue
		}
		if _, dup := seen[c.URL]; dup {
			continue
		}
		seen[c.URL] = struct{}{}
		dst = append(dst, c)
	}
	return dst
}

// extractGeminiCitations walks all candidates' GroundingMetadata and
// flattens the GroundingChunks[].Web entries into Citations. Dedupes
// on URL — Gemini emits the same chunk multiple times when several
// text spans cite it, and the trace only needs one row per source.
//
// Snippet enrichment (genai v1.52+ surface):
//
// Each GroundingSupport ties a model assertion (Segment) to a list of
// GroundingChunkIndices (which sources back the claim) plus
// RenderedParts (indices into a source-side rendered_parts text
// array on GroundingMetadata). The source-side rendered_parts array
// itself is NOT yet exposed on the Go SDK's GroundingMetadata struct
// as of v1.58 — only the indices on GroundingSupport are typed. So
// the richest source-side excerpt the Go API gives us today is
// Segment.Text: the assistant's own response span that cites this
// chunk. We populate Citation.Snippet from those segment-texts
// (joined by " | " when multiple supports cite the same chunk) so
// trace forensics gets "the model said X about this URL" instead of
// the bare URL.
//
// When the Go SDK exposes the underlying rendered_parts text array on
// GroundingMetadata, the enrichment swaps from segment.text to
// rendered_parts[support.RenderedParts[...]] for true source-side
// excerpts. The Citation surface stays the same; only the snippet
// source changes.
func extractGeminiCitations(resp *genai.GenerateContentResponse) []Citation {
	if resp == nil {
		return nil
	}
	var out []Citation
	seen := make(map[string]int) // URL → out[] index for snippet aggregation
	for _, cand := range resp.Candidates {
		if cand == nil || cand.GroundingMetadata == nil {
			continue
		}
		// Build chunk_index → []snippet from the supports. Each
		// support cites one or more chunks via GroundingChunkIndices
		// and carries the model's own response excerpt in
		// Segment.Text. We walk supports first so we have the
		// snippets ready when we emit Citations below.
		snippetByChunk := make(map[int32][]string)
		for _, sup := range cand.GroundingMetadata.GroundingSupports {
			if sup == nil || sup.Segment == nil {
				continue
			}
			text := strings.TrimSpace(sup.Segment.Text)
			if text == "" {
				continue
			}
			for _, idx := range sup.GroundingChunkIndices {
				snippetByChunk[idx] = append(snippetByChunk[idx], text)
			}
		}
		for i, ch := range cand.GroundingMetadata.GroundingChunks {
			if ch == nil || ch.Web == nil || ch.Web.URI == "" {
				continue
			}
			snippet := strings.Join(snippetByChunk[int32(i)], " | ")
			if existingIdx, dup := seen[ch.Web.URI]; dup {
				// Same URL cited in a later candidate — fold its
				// snippets into the first occurrence. Keeps Citations
				// deduped on URL while still aggregating per-URL
				// provenance across candidates.
				if snippet != "" {
					out[existingIdx].Snippet = mergeSnippet(out[existingIdx].Snippet, snippet)
				}
				continue
			}
			seen[ch.Web.URI] = len(out)
			out = append(out, Citation{URL: ch.Web.URI, Title: ch.Web.Title, Snippet: snippet})
		}
	}
	return out
}

// mergeSnippet joins two snippet strings deduping on identical
// per-piece text while preserving first-seen order. Both inputs are
// formatted as " | "-joined per-piece text (the within-candidate
// aggregation in extractGeminiCitations); merging across candidates
// must split, dedupe, and rejoin to avoid stuttering the same model
// claim N times in the trace.
func mergeSnippet(existing, incoming string) string {
	if existing == "" {
		return incoming
	}
	if incoming == "" {
		return existing
	}
	seen := make(map[string]struct{})
	var out []string
	for _, piece := range strings.Split(existing+" | "+incoming, " | ") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		if _, dup := seen[piece]; dup {
			continue
		}
		seen[piece] = struct{}{}
		out = append(out, piece)
	}
	return strings.Join(out, " | ")
}

// extractOpenAICitations parses raw output-array JSON (as captured in
// Round.Message) and flattens url_citation annotations into Citations
// deduped on URL. Operating on bytes rather than the SDK type avoids
// brittleness when the discriminated-union output items round-trip
// through the typed shape.
func extractOpenAICitations(rawOutput []byte) []Citation {
	if len(rawOutput) == 0 {
		return nil
	}
	var items []struct {
		Type    string `json:"type"`
		Content []struct {
			Annotations []struct {
				Type  string `json:"type"`
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"annotations"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rawOutput, &items); err != nil {
		return nil
	}
	var out []Citation
	seen := make(map[string]struct{})
	for _, item := range items {
		for _, c := range item.Content {
			for _, a := range c.Annotations {
				if a.Type != "url_citation" || a.URL == "" {
					continue
				}
				if _, dup := seen[a.URL]; dup {
					continue
				}
				seen[a.URL] = struct{}{}
				out = append(out, Citation{URL: a.URL, Title: a.Title})
			}
		}
	}
	return out
}

// extractAnthropicToolCalls walks an Anthropic content-array JSON and
// pairs every server_tool_use block with its matching
// web_search_tool_result / web_search_tool_result_error block by
// tool_use_id, returning one ToolCall per invocation. Anthropic emits
// the request and response as separate top-level blocks within the
// same message; this matches them so the caller sees a flat per-call
// summary instead of two parallel streams to correlate.
//
// Sources of truth for status:
//
//   - "error" when ANY result block tied to this tool_use_id has type
//     web_search_tool_result_error. Anthropic emits multiple result
//     blocks per query (one per URL fetched + one wrapper); a single
//     error in the wrapper is the signal we treat as call-level
//     failure because it means the model received no usable content.
//   - "success" when at least one wrapper-level result block carries a
//     non-empty result array.
//   - "error" with empty error code when no result block was emitted
//     at all (Anthropic dropped the response). Rare but defensible to
//     treat as failure rather than success.
//
// The granularity is "did this query return retrievable evidence?"
// which is what RunResearch needs to decide whether the model has
// grounding to cite from. Per-URL fetch errors inside an otherwise-
// successful query are not surfaced separately — the model still got
// content from the surviving URLs.
func extractAnthropicToolCalls(rawContent []byte) []ToolCall {
	if len(rawContent) == 0 {
		return nil
	}
	type contentItem struct {
		Type string `json:"type"`
	}
	var blocks []struct {
		Type string `json:"type"`
		// server_tool_use fields
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input struct {
			Query string `json:"query"`
		} `json:"input"`
		// web_search_tool_result / web_search_tool_result_error fields
		ToolUseID string        `json:"tool_use_id"`
		ErrorCode string        `json:"error_code"`
		Content   []contentItem `json:"content"`
	}
	if err := json.Unmarshal(rawContent, &blocks); err != nil {
		return nil
	}

	type usage struct {
		name    string
		query   string
		ok      bool
		errored bool
		errCode string
	}
	byID := make(map[string]*usage)
	var order []string

	for _, b := range blocks {
		switch b.Type {
		case "server_tool_use":
			if b.ID == "" {
				continue
			}
			if _, exists := byID[b.ID]; !exists {
				order = append(order, b.ID)
			}
			byID[b.ID] = &usage{name: b.Name, query: b.Input.Query}
		case "web_search_tool_result":
			u, ok := byID[b.ToolUseID]
			if !ok {
				continue
			}
			// Wrapper-level success block: at least one
			// web_search_result inside means the query produced
			// retrievable content.
			for _, c := range b.Content {
				if c.Type == "web_search_result" {
					u.ok = true
					break
				}
			}
		case "web_search_tool_result_error":
			u, ok := byID[b.ToolUseID]
			if !ok {
				continue
			}
			u.errored = true
			if u.errCode == "" {
				u.errCode = b.ErrorCode
			}
		}
	}

	out := make([]ToolCall, 0, len(order))
	for _, id := range order {
		u := byID[id]
		tc := ToolCall{Name: u.name, Query: u.query}
		switch {
		case u.ok && !u.errored:
			tc.Status = "success"
		case u.ok && u.errored:
			// Mixed: at least one wrapper-level success block was
			// returned, but errors also fired. Treat as success —
			// the model received usable content for this query.
			tc.Status = "success"
		default:
			tc.Status = "error"
			tc.ErrorCode = u.errCode
		}
		out = append(out, tc)
	}
	return out
}

// extractAnthropicCitations parses a raw content-array JSON (as
// captured in Round.Message) and flattens both web_search_tool_result
// blocks (the actual search hits) and text-block citations (in-prose
// attributions referencing those hits) into Citations deduped on URL.
// The first occurrence wins for Title/Snippet; tool-result blocks
// usually appear before text blocks that cite them, so the richer
// Title from the result wins over the text-block citation which
// mostly carries cited_text.
func extractAnthropicCitations(rawContent []byte) []Citation {
	if len(rawContent) == 0 {
		return nil
	}
	var blocks []struct {
		Type    string `json:"type"`
		Content []struct {
			Type             string `json:"type"`
			URL              string `json:"url"`
			Title            string `json:"title"`
			EncryptedContent string `json:"encrypted_content"`
		} `json:"content"`
		Citations []struct {
			Type      string `json:"type"`
			URL       string `json:"url"`
			Title     string `json:"title"`
			CitedText string `json:"cited_text"`
		} `json:"citations"`
	}
	if err := json.Unmarshal(rawContent, &blocks); err != nil {
		return nil
	}
	var out []Citation
	seen := make(map[string]struct{})
	add := func(url, title, snippet string) {
		if url == "" {
			return
		}
		if _, dup := seen[url]; dup {
			return
		}
		seen[url] = struct{}{}
		out = append(out, Citation{URL: url, Title: title, Snippet: snippet})
	}
	for _, b := range blocks {
		if b.Type == "web_search_tool_result" {
			for _, r := range b.Content {
				if r.Type == "web_search_result" {
					add(r.URL, r.Title, "")
				}
			}
		}
		for _, c := range b.Citations {
			add(c.URL, c.Title, c.CitedText)
		}
	}
	return out
}
