## DJ-070: Node ID Generation — LLM-Derived Kebab-Case Slugs

**Status:** shipped

**Decision:** All spec node IDs (Feature, Strategy, Decision, Approach, Bug, Entity) are kebab-case slugs derived from the node's title by the generating agent at creation time. Sequential counter IDs (e.g., `DEC-001`, `FEAT-003`) are retired for new nodes. Existing nodes with legacy IDs are left untouched — the ID field is a plain string and any value is valid.

**Slug format:**

- Lowercase; spaces and underscores become hyphens; non-alphanumeric characters removed
- Truncated at a word boundary to ~50 characters
- Example: `"OAuth Login via Google"` → `oauth-login-via-google`
- Example: `"Implement OAuth token exchange"` → `implement-oauth-token-exchange`

**Collision resolution (handled by `specio`, not the generating agent):**

At save time, `specio` checks whether the ID already exists in the store. On collision, it appends `-` plus the first 6 hex characters of `SHA-256(title + ISO-8601 creation timestamp)`. The agent never sees this — it proposes a title; `specio` handles uniqueness.

Example: if `oauth-login-via-google` exists, the new node becomes `oauth-login-via-google-a3f912`.

**Why LLM-generated rather than author-assigned:**
The primary author of spec nodes is the council of LLM agents, not the human. Having the LLM derive a slug from the title it just wrote is the natural equivalent of a human naming a Kubernetes manifest. No user input required; the name is always meaningful because it reflects the node's title at the moment of creation.

**Alternatives considered:**

- **Sequential IDs (`DEC-001`)** — rejected. Require a central atomic counter. In a filesystem-based spec store with concurrent agents or multiple users, two sessions can independently emit the same counter value. No safe distributed increment exists without a coordinator.
- **UUIDs / ULIDs** — rejected. Globally unique and collision-free, but opaque. A Decision ID like `01HZQK7R3P...` cannot be referenced meaningfully in prose specs, in Decision.InfluencedBy lists, or in agent prompts. Human-readability is load-bearing in Locutus.
- **Pure content hash** (SHA-256 of title alone) — rejected. Two agents independently generating a Decision with the same title produce the same ID, silently aliasing two distinct nodes. The timestamp component in the collision suffix prevents this.
- **Kubernetes-style author naming** — considered but deprioritised. Works well when humans author manifests directly; less natural when the council is the primary author. The LLM slug approach gives the same DX (readable names, no counter) without requiring a human naming step.

**Applies to Approach nodes (DJ-069):** Approach IDs are derived from the Approach's own title — the implementation brief title the generating agent writes — not from the parent node's ID. The parent relationship is expressed through `Feature.Approaches []string` and `Strategy.Approaches []string`, not encoded in the child's ID.

**CLAUDE.md count update:** this entry brings the total to 70.
