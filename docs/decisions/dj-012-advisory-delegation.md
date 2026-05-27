## DJ-012: Advisory Delegation

**Status:** shipped

**Decision:** External agents can't be forced to use Locutus. AGENTS.md provides strong guidance but is advisory only. No drift detection infrastructure.

**Context:** VS Code Copilot may not even read AGENTS.md. Claude Code generally follows it but can't be forced.

**Why no enforcement:** Building drift detection or gatekeeper infrastructure adds complexity without guarantees. Better to invest in brownfield recovery (which can reconcile spec after direct edits) than in prevention.
