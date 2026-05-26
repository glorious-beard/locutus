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
	ID string `json:"id" yaml:"id"`
	// Summary is a one-sentence "what" description of the decision,
	// authored by the producing agent. Distinct from Rationale (the
	// "why") and ArchitectRationale (a one-sentence rationale summary).
	// Consumed by the spec-lookup tools (spec_list_manifest) so the
	// council can scan the index without dumping full node content.
	// Required at write time; legacy nodes without it are filled by
	// the SummariesPresent prereq.
	Summary      string              `json:"summary,omitempty" yaml:"summary,omitempty"`
	Title        string              `json:"title" yaml:"title"`
	Status       DecisionStatus      `json:"status" yaml:"status"`
	Confidence   float64             `json:"confidence" yaml:"confidence"`
	Alternatives []Alternative       `json:"alternatives,omitempty" yaml:"alternatives,omitempty"`
	Rationale    string              `json:"rationale" yaml:"rationale"`
	Provenance   *DecisionProvenance `json:"provenance,omitempty" yaml:"provenance,omitempty"`
	InfluencedBy []string            `json:"influenced_by,omitempty" yaml:"influenced_by,omitempty"`
	// Axes are the foundational axis IDs this decision answers (DJ-124).
	// Set by the scout-controlled dispatch at decision-creation time;
	// legacy decisions authored before DJ-124 load with an empty slice
	// (the yaml/json loader does not validate against the registered
	// jsonschema, so the `,omitempty` keeps legacy on-disk decisions
	// loadable while the minItems=1 tag enforces non-empty for newly
	// authored decisions traveling through an output schema).
	Axes []string `json:"axes,omitempty" yaml:"axes,omitempty" jsonschema:"description=Stable slug-IDs of the foundational axes this decision answers. Examples: [\"auth-provider\"], [\"compute-platform\",\"deployment-target\"]. Multiple axes mean the decision spans them. Names match what the scout enumerated.,minItems=1"`
	// SurfacedBy back-references the spec nodes whose axis this
	// decision answers (DJ-124). Populated at decision-creation time
	// by the scout's dispatch; legacy decisions load with an empty
	// slice. Supports future explain/justify verbs walking the graph
	// in both directions.
	SurfacedBy   []string  `json:"surfaced_by,omitempty" yaml:"surfaced_by,omitempty" jsonschema:"description=Spec node IDs (goal / feature / strategy) that surfaced the axis this decision answers. Examples: [\"feat-realtime-dashboard\"], [\"strat-storage-platform\",\"goal-multi-tenancy\"]. Populated by the scout's dispatch at decision-creation time. Empty for legacy decisions authored before DJ-124."`
	CreatedAt    time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" yaml:"updated_at"`
	// Locked is set when the convergence loop's per-axis revision cap
	// fired and committed this decision as its best-answer snapshot
	// (DJ-128 cap-as-commit). Locked decisions are excluded from the
	// revise-fanout dispatch on subsequent iterations — their
	// associated concerns are flipped to wontfix at the moment the cap
	// fires, and the decision is shipped with the contested reasoning
	// preserved in the alternatives deliberation log. Persisted with
	// omitempty so legacy / unlocked decisions look identical on-disk
	// to their pre-DJ-128 form.
	Locked bool `json:"locked,omitempty" yaml:"locked,omitempty" jsonschema:"description=True when the per-axis revision cap fired on this decision and the loop committed its latest revision as the ship-quality answer (DJ-128 cap-as-commit). Locked decisions are excluded from subsequent revise dispatches; the deliberation log in alternatives preserves what was contested. False for decisions that converged organically."`
}

// Alternative represents a considered but not chosen option for a decision.
//
// DJ-128 adds two deliberation-log provenance fields:
// RejectedAtIteration and RejectedByConcernText. They are populated by
// mergeDecisions when the elaborator demotes a prior chosen option (or
// a critic counterproposal that lost out) into the alternatives slice
// during a revise pass. Both fields are omitempty so first-author
// alternatives (those the elaborator weighed before a critic intervened)
// load without artifacts.
type Alternative struct {
	Name            string `json:"name" yaml:"name" jsonschema:"description=The alternative's name — a concrete product or approach (e.g. 'MySQL' or 'Server-rendered React'). A noun phrase rather than a sentence; do not paraphrase the decision."`
	Rationale       string `json:"rationale" yaml:"rationale" jsonschema:"description=Why this alternative was considered seriously. A complete sentence naming the real advantages it offered over the chosen path. Empty / 'no reason' indicates the alternative wasn't worth listing."`
	RejectedBecause string `json:"rejected_because" yaml:"rejected_because" jsonschema:"description=The specific reason this alternative lost to the chosen option. A complete sentence pointing at a goal clause / constraint / trade-off. 'Not as good' is not a rejection reason — name the constraint."`
	// Citations back the rejected_because reasoning so the rejection
	// is grounded evidence rather than fabricated trade-off prose
	// (DJ-124). The field is required at the schema layer (no
	// `,omitempty`) so strict-mode providers reject alternatives
	// authored without citations; Go's encoding/json does not enforce
	// `required` during unmarshal, so legacy on-disk alternatives
	// without citations continue to deserialize cleanly.
	Citations []Citation `json:"citations" yaml:"citations" jsonschema:"description=Citations backing the rejected_because reasoning for this alternative — evidence that this option was considered seriously and the reason it lost is grounded.,minItems=1"`
	// RejectedAtIteration is the council iteration index at which this
	// alternative was demoted from the chosen position (or from a critic
	// counterproposal) into the alternatives slice (DJ-128). Zero for
	// first-author alternatives the elaborator weighed before any critic
	// engaged; set positive only by mergeDecisions during a revise pass.
	RejectedAtIteration int `json:"rejected_at_iteration,omitempty" yaml:"rejected_at_iteration,omitempty" jsonschema:"description=Iteration index at which this alternative was demoted into the alternatives slice via a revise pass. Zero for first-author alternatives the elaborator weighed before any critic engaged; positive when a critic concern displaced a prior chosen option or a counterproposal lost on review."`
	// RejectedByConcernText carries the verbatim text of the critic
	// concern that drove the demotion (DJ-128). Empty for first-author
	// alternatives. The deliberation log uses this so next-iteration
	// critics see what was contested and don't re-litigate the same axis.
	RejectedByConcernText string `json:"rejected_by_concern_text,omitempty" yaml:"rejected_by_concern_text,omitempty" jsonschema:"description=Verbatim text of the critic concern that drove this alternative into the deliberation log. Empty for first-author alternatives; populated by mergeDecisions when a revise pass demotes a prior chosen option or rejects a counterproposal. Lets next-iteration critics see what was contested and avoid re-litigating settled rejections."`
}

