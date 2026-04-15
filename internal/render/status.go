// Package render provides data types and formatting for CLI output.
package render

// StatusData holds the gathered counts and flags for the status command.
type StatusData struct {
	GoalsPresent  bool
	FeatureCount  int
	DecisionCount int
	StrategyCount int
	BugCount      int
	EntityCount   int
	OrphanCount   int
}
