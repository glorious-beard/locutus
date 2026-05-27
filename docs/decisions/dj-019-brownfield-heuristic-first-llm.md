## DJ-019: Brownfield — Heuristic First, LLM Second

**Status:** shipped

**Decision:** Brownfield analysis uses heuristics for everything deterministically derivable from parseable file content. LLM is reserved for understanding intent, meaning, and context beyond syntax.

**The line:**
- Heuristic: file inventory, config parsing, language/framework detection from dependency files, struct/type parsing, import graphs, FK detection from naming conventions
- LLM: architectural intent (monolith vs microservices), rationale recovery, cross-cutting concerns (auth patterns, error handling), entity significance, feature recovery

**Cost optimization:** LLM calls are batched (2-3 total), not one per decision. This keeps brownfield analysis fast and affordable.
