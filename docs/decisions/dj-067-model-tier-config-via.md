## DJ-067: Model Tier Config via Embedded YAML, List-per-Tier Runtime Resolution (Supersedes DJ-053)

**Status:** shipped

**Decision:** Tier → model mapping moves from hardcoded Go maps into an embedded `internal/agent/models.yaml`. Each `CapabilityTier` holds an ordered list of candidate model strings; `ModelConfig.ResolveTier(tier, providers)` walks the list and returns the first entry whose provider prefix is enabled in `DetectedProviders`. List order is the user's preference when multiple providers match.

**Supersedes DJ-053** (which established three-tier capability routing with hardcoded `DefaultModels` + `GoogleAIDefaultModels` maps). Those maps are removed. The problem with two parallel maps was that provider availability is a runtime fact — a Gemini-only user needs a `googleai/` entry for every tier at *call* time, not at compile time. The new list-per-tier form collapses the two maps into one config and picks at resolution time.

**File format:**

```yaml
tiers:
  fast:
    - googleai/gemini-2.5-flash-lite
    - anthropic/claude-haiku-4-5-20251001
  balanced:
    - googleai/gemini-2.5-flash
    - anthropic/claude-sonnet-4-6
  strong:
    - anthropic/claude-opus-4-7
    - googleai/gemini-2.5-pro
```

**Override path:** set `LOCUTUS_MODELS_CONFIG` to a YAML file with the same shape. Missing file errors loudly (user asked for it — silent fallback would hide typos). Env unset = embedded defaults.

**Refresh on `locutus update`:** deferred, not dropped. The plan is that `locutus update` refreshes the user's local override (when they have one) by keeping their provider-order preference per tier and updating the model names themselves to whatever ships in the newly-embedded defaults. `--freeze-models` opts out. The config loader and resolver are shaped to support this; the merge logic is a small follow-up whose primary design cost was the file format we now have.

**Alternatives considered:** (a) LLM-based periodic classification of model names into tiers — rejected as overkill for current scope (circular: "need a fast model to classify fast models"; nondeterministic; tokens cost). (b) Name-heuristic substring matching (`opus`/`pro` → strong, `haiku`/`flash-lite` → fast) — rejected for V1 because it breaks on naming-convention changes (what is Gemini 3 "Ultra"?). The embedded+override approach wins on simplicity and on matching the natural update cadence: Genkit plugins become aware of new models when the SDK version bumps, which is also when we'd refresh the YAML.

---

Session date: 2026-04-20
