package render

import (
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// ExplainNode renders a Markdown explanation of a single spec node by
// id. The id's prefix selects the kind: dec-, feat-, strat-, app-,
// bug-. Returns an error when the prefix is unknown or the node is
// not present in the loaded graph.
//
// stage is optional; pass nil or an empty map to skip implementation-
// stage tagging.
//
// No LLM calls. Reads the same Loaded graph the snapshot uses, so
// output is reproducible across runs given the same .borg/ contents.
func ExplainNode(l *spec.Loaded, stage spec.StageMap, id string) (string, error) {
	switch {
	case strings.HasPrefix(id, "dec-"):
		n := l.DecisionNodeByID(id)
		if n == nil {
			return "", fmt.Errorf("explain: decision %q not found", id)
		}
		return decorateExplain(id, RenderDecision(*n, l, stage)), nil
	case strings.HasPrefix(id, "feat-"):
		n := l.FeatureNodeByID(id)
		if n == nil {
			return "", fmt.Errorf("explain: feature %q not found", id)
		}
		return decorateExplain(id, RenderFeature(*n, l, stage)), nil
	case strings.HasPrefix(id, "strat-"):
		n := l.StrategyNodeByID(id)
		if n == nil {
			return "", fmt.Errorf("explain: strategy %q not found", id)
		}
		return decorateExplain(id, RenderStrategy(*n, l, stage)), nil
	case strings.HasPrefix(id, "app-"):
		n := l.ApproachNodeByID(id)
		if n == nil {
			return "", fmt.Errorf("explain: approach %q not found", id)
		}
		return decorateExplain(id, RenderApproach(*n, l)), nil
	case strings.HasPrefix(id, "bug-"):
		n := l.BugNodeByID(id)
		if n == nil {
			return "", fmt.Errorf("explain: bug %q not found", id)
		}
		return decorateExplain(id, RenderBug(*n, l)), nil
	}
	return "", fmt.Errorf("explain: id %q has unknown prefix (want dec-, feat-, strat-, app-, or bug-)", id)
}

// decorateExplain wraps a per-node Markdown section with a top-level
// `# id` header so a single-node document stands on its own. The
// per-node renderers produce subsection headers (### / ####) suited
// to inclusion in the snapshot — those still nest cleanly under the
// added top-level header here.
func decorateExplain(id, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# `%s`\n\n", id)
	b.WriteString(body)
	return b.String()
}
