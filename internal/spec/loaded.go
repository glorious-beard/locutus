package spec

import (
	"encoding/json"
	"strings"

	"github.com/chetan/locutus/internal/specio"
)

// Loaded is the in-memory snapshot of a project's spec under .borg/.
// Distinct from SpecGraph (which is for traversal — blast radius,
// transitive deps, topological sort): Loaded is for read-and-render
// paths that need typed nodes paired with their markdown bodies plus
// inverse-reference lookups. Snapshot rendering, explain, justify,
// and refine --diff all consume this shape.
//
// Inverse indexes are populated once at load time so per-call
// lookups are O(1).
type Loaded struct {
	Manifest  *Manifest
	GoalsBody string

	Features   []FeatureNode
	Strategies []StrategyNode
	Decisions  []DecisionNode
	Approaches []ApproachNode
	Bugs       []BugNode

	// Lookup tables.
	featureByID  map[string]*FeatureNode
	strategyByID map[string]*StrategyNode
	decisionByID map[string]*DecisionNode
	approachByID map[string]*ApproachNode
	bugByID      map[string]*BugNode

	// Inverse indexes built once at load.
	decisionRefsFromFeatures   map[string][]string
	decisionRefsFromStrategies map[string][]string
	approachRefsFromFeatures   map[string][]string
	approachRefsFromStrategies map[string][]string
	decisionInfluences         map[string][]string
	strategyInfluences         map[string][]string

	// Validation surfaces produced at load time.
	DanglingRefs []DanglingRef
	Orphans      []Orphan
}

// FeatureNode pairs a Feature with its markdown body and a per-load
// error. The Loaded graph still includes nodes whose pair loaded
// with a non-fatal error so the snapshot can render partial state;
// callers with stricter needs can filter on LoadErr.
type FeatureNode struct {
	Spec    Feature
	Body    string
	LoadErr error
}

// StrategyNode pairs a Strategy with its markdown body. Strategy.Body
// is not a field on the typed struct — the prose body lives only in
// the .md sidecar. This wrapper keeps them paired.
type StrategyNode struct {
	Spec    Strategy
	Body    string
	LoadErr error
}

// DecisionNode pairs a Decision with its markdown body. Body is
// typically empty for council-emitted decisions whose .md sidecar
// only carries the frontmatter; assimilated decisions may carry
// inferred-prose content.
type DecisionNode struct {
	Spec    Decision
	Body    string
	LoadErr error
}

// ApproachNode pairs an Approach with its markdown body. Approaches
// are markdown-only on disk (no .json sidecar); the typed Approach
// struct holds frontmatter, Body holds the synthesized brief.
type ApproachNode struct {
	Spec    Approach
	Body    string
	LoadErr error
}

// BugNode pairs a Bug with its markdown body.
type BugNode struct {
	Spec    Bug
	Body    string
	LoadErr error
}

// DanglingRef is a forward reference whose target id is not in the
// graph. Surfaced in Loaded.DanglingRefs for the snapshot's
// validation section.
type DanglingRef struct {
	FromKind   NodeKind
	FromID     string
	Field      string // "decisions", "approaches", "influenced_by", "parent_id"
	TargetID   string
	TargetKind NodeKind // empty when ParentID could be any of feature/strategy/bug
}

// Orphan is a node with no incoming references. Decisions and
// Approaches are flagged when nothing references them; features and
// bugs are top-level by design. Strategies stand alone normally so
// they're suppressed from orphan classification — a future "deeply
// orphaned" surface could flag strategies whose decisions are also
// unused.
type Orphan struct {
	Kind NodeKind
	ID   string
}

