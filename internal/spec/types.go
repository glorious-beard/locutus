package spec

import "time"

// Decision represents an architectural or implementation decision.
//
// Provenance carries a denormalized subset of the council exchange that
// produced the decision — citations to GOALS.md / docs / named best
// practices / other spec nodes, plus the architect's own rationale
// summary. Per DJ-085, this lives on the Decision itself (durable in
// the spec) rather than as a pointer to an ephemeral session file. The
// full LLM transcript under .locutus/sessions/ remains available as
// debug context but is not load-bearing for justifying the decision.
type Decision struct {
	ID           string              `json:"id" yaml:"id"`
	Title        string              `json:"title" yaml:"title"`
	Status       DecisionStatus      `json:"status" yaml:"status"`
	Confidence   float64             `json:"confidence" yaml:"confidence"`
	Alternatives []Alternative       `json:"alternatives,omitempty" yaml:"alternatives,omitempty"`
	Rationale    string              `json:"rationale" yaml:"rationale"`
	Provenance   *DecisionProvenance `json:"provenance,omitempty" yaml:"provenance,omitempty"`
	InfluencedBy []string            `json:"influenced_by,omitempty" yaml:"influenced_by,omitempty"`
	CreatedAt    time.Time           `json:"created_at" yaml:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at" yaml:"updated_at"`
}

// Alternative represents a considered but not chosen option for a decision.
type Alternative struct {
	Name            string `json:"name" yaml:"name"`
	Rationale       string `json:"rationale" yaml:"rationale"`
	RejectedBecause string `json:"rejected_because" yaml:"rejected_because"`
}

// Citation is one durable reference backing a decision: a span of
// GOALS.md, a feature document the user imported, a named engineering
// best practice ("12-factor app: stateless processes"), another spec
// node, or a fact lifted from the scout brief (DJ-104). Excerpt holds
// the verbatim text when relevant so the citation survives the source
// file being moved or rewritten.
type Citation struct {
	// Kind is one of "goals", "doc", "best_practice", "spec_node",
	// "scout_brief". The scout_brief variant requires Excerpt — the
	// scout's grounded output is the load-bearing source for that
	// citation, and the verbatim copy keeps the provenance durable
	// even after the survey artifact is gone.
	Kind string `json:"kind" yaml:"kind"`
	// Reference identifies the source: a path ("GOALS.md",
	// "docs/dashboard.md"), a named principle ("12-factor app: stateless
	// processes"), or a spec node id ("strat-frontend").
	Reference string `json:"reference" yaml:"reference"`
	// Span localises within Reference when applicable: a line range
	// ("lines 12-18"), a section heading ("## In Scope"), a factor name
	// ("factor VI"), or empty for whole-document references.
	Span string `json:"span,omitempty" yaml:"span,omitempty"`
	// Excerpt is the verbatim quote being cited. Persisted so a
	// citation survives the source moving — durable evidence, not a
	// pointer.
	Excerpt string `json:"excerpt,omitempty" yaml:"excerpt,omitempty"`
}

// DecisionProvenance captures the durable subset of the council
// exchange that produced a decision. It is denormalized into the
// Decision itself (per DJ-085) so that the spec graph is self-contained:
// deleting .locutus/sessions/ never costs the project its justification
// record.
type DecisionProvenance struct {
	Citations          []Citation `json:"citations,omitempty" yaml:"citations,omitempty"`
	ArchitectRationale string     `json:"architect_rationale,omitempty" yaml:"architect_rationale,omitempty"`
	// SourceSession is a non-load-bearing convenience pointer at the
	// transcript file under .locutus/sessions/<date>/<time>/<sid>.yaml
	// that produced this decision. Deleting that file does not break
	// the decision's justification — the durable Citations and
	// ArchitectRationale stand on their own. Empty when the decision
	// was not council-generated.
	SourceSession string    `json:"source_session,omitempty" yaml:"source_session,omitempty"`
	GeneratedAt   time.Time `json:"generated_at,omitempty" yaml:"generated_at,omitempty"`
}

// Strategy represents a high-level engineering approach (architecture, quality, etc.).
// Decisions and Approaches are parent→children references — no child→parent back-refs.
type Strategy struct {
	ID            string            `json:"id" yaml:"id"`
	Title         string            `json:"title" yaml:"title"`
	Kind          StrategyKind      `json:"kind" yaml:"kind"`
	Decisions     []string          `json:"decisions,omitempty" yaml:"decisions,omitempty"`
	Approaches    []string          `json:"approaches,omitempty" yaml:"approaches,omitempty"`
	Status        string            `json:"status" yaml:"status"`
	Prerequisites []string          `json:"prerequisites,omitempty" yaml:"prerequisites,omitempty"`
	Commands      map[string]string `json:"commands,omitempty" yaml:"commands,omitempty"`
	Skills        []string          `json:"skills,omitempty" yaml:"skills,omitempty"`
	InfluencedBy  []string          `json:"influenced_by,omitempty" yaml:"influenced_by,omitempty"`
}

// Entity represents a domain model entity extracted from code during
// assimilation. Per DJ-076 Entity is a context carrier, not a persisted
// spec node: the assimilation pipeline builds Entities in memory and
// feeds them to downstream agents (planner, supervisor, remediator) as
// structured context, but no file is ever written to `.borg/spec/
// entities/`. The code itself — Go structs, DB migrations, proto
// definitions — remains the authoritative schema.
type Entity struct {
	ID            string         `json:"id" yaml:"id"`
	Name          string         `json:"name" yaml:"name"`
	Kind          string         `json:"kind" yaml:"kind"`
	Fields        []EntityField  `json:"fields,omitempty" yaml:"fields,omitempty"`
	Relationships []Relationship `json:"relationships,omitempty" yaml:"relationships,omitempty"`
	Source        string         `json:"source,omitempty" yaml:"source,omitempty"`
	Confidence    float64        `json:"confidence" yaml:"confidence"`
}

// EntityField represents a single field on a domain entity.
type EntityField struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
	Tags string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// Relationship represents a relationship between two entities.
type Relationship struct {
	TargetEntity string `json:"target_entity" yaml:"target_entity"`
	Kind         string `json:"kind" yaml:"kind"`
	ForeignKey   string `json:"foreign_key,omitempty" yaml:"foreign_key,omitempty"`
}

// Feature represents a product-level capability below GOALS.md.
type Feature struct {
	ID                 string        `json:"id" yaml:"id"`
	Title              string        `json:"title" yaml:"title"`
	Status             FeatureStatus `json:"status" yaml:"status"`
	Description        string        `json:"description,omitempty" yaml:"description,omitempty"`
	AcceptanceCriteria []string      `json:"acceptance_criteria,omitempty" yaml:"acceptance_criteria,omitempty"`
	Decisions          []string      `json:"decisions,omitempty" yaml:"decisions,omitempty"`
	Approaches         []string      `json:"approaches,omitempty" yaml:"approaches,omitempty"`
	CreatedAt          time.Time     `json:"created_at" yaml:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at" yaml:"updated_at"`
}

// Manifest holds top-level project metadata.
type Manifest struct {
	ProjectName string    `json:"project_name" yaml:"project_name"`
	Version     string    `json:"version" yaml:"version"`
	Model       string    `json:"model,omitempty" yaml:"model,omitempty"`
	CreatedAt   time.Time `json:"created_at" yaml:"created_at"`
}
