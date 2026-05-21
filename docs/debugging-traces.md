# Debugging Locutus Sessions

When `locutus refine`, `locutus import`, or any other LLM-driven verb
produces unexpected output — a council that converged on an incomplete
spec, a critic that emitted thin findings, a synthesis that dropped
content — the session trace is the load-bearing artifact for
diagnosis. This guide walks the trace layout, names the common
failure patterns, and points at the one-liners that get you to the
relevant per-call YAML quickly.

## Where the trace lives

Every Locutus verb that touches an LLM opens one session directory
under `.locutus/sessions/`:

```
.locutus/sessions/
  20260521/                              # YYYYMMDD
    1407/                                # HHMM
      30-a1b2c3/                         # SS-<short-id>
        session.yaml                     # manifest (session id, command, trace id)
        trace.jsonl                      # OTLP-JSON span dump (per-session)
        calls/                           # per-step folders
          0001-spec_scout/               # one folder per agent step
            step.yaml                    # parent summary (token sums, child ids, duration)
            01-single.yaml               # one child per real SDK call
          0002-spec_scout/               # next step
            step.yaml
            01-reason.yaml               # split: reasoning pass
            02-format.yaml               # split: format pass
          ...
```

`session.yaml` cross-references the OTLP-JSON trace via `trace_id`,
and every per-call YAML carries the matching `span_id` so you can
pivot from either surface to the other.

## Per-step folder layout (post-DJ-130)

Each step the workflow ran produces one folder under `calls/`. The
folder name encodes step index + agent id + optional fanout tag:

- `0001-spec_scout/` — non-fanout step, agent `spec_scout`
- `0042-spec_feature_elaborator-feat-dashboard/` — fanout step, per-item id `feat-dashboard`

Inside each folder:

- **`step.yaml`** — parent summary. Agent id, role, status, started/completed
  timestamps, summed token counts across children, list of child call
  ids in dispatch order. Read this first when looking at a step.
- **`NN-<role>.yaml`** — per-SDK-call detail. `NN` is the 2-digit
  child index; `<role>` names the SDK call's purpose:
    - `single` — agent's Run was one SDK call (the common case).
    - `reason` — reasoning pass of the DJ-130 thinking + schema split
      (thinking on, schema cleared, tools/grounding retained).
    - `format` — format pass of the split (thinking off, schema set,
      tools stripped, provider's fast tier).

When the adapter's `requiresThinkingSchemaSplit` predicate fires, you
see both `01-reason.yaml` and `02-format.yaml` under the step. The
parent `step.yaml` sums tokens across both.

## Finding the right session for a failure

By timestamp: the session directory's name encodes the start time
(`YYYYMMDD/HHMM/SS-<short>`). The session you ran 30 seconds ago is
the latest `<short-id>` folder under today's `HHMM/`.

By command: every `session.yaml` carries `command:` (e.g.
`"refine goals"`, `"import docs/X.md"`). Grep:

```sh
grep -l 'command: refine goals' .locutus/sessions/**/session.yaml
```

By history event: when a convergence-time failure fires
(`convergence_failed`, `convergence_stuck`, `convergence_revision_capped`,
`decision_locked`), the matching `.borg/history/evt-*.json` event
carries `session_id` so you can jump from the past-tense record back
to the source trace.

## Extracting structured responses

Per-call YAMLs store the model's structured response in the `response:`
field. When the response is JSON, it's quoted YAML — single-line for
small responses, folded scalars for large ones. Get the raw JSON:

```sh
# Specific call, structured output
yq '.response' calls/0007-spec_scout/01-single.yaml

# All scout responses in a session
for f in calls/*-spec_scout/*-single.yaml; do
  echo "=== $f ===" ; yq '.response' "$f"
done

# Walk thinking text alongside structured output
yq '.reasoning,.response' calls/0007-spec_scout/01-reason.yaml
```

When the response is a tool-use loop, prefer `rounds[]` over the
top-level `response:`/`raw_message:` fields — each round captures one
model invocation in the loop.

## Common failure patterns

### Output is thin / missing content

When a structured response shows up empty (`{}`), with placeholder
values (`"dummy"`, `"TBD"`), or with fields the model's thinking
clearly drafted that didn't make it into the JSON — that's the
DJ-130 motivating case: lossy serialization between thinking and
structured output.

- Pre-DJ-130: you'd see one per-call YAML with thinking text full of
  drafted strategies and a structured response that omitted them.
- Post-DJ-130: the adapter splits automatically, so you should see
  `01-reason.yaml` (rich thinking + prose) and `02-format.yaml`
  (clean structured JSON). If you see thin output post-DJ-130, check
  whether the adapter's `requiresThinkingSchemaSplit` predicate
  actually fired for that model — grep the step folder for both
  child files; missing `02-format.yaml` means the split was skipped.
  Likely culprit: empty `FormatModel` on the Request, meaning the
  executor couldn't resolve the provider's fast tier.

### Convergence failed

The spec-generation council exited without converging. Walk the
scout iterations in order — every `spec_scout` step in the session
maps to one iteration:

```sh
for f in calls/*-spec_scout/01-*.yaml; do
  echo "=== $f ===" ; yq '.response | fromjson | {converged: .converged, axes_open_count: (.axes_open | length), new_nodes_count: (.new_nodes | length)}' "$f"
done
```

Look for `axes_open` not draining, dispositions stuck at `still_open`,
or the dispatch step not firing (no elaborator step after the scout).

### Loop capped on revision (DJ-126)

A `decision_revised` history event firing more than `LOCUTUS_DECISION_REVISION_CAP`
times on the same axis means the revision oscillated rather than
converged. Walk the revision chain: each revise step's
`response.alternatives[]` carries the prior decision's rationale +
the new alternative's argument. Oscillation looks like
A → B → A again (the model picked the previously-rejected option).

### Schema validation rejected (degenerate output)

`degenerateXxxValidator` surfaces fire when a structured response
trips one of the dispatcher's corrective-retry triggers (empty
required arrays, enum drift, placeholder fields). The dispatcher
appends a corrective turn and retries on the same provider; after
`CorrectiveRetries` (default 2) it rotates to the next provider.

