# DJ-152: Package Locutus as a Claude Code Plugin — MCP Server Descriptor + Verb Skills + Council Agents Assembled From the Canonical Scaffold Sources by a New Publisher Target; the `locutus` Binary Is a PATH Prerequisite (No Bundling, No Bootstrap Downloader); `locutus init` / `update --reset` Detects the Installed Plugin and Skips Claude Code Per-Project Publishing to Prevent Duplicates; Scope Deliberately Limited to Packaging — the ACP Dispatch Layer, Multi-Runtime Publisher, and Codex/Gemini Treatment Are Unchanged, With the Larger Inversion (Harness-Owned Orchestration, Dispatch-Layer Sunset) Deferred to a Future DJ

**Status:** settled (designed 2026-07-07; no code yet). Additive packaging decision — nothing existing is removed or demoted. The larger strategic question this was carved down from (going Claude-Code-first on orchestration, sunsetting the ACP dispatch harness, demoting Codex/Gemini to MCP-substrate access) is explicitly deferred; see "Deferred scope" below.

**Context.** A strategic step-back (2026-07-06) asked whether Locutus's runtime-agnostic posture still earns its maintenance cost, and whether wrapping Locutus as an MCP server + Claude Code plugin is the better distribution shape. Two inputs resolved the packaging half:

1. **The layer split.** Locutus's substrate — the spec graph on disk, the state store, the `spec_*` / `state_*` MCP tools served by the per-project daemon (DJ-134/DJ-135) — is already runtime-agnostic by construction, because MCP is the one protocol all three runtimes standardized on. The orchestration layer (CLI verbs, ACP dispatch, per-runtime overlays, convergence drivers) is where agnosticism costs recurring maintenance and where harness absorption keeps moving the boundary (DJ-135 retired the council; DJ-144 handed Claude Code's convergence loop to its native dynamic workflows). Packaging the Claude Code front-end as a plugin rides that trend without betting the architecture on it.

2. **Deep-research findings (2026-07-06, adversarially verified against live sources).** Claude Code plugins are a first-class shipped distribution channel for MCP-backed tools — a plugin bundles `.mcp.json` MCP-server configs alongside skills, agents, and hooks, and Anthropic operates two public marketplaces (`claude-plugins-official`, auto-registered on first interactive launch, and `claude-community` for reviewed third-party submissions). Claude Code's native auto memory is explicitly unstructured soft-context markdown ("context, not enforced configuration") with no typed nodes, hashing, or reconciliation semantics — it does not absorb the layer Locutus occupies. GitHub Spec Kit's distribution (a CLI publishing slash commands/skills into 30+ agents) independently validates the publish-into-runtimes pattern Locutus already uses; Codex's executor plugins (0.142.0, stdio MCP activation) suggest a future Codex analog of this DJ when that surface matures.

