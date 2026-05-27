## DJ-003: LLM Access — The Claude CLI Pivot and Reversal

**Status:** shipped

**Decision:** Use Genkit Go with Anthropic API keys.

**Journey:**
1. **Initial approach:** Shell out to `claude -p --output-format json` to use the user's Claude Max subscription (no API token costs). Research confirmed this was the officially supported way to use Max programmatically.
2. **Problem discovered:** Shelling out to `claude` CLI sacrifices control over conversation flow. No good way to forward feedback requests from the `claude` process back to the user through Locutus. Too much complexity for a cost optimization.
3. **Pivot back to API:** A separate conversation confirmed that `claude -p` was "a cost optimization that was going to compromise the architecture." Building a thin tool loop against the Messages API is a better fit.
4. **Framework selection:** Chose Genkit Go over rolling our own or using Eino (ByteDance). Genkit provides config-over-code model selection — swap providers by changing a string, no recompile.

**Key finding:** The Anthropic Go SDK cannot authenticate with Claude Max subscription — it requires API keys. Anthropic banned OAuth tokens from third-party SDKs in Feb 2026. This was a significant factor in the initial push toward `claude` CLI, and its resolution.
