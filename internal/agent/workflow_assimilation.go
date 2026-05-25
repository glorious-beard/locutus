package agent

// AssimilationWorkflow runs the codebase-inference council:
// scan → analyze (parallel domain analyzers) → gaps.
//
// Remediation runs OUTSIDE the workflow per DJ-045: cmd/assimilate.go
// invokes internal/remediate after Analyze returns, applying
// consolidation and attachment rules that a blind merge cannot honor.
var AssimilationWorkflow = &Workflow[PlanningState]{
	Snapshot:       snapshotPlanningState,
	DefaultProject: projectDefault,
	Rounds: []WorkflowStep[PlanningState]{
		{
			ID:      "scan",
			Agents:  []string{"scout"},
			Project: projectDefault,
			Merge:   mergeNoop,
		},
		{
			ID:        "analyze",
			Agents:    []string{"backend-analyzer", "frontend-analyzer", "infra-analyzer"},
			Parallel:  true,
			DependsOn: []string{"scan"},
			Project:   projectDefault,
			Merge:     mergeNoop,
		},
		{
			ID:        "gaps",
			Agents:    []string{"gap-analyst"},
			DependsOn: []string{"analyze"},
			Project:   projectDefault,
			Merge:     mergeNoop,
		},
	},
	MaxRounds: 1,
}