Today's Claude Code onboarding is: install the Go binary, run `locutus init` per project, which emits `.claude/agents/locutus/` copies + slash commands + an MCP-server descriptor. A plugin collapses that to one install that travels across all the operator's projects, versions independently of any one repo, and puts the interactive verb surface (which, per the operator's billing experience, draws on the subscription rather than Agent SDK credits — a claim to re-verify at implementation, as billing policy moves) one `/locutus:` prefix away in every session.

**Decision.**

1. **Plugin contents.** A `locutus` Claude Code plugin containing: the plugin manifest; the MCP-server config invoking `locutus mcp` (the existing smart client/daemon bridge — the plugin adds no new server); skills for the interactive verbs (`/locutus:refine`, `/locutus:import`, `/locutus:adopt`, `/locutus:assimilate`, `/locutus:justify`) carrying the same content the publisher's Claude Code target emits today (the CC overlay playbooks and slash-command wrappers, DJ-136/DJ-144); and the council agents from `internal/scaffold/agents/`. Exact manifest/file layout follows the current Claude Code plugin docs at implementation time — verify against the live docs, not this DJ's snapshot.

2. **Binary acquisition: prerequisite on PATH.** The plugin's MCP config runs `locutus mcp` and assumes the binary is installed (brew / `go install`). The plugin README and a graceful MCP-startup failure message name the install step. No bundled per-platform binaries, no bootstrap downloader (alternatives below).

3. **Generated, not hand-maintained.** The publisher (`internal/publisher/`) gains a plugin assembly target that reads the same canonical sources (`internal/scaffold/agents/*.md`, `internal/scaffold/plans/*`) and emits the plugin directory. One-way publishing per DJ-135: edits to the emitted plugin are overwritten on the next assembly; canonical sources stay the source of truth. The plugin version tracks the binary version that assembled it.

4. **Coexistence: init detects the plugin and skips Claude Code per-project publishing.** `locutus init` / `locutus update --reset` checks for the installed plugin and, when present, skips emitting `.claude/agents/locutus/` + CC slash commands + the CC MCP descriptor for that project (Codex/Gemini publishing unchanged). Per-project CC publishing remains the fallback for operators without the plugin. This prevents duplicate agents/commands, the known footgun of a document-only posture.

5. **Distribution v1: installable from a git repo.** Marketplace submission (`claude-community`) is a follow-up once the plugin has real usage; nothing in the layout should preclude it.

## Resolved design questions

1. **Binary dependency — PATH prerequisite vs bootstrap downloader vs bundling.** PATH prerequisite. Simplest, honest about the dependency, standard for binary-backed MCP servers. A bootstrap downloader adds a release-artifact pipeline, checksum/trust surface, and update semantics; bundling adds ~40–60MB per platform and version-locks plugin releases to binary releases. Either can be revisited if install friction proves to be a real adoption blocker.

2. **Coexistence — detection vs documentation vs replacement.** Detection in `init`/`update --reset`. Document-only guarantees duplicate subagents and commands on every existing project; removing the CC publish target entirely breaks non-plugin users and expands scope beyond packaging.

3. **Which runtime gets the plugin treatment.** Claude Code, for concrete reasons: the operator lives there; DJ-144 already invested in CC dynamic workflows as the richest convergence driver; the CC plugin channel is the most mature of the three (shipped marketplaces vs Codex's "first vertical"). A Codex executor-plugin analog is future work keyed to that surface maturing.

## Alternatives considered

- **Full inversion: plugin-first with ACP dispatch sunset and Codex/Gemini demotion.** The larger design this DJ was deliberately carved down from. Deferred, not rejected: it makes the harness the orchestrator everywhere and deletes the commodity dispatch layer, but it is a rearchitecture with real switching costs, and the DJ-151 winplan validation plus actual plugin usage should inform it. Nothing in this DJ's packaging forecloses it.
- **Bundle per-platform binaries in the plugin.** Rejected (RQ1).
- **Bootstrap script downloading the binary on first run.** Rejected for v1 (RQ1).
- **Document-only coexistence.** Rejected (RQ2).
- **Plugin replaces the CC publish target entirely.** Rejected for v1 (RQ2) — scope.
- **Hand-maintained plugin repo.** Rejected: violates the DJ-135 one-way publishing discipline; the canonical scaffold sources must stay the single source of truth for prompts and playbooks.

## Consequences

### New

- Publisher plugin-assembly target in `internal/publisher/` (new emit path + layout mapping from canonical sources to plugin structure).
- Plugin manifest/config files (layout per live Claude Code plugin docs at implementation time) + plugin README with the PATH-prerequisite install step.
- Plugin-detection check in the `init` / `update --reset` publish path (Claude Code target only).

### Modified

- `docs/mcp.md` / `docs/runtime-affordances.md` — document the plugin as the preferred Claude Code onboarding; per-project publishing as fallback.
- `CLAUDE.md` — Sources of Truth bullet.
- `docs/DECISION_JOURNAL.md` — manifest row.

### Not changed

- ACP dispatch harness, `OuterLoopRunner`, activity registry, per-runtime overlays, hooks publishing, runtime policy registry — untouched.
- Codex and Gemini publishing — untouched.
- The daemon and MCP tool surface — the plugin points at `locutus mcp` as-is.
- CLI verbs — unchanged and co-exist with the plugin's skills.

## Deferred scope / future work

- **Dispatch-layer inversion DJ** (harness-owned orchestration, ACP sunset, Codex/Gemini demoted to MCP-substrate access) — revisit after DJ-151's winplan validation and initial plugin adoption.
- **Codex executor-plugin analog** — keyed to Codex's plugin surface maturing beyond stdio-only "first vertical."
- **Marketplace submission** — after real usage.
- **Billing-split verification** — re-verify the subscription-vs-Agent-SDK-credits split for in-session skill invocations vs ACP dispatch before citing cost as a plugin benefit in user-facing docs.

## References

- [DJ-135](dj-135-multi-runtime-pivot.md) — one-way publishing discipline; the daemon + `locutus mcp` bridge the plugin points at.
- [DJ-136](dj-136-per-runtime-idiomatic.md) / [DJ-144](dj-144-cc-workflow-convergence.md) — the CC overlay playbooks and dynamic-workflow convergence the plugin's skills carry.
- [DJ-151](dj-151-journey-walk-ownership-coverage.md) — the concurrently-designed critic amendment whose winplan validation gates the deferred inversion DJ.
- Deep-research report (2026-07-06, session record) — CC plugin channel + marketplaces (verified), native-memory non-absorption (verified), Codex 0.142.0 executor plugins (verified), Spec Kit distribution pattern (verified).
