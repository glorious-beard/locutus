## DJ-020: Retry Uses Session Resume, Not Cold Start

**Status:** shipped

**Decision:** When the supervisor retries a failed agent step, it resumes the agent's existing session (`claude -p --resume <session-id>`) rather than starting a fresh conversation.

**Why:** Session resume gives the agent full context of what it tried and what failed. This is far more token-efficient and produces better results than cold-starting with "here's the task again plus what went wrong." The session ID is controlled by Locutus, not the agent.
