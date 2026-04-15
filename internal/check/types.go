package check

// Result holds the outcome of checking a single strategy's prerequisites.
type Result struct {
	StrategyID    string
	StrategyTitle string
	Passed        []string
	Failed        []CheckFailure
}

// CheckFailure describes a single prerequisite that was not met.
type CheckFailure struct {
	Prerequisite string
	Err string
}