Look at the per-call YAMLs in order — the first call's response
trips the validator; subsequent calls in the same step carry the
corrective turn appended to the input messages. If all retries fail
the same way, the prompt or schema needs a fix, not just a retry.

## OTel / YAML correlation

Every per-call YAML carries `span_id` matching the `provider.generate`
span in `trace.jsonl`. The span tree:

```
workflow.phase (one per step)
└── agent.dispatch (one per Dispatcher.Dispatch)
    └── llm.attempt (one per provider rotation × corrective retry)
        └── provider.generate (one per adapter SDK call)
```

A reader holding a span id from `trace.jsonl` can find the matching
per-call YAML by grepping `calls/*/NN-*.yaml` for that span_id. A
reader holding a per-call YAML can find its span (and ancestors) in
`trace.jsonl` by the same id.

## Useful one-liners

```sh
# Total tokens for a session (sums step.yaml totals)
yq -s 'map(.total_tokens // 0) | add' calls/*/step.yaml

# All steps that errored
grep -l 'status: error' calls/*/step.yaml

# Find which step(s) fired the thinking+schema split (have 2+ child calls)
for d in calls/*/; do
  n=$(ls "$d" | grep -E '^[0-9]+-' | wc -l)
  [ "$n" -gt 1 ] && echo "$d (children: $n)"
done

# Walk concerns across all critic outputs
for f in calls/*-*critic*/01-*.yaml; do
  yq '.response | fromjson | .issues[]?.weakness' "$f"
done

# Compare reasoning vs format for a specific split step
diff <(yq '.response' calls/0007-spec_scout/01-reason.yaml) \
     <(yq '.response' calls/0007-spec_scout/02-format.yaml)
```

## What NOT to do

- **Don't edit per-call YAMLs.** `.borg/spec/` is the source of truth
  for the spec graph; the session traces are observability. Editing a
  YAML won't change what the model returned.
- **Don't infer convergence from `trace.jsonl` alone.** The history
  events under `.borg/history/` are the authoritative record of
  whether the loop converged and what it produced. The session trace
  shows *how* convergence was attempted; `.borg/history/` records *what*
  was decided.
- **Don't assume both `reason.yaml` and `format.yaml` will always
  exist together.** Single-call steps (thinking off, no schema, or a
  hypothetical future model that handles the combination cleanly)
  produce one `01-single.yaml` and no split children. The folder
  shape is uniform; the child count varies.
