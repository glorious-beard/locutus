package cmd

import (
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// sentinelPath is written before the persistence loop runs and removed on
// successful completion. If a later assimilate run finds it present, the
// prior run didn't finish cleanly — the sentinel surfaces that to the
// caller without needing an external lock.
const sentinelPath = ".borg/spec/.assimilating"

// persistAssimilationResult writes every node in the result to
// `.borg/spec/` using per-file write-temp-and-rename atomicity. New IDs
// land as fresh files; IDs that match an existing spec node overwrite
// the existing file (the LLM's output is the authoritative update per
// the Round-1 ambiguity 2 resolution: we gave it existing-spec context
// specifically so its output already reflects the merge decision).
//
// The top-level sentinel protects against crash-mid-write surfacing as
// a silently-committable half-state: if assimilate dies after N files
// but before all N+M, `.borg/spec/.assimilating` stays on disk and the
// next run warns.
//
// Per-file atomicity comes from temp-file-and-rename inside specio's
// SavePair / SaveMarkdown (or directly via WriteFile for pure markdown
// nodes). We don't currently have that — it's added in specio_atomic.go
// alongside this file.
func persistAssimilationResult(fsys specio.FS, result *agent.AssimilationResult) error {
	if result == nil {
		return nil
	}

	if err := writeSentinel(fsys); err != nil {
		return fmt.Errorf("assimilate: sentinel: %w", err)
	}

	if err := persistFeatures(fsys, result.Features); err != nil {
		return err
	}
	if err := persistDecisions(fsys, result.Decisions); err != nil {
		return err
	}
	if err := persistStrategies(fsys, result.Strategies); err != nil {
		return err
	}
	if err := persistApproaches(fsys, result.Approaches); err != nil {
		return err
	}
	// Entities are deliberately NOT persisted per DJ-076. They stay as
	// in-memory context on AssimilationResult for downstream agents to
	// consume in the same run; the authoritative schema is the code
	// itself (Go structs, migrations, protos).

	if err := removeSentinel(fsys); err != nil {
		return fmt.Errorf("assimilate: remove sentinel: %w", err)
	}
	return nil
}

func writeSentinel(fsys specio.FS) error {
	if err := fsys.MkdirAll(".borg/spec", 0o755); err != nil {
		return err
	}
	payload := []byte(fmt.Sprintf("started_at: %s\n", time.Now().UTC().Format(time.RFC3339)))
	return fsys.WriteFile(sentinelPath, payload, 0o644)
}

func removeSentinel(fsys specio.FS) error {
	err := fsys.Remove(sentinelPath)
	if err == nil {
		return nil
	}
	// Best-effort: if the file isn't there (dry-run) swallow the error.
	if _, statErr := fsys.Stat(sentinelPath); statErr != nil {
		return nil
	}
	return err
}

func persistFeatures(fsys specio.FS, features []spec.Feature) error {
	if err := fsys.MkdirAll(".borg/spec/features", 0o755); err != nil {
		return err
	}
	for i := range features {
		f := normalizeFeature(features[i])
		target := path.Join(".borg/spec/features", f.ID)
		body := f.Description
		if err := specio.SavePair(fsys, target, f, body); err != nil {
			return fmt.Errorf("save feature %s: %w", f.ID, err)
		}
	}
	return nil
}

func persistDecisions(fsys specio.FS, decisions []spec.Decision) error {
	if err := fsys.MkdirAll(".borg/spec/decisions", 0o755); err != nil {
		return err
	}
	for i := range decisions {
		d := normalizeDecision(decisions[i])
		target := path.Join(".borg/spec/decisions", d.ID)
		if err := specio.SavePair(fsys, target, d, d.Rationale); err != nil {
			return fmt.Errorf("save decision %s: %w", d.ID, err)
		}
	}
	return nil
}

func persistStrategies(fsys specio.FS, strategies []spec.Strategy) error {
	if err := fsys.MkdirAll(".borg/spec/strategies", 0o755); err != nil {
		return err
	}
	for _, s := range strategies {
		if s.ID == "" {
			continue
		}
		if s.Status == "" {
			s.Status = "inferred"
		}
		target := path.Join(".borg/spec/strategies", s.ID)
		if err := specio.SavePair(fsys, target, s, ""); err != nil {
			return fmt.Errorf("save strategy %s: %w", s.ID, err)
		}
	}
	return nil
}

func persistApproaches(fsys specio.FS, approaches []spec.Approach) error {
	if err := fsys.MkdirAll(".borg/spec/approaches", 0o755); err != nil {
		return err
	}
	now := time.Now()
	for i := range approaches {
		a := approaches[i]
		if a.ID == "" {
			continue
		}
		if a.CreatedAt.IsZero() {
			a.CreatedAt = now
		}
		a.UpdatedAt = now
		target := path.Join(".borg/spec/approaches", a.ID+".md")
		if err := specio.SaveMarkdown(fsys, target, a, a.Body); err != nil {
			return fmt.Errorf("save approach %s: %w", a.ID, err)
		}
	}
	return nil
}

// normalizeFeature backfills defaults for LLM-inferred Features that may
// have missing fields. Per DJ-019 a freshly-inferred Feature lands as
// `inferred` status. CreatedAt is stamped only when the node is new.
//
// Note on the two callers: brownfield `assimilate` leaves Status empty
// so the default fires (inferred — the spec was reverse-engineered from
// existing code). Greenfield/doc-driven `runSpecGeneration` stamps
// `proposed` on the Feature in `(*SpecProposal).ToAssimilationResult`
// before this function runs, so the default is bypassed for that path.
// The semantic split is intentional: inferred = derived-from-code,
// proposed = derived-from-doc-or-goals.
func normalizeFeature(f spec.Feature) spec.Feature {
	if f.Status == "" {
		f.Status = spec.FeatureStatusInferred
	}
	now := time.Now()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	return f
}

// normalizeDecision mirrors normalizeFeature for Decisions. The Decision
// enum already has DecisionStatusInferred; we default to it for missing
// status. Confidence is left as-is (zero is a meaningful signal).
//
// When Provenance is set but its GeneratedAt is zero, stamp it now —
// the council-generated path (greenfield/doc-driven) doesn't carry
// timestamps in the LLM proposal, but persisted provenance should know
// when it was written so future audits can see how stale it is.
func normalizeDecision(d spec.Decision) spec.Decision {
	if d.Status == "" {
		d.Status = spec.DecisionStatusInferred
	}
	now := time.Now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	if d.Provenance != nil && d.Provenance.GeneratedAt.IsZero() {
		d.Provenance.GeneratedAt = now
	}
	return d
}

// loadExistingSpec reads every spec node currently in .borg/spec/ and
// returns a snapshot suitable for the AssimilationRequest's ExistingSpec
// field. Missing directories (greenfield) are not an error — they yield
// an empty snapshot.
func loadExistingSpec(fsys specio.FS) *agent.ExistingSpec {
	snap := &agent.ExistingSpec{}

	if pairs, err := specio.WalkPairs[spec.Feature](fsys, ".borg/spec/features"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				snap.Features = append(snap.Features, p.Object)
			}
		}
	}
	if pairs, err := specio.WalkPairs[spec.Decision](fsys, ".borg/spec/decisions"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				snap.Decisions = append(snap.Decisions, p.Object)
			}
		}
	}
	if pairs, err := specio.WalkPairs[spec.Strategy](fsys, ".borg/spec/strategies"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				snap.Strategies = append(snap.Strategies, p.Object)
			}
		}
	}
	// Approaches are pure markdown.
	snap.Approaches = loadApproachesFromDir(fsys, ".borg/spec/approaches")
	// Entities are not loaded from disk — per DJ-076 they live only in
	// the transient AssimilationResult and are not persisted. A fresh
	// assimilate run reconstructs the entity projection from code.

	return snap
}

func loadApproachesFromDir(fsys specio.FS, dir string) []spec.Approach {
	paths, err := fsys.ListDir(dir)
	if err != nil {
		return nil
	}
	var out []spec.Approach
	for _, p := range paths {
		if len(p) < 3 || p[len(p)-3:] != ".md" {
			continue
		}
		obj, _, err := specio.LoadMarkdown[spec.Approach](fsys, p)
		if err == nil {
			out = append(out, obj)
		}
	}
	return out
}

// Helper shared with the tests that inspect the JSON of a recently-saved
// spec node. Declared here so tests don't re-parse inside their own
// scope.
func mustDecodeJSON(data []byte, into any) error {
	return json.Unmarshal(data, into)
}
