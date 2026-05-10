package agent

// Cross-workflow helpers that aren't tied to any single verb. Per-verb
// closures (mergeProposedSpec, fanoutOutlineFeatures, etc.) live with
// their workflow declaration in workflow_<verb>.go.

// firstNonEmpty returns the Output of the first successful result whose
// content is non-empty. Used by single-output merge handlers across
// every workflow.
func firstNonEmpty(results []RoundResult) string {
	for _, r := range results {
		if r.Err == nil && r.Output != "" {
			return r.Output
		}
	}
	return ""
}

// mergeNoop applies no state mutation. Used by steps whose output is
// consumed downstream of the workflow (e.g. the assimilation pipeline
// parses RoundResults directly in parseAssimilationResults).
func mergeNoop(*PlanningState, []RoundResult) {}
