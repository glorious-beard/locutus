package spec

import "time"

// Decision represents an architectural or implementation decision.
type Decision struct {
	ID           string         `json:"id" yaml:"id"`
	Title        string         `json:"title" yaml:"title"`
	Status       DecisionStatus `json:"status" yaml:"status"`
	Confidence   float64        `json:"confidence" yaml:"confidence"`
	Alternatives []Alternative  `json:"alternatives,omitempty" yaml:"alternatives,omitempty"`
	Rationale    string         `json:"rationale" yaml:"rationale"`
	InfluencedBy []string       `json:"influenced_by,omitempty" yaml:"influenced_by,omitempty"`
	CreatedAt    time.Time      `json:"created_at" yaml:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at" yaml:"updated_at"`
}

// Alternative represents a considered but not chosen option for a decision.
type Alternative struct {
	Name            string `json:"name" yaml:"name"`
	Rationale       string `json:"rationale" yaml:"rationale"`
	RejectedBecause string `json:"rejected_because" yaml:"rejected_because"`
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
