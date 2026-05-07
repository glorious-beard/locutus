package adapters

import (
	"encoding/json"

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
func extractGeminiCitations(resp *genai.GenerateContentResponse) []Citation {
	if resp == nil {
		return nil
	}
	var out []Citation
	seen := make(map[string]struct{})
	for _, cand := range resp.Candidates {
		if cand == nil || cand.GroundingMetadata == nil {
			continue
		}
		for _, ch := range cand.GroundingMetadata.GroundingChunks {
			if ch == nil || ch.Web == nil || ch.Web.URI == "" {
				continue
			}
			if _, dup := seen[ch.Web.URI]; dup {
				continue
			}
			seen[ch.Web.URI] = struct{}{}
			out = append(out, Citation{URL: ch.Web.URI, Title: ch.Web.Title})
		}
	}
	return out
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