// LoadSpec reads .borg/spec/{features,strategies,decisions,approaches,bugs}/
// and the manifest + goals into one in-memory Loaded. Per-node load
// errors are non-fatal — the node is included with LoadErr set so
// the snapshot still renders something useful even when one file is
// corrupt.
func LoadSpec(fsys specio.FS) (*Loaded, error) {
	l := &Loaded{
		featureByID:                map[string]*FeatureNode{},
		strategyByID:               map[string]*StrategyNode{},
		decisionByID:               map[string]*DecisionNode{},
		approachByID:               map[string]*ApproachNode{},
		bugByID:                    map[string]*BugNode{},
		decisionRefsFromFeatures:   map[string][]string{},
		decisionRefsFromStrategies: map[string][]string{},
		approachRefsFromFeatures:   map[string][]string{},
		approachRefsFromStrategies: map[string][]string{},
		decisionInfluences:         map[string][]string{},
		strategyInfluences:         map[string][]string{},
	}

	if data, err := fsys.ReadFile(".borg/manifest.json"); err == nil {
		var m Manifest
		if err := json.Unmarshal(data, &m); err == nil {
			l.Manifest = &m
		}
	}
	if data, err := fsys.ReadFile(".borg/GOALS.md"); err == nil {
		l.GoalsBody = string(data)
	}

	if pairs, err := specio.WalkPairs[Feature](fsys, ".borg/spec/features"); err == nil {
		for _, p := range pairs {
			l.Features = append(l.Features, FeatureNode{Spec: p.Object, Body: p.Body, LoadErr: p.Err})
		}
		for i := range l.Features {
			l.featureByID[l.Features[i].Spec.ID] = &l.Features[i]
		}
	}
	if pairs, err := specio.WalkPairs[Strategy](fsys, ".borg/spec/strategies"); err == nil {
		for _, p := range pairs {
			l.Strategies = append(l.Strategies, StrategyNode{Spec: p.Object, Body: p.Body, LoadErr: p.Err})
		}
		for i := range l.Strategies {
			l.strategyByID[l.Strategies[i].Spec.ID] = &l.Strategies[i]
		}
	}
	if pairs, err := specio.WalkPairs[Decision](fsys, ".borg/spec/decisions"); err == nil {
		for _, p := range pairs {
			l.Decisions = append(l.Decisions, DecisionNode{Spec: p.Object, Body: p.Body, LoadErr: p.Err})
		}
		for i := range l.Decisions {
			l.decisionByID[l.Decisions[i].Spec.ID] = &l.Decisions[i]
		}
	}
	if pairs, err := specio.WalkPairs[Bug](fsys, ".borg/spec/bugs"); err == nil {
		for _, p := range pairs {
			l.Bugs = append(l.Bugs, BugNode{Spec: p.Object, Body: p.Body, LoadErr: p.Err})
		}
		for i := range l.Bugs {
			l.bugByID[l.Bugs[i].Spec.ID] = &l.Bugs[i]
		}
	}
	if files, err := fsys.ListDir(".borg/spec/approaches"); err == nil {
		for _, f := range files {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			obj, body, loadErr := specio.LoadMarkdown[Approach](fsys, f)
			l.Approaches = append(l.Approaches, ApproachNode{Spec: obj, Body: body, LoadErr: loadErr})
		}
		for i := range l.Approaches {
			l.approachByID[l.Approaches[i].Spec.ID] = &l.Approaches[i]
		}
	}

	l.buildInverseIndexes()
	l.findDanglingRefs()
	l.findOrphans()
	return l, nil
}

func (l *Loaded) buildInverseIndexes() {
	for _, f := range l.Features {
		for _, did := range f.Spec.Decisions {
			l.decisionRefsFromFeatures[did] = append(l.decisionRefsFromFeatures[did], f.Spec.ID)
		}
		for _, aid := range f.Spec.Approaches {
			l.approachRefsFromFeatures[aid] = append(l.approachRefsFromFeatures[aid], f.Spec.ID)
		}
	}
	for _, s := range l.Strategies {
		for _, did := range s.Spec.Decisions {
			l.decisionRefsFromStrategies[did] = append(l.decisionRefsFromStrategies[did], s.Spec.ID)
		}
		for _, aid := range s.Spec.Approaches {
			l.approachRefsFromStrategies[aid] = append(l.approachRefsFromStrategies[aid], s.Spec.ID)
		}
		for _, sid := range s.Spec.InfluencedBy {
			l.strategyInfluences[sid] = append(l.strategyInfluences[sid], s.Spec.ID)
		}
	}
	for _, d := range l.Decisions {
		for _, did := range d.Spec.InfluencedBy {
			l.decisionInfluences[did] = append(l.decisionInfluences[did], d.Spec.ID)
		}
	}
}

