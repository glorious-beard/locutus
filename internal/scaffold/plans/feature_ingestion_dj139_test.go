// DJ-139 phase 7 — assertions on the goal-layer reads, structural
// conflict detection, GOALS.md diff drafter, and citation population
// extensions to the feature_ingestion playbook. The import workflow
// no longer LLM-judges scope against GOALS.md prose; instead it
// fetches the persisted goal-* / agoal-* nodes via the manifest and
// tests the feature's domain structurally against each agoal-*
// node's body and kept_in carve-outs. When a conflict surfaces and
// the feature plausibly fits under an extended or new carve-out,
// the playbook drafts a unified diff against GOALS.md for the
// operator. On admission the playbook populates .advances /
// .respects via the propose tool's DJ-139 optional fields.
// See DJ-139 phase 7.

package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadFeatureIngestion returns the canonical feature_ingestion.md
// content. Reads from the package directory at test time — go test
// sets cwd to the package directory.
func loadFeatureIngestion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("feature_ingestion.md")
	require.NoError(t, err)
	return string(b)
}

// TestFeatureIngestionPlaybookReadsGoalLayer — the playbook's
// context-fetch step reads goal-* and agoal-* nodes alongside the
// manifest. The structural conflict-detection step (replacing the
// prior LLM-judges-GOALS.md-prose flow) depends on these node
// kinds being addressable by id prefix in the playbook prose.
func TestFeatureIngestionPlaybookReadsGoalLayer(t *testing.T) {
	body := loadFeatureIngestion(t)
	assert.Contains(t, body, "goal-*",
		"playbook must name the goal-* prefix it reads for forward-direction context")
	assert.Contains(t, body, "agoal-*",
		"playbook must name the agoal-* prefix it tests the feature's domain against")
	assert.Contains(t, body, "mcp__locutus__spec_list_manifest",
		"playbook must name the manifest tool that surfaces the goal-layer ids")
	assert.Contains(t, body, "mcp__locutus__spec_get",
		"playbook must name the batched-fetch tool the agent uses to load goal-layer bodies")
}

// TestFeatureIngestionPlaybookReferencesKeptInForCarveOuts — the
// structural conflict test reads each agoal-*'s body and kept_in
// arrays to judge whether the feature's domain fits under an
// existing carve-out. The playbook must name kept_in by field name
// so the orchestrator reaches for it.
func TestFeatureIngestionPlaybookReferencesKeptInForCarveOuts(t *testing.T) {
	body := loadFeatureIngestion(t)
	assert.Contains(t, body, "kept_in",
		"playbook must name the kept_in field on agoal-* nodes for carve-out fit judgment")
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "carve-out",
		"playbook must use the carve-out vocabulary that matches the agoal model")
}

// TestFeatureIngestionPlaybookDescribesGoalsMdDiffDrafter — when
// the feature conflicts with an agoal-* but plausibly fits under
// an extended or new carve-out, the playbook drafts a unified
// diff against GOALS.md. The diff goes to the operator's stdout
// (the playbook does not edit GOALS.md itself). The playbook
// must name the diff shape (standard --- / +++ headers in a
// fenced code block) and the Read tool used to load the current
// GOALS.md before drafting.
func TestFeatureIngestionPlaybookDescribesGoalsMdDiffDrafter(t *testing.T) {
	body := loadFeatureIngestion(t)
	assert.Contains(t, body, "--- GOALS.md",
		"playbook must show the unified-diff '--- GOALS.md' header so the agent emits standard diff shape")
	assert.Contains(t, body, "+++ GOALS.md",
		"playbook must show the unified-diff '+++ GOALS.md' header")
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "unified diff",
		"playbook must name the unified-diff format the orchestrator drafts")
	assert.Contains(t, lower, "fenced code block",
		"playbook must describe how the diff is emitted (fenced code block) so the operator can copy it cleanly")
	assert.Contains(t, body, "Read",
		"playbook must name the Read tool the orchestrator uses to load the current GOALS.md before drafting")
}

// TestFeatureIngestionPlaybookPopulatesAdvancesAndRespectsOnAdmit
// — on feature admission the playbook populates the optional
// advances and respects citation arrays via spec_propose_feature
// (DJ-139 phase 2 extended the input shape). The playbook must
// name both fields and the propose tool so the orchestrator
// knows to set them inline on the same call.
func TestFeatureIngestionPlaybookPopulatesAdvancesAndRespectsOnAdmit(t *testing.T) {
	body := loadFeatureIngestion(t)
	assert.Contains(t, body, "advances",
		"playbook must name the advances citation array populated on admission")
	assert.Contains(t, body, "respects",
		"playbook must name the respects citation array populated on admission")
	assert.Contains(t, body, "mcp__locutus__spec_propose_feature",
		"playbook must name the propose tool that accepts the citation fields")
}
