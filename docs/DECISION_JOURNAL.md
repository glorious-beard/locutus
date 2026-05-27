# Decision Journal

This document captures the series of architectural decisions and pivots that shaped the Locutus implementation plan. Each entry records what was decided, what alternatives were considered, and why the final choice was made. This is the "historian for the historian" — a record of how Locutus itself was designed.

## Status legend

Every DJ carries a **Status:** line immediately after its heading. A DJ's status is what distinguishes "we've decided to do this" from "this is observable in code today." When citing a DJ, always read the status first.

- **shipped** — code matches the decision. Safe to rely on as current behavior.
- **shipping** — partially implemented. Some aspects of the DJ are live; others are gaps. The DJ body (or a linked note) should describe what's in vs. out. Citing a shipping DJ requires naming which part you rely on.
- **settled** — design agreed, no code yet. The DJ is a commitment, not a fact. Citing a settled DJ must flag that it isn't yet observable.
- **superseded by DJ-N** — a later decision replaced this one. Read DJ-N for current direction; keep the original entry for historical context.

Backfilled on 2026-04-23 after an audit surfaced a recurring "we keep discovering designed but unimplemented features" pattern — DJs were being read as state when they were really direction.

Session date: 2026-04-13 to 2026-04-14


## Decisions

Full text for each decision lives under [`decisions/`](decisions/). Cross-references from elsewhere in the repo use the short anchor `DECISION_JOURNAL.md#dj-NNN` and land on the row below.

