package check

import (
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// Commander abstracts command execution for testability.
type Commander interface {
	Run(name string, args ...string) (output []byte, err error)
}

// Check validates prerequisites for the given strategies using the provided Commander.
// Each strategy's Prerequisites is a list of shell command strings (e.g. "go version").
// The first token is the command name, remaining tokens are args.
func Check(cmd Commander, strategies []spec.Strategy) []Result {
	results := make([]Result, 0, len(strategies))

	for _, s := range strategies {
		r := Result{
			StrategyID:    s.ID,
			StrategyTitle: s.Title,
		}

		for _, prereq := range s.Prerequisites {
			parts := strings.Fields(prereq)
			name := parts[0]
			var args []string
			if len(parts) > 1 {
				args = parts[1:]
			}

			_, err := cmd.Run(name, args...)
			if err != nil {
				r.Failed = append(r.Failed, CheckFailure{
					Prerequisite: prereq,
					Err:          err.Error(),
				})
			} else {
				r.Passed = append(r.Passed, prereq)
			}
		}

		results = append(results, r)
	}

	return results
}
