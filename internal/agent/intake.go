package agent

import (
	"context"
	"fmt"
)

// IntakeResult is the structured output of an intake LLM call: the id and
// title proposed for the candidate document plus, when GOALS.md was passed
// in, an admission verdict.
//
// ID and Title are always populated. The triage fields (Accepted, Reason,
// SuggestedLabels, Duplicate, DuplicateOf) are populated only when a
// non-empty goalsBody was provided to IntakeDocument; the caller decides
// whether to gate admission on them.
type IntakeResult struct {
	ID              string   `json:"id" jsonschema:"description=Stable slug for the document. Starts with a kind prefix (feat- / bug-); lowercase; hyphen-separated; three to five words derived from the subject. Example: 'feat-realtime-dashboard'."`
	Title           string   `json:"title" jsonschema:"description=Concise human-readable title in sentence case. Captures what the document is about; not a copy of the document's filename."`
	Accepted        bool     `json:"accepted" jsonschema:"description=Whether the document aligns with project goals (when GOALS.md was provided). False when out-of-scope or off-strategy; true when admissible. Greenfield projects with empty goals body get true by instruction."`
	Reason          string   `json:"reason" jsonschema:"description=One to two sentences explaining the admission verdict. Cites the GOALS.md clause that decides admission (when relevant). Empty when greenfield (no goals to evaluate against)."`
	SuggestedLabels []string `json:"suggested_labels,omitempty" jsonschema:"description=Optional taxonomy labels for downstream filtering ('frontend'; 'auth'; 'observability'). Empty when no clear classification fits; do not invent labels."`
	Duplicate       bool     `json:"duplicate" jsonschema:"description=Whether this document overlaps substantially with an existing spec node. True when the LLM finds a clear duplicate by topic; false when content is novel."`
	DuplicateOf     string   `json:"duplicate_of,omitempty" jsonschema:"description=The id of the existing node this duplicates; when duplicate=true. Must be a real id from the spec graph context provided; empty otherwise."`
}

// IntakeDocument runs a single agent call that derives a stable id
// and title for the document, and (when goalsBody is non-empty)
// evaluates whether the document aligns with project goals.
//
// kind selects the id prefix: "feature" → "feat-", "bug" → "bug-".
//
// The output schema is enforced at the provider layer via the
// adapter's strict-mode shape (Anthropic forced tool-use, Gemini
// responseSchema, OpenAI json_schema strict); the response is
// JSON-by-construction rather than parsed out of free-form text.
func IntakeDocument(ctx context.Context, exec AgentExecutor, kind, content, goalsBody string) (*IntakeResult, error) {
	prefix := idPrefix(kind)
	system := fmt.Sprintf(`You are reviewing a candidate %s document for admission to a project's spec.

For every call, propose:
- id: a stable slug starting with %q, lowercase, hyphen-separated, derived from the document's subject (e.g. %q for a real-time dashboard feature). Keep it short — three to five words.
- title: a concise human-readable title in sentence case.

%s

Respond with valid JSON.`,
		kind,
		prefix,
		prefix+"realtime-dashboard",
		triageInstructions(goalsBody != ""),
	)

	user := "## Document\n\n" + content
	if goalsBody != "" {
		user = "## GOALS.md\n\n" + goalsBody + "\n\n" + user
	}

	def := AgentDef{
		ID:           "intake",
		SystemPrompt: system,
		OutputSchema: "IntakeResult",
	}
	input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}

	var result IntakeResult
	if err := RunInto(ctx, exec, def, input, &result); err != nil {
		return nil, fmt.Errorf("intake: %w", err)
	}
	if result.ID == "" {
		return nil, fmt.Errorf("intake: LLM returned empty id")
	}
	return &result, nil
}

func idPrefix(kind string) string {
	if kind == "bug" {
		return "bug-"
	}
	return "feat-"
}

func triageInstructions(includeTriage bool) string {
	if !includeTriage {
		return `Set accepted=true and leave reason empty — no goals were provided to evaluate against.`
	}
	return `Then evaluate the document against GOALS.md:
- accepted: true if the document is in scope, false otherwise.
- reason: one short sentence explaining the decision.
- suggested_labels: optional tags (e.g. "enhancement", "infrastructure").
- duplicate: true if this duplicates an existing feature already in scope.
- duplicate_of: when duplicate=true, the id of the existing feature.`
}