| # | Title | Status | Full text |
|---|---|---|---|
| <a id="dj-139"></a>DJ-139 | Persist the LLM's Interpretation of `GOALS.md` as First-Class `goal-*` and `agoal-*` Graph Nodes... | design | [open](decisions/dj-139-goal-layer-node-kinds.md) |
| <a id="dj-138"></a>DJ-138 | Add `--with "<bias>"` Strong-Bias Cascade to `locutus refine` for Targeted Override of... | shipping | [open](decisions/dj-138-refine-with-bias-cascade.md) |
| <a id="dj-137"></a>DJ-137 | Restore `locutus justify` as an ACP-Dispatched Activity | shipping | [open](decisions/dj-137-justify-activity.md) |
| <a id="dj-136"></a>DJ-136 | Per-Runtime Idiomatic Dispatch Layer | shipping | [open](decisions/dj-136-per-runtime-idiomatic.md) |
| <a id="dj-135"></a>DJ-135 | Locutus Pivots from In-Process Council to Activity-Driven Multi-Runtime Execution via ACP and MCP | shipped 2026-05-26 | [open](decisions/dj-135-multi-runtime-pivot.md) |
| <a id="dj-134"></a>DJ-134 | Unified In-Process Spec Store Supersedes the Swappable-Wrapper Stack | shipped 2026-05-23 | [open](decisions/dj-134-unified-spec-store.md) |
| <a id="dj-133"></a>DJ-133 | Decisions Identified by Axis, Not Chosen Option | shipped 2026-05-23 | [open](decisions/dj-133-decisions-identified-by-axis.md) |
| <a id="dj-132"></a>DJ-132 | Candidate-Survey Agent for Decision Elaboration | superseded by [DJ-135](#dj-135) | [open](decisions/dj-132-candidate-survey-agent.md) |
| <a id="dj-131"></a>DJ-131 | Phase-Fanout Collapse for ACP-Driven Council Steps | superseded by [DJ-135](#dj-135) | [open](decisions/dj-131-phase-fanout-collapse.md) |
| <a id="dj-130"></a>DJ-130 | Provider Mechanics Encapsulated in Adapters | superseded by [DJ-135](#dj-135) | [open](decisions/dj-130-provider-mechanics-and-trace-layer.md) |
| <a id="dj-129"></a>DJ-129 | Dimension-Driven Critics | superseded by [DJ-135](#dj-135) | [open](decisions/dj-129-dimension-driven-critics.md) |
| <a id="dj-128"></a>DJ-128 | Decisions as Deliberation Logs | superseded by [DJ-135](#dj-135) | [open](decisions/dj-128-deliberation-log-and-cap-as-commit.md) |
| <a id="dj-127"></a>DJ-127 | Spec Mutation Tools as MCP Write Surface | subsumed by [DJ-135](#dj-135) | [open](decisions/dj-127-spec-mutation-tools-mcp.md) |
| <a id="dj-126"></a>DJ-126 | Decision Re-Elaboration for Cross-Decision Contradictions | superseded by [DJ-135](#dj-135) | [open](decisions/dj-126-decision-revision.md) |
| <a id="dj-125"></a>DJ-125 | In-Flight Manifest + Enriched Concern Model | superseded by [DJ-135](#dj-135) | [open](decisions/dj-125-in-flight-manifest.md) |
| <a id="dj-124"></a>DJ-124 | Spec Generation Re-Architecture | superseded by [DJ-135](#dj-135) | [open](decisions/dj-124-decisions-before-narrative.md) |
| <a id="dj-123"></a>DJ-123 | In-Flight Spec Search for Council Agents | superseded by [DJ-134](#dj-134) | [open](decisions/dj-123-in-flight-spec-search.md) |
| <a id="dj-122"></a>DJ-122 | Graph-Mutation Workflow Executor with Spawner Nodes | superseded by [DJ-135](#dj-135) | [open](decisions/dj-122-spawner-executor.md) |
| <a id="dj-121"></a>DJ-121 | Coarsen Pre-Planning to Workstream Grain | superseded by [DJ-135](#dj-135) | [open](decisions/dj-121-adoption.md) |
| <a id="dj-120"></a>DJ-120 | Adopt Resume Narrows to Step-Level Under the ACP Lifecycle | settled | [open](decisions/dj-120-adopt-resume-narrows-step-level.md) |
| <a id="dj-119"></a>DJ-119 | Agent Client Protocol Replaces the Coding-Agent Driver Layer | proposed | [open](decisions/dj-119-agent-client-protocol-replaces.md) |
| <a id="dj-118"></a>DJ-118 | JSON Schema Generation Uses invopop/jsonschema, Not google/jsonschema-go | shipped | [open](decisions/dj-118-json-schema-generation-uses.md) |
| <a id="dj-117"></a>DJ-117 | Explainable Search Ranking via Per-Field Scoring Scans | shipped | [open](decisions/dj-117-explainable-search-ranking-via.md) |
| <a id="dj-116"></a>DJ-116 | Persistent BM25 Spec Index Backing `list` and `spec_search` | shipped | [open](decisions/dj-116-persistent-bm25-spec-index.md) |
| <a id="dj-115"></a>DJ-115 | Prerequisite Layer Is a Category of Functions, Not an Interface | shipped | [open](decisions/dj-115-prerequisite-layer-category-functions.md) |
| <a id="dj-114"></a>DJ-114 | Authored `Summary` Field on Every Spec Node | shipped | [open](decisions/dj-114-authored-field-every-spec.md) |
| <a id="dj-113"></a>DJ-113 | Spec Node Supersession Deletes the Old Node | shipped | [open](decisions/dj-113-spec-node-supersession-deletes.md) |
| <a id="dj-112"></a>DJ-112 | Workflows Move from External YAML to Go Values | shipping | [open](decisions/dj-112-workflows-move-from-external.md) |
| <a id="dj-111"></a>DJ-111 | Flatten `ReconciliationVerdict` Schema | shipped | [open](decisions/dj-111-flatten-schema-drop.md) |
| <a id="dj-110"></a>DJ-110 | Per-Pick Timeout in `Executor.Run` | shipped | [open](decisions/dj-110-per-pick-timeout-bug-b.md) |
| <a id="dj-109"></a>DJ-109 | Bypass Anthropic SDK Non-Streaming Preflight via Explicit Per-Request Timeout | shipped | [open](decisions/dj-109-bypass-anthropic-sdk-non-streaming.md) |
| <a id="dj-108"></a>DJ-108 | Anthropic Native Structured Output + Adaptive Thinking | shipped | [open](decisions/dj-108-anthropic-native-structured-output.md) |
| <a id="dj-107"></a>DJ-107 | Council Model-Tier Audit + Anthropic-First Provider Order | shipped | [open](decisions/dj-107-council-model-tier-audit-anthropic-first.md) |
| <a id="dj-106"></a>DJ-106 | User-Message Prompt Caching for Anthropic via `Cacheable` Flag | shipped | [open](decisions/dj-106-user-message-prompt-caching-anthropic.md) |
| <a id="dj-105"></a>DJ-105 | Elaborator `decisions` Is API-Layer Required, Not Prompt-Layer Required | shipped | [open](decisions/dj-105-elaborator-api-layer-required-not.md) |
| <a id="dj-104"></a>DJ-104 | `scout_brief` Is a First-Class Citation Kind | shipped | [open](decisions/dj-104-first-class-citation-kind.md) |
| <a id="dj-103"></a>DJ-103 | History Narrative Is a Cache, and the Archivist Should Tell a Story | shipped | [open](decisions/dj-103-history-narrative-cache-archivist.md) |
| <a id="dj-102"></a>DJ-102 | Refine Becomes a Deliberate-Evolution Loop | shipped | [open](decisions/dj-102-refine-becomes-deliberate-evolution-loop.md) |
| <a id="dj-101"></a>DJ-101 | Explain Is Pure-Render | shipped | [open](decisions/dj-101-explain-pure-render-justify-active.md) |
| <a id="dj-100"></a>DJ-100 | Comprehensive Spec Snapshot Lives Under `status --full` | shipped | [open](decisions/dj-100-comprehensive-spec-snapshot-lives.md) |
| <a id="dj-097"></a>DJ-097 | Projections Are Data-Only | shipped | [open](decisions/dj-097-projections-are-data-only-rules.md) |
| <a id="dj-096"></a>DJ-096 | State Store Lives Under `.borg/state/`, Not `.locutus/state/` | shipped | [open](decisions/dj-096-state-store-lives-under.md) |
| <a id="dj-095"></a>DJ-095 | Lossless Triage + Per-Finding Additions Fanout | shipped | [open](decisions/dj-095-lossless-triage-per-finding-additions.md) |
| <a id="dj-094"></a>DJ-094 | Spec-Lookup Tools for the Reconciler + Per-Round Tool-Use Capture | shipped | [open](decisions/dj-094-spec-lookup-tools-reconciler-per-round.md) |
| <a id="dj-093"></a>DJ-093 | Scout Grounding via `grounding | shipped | [open](decisions/dj-093-scout-grounding-via-frontmatter.md) |
| <a id="dj-092"></a>DJ-092 | Revise Step Is a Per-Node Fanout, Not a Single Architect Call | shipped | [open](decisions/dj-092-revise-step-per-node-fanout.md) |
| <a id="dj-091"></a>DJ-091 | Session Trace Storage Is a Per-Call File Layout | shipped | [open](decisions/dj-091-session-trace-storage-per-call.md) |
| <a id="dj-090"></a>DJ-090 | Outline + Per-Node Elaborate Fanout | shipped | [open](decisions/dj-090-outline-per-node-elaborate-fanout.md) |
| <a id="dj-089"></a>DJ-089 | Mechanical Integrity Critic + Directive Revise Prompt | shipped | [open](decisions/dj-089-mechanical-integrity-critic-directive.md) |
| <a id="dj-088"></a>DJ-088 | Architect Emits Inline Decisions | shipped | [open](decisions/dj-088-architect-emits-inline-decisions.md) |
| <a id="dj-087"></a>DJ-087 | Approaches Are Synthesized at Adopt Time, Not Refine Time | shipped | [open](decisions/dj-087-approaches-are-synthesized-adopt.md) |
| <a id="dj-086"></a>DJ-086 | `update` Has Two Orthogonal Flags | shipped | [open](decisions/dj-086-has-two-orthogonal-flags.md) |
| <a id="dj-085"></a>DJ-085 | Decisions Denormalize Their Justification | shipped | [open](decisions/dj-085-decisions-denormalize-their-justification.md) |
| <a id="dj-084"></a>DJ-084 | `dominikbraun/graph` Is the Canonical Graph Library | shipped | [open](decisions/dj-084-canonical-graph-library-spec.md) |
| <a id="dj-083"></a>DJ-083 | Spec Generation Uses Externalized Agents + Dedicated Workflow YAML | shipped | [open](decisions/dj-083-spec-generation-uses-externalized.md) |
| <a id="dj-082"></a>DJ-082 | Spec Generation Uses Single-Pass Council, Not the Planner Workflow | superseded by DJ-083 | [open](decisions/dj-082-spec-generation-uses-single-pass.md) |
| <a id="dj-081"></a>DJ-081 | Project-Root Walk-Up for All Subcommands Except `init` | shipped | [open](decisions/dj-081-project-root-walk-up-all-subcommands.md) |
| <a id="dj-080"></a>DJ-080 | `models.yaml` Follows the Embedded-Then-Editable Pattern | shipped | [open](decisions/dj-080-follows-embedded-then-editable-pattern.md) |
| <a id="dj-079"></a>DJ-079 | `refine goals` Generates the Spec Graph from GOALS.md | shipped | [open](decisions/dj-079-generates-spec-graph-from.md) |
| <a id="dj-078"></a>DJ-078 | Agent Definition and Prompt Templating Policy | settled | [open](decisions/dj-078-agent-definition-prompt-templating.md) |
| <a id="dj-077"></a>DJ-077 | Selective Adoption from Google ADK, Not Wholesale | settled | [open](decisions/dj-077-selective-adoption-from-google.md) |
| <a id="dj-076"></a>DJ-076 | Entity Is In-Memory Context, Not a Persisted Spec Node | shipped | [open](decisions/dj-076-entity-in-memory-context-not.md) |
| <a id="dj-075"></a>DJ-075 | Assimilate Reads Existing Spec, Writes Back Atomically | shipped | [open](decisions/dj-075-assimilate-reads-existing-spec.md) |
| <a id="dj-074"></a>DJ-074 | True `--resume` for Interrupted Adoption | shipped | [open](decisions/dj-074-true-interrupted-adoption.md) |
| <a id="dj-073"></a>DJ-073 | Active Workstream Persistence for Crash Recovery | shipped | [open](decisions/dj-073-active-workstream-persistence-crash.md) |
| <a id="dj-072"></a>DJ-072 | CLI Surface Consolidated to 8-Verb Lifecycle Shape | shipped | [open](decisions/dj-072-cli-surface-consolidated-8-verb.md) |
| <a id="dj-071"></a>DJ-071 | Pre-Flight Clarification Protocol | shipped | [open](decisions/dj-071-pre-flight-clarification-protocol-coding.md) |
| <a id="dj-070"></a>DJ-070 | Node ID Generation | shipped | [open](decisions/dj-070-node-id-generation-llm-derived.md) |
| <a id="dj-069"></a>DJ-069 | DAG Node Type Redesign | shipped | [open](decisions/dj-069-dag-node-type-redesign.md) |
| <a id="dj-068"></a>DJ-068 | Manifest/State Separation | shipped | [open](decisions/dj-068-manifest-state-separation-kubernetes-inspired.md) |
| <a id="dj-067"></a>DJ-067 | Model Tier Config via Embedded YAML, List-per-Tier Runtime Resolution | shipped | [open](decisions/dj-067-model-tier-config-via.md) |
| <a id="dj-066"></a>DJ-066 | Genkit Wired with Env-Driven Plugin Auto-Detection | shipped | [open](decisions/dj-066-genkit-wired-with-env-driven.md) |
| <a id="dj-065"></a>DJ-065 | End-to-End Smoke Test Caught Three Production Bugs That Mock-Only Unit Tests Had Hidden | shipped | [open](decisions/dj-065-end-to-end-smoke-test-caught.md) |
| <a id="dj-064"></a>DJ-064 | FastLLM Field Bounds Monitor Cost | shipped | [open](decisions/dj-064-fastllm-field-bounds-monitor.md) |
| <a id="dj-063"></a>DJ-063 | Sliding-Window Churn Rule over Consecutive Counter | shipped | [open](decisions/dj-063-sliding-window-churn-rule-over.md) |
| <a id="dj-062"></a>DJ-062 | Permission Bridge via In-Process MCP Server, Not Stream Parsing | shipped | [open](decisions/dj-062-permission-bridge-via-in-process.md) |
| <a id="dj-061"></a>DJ-061 | Streaming Supervision Plan Executed End-to-End | shipped | [open](decisions/dj-061-streaming-supervision-plan-executed.md) |
| <a id="dj-060"></a>DJ-060 | Dispatcher Uses Executor, Steps Within a Workstream Use a For-Loop | shipped | [open](decisions/dj-060-dispatcher-uses-executor-steps.md) |
| <a id="dj-059"></a>DJ-059 | Streaming Supervision Deferred to Follow-Up Plan | superseded by DJ-061 | [open](decisions/dj-059-streaming-supervision-deferred-follow-up.md) |
| <a id="dj-058"></a>DJ-058 | Churn and Retry Are Distinct | shipped | [open](decisions/dj-058-churn-retry-are-distinct.md) |
| <a id="dj-057"></a>DJ-057 | Permission/Question Routing via Tool-Name Registry, Not Heuristics | superseded by DJ-062 | [open](decisions/dj-057-permission-question-routing-via.md) |
| <a id="dj-056"></a>DJ-056 | Fast-Tier LLM Monitor Replaces Go Heuristic Watchdog | shipped | [open](decisions/dj-056-fast-tier-llm-monitor-replaces.md) |
| <a id="dj-055"></a>DJ-055 | Executor Uses func | shipped | [open](decisions/dj-055-executor-uses-func-any.md) |
| <a id="dj-054"></a>DJ-054 | JSON Schema via Struct Tags and Registry | shipped | [open](decisions/dj-054-json-schema-via-struct.md) |
| <a id="dj-053"></a>DJ-053 | Capability Tiers with Multi-Provider Resolution | superseded by DJ-067 | [open](decisions/dj-053-capability-tiers-with-multi-provider.md) |
| <a id="dj-052"></a>DJ-052 | Agent Definitions Are the Prompt Source of Truth | shipped | [open](decisions/dj-052-agent-definitions-are-prompt.md) |
| <a id="dj-051"></a>DJ-051 | Flat Scaffold Layout | shipped | [open](decisions/dj-051-flat-scaffold-layout.md) |
| <a id="dj-050"></a>DJ-050 | brownfield → assimilation Rename | shipped | [open](decisions/dj-050-brownfield-assimilation-rename.md) |
| <a id="dj-049"></a>DJ-049 | Generic Step Executor Extraction | shipped | [open](decisions/dj-049-generic-step-executor-extraction.md) |
| <a id="dj-048"></a>DJ-048 | Minimal CLI, MCP as Primary Interface, Headless via --json | shipped | [open](decisions/dj-048-minimal-cli-mcp-primary.md) |
| <a id="dj-047"></a>DJ-047 | Full Build Order Rewrite | shipped | [open](decisions/dj-047-full-build-order-rewrite.md) |
| <a id="dj-046"></a>DJ-046 | Hybrid Remediation | shipped | [open](decisions/dj-046-hybrid-remediation-cross-cutting-feature-specific.md) |
| <a id="dj-045"></a>DJ-045 | Brownfield Includes Gap Analysis and Autonomous Remediation | shipped | [open](decisions/dj-045-brownfield-includes-gap-analysis.md) |
| <a id="dj-044"></a>DJ-044 | Markdown Input for Triage/Import, Not JSON | shipped | [open](decisions/dj-044-markdown-input-triage-import.md) |
| <a id="dj-043"></a>DJ-043 | Triage Command + CI-Bridge Pattern | shipped | [open](decisions/dj-043-triage-command-ci-bridge-pattern.md) |
| <a id="dj-042"></a>DJ-042 | Local-Only, No Write-Back to External Issue Trackers | shipped | [open](decisions/dj-042-local-only-no-write-back-external.md) |
| <a id="dj-041"></a>DJ-041 | GOALS.md as Project Root + Issue-Driven Intake | shipped | [open](decisions/dj-041-goals-md-project-root.md) |
| <a id="dj-040"></a>DJ-040 | Test-First Workstream Pattern as a Quality Strategy | settled | [open](decisions/dj-040-test-first-workstream-pattern-quality.md) |
| <a id="dj-039"></a>DJ-039 | Agent Writes Tests, Plan Specifies Criteria | shipped | [open](decisions/dj-039-agent-writes-tests-plan.md) |
| <a id="dj-038"></a>DJ-038 | On-Demand Specialist Agents for Plan Fleshing-Out | settled | [open](decisions/dj-038-on-demand-specialist-agents-plan.md) |
| <a id="dj-037"></a>DJ-037 | Convergence Monitor Uses LLM, Not Just Deterministic Checks | shipping | [open](decisions/dj-037-convergence-monitor-uses-llm.md) |
| <a id="dj-036"></a>DJ-036 | Council Agents and Workflow DAG Are Externalizable Files | shipped | [open](decisions/dj-036-council-agents-workflow-dag.md) |
| <a id="dj-035"></a>DJ-035 | LLM-Based Assertions Alongside Deterministic Checks | settled | [open](decisions/dj-035-llm-based-assertions-alongside-deterministic.md) |
| <a id="dj-034"></a>DJ-034 | Quality Strategies for Best Practice Enforcement | shipping | [open](decisions/dj-034-quality-strategies-best-practice.md) |
| <a id="dj-033"></a>DJ-033 | Features Are Human-Initiated, Council-Enriched | shipped | [open](decisions/dj-033-features-are-human-initiated-council-enriched.md) |
| <a id="dj-032"></a>DJ-032 | Commit-Per-Workstream on a Local Feature Branch | shipped | [open](decisions/dj-032-commit-per-workstream-local-feature-branch.md) |
| <a id="dj-031"></a>DJ-031 | Concurrency Scheduler with Configurable Resource Limits | shipping | [open](decisions/dj-031-concurrency-scheduler-with-configurable.md) |
| <a id="dj-030"></a>DJ-030 | File Conflict Prevention at Plan Time, Rebase as Fallback | shipped | [open](decisions/dj-030-file-conflict-prevention-plan.md) |
| <a id="dj-029"></a>DJ-029 | Genkit Go + Custom Orchestration, Not LangGraphGo | shipped | [open](decisions/dj-029-genkit-go-custom-orchestration.md) |
| <a id="dj-028"></a>DJ-028 | Plan Readiness Is a Collaborative Gate, Not a Single Agent | shipping | [open](decisions/dj-028-plan-readiness-collaborative-gate.md) |
| <a id="dj-027"></a>DJ-027 | Hierarchical Plans | shipped | [open](decisions/dj-027-hierarchical-plans-plan-plans.md) |
| <a id="dj-026"></a>DJ-026 | Historian Uses LLM for Narrative, Not Just Deterministic Recording | shipped | [open](decisions/dj-026-historian-uses-llm-narrative.md) |
| <a id="dj-025"></a>DJ-025 | Planning as a Cooperative Council, Not a Single LLM Call | shipping | [open](decisions/dj-025-planning-cooperative-council-not.md) |
| <a id="dj-024"></a>DJ-024 | Full Scope Validated | shipped | [open](decisions/dj-024-full-scope-validated-supervision.md) |
| <a id="dj-023"></a>DJ-023 | Agent File Generation Strategy | shipped | [open](decisions/dj-023-agent-file-generation-strategy.md) |
| <a id="dj-022"></a>DJ-022 | Features as Product-Level Layer Above Decisions | shipped | [open](decisions/dj-022-features-product-level-layer-above.md) |
| <a id="dj-021"></a>DJ-021 | Genkit Go | shipped | [open](decisions/dj-021-genkit-go-llm-plumbing.md) |
| <a id="dj-020"></a>DJ-020 | Retry Uses Session Resume, Not Cold Start | shipped | [open](decisions/dj-020-retry-uses-session-resume.md) |
| <a id="dj-019"></a>DJ-019 | Brownfield | shipped | [open](decisions/dj-019-brownfield-heuristic-first-llm.md) |
| <a id="dj-018"></a>DJ-018 | Tier 3 Uses Synthetic Fixtures | shipped | [open](decisions/dj-018-tier-3-uses-synthetic.md) |
| <a id="dj-017"></a>DJ-017 | Locutus Writes Tests, Not the Agent | superseded by DJ-039 | [open](decisions/dj-017-locutus-writes-tests-not.md) |
| <a id="dj-016"></a>DJ-016 | Execution Plan | superseded by DJ-027 | [open](decisions/dj-016-execution-plan-one-strategy.md) |
| <a id="dj-015"></a>DJ-015 | Competitive Positioning | shipped | [open](decisions/dj-015-competitive-positioning.md) |
| <a id="dj-014"></a>DJ-014 | Brownfield Self-Analysis | shipped | [open](decisions/dj-014-brownfield-self-analysis.md) |
| <a id="dj-013"></a>DJ-013 | Test-First Tier Implementation | shipped | [open](decisions/dj-013-test-first-tier-implementation.md) |
| <a id="dj-012"></a>DJ-012 | Advisory Delegation | shipped | [open](decisions/dj-012-advisory-delegation.md) |
| <a id="dj-011"></a>DJ-011 | Historian | shipped | [open](decisions/dj-011-historian.md) |
| <a id="dj-010"></a>DJ-010 | Agent Routing and Supervision | shipped | [open](decisions/dj-010-agent-routing-supervision.md) |
| <a id="dj-009"></a>DJ-009 | Autonomous Decisions During Planning | shipped | [open](decisions/dj-009-autonomous-decisions-during-planning.md) |
| <a id="dj-008"></a>DJ-008 | Planner + Delegator, Not Coder | shipped | [open](decisions/dj-008-planner-delegator-not-coder.md) |
| <a id="dj-007"></a>DJ-007 | Everything Is a Strategy | shipped | [open](decisions/dj-007-everything-strategy.md) |
| <a id="dj-006"></a>DJ-006 | Skills Over Templates | shipped | [open](decisions/dj-006-skills-over-templates.md) |
| <a id="dj-005"></a>DJ-005 | No Archetype Selection at Init | shipped | [open](decisions/dj-005-no-archetype-selection-init.md) |
| <a id="dj-004"></a>DJ-004 | MCP Transport | shipped | [open](decisions/dj-004-mcp-transport.md) |
| <a id="dj-003"></a>DJ-003 | LLM Access | shipped | [open](decisions/dj-003-llm-access-claude-cli.md) |
| <a id="dj-002"></a>DJ-002 | Console Output Library | shipped | [open](decisions/dj-002-console-output-library.md) |
| <a id="dj-001"></a>DJ-001 | CLI Framework | shipped | [open](decisions/dj-001-cli-framework.md) |
