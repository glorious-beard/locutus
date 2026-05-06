package spec

import (
	"strings"

	"github.com/chetan/locutus/internal/specio"
)

// ImplementationStage classifies where a feature/strategy/decision
// sits in the spec → implementation → done pipeline.
//
// The five canonical stages cover the full lifecycle, but only three
// are derivable from the data we persist today (approach existence
// + .locutus/workstreams/ activity). `Done` and `Drifted` require a
// SpecHash on Approach that we haven't added yet — see the comment
// on DeriveStage. They're declared here so the type's vocabulary is
// stable; the derivation will surface them once the hash field
// lands.
type ImplementationStage string

const (
	StageDrafted      ImplementationStage = "drafted"
	StagePlanned      ImplementationStage = "planned"
	StageImplementing ImplementationStage = "implementing"
	StageDone         ImplementationStage = "done"
	StageDrifted      ImplementationStage = "drifted"
)

// StageMap holds per-node implementation stage. Keyed by node id so
// callers can look up any feature/strategy/decision by its known id.
// Entries are added for every node in Loaded; nodes with insufficient
// signal default to StageDrafted.
type StageMap map[string]ImplementationStage

// DeriveStages walks the loaded spec graph plus the on-disk
// .locutus/workstreams/ tree and assigns each feature, strategy, and
// decision an ImplementationStage. Decisions inherit from the most-
// advanced feature/strategy that references them (a decision used by
// a feature in `implementing` is itself `implementing` even if
// another feature using it is still `drafted`).
//
// Today's derivation:
//   - drafted: node has no approaches in its approaches[] list
//   - planned: node lists ≥1 approach; no active workstream
//     references any of those approaches
//   - implementing: a workstream record under .locutus/workstreams/
//     lists one of the node's approach ids in its approach_ids
//
// Future:
//   - done: workstream record was removed (run completed) AND the
//     approach's stored SpecHash matches the current node hash.
//     Requires SpecHash on Approach (DJ-072 sketches this; not
//     implemented).
//   - drifted: workstream completed but spec moved underneath. Same
//     SpecHash dependency.
//
// Callers needing only "is this implementing?" can read the
// returned map directly; the full five-stage discrimination is
// available when the data supports it.
func DeriveStages(l *Loaded, fsys specio.FS) StageMap {
	stages := make(StageMap, len(l.Features)+len(l.Strategies)+len(l.Decisions))

	// Walk active workstreams and build "approach id → has-active-
	// workstream" set. Workstreams live at
	// .locutus/workstreams/<plan-id>/<ws-id>.yaml; we treat the
	// presence of any workstream YAML referencing an approach as
	// evidence of in-flight implementation.
	approachInFlight := map[string]bool{}
	if planDirs, err := fsys.ListSubdirs(".locutus/workstreams"); err == nil {
		for _, planDir := range planDirs {
			files, err := fsys.ListDir(planDir)
			if err != nil {
				continue
			}
			for _, f := range files {
				if !strings.HasSuffix(f, ".yaml") {
					continue
				}
				// We don't decode the workstream YAML here — that
				// would couple internal/spec to internal/workstream
				// (currently a one-way dep, internal/spec is
				// upstream of workstream). Instead we infer
				// approach refs by the filename pattern: workstream
				// IDs encode the approach id when present, but the
				// canonical link is in the YAML's approach_ids
				// field. Without parsing, treat ANY workstream YAML
				// as evidence of in-flight activity for the
				// approaches its filename references — and for the
				// detailed link, use a small parse-free helper that
				// extracts approach_ids via line-grep. Rough but
				// avoids the import cycle.
				//
				// See approachIDsInWorkstream below.
				for _, aid := range approachIDsInWorkstream(fsys, f) {
					approachInFlight[aid] = true
				}
			}
		}
	}

	classify := func(approachIDs []string) ImplementationStage {
		if len(approachIDs) == 0 {
			return StageDrafted
		}
		for _, aid := range approachIDs {
			if approachInFlight[aid] {
				return StageImplementing
			}
		}
		return StagePlanned
	}

	for _, f := range l.Features {
		stages[f.Spec.ID] = classify(f.Spec.Approaches)
	}
	for _, s := range l.Strategies {
		stages[s.Spec.ID] = classify(s.Spec.Approaches)
	}

	// Decisions inherit the most-advanced stage of any feature or
	// strategy that references them. Order: implementing > planned
	// > drafted (done/drifted can't fire today).
	stageRank := map[ImplementationStage]int{
		StageDrafted:      0,
		StagePlanned:      1,
		StageImplementing: 2,
		StageDone:         3,
		StageDrifted:      4,
	}
	for _, d := range l.Decisions {
		current := StageDrafted
		for _, fid := range l.FeaturesReferencingDecision(d.Spec.ID) {
			if s, ok := stages[fid]; ok && stageRank[s] > stageRank[current] {
				current = s
			}
		}
		for _, sid := range l.StrategiesReferencingDecision(d.Spec.ID) {
			if s, ok := stages[sid]; ok && stageRank[s] > stageRank[current] {
				current = s
			}
		}
		stages[d.Spec.ID] = current
	}

	return stages
}

// approachIDsInWorkstream extracts approach ids from a workstream
// YAML by scanning lines for `approach_ids:` followed by a list of
// quoted ids. This avoids importing internal/workstream (which would
// flip the dependency direction). The shape it parses:
//
//	approach_ids:
//	  - app-foo
//	  - app-bar
//
// Tolerates malformed files by returning whatever it could parse —
// best-effort, since the stage map is informational, not load-
// bearing for any state transition.
func approachIDsInWorkstream(fsys specio.FS, p string) []string {
	data, err := fsys.ReadFile(p)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var ids []string
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "approach_ids:" {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if !strings.HasPrefix(trimmed, "-") {
			// End of the list (any non-list-item line).
			break
		}
		id := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		id = strings.Trim(id, `"'`)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// StageDistribution returns a count of nodes per stage, suitable for
// rendering a status header summary. Decisions, features, and
// strategies all contribute.
type StageDistribution struct {
	Drafted      int
	Planned      int
	Implementing int
	Done         int
	Drifted      int
}

// CountStages tallies a StageMap into a StageDistribution.
func CountStages(stages StageMap) StageDistribution {
	var d StageDistribution
	for _, s := range stages {
		switch s {
		case StageDrafted:
			d.Drafted++
		case StagePlanned:
			d.Planned++
		case StageImplementing:
			d.Implementing++
		case StageDone:
			d.Done++
		case StageDrifted:
			d.Drifted++
		}
	}
	return d
}
