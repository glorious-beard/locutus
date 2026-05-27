## DJ-105: Elaborator `decisions` Is API-Layer Required, Not Prompt-Layer Required

**Status:** shipped

**Decision:** Tighten both `RawFeatureProposal` and `RawStrategyProposal` so the strict-mode JSON schema requires `decisions` with `minItems=1`, and remove the contradicting "or omit `decisions` entirely" escape hatch from both elaborator scaffolds. Strict-mode-conformant providers (Gemini responseSchema, OpenAI json_schema strict, Anthropic forced tool-use) will now reject decision-less responses at the API layer; the executor's retry loop kicks in instead of the malformed proposal flowing through to reconcile and dying at the spec validator.

**Why:** a `refine goals` run on the winplan project failed with six dangling-reference errors after two revise rounds — every error was "feature has no decisions; every feature must commit to at least one architectural choice." The session traces revealed two distinct pathologies that converged:

1. *Decision-omitting outputs* (e.g., session call `0004-spec_feature_elaborator-feat-campaign-dashboard.yaml`): the model's thinking transcript planned two real decisions, then the final JSON emitted only `id`/`title`/`description` with no `decisions` field. `finishReason: STOP`, 14-second duration, 102 output tokens — the model voluntarily ended.
2. *Mid-stream-redo-into-string-field* (e.g., session call `0035-spec_feature_elaborator-feat-win-calculator.yaml`): the model started emitting valid JSON, mid-stream realized it had spilled prose into the title field, wrote `"Wait, I shouldn't add prose. Let me start over"` and then a complete corrected JSON document — all captured *as part of the title string value*, with no escape from the structural commitment. Top-level object ended without `decisions`.

Two contributing factors made the pathologies survivable in the pipeline:

- The struct tag `Decisions []InlineDecisionProposal json:"decisions,omitempty"` made the field optional in the strict-mode schema. The reflector at [`internal/agent/schema.go`](../internal/agent/schema.go) computes the `required` array from `omitempty` tags via `RequiredFromJSONSchemaTags: false`, so the API contract didn't enforce presence.
- The elaborator prompts carried mutually contradictory rules four lines apart: *"Every feature MUST have at least one inline decision"* and *"Emit real, complete inline decisions or omit `decisions` entirely (and reconsider whether the feature belongs)."* The second rule gave the model a permission slip the first rule denied. Under uncertainty, `gemini-3.1-pro-preview` repeatedly took the easier path.

**Why API-layer enforcement, not post-receive validation:** strict-mode JSON schema rejection happens server-side at the provider, *before* a non-conformant response is returned to us. The executor's retry loop re-issues the call, and the model has to produce a conformant output to satisfy the API. Post-receive validation in the elaborator dispatch path would also work but is downstream of where the cost is paid: the model has already burned thinking tokens and output tokens on a malformed response, and the operator sees the failure as a soft error rather than an automatic retry. Schema enforcement is structural; post-receive validation is reactive.

**Why this isn't a re-introduction of the failures DJ-090's follow-up warned about:** that warning targeted *aspirational fields in LLM output schemas* — fields downstream code didn't consume, which created shape pressure on weaker models to fill them with degenerate content (the named precedent was the Span citation removal). DJ-105 does not add a field. It tightens the constraint on a field that downstream code (reconciler, validator, render) already requires for the spec to be well-formed. The shape pressure is unchanged in cardinality; the API just stops accepting empty arrays where empty arrays were never going to survive the rest of the pipeline anyway.

**What lands:**

- `RawFeatureProposal.Decisions` and `RawStrategyProposal.Decisions` lose `omitempty` and gain `jsonschema:"minItems=1"`. The reflector emits `required: ["...", "decisions"]` and `decisions.minItems: 1` in the generated schema.
- Both elaborator scaffolds delete the "or omit `decisions` entirely" clause and replace it with positive guidance: when a complete decision genuinely cannot be authored, emit a minimal "Defer architectural commitment" decision so the critic can route the feature/strategy for removal — but always emit a conformant response.
- `TestRawProposalSchemasRequireDecisions` in [`internal/agent/raw_proposal_schema_test.go`](../internal/agent/raw_proposal_schema_test.go) locks in the schema requirement against accidental regression (e.g., someone re-introducing `omitempty`).
- `TestElaboratorPromptsForbidDecisionsOmission` in [`internal/scaffold/scaffold_test.go`](../internal/scaffold/scaffold_test.go) locks in the prompt-level change.

**What stays the same:**

- The reconciler ([internal/agent/reconcile.go](../internal/agent/reconcile.go)) still passes `Citations` and `Decisions` through unchanged. The integrity validator catches dangling references as a defense-in-depth backstop, but the API-layer enforcement makes that path much rarer.
- The architect's own `RawSpecProposal` shape (different from the elaborator's per-node shapes). The architect operates pre-fanout; this DJ scopes to the per-node elaborators only.
- The acceptance_criteria field on RawFeatureProposal stays optional (`omitempty`). It's a quality-of-life field, not load-bearing for spec integrity.

**Reversal criteria:** revert if (a) the strict schema causes legitimate retries to spiral on edge cases where a model genuinely cannot produce a decision (would surface as repeated retry failures on the same node — at which point the prompt needs better guidance on the "Defer architectural commitment" escape pattern, not relaxed schema); or (b) a future architecture genuinely needs decision-less proposal nodes (would mean the elaborator's contract has changed and this DJ should be replaced rather than relaxed). Neither is structural; both are addressable in the same files.

**Reference:** governed by DJ-085 (decision-citation completeness), DJ-090's follow-up (no aspirational fields — and what that rule actually targets vs. what it doesn't). Companion to DJ-104 (citation-kind expansion). Distinct from the timeout / fallback bugs surfaced in the same session — `gemini`-side 504 misclassification and per-call timeout wrapping the entire fallback walk are real but separate concerns; the symptom user-visible from the failed run was DJ-105's pathology, not those.
