## DJ-013: Test-First Tier Implementation

**Status:** shipped

**Decision:** Each implementation tier starts with acceptance tests and ends with running them.

**Why:** Locutus's own supervision loop enforces test-first discipline on external agents. We should eat our own cooking. Writing tests first also forces us to define contracts before implementation, catching design issues early.
