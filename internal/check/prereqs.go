package check

import (
	"os/exec"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// osCommander runs commands via os/exec. It's the default Commander used by
// CheckPrereqs; tests should still use a mock.
type osCommander struct{}

func (osCommander) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// NewOSCommander returns a Commander that shells out via os/exec.
func NewOSCommander() Commander { return osCommander{} }

// CheckPrereqs loads the active strategies from fsys and runs their
// prerequisites via the OS. This is the consolidated entry point used by
// both the `check` CLI command today and (in Phase C) by `adopt`'s
// pre-dispatch gate. Returning [] + nil means no strategies were found,
// which is not an error — a brand-new project has no prerequisites yet.
func CheckPrereqs(fsys specio.FS) ([]Result, error) {
	strategies, err := loadActiveStrategies(fsys)
	if err != nil {
		return nil, err
	}
	if len(strategies) == 0 {
		return nil, nil
	}
	return Check(NewOSCommander(), strategies), nil
}

// AnyFailed returns true if any result contains at least one failed
// prerequisite. Callers typically use this as the gate condition.
func AnyFailed(results []Result) bool {
	for _, r := range results {
		if len(r.Failed) > 0 {
			return true
		}
	}
	return false
}

func loadActiveStrategies(fsys specio.FS) ([]spec.Strategy, error) {
	results, err := specio.WalkPairs[spec.Strategy](fsys, ".borg/spec/strategies")
	if err != nil {
		return nil, nil
	}
	var active []spec.Strategy
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		if r.Object.Status == "" || r.Object.Status == "active" {
			active = append(active, r.Object)
		}
	}
	return active, nil
}
