# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Locutus — a Go CLI and MCP server that acts as an autonomous project manager for spec-driven software. It maintains a persistent spec graph (`Goal → Decision → (Feature | Strategy) → Approach` — decisions inform features and strategies; approaches are the synthesis layer for coding agents over features and strategies. Axes are surfaced from goals, features, and strategies and resolved by decisions; see DJ-124), produces execution plans, delegates coding to external agents, and supervises their output. The spec is the source of truth; artifacts are derived outputs.

## Sources of Truth

- **Decision IDs are axis-shaped (DJ-133).** Every decision's `id` equals `dec-` followed by the primary axis ID it answers verbatim (axis `oltp-store` → `dec-oltp-store`). The id names the *question*; `title`, `chosen_option`, and `rationale` carry the human-readable *answer*. A revision that flips the chosen option keeps the same id — backreferences from features, strategies, and approaches stay byte-stable across Flips. The merge step matches revise dispatches against prior decisions by exact id equality (the retired pre-DJ-133 path matched by axis intersection).
- **The in-process `SpecStore` is the source of truth for spec graph reads and writes during a session (DJ-134).** `internal/agent/spec_store.go` holds typed entries tagged by origin (settled / proposed) and a working flag under one RWMutex, with a write-through Bluge search index. The RAG tools (spec_list_manifest, spec_get, spec_search), CLI verbs (explain, justify), and the council's merge helpers all read and write through it. `.borg/spec/` is its persistence backing, not a parallel data source. `spec_get` is batched by design — input is `{ids: [string]}`, output is `{results: {id: {status, body?, working?, reason?}}, available_ids?, working?}` with status in `settled | in_flight | missing`. There is no scalar-id variant; batching is structural. The pre-DJ-134 swappable wrapper stack (SwappableSpecSearch, SwappableSpecListManifest, SwappableSpecGet, InFlightSpecStore, fsSpecProvider) is retired.
- **Tool descriptions live in registration, not prompts (DJ-134).** When an agent prompt references a tool (spec_*, future write tools), the prompt's job is workflow guidance — when to reach for the tool and why, in the context of the agent's task. Tool-behavior text (field shapes, kind-prefix routing, in-flight-vs-settled semantics, batched-input contract) lives in the `ToolDef.Description` and `InputSchema.properties[].description` passed to `RegisterSpecTools`. See `docs/agent-conventions.md` § "Tool descriptions live in registration, not prompts."
- `docs/DECISION_JOURNAL.md` — architectural decisions with rationale, alternatives considered, and reversals. Authoritative design record.
- `.claude/plans/` — active implementation plans (current consolidation work is in `verb-set-phase-{a,b,c,d}.md`). Copy to `docs/plans/` once a phase stabilises.
- `docs/agent-conventions.md` — documented anti-patterns and conventions for agent prompt files. **Read this before editing or creating any file under `internal/scaffold/agents/`.** It captures lessons we've re-learned multiple times (anti-pattern priming, thinking-leakage, schema-skeleton placeholders) and the prefer-positive-phrasing + push-constraints-to-schema-tags patterns that replace them.
- `docs/debugging-traces.md` — operational guide for walking session traces and OTel spans when an LLM-driven verb misbehaves. Covers the per-step folder layout (DJ-130), the per-call YAML / OTel correlation, common failure patterns, and one-liners for grepping concerns / extracting structured responses.
- `docs/council.md` — workflow diagram + per-agent reference for the spec-generation council (and the other verb-level workflows that reuse council agents). Read this when reasoning about which agent runs when, what schema each emits, or which DJ governs a given step.

When these documents conflict with any other file in the repo, `docs/` and `.claude/plans/` win.

## Rule: Structs used as LLM response shapes MUST carry `jsonschema` tags

If a Go struct is registered via `RegisterSchema` (or otherwise travels into an `OutputSchema` on an `adapters.Request`), every meaningful field MUST carry an invopop/jsonschema struct tag with enough detail to prevent the degenerate-output failure modes documented in `docs/agent-conventions.md`:

