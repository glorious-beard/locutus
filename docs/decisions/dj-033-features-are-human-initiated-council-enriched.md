## DJ-033: Features Are Human-Initiated, Council-Enriched

**Status:** shipped

**Decision:** The human writes the feature spec (any level of detail). The council enriches it with acceptance criteria, edge cases, entity links, and technical considerations. The human reviews the enriched spec before it drives decisions.

**Why not human-only:** A one-liner prompt ("add auth") should be enough to kick off work. The council can flesh out acceptance criteria and edge cases that the human might not think of. But the human always writes the initial intent.

**Why not LLM-generated:** Features can include rich artifacts — Figma mockups, screenshots, user stories from customer research — that an LLM can't produce. The `.md` body is the human's space (prose, links, images). The `.json` sidecar is Locutus's space (structured acceptance criteria, entity refs, decision links).

**The enrichment flow:** Human writes feature → planner adds acceptance criteria and edge cases → stakeholder validates it represents user intent → critic checks for gaps → human reviews enriched spec → spec drives decisions and strategies.
