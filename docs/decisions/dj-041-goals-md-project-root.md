## DJ-041: GOALS.md as Project Root + Issue-Driven Intake

**Status:** shipped

**Decision:** GOALS.md is a human-authored document at the project root that defines project scope, success criteria, and in/out-of-scope boundaries. GitHub issues are automatically evaluated against GOALS.md for intake. Features and bugs are spec artifacts.

**The hierarchy:** GOALS.md → Feature/Bug → Decision → Strategy → Source Files

**Why GOALS.md:**
- Gives the stakeholder agent an objective reference for scope evaluation instead of relying on LLM judgment
- Automatic scope filtering: "add blockchain support" to a medical device project → rejected
- Automatic bug triage: security bugs auto-escalated if GOALS.md says "security is critical"
- Duplicate detection: new issue matches existing spec → closed with link

**Issue-driven intake:** GitHub issues → evaluated against GOALS.md → in-scope features enter the planning council, out-of-scope rejected with explanation, bugs triaged by severity. Zero-issue count as a quality strategy.

**Bug as a spec artifact:** Lives in `.borg/spec/bugs/`. Has: id, title, severity (auto-triaged), status, reproduction steps, related feature/decision, root cause (filled after analysis), fix plan. Simpler lifecycle than features but follows the same markdown+JSON sidecar pattern.

**Motivation:** The .NET/5000-issues problem. Open-source projects drown in untriaged issues. Locutus as an autonomous triage + resolution engine is genuinely novel and addresses a real pain point. The goal is 90% autonomous improvement, driving toward zero open issues.