func (l *Loaded) findDanglingRefs() {
	addIfMissing := func(fromKind NodeKind, fromID, field, targetID string, targetKind NodeKind) {
		exists := false
		switch targetKind {
		case KindDecision:
			_, exists = l.decisionByID[targetID]
		case KindStrategy:
			_, exists = l.strategyByID[targetID]
		case KindApproach:
			_, exists = l.approachByID[targetID]
		case KindFeature:
			_, exists = l.featureByID[targetID]
		}
		if !exists {
			l.DanglingRefs = append(l.DanglingRefs, DanglingRef{
				FromKind: fromKind, FromID: fromID,
				Field: field, TargetID: targetID, TargetKind: targetKind,
			})
		}
	}
	for _, f := range l.Features {
		for _, id := range f.Spec.Decisions {
			addIfMissing(KindFeature, f.Spec.ID, "decisions", id, KindDecision)
		}
		for _, id := range f.Spec.Approaches {
			addIfMissing(KindFeature, f.Spec.ID, "approaches", id, KindApproach)
		}
	}
	for _, s := range l.Strategies {
		for _, id := range s.Spec.Decisions {
			addIfMissing(KindStrategy, s.Spec.ID, "decisions", id, KindDecision)
		}
		for _, id := range s.Spec.Approaches {
			addIfMissing(KindStrategy, s.Spec.ID, "approaches", id, KindApproach)
		}
		for _, id := range s.Spec.InfluencedBy {
			addIfMissing(KindStrategy, s.Spec.ID, "influenced_by", id, KindStrategy)
		}
	}
	for _, d := range l.Decisions {
		for _, id := range d.Spec.InfluencedBy {
			addIfMissing(KindDecision, d.Spec.ID, "influenced_by", id, KindDecision)
		}
	}
	for _, a := range l.Approaches {
		if a.Spec.ParentID == "" {
			continue
		}
		_, fOK := l.featureByID[a.Spec.ParentID]
		_, sOK := l.strategyByID[a.Spec.ParentID]
		_, bOK := l.bugByID[a.Spec.ParentID]
		if !fOK && !sOK && !bOK {
			l.DanglingRefs = append(l.DanglingRefs, DanglingRef{
				FromKind: KindApproach, FromID: a.Spec.ID,
				Field: "parent_id", TargetID: a.Spec.ParentID,
			})
		}
		for _, id := range a.Spec.Decisions {
			addIfMissing(KindApproach, a.Spec.ID, "decisions", id, KindDecision)
		}
	}
}

func (l *Loaded) findOrphans() {
	for _, d := range l.Decisions {
		if len(l.decisionRefsFromFeatures[d.Spec.ID]) == 0 &&
			len(l.decisionRefsFromStrategies[d.Spec.ID]) == 0 &&
			len(l.decisionInfluences[d.Spec.ID]) == 0 {
			l.Orphans = append(l.Orphans, Orphan{Kind: KindDecision, ID: d.Spec.ID})
		}
	}
	for _, a := range l.Approaches {
		if len(l.approachRefsFromFeatures[a.Spec.ID]) == 0 &&
			len(l.approachRefsFromStrategies[a.Spec.ID]) == 0 {
			l.Orphans = append(l.Orphans, Orphan{Kind: KindApproach, ID: a.Spec.ID})
		}
	}
}

// FeaturesReferencingDecision returns the feature ids that list
// decisionID in their decisions[]. Returned slice is in directory-
// listing order (alphabetical).
func (l *Loaded) FeaturesReferencingDecision(decisionID string) []string {
	return l.decisionRefsFromFeatures[decisionID]
}

// StrategiesReferencingDecision returns the strategy ids that list
// decisionID in their decisions[].
func (l *Loaded) StrategiesReferencingDecision(decisionID string) []string {
	return l.decisionRefsFromStrategies[decisionID]
}

// FeaturesReferencingApproach returns the feature ids whose
// approaches[] contains approachID.
func (l *Loaded) FeaturesReferencingApproach(approachID string) []string {
	return l.approachRefsFromFeatures[approachID]
}

// StrategiesReferencingApproach returns the strategy ids whose
// approaches[] contains approachID.
func (l *Loaded) StrategiesReferencingApproach(approachID string) []string {
	return l.approachRefsFromStrategies[approachID]
}

// DecisionsInfluencedByDecision returns the decision ids that list
// decisionID in their influenced_by — i.e., decisions this one
// helped shape.
func (l *Loaded) DecisionsInfluencedByDecision(decisionID string) []string {
	return l.decisionInfluences[decisionID]
}

// StrategiesInfluencedByStrategy returns the strategy ids that list
// strategyID in their influenced_by.
func (l *Loaded) StrategiesInfluencedByStrategy(strategyID string) []string {
	return l.strategyInfluences[strategyID]
}

// FeatureNode lookup by id; nil if absent.
func (l *Loaded) FeatureNodeByID(id string) *FeatureNode { return l.featureByID[id] }

// StrategyNode lookup by id; nil if absent.
func (l *Loaded) StrategyNodeByID(id string) *StrategyNode { return l.strategyByID[id] }

// DecisionNode lookup by id; nil if absent.
func (l *Loaded) DecisionNodeByID(id string) *DecisionNode { return l.decisionByID[id] }

// ApproachNode lookup by id; nil if absent.
func (l *Loaded) ApproachNodeByID(id string) *ApproachNode { return l.approachByID[id] }

// BugNode lookup by id; nil if absent.
func (l *Loaded) BugNodeByID(id string) *BugNode { return l.bugByID[id] }
