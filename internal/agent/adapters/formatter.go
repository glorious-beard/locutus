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

// ReasoningPassProseDirective is the trailing user message each
// adapter's runSplit appends to the reasoning pass's projected input.
// Tells the model that JSON output is handled by the downstream
// format pass — its job is to think out loud in prose. Without this,
// models default to JSON for analytical tasks regardless of how the
// agent prompt is phrased: spec-scout's iter-0 thinking-on call on
// Gemini 3 Pro Preview emitted a `​```json` code-fenced response even
// with OutputSchema=nil and the prompt scrubbed of "produce a single
// X object" framings, because the prompt's section-name structure
// (and Gemini's strong-tier behavioral default) primed JSON output.
// Explicit guidance overrides the default.
//
// Placed as the last user message so it's the most recent instruction
// the model reads before generating; Cacheable=false because it's
// guidance, not content (no cache value in marking it). Universal
// across providers — Anthropic and OpenAI exhibit milder versions of
// the same JSON-mode bias on thinking-on + analytical tasks.
const ReasoningPassProseDirective = "Make sure to output well-formatted prose, not JSON. I'll convert it to JSON on your behalf in a later step."

// buildReasoningPassMessages assembles the user-message list the
// reasoning pass receives. Two optional layers wrap the projected
// input:
//
//   - Prose example (when exampleProse is non-empty): a Cacheable
//     user message that goes FIRST, demonstrating the kind of
//     section-anchored content the reasoner should produce. Per-agent
//     content; hits the cache on every iteration of the same agent.
//   - Prose directive: a Cacheable=false trailing user message
//     (ReasoningPassProseDirective) that fires LAST so the model
//     reads "produce prose, not JSON" most recently before
//     generating.
//
// The projected input sits between them, carrying whatever
// Cacheable markers the upstream layer set. Used by each adapter's
// runSplit; centralised here so the directive + example layering
// stays consistent across providers.
func buildReasoningPassMessages(exampleProse string, in []Message) []Message {
	out := make([]Message, 0, len(in)+2)
	if exampleProse != "" {
		out = append(out, Message{
			Role:      RoleUser,
			Content:   "## Example output shape\n\n" + exampleProse,
			Cacheable: true,
		})
	}
	out = append(out, in...)
	out = append(out, Message{Role: RoleUser, Content: ReasoningPassProseDirective})
	return out
}

// buildFormatPassMessages composes the user-message list each
// adapter's runSplit hands to the format pass. Layered:
//
//   - Prose example (when exampleProse is non-empty): a Cacheable
//     user message showing the kind of reasoning-prose input the
//     formatter receives. Per-agent; hits the cache on every
//     iteration of the same agent.
//   - JSON example (when exampleDoc is non-empty): a Cacheable user
//     message showing the kind of structured output the formatter
//     produces. Paired with the prose example above, this is a
//     one-shot input → output demonstration.
//   - Reasoning prose: the per-call trailing user message
//     (Cacheable=false) carrying the actual reasoning pass's output
//     to extract from.
//
// CanonicalFormatterPrompt stays as the universal Layer-1 cache
// prefix in the system position; the prose+JSON example layers stack
// as Layer-2 per-agent cache hits; the reasoning prose is the only
// per-call uncacheable segment.
//
// When both example fields are empty (RegisterSchemaOverride paths
// with no Go example), the list collapses to a single reasoning-prose
// user message — no example layers, but the formatter prompt still
// drives shape via the OutputSchema struct tags the provider's
// strict-mode enforcement reads.
func buildFormatPassMessages(exampleProse, exampleDoc, reasoningProse string) []Message {
	var msgs []Message
	if exampleProse != "" {
		msgs = append(msgs, Message{
			Role:      RoleUser,
			Content:   "## Example reasoning-prose input\n\n" + exampleProse,
			Cacheable: true,
		})
	}
	if exampleDoc != "" {
		msgs = append(msgs, Message{
			Role:      RoleUser,
			Content:   "## Example structured output for the prose above\n\n```json\n" + exampleDoc + "\n```\n",
			Cacheable: true,
		})
	}
	msgs = append(msgs, Message{Role: RoleUser, Content: reasoningProse})
	return msgs
}

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
