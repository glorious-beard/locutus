## DJ-035: LLM-Based Assertions Alongside Deterministic Checks

**Status:** settled

**Decision:** Assertions can be either deterministic (`test_pass`, `contains`, `compiles`, `lint_clean`, etc.) or LLM-based (`llm_review`). Deterministic assertions run first (fast, cheap). LLM review assertions run last (slower, costlier, but catch semantic issues).

**Why not deterministic-only:** Some quality checks require judgment that regex and linters can't provide: "Does this code follow the separation of concerns in the architecture strategy?", "Is the error handling consistent with patterns elsewhere?", "Does this UI match the visual language of the design system?" These are real concerns that agents routinely get wrong, and no heuristic can catch them.

**The `llm_review` assertion:** Carries a `Prompt` field with the specific review question. The supervisor sends the changed files (or diff) plus the prompt to an LLM and evaluates the response. This is a separate LLM call from the coding agent — an independent reviewer, not the agent reviewing its own work.

**Cost management:** Deterministic assertions short-circuit — if they fail, LLM reviews don't run (fix the cheap failures first). LLM reviews only run on passing code, keeping cost proportional to quality.
