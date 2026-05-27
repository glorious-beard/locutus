## DJ-005: No Archetype Selection at Init

**Status:** shipped

**Decision:** `locutus init` creates a bare spec structure with no stack assumptions. No archetype enum.

**Journey:**
1. Initially planned opinionated defaults from PLAN.md D-008 (Go + TanStack + Connect RPC)
2. Then planned to generate a full Hello World SaaS app at init
3. User questioned whether we were biased toward traditional SaaS monoliths — what about CLIs, microservices, daemons, libraries?
4. Realized that archetypes should emerge organically from the user's first prompt (greenfield) or codebase analysis (brownfield)
5. Also realized that "asking for archetype" at init was wrong — brownfield should discover it automatically

**Why no archetype:** The "archetype" is just the emergent combination of active skills and strategies. There's no enum to select because the possibilities are unbounded.
