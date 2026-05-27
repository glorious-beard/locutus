## DJ-107: Council Model-Tier Audit + Anthropic-First Provider Order

**Status:** shipped

**Decision:** Three coordinated changes to agent frontmatter across the 32-agent council:

1. **Tier downgrades on four agents.** `spec_feature_elaborator`, `spec_strategy_elaborator`, `spec_scout`, and `guide` move from strong → balanced. Strong tier is now reserved for `spec_architect` and `spec_reconciler` only.
2. **Provider order flipped to anthropic-first** on every agent whose primary work is structured-output generation or critique (~25 of 32 agents). Cost-first ordering (`googleai → anthropic → openai`) is retained only for mechanical / cost-sensitive agents — `historian`, `monitor`, `convergence` — and for `spec_reconciler` which was already anthropic-first.
3. **`cost_critic` gains `grounding: true`** with prompt guidance to use search for verifying current pricing/free-tier limits/vendor lifecycle, since training-data pricing ages quickly. Brings the count of grounded agents to three (`spec_scout`, `researcher`, `cost_critic`).

**Why the tier downgrades:**

The two elaborators are the council's biggest fanout (15-25 calls per `refine goals`). Per-feature / per-strategy elaboration is a medium-complexity strict-JSON task — committing on architectural shape with rationale, alternatives, and citations — not deep multi-step reasoning. Sonnet 4.6 (balanced) handles this shape well; Opus 4.7 / o3-pro / `gemini-3.1-pro-preview` (strong) are paying a substantial cost premium for a marginal capability premium that doesn't materially change output quality on this task. Combined with DJ-106's prompt caching, the cost compression on the elaborator fanout is the single biggest operational win in this set.

`spec_scout` runs once per refine and produces a `ScoutBrief` (domain read + technology options + implicit assumptions + watch-outs). It needs grounding (per DJ-093) but doesn't need strong-tier reasoning — `gemini-3-flash-preview` supports grounding+schema together and is more stable under strict JSON than `gemini-3.1-pro-preview` (which exhibited the title-stuffing pathology in the winplan run that produced DJ-105). Downgrade is both a quality and cost win.

`guide` is a conversational/narrative role; strong tier is paying for reasoning depth the agent doesn't use.

**Why anthropic-first provider order on most agents:**

Three observed-evidence reasons:

1. The strict-JSON pathology that killed the winplan `refine goals` run (DJ-105) was on `gemini-3.1-pro-preview`. Putting Anthropic first for any agent with a non-trivial `output_schema` reduces exposure to that failure mode without removing Gemini as a fallback.
2. With DJ-106 prompt caching on Anthropic, the cost differential between Anthropic and Gemini on cached fanout calls compresses significantly — the historical cost-first argument for `googleai-first` was load-bearing in 2023-2024 but is much weaker in 2026 with caching.
3. With DJ-093/DJ-104 putting all grounded agents (`spec_scout`, `researcher`, now `cost_critic`) on grounding-capable models, the "Gemini first because Google has the strongest search index" argument also weakens — Anthropic and OpenAI both have native grounding now, and Sonnet's reasoning over retrieved content compensates for any modest search-quality gap.

**Bias acknowledgment.** This audit was authored by Claude (Anthropic's model) and the recommendation to put Anthropic first carries a real bias surface that the operator (Chetan) flagged explicitly during review. Self-reported claims about Sonnet's strict-JSON reliability, reasoning quality on retrieved content, and synthesis prose quality are model-mediated and lack independent benchmark evidence in this conversation. The defensible evidence is narrower:

- Concrete observation: `gemini-3.1-pro-preview` produced pathological JSON on specific elaborator calls in the winplan run (DJ-105).
- Concrete observation: the `gemini-3-flash-preview` swap is a viable mitigation per the user's grounding+schema constraint analysis.
- Conjecture (model-mediated, not benchmarked): Anthropic generally handles strict JSON more reliably than Gemini on the same workload.

The third claim is the load-bearing one for "anthropic-first across the council," and it is the weakest link in the chain. The reversal criteria below treat it as such: if measurement contradicts the conjecture in real runs, the order flips back without ceremony.

**What lands:**

- 22 agents reordered to `anthropic-first` at balanced tier (analyst, architect_critic, archivist, backend_analyzer, cost_critic, critic, devops_critic, frontend_analyzer, gap_analyst, infra_analyzer, planner, preflight, refiner, remediator, reviewer, scout, spec_finding_clusterer, spec_outliner, sre_critic, stakeholder, synthesizer, validator).
- `researcher` reordered to anthropic-first balanced (was googleai-first), grounding flag retained.
- `rewriter` and `llm_judge` reordered to anthropic-first fast.
- `spec_architect` reordered to anthropic → openai → googleai at strong tier (avoid `gemini-3.1-pro-preview` second on the largest schema in the system).
- `spec_feature_elaborator`, `spec_strategy_elaborator` downgraded to anthropic-first balanced (S → B).
- `spec_scout`, `guide` downgraded to anthropic-first balanced.
- `cost_critic` gains `grounding: true` and a "Use Search to Verify Current Pricing" section in its prompt mirroring `spec_scout`'s sanity-check framing.

**What stays the same:**

- `historian`, `monitor`, `convergence` keep `googleai-first fast` ordering. These are mechanical / cost-sensitive roles where the bias-corrected logic still favors cost over quality, and the failure modes aren't strict-JSON-pathology-shaped.
- `spec_reconciler` keeps its existing `anthropic → openai → googleai strong` ordering. Already correct.

**Reversal criteria:**

- Revert tier downgrades if balanced-tier elaborator output proves substantively shallower than strong-tier in real runs (would surface as repeat critic findings on rationale depth, named-principle imprecision, or alternative-list thinness). Concrete signal: `architect_critic` rule 6 firings increase materially after the downgrade.
- Revert anthropic-first ordering if Anthropic-side rate-limit cascades become the dominant failure mode in production runs, suggesting the Anthropic Build tier ceiling is the binding constraint and `googleai-first` was load-balancing better than I credited.
- Revert `cost_critic` grounding if the agent starts citing tangential pricing data (overfilling against DJ-093's reversal criterion (a)) — would suggest the prompt's "sanity check, not enumeration" framing needs sharpening rather than the flag being wrong.

None of these are structural; all are addressable in agent frontmatter without code changes.

**Reference:** governed by DJ-093 (grounding mandate) and its reversal criterion (a) for the cost_critic addition; DJ-099 (direct-SDK migration that made tier choice meaningful per-provider); DJ-105 (the strict-JSON pathology that motivated the anthropic-first lean); DJ-106 (prompt caching that compresses the cost-of-anthropic argument). Bias caveat: the operator flagged the model's self-favoring lean during the audit conversation — the reversal criteria above are written to honor measurement-driven correction rather than defending the recommendation against revision.