// Citation is one durable reference backing a decision: a span of
// GOALS.md, a feature document the user imported, a named engineering
// best practice ("12-factor app: stateless processes"), another spec
// node, or a fact lifted from the scout brief (DJ-104). Excerpt holds
// the verbatim text when relevant so the citation survives the source
// file being moved or rewritten.
type Citation struct {
	// Kind is one of "goals", "doc", "best_practice", "spec_node",
	// "scout_brief", "web". The scout_brief variant requires Excerpt —
	// the scout's grounded output is the load-bearing source for that
	// citation, and the verbatim copy keeps the provenance durable
	// even after the survey artifact is gone. The web variant (DJ-124)
	// captures grounded-research evidence fetched by the decision-
	// elaborator: Reference holds the URL, Excerpt holds the verbatim
	// quote so the citation survives the page changing.
	Kind string `json:"kind" yaml:"kind" jsonschema:"enum=goals,enum=doc,enum=best_practice,enum=spec_node,enum=scout_brief,enum=web,description=The source category. goals=GOALS.md clause; doc=user-imported feature document; best_practice=named engineering principle; spec_node=another node in the spec graph; scout_brief=fact from the spec-scout's output (this variant requires Excerpt to keep provenance durable); web=URL fetched as grounded research evidence (Reference is the URL; Excerpt carries the verbatim quote)."`
	// Reference identifies the source: a path ("GOALS.md",
	// "docs/dashboard.md"), a named principle ("12-factor app: stateless
	// processes"), or a spec node id ("strat-frontend").
	Reference string `json:"reference" yaml:"reference" jsonschema:"description=Identifier of the source — a filesystem path like 'GOALS.md' or 'docs/dashboard.md'; a named principle like '12-factor app: stateless processes'; a spec node id like 'strat-frontend'; or a URL for web kind (e.g., 'https://www.postgresql.org/docs/16/datatype-json.html'). Matches the Kind: paths for goals/doc; principle names for best_practice; ids for spec_node; an agent label for scout_brief; URLs for web."`
	// Span localises within Reference when applicable: a line range
	// ("lines 12-18"), a section heading ("## In Scope"), a factor name
	// ("factor VI"), or empty for whole-document references.
	Span string `json:"span,omitempty" yaml:"span,omitempty" jsonschema:"description=Localiser within Reference — a line range like 'lines 12-18'; a section heading like '## In Scope'; or a factor name like 'factor VI'. Empty for whole-document references."`
	// Excerpt is the verbatim quote being cited. Persisted so a
	// citation survives the source moving — durable evidence, not a
	// pointer.
	Excerpt string `json:"excerpt,omitempty" yaml:"excerpt,omitempty" jsonschema:"description=Verbatim quote from the source being cited. Required for Kind=scout_brief and Kind=web — both sources are ephemeral relative to the citation (scout briefs are session-scoped; web pages change after the citation is captured). Strongly recommended for goals/doc; the excerpt survives the source being moved or rewritten."`
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
	ID string `json:"id" yaml:"id"`
	// Summary: see Decision.Summary.
	Summary       string            `json:"summary,omitempty" yaml:"summary,omitempty"`
	Title         string            `json:"title" yaml:"title"`
	Kind          StrategyKind      `json:"kind" yaml:"kind"`
	Decisions     []string          `json:"decisions,omitempty" yaml:"decisions,omitempty" jsonschema:"description=Decision IDs this strategy depends on. The scout determines membership during gap analysis; every entry must reference a decision present in the graph at integrity-check time.,minItems=1"`
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
	ID string `json:"id" yaml:"id"`
	// Summary: see Decision.Summary.
	Summary            string        `json:"summary,omitempty" yaml:"summary,omitempty"`
	Title              string        `json:"title" yaml:"title"`
	Status             FeatureStatus `json:"status" yaml:"status"`
	Description        string        `json:"description,omitempty" yaml:"description,omitempty"`
	AcceptanceCriteria []string      `json:"acceptance_criteria,omitempty" yaml:"acceptance_criteria,omitempty"`
	Decisions          []string      `json:"decisions,omitempty" yaml:"decisions,omitempty" jsonschema:"description=Decision IDs this feature depends on. The scout determines membership during gap analysis; every entry must reference a decision present in the graph at integrity-check time.,minItems=1"`
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
