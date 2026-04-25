// Package overlap detects file conflicts between parallel workstreams in
// a MasterPlan, per DJ-030. The detector is mechanical and layout-agnostic:
// it answers "given the files each workstream declares it will touch,
// which workstream pairs literally overlap?". It does not judge whether
// the overlap is benign or harmful — that qualitative call belongs to the
// planner on retry.
//
// Inputs to a workstream's "files-it-will-touch" set:
//   - PlanStep.ExpectedFiles for every step in the workstream.
//   - Approach.ArtifactPaths for every Approach a step references.
//
// The two are unioned so a planner that populates ExpectedFiles beyond
// the underlying Approach's ArtifactPaths still has its declared writes
// counted. Round 6 takes no position on whether ExpectedFiles is supposed
// to be a strict subset, additive, or free — that's a separate spec
// architecture decision tracked as a follow-up.
//
// Two workstreams are considered "parallel" (and thus subject to overlap
// detection) if neither one transitively depends on the other via the
// DependsOn graph. Sequential workstreams sharing a file is fine — they
// run in series, no actual write contention.
//
// Intra-workstream sharing is also allowed: two Approaches inside the
// same workstream may target the same file. They execute in the same
// agent session; the agent decides write order.
package overlap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// Report describes one overlapping pair. WorkstreamA and WorkstreamB are
// always sorted (A < B lexicographically) so the report list itself is
// deterministic.
type Report struct {
	WorkstreamA string
	WorkstreamB string
	SharedFiles []string
}

// Detect returns every inter-workstream file overlap in the plan.
// Returns nil when the plan is nil, has fewer than two workstreams, or
// has no overlaps.
func Detect(plan *spec.MasterPlan, approachesByID map[string]spec.Approach) []Report {
	if plan == nil || len(plan.Workstreams) < 2 {
		return nil
	}

	filesPerWS := buildFileSets(plan.Workstreams, approachesByID)
	reachable := buildTransitiveDeps(plan.Workstreams)

	ids := make([]string, 0, len(plan.Workstreams))
	for _, ws := range plan.Workstreams {
		ids = append(ids, ws.ID)
	}
	sort.Strings(ids)

	var reports []Report
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := ids[i], ids[j]
			if isSequential(a, b, reachable) {
				continue
			}
			shared := intersect(filesPerWS[a], filesPerWS[b])
			if len(shared) == 0 {
				continue
			}
			reports = append(reports, Report{
				WorkstreamA: a,
				WorkstreamB: b,
				SharedFiles: shared,
			})
		}
	}
	return reports
}

// FormatReports renders the overlap list as a human-readable block
// suitable for embedding in a planner-retry prompt. Sorted output;
// stable across calls.
func FormatReports(reports []Report) string {
	if len(reports) == 0 {
		return "(no overlaps)"
	}
	var b strings.Builder
	for _, r := range reports {
		fmt.Fprintf(&b, "- %s ↔ %s: %s\n", r.WorkstreamA, r.WorkstreamB, strings.Join(r.SharedFiles, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildFileSets walks each workstream's steps and unions the step's
// ExpectedFiles with the referenced Approach's ArtifactPaths. A workstream
// with no steps and no resolvable approaches yields an empty set — it
// will overlap with no one, which is the correct conservative behavior.
func buildFileSets(workstreams []spec.Workstream, approachesByID map[string]spec.Approach) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{}, len(workstreams))
	for _, ws := range workstreams {
		files := make(map[string]struct{})
		for _, step := range ws.Steps {
			for _, f := range step.ExpectedFiles {
				files[f] = struct{}{}
			}
			if a, ok := approachesByID[step.ApproachID]; ok {
				for _, f := range a.ArtifactPaths {
					files[f] = struct{}{}
				}
			}
		}
		out[ws.ID] = files
	}
	return out
}

// buildTransitiveDeps returns reachable[a] = the set of workstream IDs
// reachable from `a` by following DependsOn edges (i.e. workstreams `a`
// transitively depends on). Used for the sequential-pair check.
func buildTransitiveDeps(workstreams []spec.Workstream) map[string]map[string]struct{} {
	direct := make(map[string][]string, len(workstreams))
	for _, ws := range workstreams {
		for _, dep := range ws.DependsOn {
			direct[ws.ID] = append(direct[ws.ID], dep.WorkstreamID)
		}
	}

	reachable := make(map[string]map[string]struct{}, len(workstreams))
	for _, ws := range workstreams {
		seen := make(map[string]struct{})
		var dfs func(string)
		dfs = func(id string) {
			for _, next := range direct[id] {
				if _, already := seen[next]; already {
					continue
				}
				seen[next] = struct{}{}
				dfs(next)
			}
		}
		dfs(ws.ID)
		reachable[ws.ID] = seen
	}
	return reachable
}

// isSequential reports whether `a` and `b` are linked by a transitive
// DependsOn chain in either direction. Sequential pairs are exempt from
// overlap detection.
func isSequential(a, b string, reachable map[string]map[string]struct{}) bool {
	if _, ok := reachable[a][b]; ok {
		return true
	}
	if _, ok := reachable[b][a]; ok {
		return true
	}
	return false
}

func intersect(a, b map[string]struct{}) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	var out []string
	for f := range a {
		if _, ok := b[f]; ok {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}
