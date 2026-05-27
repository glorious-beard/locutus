## DJ-057: Permission/Question Routing via Tool-Name Registry, Not Heuristics

**Status:** superseded by DJ-062

**Decision:** `EventPermissionRequest` and `EventClarifyQuestion` are identified by matching the event's tool name against a per-driver registry:

- Permission tool: the tool name we registered via `claude -p --permission-prompt-tool <name>`. Because we configured the name, match is definitional, not inferred.
- Question tool: the provider's documented SDK tool name (e.g., Claude's `AskUserQuestion`).

If a driver doesn't support either mechanism, those events simply don't fire for that provider — acknowledged limitation, not papered over.

**Why:** Heuristic detection ("is this a Bash command that looks dangerous?") would be fragile and lead to false positives/negatives. The tool-name match is structural because the identification is either by configuration we chose or by documented provider convention.
