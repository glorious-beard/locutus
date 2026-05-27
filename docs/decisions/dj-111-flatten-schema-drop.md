## DJ-111: Flatten `ReconciliationVerdict` Schema (Drop `oneOf`)

**Status:** shipped

**Decision:** Replace the `actions[]` discriminated-union (`oneOf` of three variants — `dedupe`, `resolve_conflict`, `reuse_existing` — each with its own required-fields set) with a single flat object schema. The flat shape has `kind` (enum of the three variants) and `sources` as the only required fields; `canonical`, `loser`, `rejected_because`, and `existing_id` are all optional at the schema layer. Per-kind required fields move to prompt guidance (`spec_reconciler.md` already documents them) and apply-time validation (the switch in `reconcile.go:243+` already validates per-kind).

**Why:** Anthropic's native `output_config.format.schema` API rejects `oneOf` constructs. The spec_reconciler dispatch produced a 400 — *"output_config.format.schema: Schema type 'oneOf' is not supported"* — every time it routed to Anthropic (which is now first per DJ-107). The DJ-098-era comment that said *"Strict-mode adapters (Anthropic forced tool-use, Gemini responseJsonSchema, OpenAI json_schema strict) all honor oneOf with enum discriminants"* was true under the legacy forced-tool path; the migration to native output_config (DJ-108) lost the oneOf support specifically on Anthropic, while Gemini and OpenAI still accept it.

**Why flat instead of provider-specific schema:** considered and rejected. A per-provider schema would mean maintaining two shapes (oneOf for Gemini/OpenAI, flat for Anthropic), choosing per-dispatch which to send, and doubling the schema-test surface. The cost-benefit is wrong: the Go-side `ReconciliationAction` is already a flat struct ([reconcile.go:127-134](../internal/agent/reconcile.go#L127-L134)) with all variant-specific fields tagged `omitempty`, the apply switch already does per-kind validation, and the spec_reconciler prompt already documents per-kind requirements. The "loss of strict per-kind required fields at the API layer" is purely on the LLM input side; consumer code never relied on it.

**Why this isn't a regression to "the model emits dedupe without canonical" (the bug DJ-098-era oneOf was preventing):** the original failure was strict-mode JSON enforcement gone permissive — `omitempty` on every Go field made the reflected schema treat everything as optional, so the model was free to produce empty actions. That issue is now addressed at three layers stacked together:

1. The schema's `kind` enum still constrains the discriminator: the model can't emit a typo.
2. The spec_reconciler prompt explicitly documents per-kind requirements (lines 36-38: *"emit it as `canonical`"*, *"emit the rejected decision as `loser`"*, etc.).
3. The apply switch in `reconcile.go` validates per-kind and warns on mismatches (currently warns + skips; could promote to hard error if needed).

So the structural defense against malformed actions is preserved, just reshuffled across layers.

**Provider compatibility matrix:**

| Provider | `oneOf` in JSON Schema |
| --- | --- |
| Anthropic forced tool-use input_schema | accepted (legacy) |
| **Anthropic native `output_config.format`** | **rejected (current)** |
| Gemini `responseJsonSchema` | accepted |
| OpenAI `json_schema` strict | accepted |

The flat shape works uniformly across all three providers.

**What lands:**

- [`buildReconciliationVerdictSchema`](../internal/agent/schemas.go) emits a single object schema for `actions[].items` instead of an `oneOf` of three variants.
- [`TestReconciliationVerdictFlatSchema`](../internal/agent/schemas_reconcile_test.go) replaces the previous `TestReconciliationVerdictDiscriminatedSchema` — same shape of test, different assertions matching the new schema.
- [`TestReconciliationVerdictSchema_NoOneOf`](../internal/agent/reconciliation_schema_test.go) walks the schema tree and asserts `oneOf` is absent at any nesting depth, locking in the cross-provider compatibility.

**What stays the same:**

- `ReconciliationAction` Go struct unchanged.
- `reconcile.go` apply switch unchanged. Per-kind validation already lived there.
- `spec_reconciler.md` prompt unchanged — already documents per-kind requirements.

**Reversal criteria:** revert if (a) Anthropic adds `oneOf` support to `output_config.format.schema` AND (b) measurement shows the flat schema is producing more malformed actions than the discriminated-union shape did. Both conditions would have to hold; (a) without (b) means flatness is gratuitous but harmless, (b) without (a) means we have no provider-uniform alternative. Currently neither is true.

**Reference:** triggered by `spec_reconciler` 400 in winplan session 20260507/1522/28-5d9ba2. Reverses the DJ-098-era oneOf decision specifically for Anthropic compatibility under DJ-108's native structured-output path. The "per-kind enforcement migrates to prompt + apply-time" posture matches the rest of the codebase's general pattern (most agent outputs aren't discriminated-union-validated at the schema layer either).
