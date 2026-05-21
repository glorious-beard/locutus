package adapters

// CanonicalFormatterPrompt is the system prompt the format pass of the
// DJ-130 thinking + schema split uses. Each adapter's runSplit hands
// it to the SDK alongside the reasoning pass's prose response as the
// sole user message.
//
// Deliberately narrow — extraction, not analysis. Mirrors the prompt
// used in the formatter reliability eval at output_formatter_eval_test
// that scored 6/6 on Haiku and gpt-5-mini before the dispatcher-level
// split was retired in favour of per-adapter mechanics. Moving the
// constant from internal/agent (the dispatcher layer) into the
// adapters package keeps the layering honest: the prompt is part of
// the adapter's split mechanic, not a dispatcher concern.
const CanonicalFormatterPrompt = `You receive a reasoning agent's prose output. Extract the structured content into a JSON object matching the supplied schema.

Rules:
- Do not reason about the underlying topic. Your job is extraction; not analysis.
- Do not invent facts that aren't in the prose. If a field has no source in the prose; omit it (when optional) or copy the closest matching content.
- Preserve specifics: vendor names; version numbers; citations; quoted text.
- Each schema field receives the corresponding content from the prose.`

// mergeSplitResponses composes the final Response from the
// reason+format pair. The format pass's structured Content is the
// agent's contract; reasoning-side state (the thinking text, tool
// calls, citations, rounds) carries forward so trace consumers see
// the full picture; token counts sum across both calls.
//
// Used by each adapter's runSplit; centralised here so the merge
// semantics stay consistent across providers (a Gemini split should
// look the same as an Anthropic split from the trace consumer's POV).
func mergeSplitResponses(reasoning, formatted *Response) *Response {
	if formatted == nil {
		return reasoning
	}
	merged := *formatted
	if reasoning == nil {
		return &merged
	}
	if reasoning.Reasoning != "" {
		merged.Reasoning = reasoning.Reasoning
	}
	if len(reasoning.ToolCalls) > 0 {
		merged.ToolCalls = reasoning.ToolCalls
	}
	if len(reasoning.Citations) > 0 {
		merged.Citations = reasoning.Citations
	}
	if len(reasoning.Rounds) > 0 {
		merged.Rounds = reasoning.Rounds
	}
	merged.InputTokens += reasoning.InputTokens
	merged.OutputTokens += reasoning.OutputTokens
	merged.ThoughtsTokens += reasoning.ThoughtsTokens
	merged.TotalTokens += reasoning.TotalTokens
	merged.CacheCreationInputTokens += reasoning.CacheCreationInputTokens
	merged.CacheReadInputTokens += reasoning.CacheReadInputTokens
	return &merged
}