- **Every enum-shaped string field** carries `jsonschema:"enum=v1,enum=v2,enum=v3"`. Without this, strict-mode providers don't constrain the decoder and we depend entirely on the model's prose-following. Validators that catch enum drift after the fact (`degenerateSynthesisVerdict`, etc.) are the second line of defence, not the first.
- **Every field with semantic constraints** (must-be-non-empty, must-be-a-sentence, must-cite-a-real-source, must-not-be-placeholder) carries `jsonschema:"description=..."` naming the constraint in language the model will read on every call. The description travels into the schema doc every adapter sends to its provider's structured-output mode. This is load-bearing — schema-skeleton failures ("dummy" placeholders, one-word answers) trace back to fields with no inline guidance.
- **Required-non-empty arrays** carry `jsonschema:"minItems=1"` (or higher). Empty arrays for things like `Concerns` or `Decisions` are a known degenerate-output mode.
- **Fields whose enum/constraint set is too dynamic for the tag** (e.g. ids that must match an input list) carry a description that names the constraint and points the model at where to find the legal values.

`RegisterSchema` example payloads (the value passed as the second arg) must use **descriptive prose** for example field values — never `"dummy"`, `"placeholder"`, `"TBD"`, `"foo"`. The example payload is rendered into the system prompt as "what a valid response looks like"; placeholder tokens prime the schema-skeleton failure the validator exists to catch.

The library is `github.com/invopop/jsonschema` v0.13.0 (per DJ-118). Tag syntax is key=value, comma-separated. Don't use `github.com/google/jsonschema-go` syntax (bare-string-as-description) — it produces silent no-ops in invopop.

When adding a new struct to the response-shape set: walk the field list, ask "if the model gives me garbage in this field, would the user know what went wrong?", and tag every field where the answer is "only because we wrote a validator." Push the validator's constraint into the schema. The validator becomes a safety net for the rare cases that slip through, not the primary enforcement.

## LLM Layering Invariant (DJ-130)

The workflow expresses intent; the dispatcher orchestrates retry/rotation/observability; the adapter handles provider-specific mechanics including any internal multi-call workaround. Observability follows the provider-call boundary on both surfaces (OTel and YAML).

Concretely:

- **Workflow** ([internal/agent/workflow.go](internal/agent/workflow.go)) emits one `Dispatcher.Dispatch` call per agent step. It knows nothing about which provider serves the call, whether the call splits, or how token cost is computed.
- **Dispatcher** ([internal/agent/dispatcher.go](internal/agent/dispatcher.go)) handles provider rotation, corrective retry, and ReAct iteration. From its perspective every adapter Run is one logical SDK call (even when the adapter splits internally).
- **Adapter** ([internal/agent/adapters/](internal/agent/adapters/)) handles provider-specific mechanics: native structured output via `OutputConfig.Format.Schema` (Anthropic, DJ-108), `responseJsonSchema` (Gemini), `json_schema strict:true` (OpenAI Responses); the multi-round tool-use loop; AND the thinking + schema split via `requiresThinkingSchemaSplit` → `runSplit`. Each adapter's `runSplit` issues two SDK calls back-to-back (reasoning pass keeps tools + grounding; format pass strips them and runs against the provider's own `fast:` tier from models.yaml).
- **Recorder** ([internal/agent/session.go](internal/agent/session.go)) opens a parent `step.yaml` when `LoggingExecutor.Run` is invoked and plumbs an `adapters.CallRecorder` bridge onto ctx; each adapter then writes one per-SDK-call YAML per real provider round-trip into the step's folder. Per-call YAMLs and OTel `provider.generate` spans now agree on what constitutes a "call." See [docs/debugging-traces.md](docs/debugging-traces.md) for the operational guide to walking session traces.

