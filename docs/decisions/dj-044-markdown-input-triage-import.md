## DJ-044: Markdown Input for Triage/Import, Not JSON

**Status:** shipped

**Decision:** The input format for `locutus triage` and `locutus import` is markdown with YAML frontmatter, not JSON. The CI exporter (provider-specific) converts from the external system's format to markdown.

**Why markdown:** Issues are already written in markdown. Markdown carries inline images, Figma links, code blocks, discussion threads — rich content that JSON can't naturally represent. Locutus already has a frontmatter parser. The markdown body becomes the feature/bug `.md` file directly.

**The flow:** External system → provider-specific exporter → markdown+frontmatter → `locutus triage`/`locutus import` → structured JSON verdict (for triage) or local spec artifact (for import). If import is called without prior triage, it runs triage internally and rejects out-of-scope items.