The per-deployer `format_providers:` rotation in `models.yaml` is retired — each adapter handles its own provider's fast tier for the format pass, no cross-provider rotation. A stale `format_providers:` block left in a user-edited `models.yaml` is silently ignored by the parser.

## Cognitive Task Separation (DJ-130 + DJ-132)

When one LLM call is asked to do two cognitive tasks that conflict in the attention budget, separate them into sequential calls that each focus on one task. The principle shows up at two layers:

- **DJ-130 separates reasoning from formatting at the adapter layer.** A thinking-on agent with a strict-mode schema dispatches as two SDK round-trips: a reasoning pass (thinking on, schema cleared, tools + grounding kept) and a format pass (thinking off, schema set, tools stripped, fast-tier). The split prevents thinking-output corruption in structured fields and keeps reasoning attention from being crowded out by JSON-shape attention.
- **DJ-132 separates enumeration from judgment at the workflow layer.** The decision-elaboration fanout gains a per-axis `spec_candidate_survey` pre-step (fast tier, grounded) that enumerates the candidate space; the decision-elaborator runs after with the candidate list as input and spends its attention budget on judgment. The split prevents commit-mode attention from crowding out exhaustive option-space exploration; initial alternatives slices ship with 6-10 entries instead of 1-2.

Both are instances of the same pattern. Future cognitive-task conflations should be diagnosed with the same lens: is one LLM call carrying two tasks whose attention demands fight each other? If yes, split into sequential calls. The cost of an extra call amortizes against the avoided work the cross-task-suppression was previously generating.

## Command Surface

The verb set splits into 8 mutating/operational verbs plus 2 read-only deliberation aids (DJ-101).

**Mutating and operational (8):**

1. `locutus init` — Bootstrap `.borg/` scaffold.
2. `locutus update` — Refresh binary and embedded defaults.
3. `locutus import <source>` — Admit a new feature/bug with GOALS.md triage.
4. `locutus refine <node>` — Council-driven deliberation on any spec node. Flags: `--brief "..."` threads a focused refinement intent through to the dedicated `refiner` agent (DJ-102); `--diff` prints a unified diff over the rendered Markdown after the rewrite; `--rollback` undoes the most recent refine using the prior bytes captured in `.borg/history/`.
5. `locutus assimilate` — Infer or update spec from code.
6. `locutus adopt` — Bring code into alignment with spec (reconcile loop).
7. `locutus status` — Show state, drift, and validation errors. With `--full` emits a comprehensive snapshot of the spec graph (DJ-100).
8. `locutus history` — Query the past-tense record. `--narrative` auto-regenerates the LLM-authored summary from `.borg/history/evt-*.json` (committed events) into `.locutus/history/summary.md` (gitignored cache) when the event hash diverges (DJ-103).

**Read-only deliberation aids (2):**

- `locutus explain <id>` — Render a single spec node's rationale, alternatives, citations, and back-references. No LLM.
- `locutus justify <id> [--against "..."]` — Spec advocate writes an active defense; `--against` runs the challenger first for an adversarial dialogue.

Every mutating verb supports `--dry-run`. `locutus mcp` starts the MCP server; `locutus mcp-perm-bridge` is a hidden internal subprocess. Every CLI verb has MCP parity.

## Build & Test

```bash
go build ./...
go test ./...
go test ./path/to/pkg                  # single package
go test ./path/to/pkg -run TestName    # single test
go vet ./...
go test ./... -race                    # race detector
```

## Libraries

- **CLI**: `github.com/alecthomas/kong`
- **YAML**: `gopkg.in/yaml.v3`
- **Testing**: `github.com/stretchr/testify/assert`
- **Console output**: `github.com/pterm/pterm`
- **Logging**: `log/slog` (stdlib)
- **LLM**: direct-SDK adapters per provider (DJ-099) — `github.com/anthropics/anthropic-sdk-go`, `google.golang.org/genai`, `github.com/openai/openai-go` (Responses API).
