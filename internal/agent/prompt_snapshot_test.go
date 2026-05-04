package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/stretchr/testify/require"
)

// Snapshot tests for rendered prompts. Per DJ-097, rules of behavior
// belong in the agent .md system prompt; projections supply data only.
// These tests render the FULL prompt (system + user message) the model
// will see for each council step and diff it against a golden file
// under testdata/golden/<step>.txt. Any change to either an agent's
// .md OR the projection function shows up here as a diff that must be
// reviewed and (when intentional) refreshed via:
//
//	LOCUTUS_UPDATE_GOLDEN=1 go test ./internal/agent -run TestRenderedPrompt
//
// What this catches:
//   - Drift between system prompt and user-message tail (the Phase 4
//     bug: triager .md said "route everything"; projection said "omit
//     non-actionable" — model honored the projection. The golden file
//     would have surfaced this contradiction at PR time).
//   - Schema example regressions (RegisterSchema changes propagate
//     into the system prompt).
//   - Subtle prompt restructurings that pass smoke tests but ship
//     reordered context.
//
// Limitations:
//   - The model-resolution path (resolveModel → ModelConfig) reads
//     embedded models.yaml; goldens are insulated by overriding the
//     def's Model field directly so tier resolution doesn't intrude.
//   - Goldens reflect deterministic test fixtures, not real-world
//     rendered prompts. They prove projection + system-prompt
//     coherence, not absolute correctness on arbitrary inputs.

// renderedPromptCase is one snapshot scenario.
type renderedPromptCase struct {
	name     string
	stepID   string
	agentMD  string // basename in internal/scaffold/agents/
	snap     StateSnapshot
	goldenAs string // basename in testdata/golden/
}

// loadAgentDefForSnapshot reads an agent .md from the source tree and
// returns an AgentDef ready for BuildGenerateRequest. Pinned Model so
// snapshots don't drift with the embedded models.yaml.
func loadAgentDefForSnapshot(t *testing.T, mdName string) AgentDef {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // .../internal/agent → .../locutus
	mdPath := filepath.Join(repoRoot, "internal/scaffold/agents", mdName)
	data, err := os.ReadFile(mdPath)
	require.NoError(t, err, "read agent .md")
	var def AgentDef
	body, err := frontmatter.Parse(data, &def)
	require.NoError(t, err, "parse frontmatter")
	def.SystemPrompt = body
	// Pin model to a known string so the rendered prompt is stable
	// regardless of environment-detected providers. The actual
	// rendered system prompt is unaffected by model choice; pinning
	// here protects the snapshot when models.yaml is edited.
	def.Model = "snapshot/test-model"
	return def
}

// renderPrompt builds the full system + user message text the model
// would see for one step. Returns a single string with markers between
// messages so the golden file is human-readable.
func renderPrompt(t *testing.T, agentDef AgentDef, stepID string, snap StateSnapshot) string {
	t.Helper()
	userMsgs := ProjectState(stepID, snap)
	req := BuildGenerateRequest(agentDef, userMsgs)
	var b strings.Builder
	for i, m := range req.Messages {
		if i > 0 {
			b.WriteString("\n\n--- next message ---\n\n")
		}
		b.WriteString("ROLE: ")
		b.WriteString(m.Role)
		b.WriteString("\n\n")
		b.WriteString(m.Content)
	}
	return b.String()
}

// goldenPath returns the testdata path for a snapshot.
func goldenPath(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(thisFile)
	return filepath.Join(dir, "testdata/golden", name)
}

// assertGolden compares actual against the golden file at name. When
// LOCUTUS_UPDATE_GOLDEN=1 is set, writes actual instead. Failure mode
// dumps a unified-style first-divergence excerpt so reviewers can
// pinpoint the change without scrolling pages of identical text.
func assertGolden(t *testing.T, name, actual string) {
	t.Helper()
	path := goldenPath(t, name)
	if os.Getenv("LOCUTUS_UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(actual), 0o644))
		t.Logf("wrote golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\n(hint: run LOCUTUS_UPDATE_GOLDEN=1 go test ... to seed)", path, err)
	}
	if string(want) == actual {
		return
	}
	t.Errorf("rendered prompt mismatch for %s\n\nfirst divergence:\n%s\n\n(refresh with LOCUTUS_UPDATE_GOLDEN=1 go test ./internal/agent -run TestRenderedPrompt)",
		name, firstDivergence(string(want), actual))
}

// firstDivergence returns a human-readable snippet around the first
// byte where want and got differ — bounded to a few hundred chars so
// the test failure stays scannable.
func firstDivergence(want, got string) string {
	const window = 80
	maxLen := len(want)
	if len(got) < maxLen {
		maxLen = len(got)
	}
	for i := 0; i < maxLen; i++ {
		if want[i] != got[i] {
			start := i - window
			if start < 0 {
				start = 0
			}
			endWant := i + window
			if endWant > len(want) {
				endWant = len(want)
			}
			endGot := i + window
			if endGot > len(got) {
				endGot = len(got)
			}
			return "want: ...\n" + want[start:endWant] + "\n...\n\ngot: ...\n" + got[start:endGot] + "\n..."
		}
	}
	if len(want) != len(got) {
		// Identical prefix but one is longer.
		return "(prefixes match; lengths differ — want=" + intStr(len(want)) + " got=" + intStr(len(got)) + ")"
	}
	return "(equal — false alarm in firstDivergence)"
}

func intStr(n int) string {
	// Local sprintf-free helper to keep this test file's imports tight.
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// fixtureRawProposal returns a deterministic RawSpecProposal JSON used
// across snapshot scenarios. Two features + two strategies + a few
// inline decisions — enough to exercise projection paths without
// inflating goldens.
func fixtureRawProposal(t *testing.T) string {
	t.Helper()
	p := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-dashboard", Title: "Dashboard", Description: "Campaign-health view."},
			{ID: "feat-ingestion", Title: "Voter ingestion", Description: "CSV pipeline."},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-frontend", Title: "Stack", Kind: "foundational", Body: "Vite + React."},
			{ID: "strat-data", Title: "Data store", Kind: "foundational", Body: "Aurora + ClickHouse."},
		},
	}
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	return string(raw)
}

func fixtureOutline(t *testing.T) string {
	t.Helper()
	o := Outline{
		Features: []OutlineFeature{
			{ID: "feat-dashboard", Title: "Dashboard", Summary: "Campaign-health view."},
		},
		Strategies: []OutlineStrategy{
			{ID: "strat-frontend", Title: "Stack", Kind: "foundational", Summary: "Vite + React."},
		},
	}
	raw, err := json.Marshal(o)
	require.NoError(t, err)
	return string(raw)
}

// TestRenderedPromptSnapshots — golden-file diff for every council
// step we actively maintain. A diff means SOMETHING changed in either
// the agent .md or the projection; the reviewer judges whether it's
// intentional and refreshes the golden if so.
func TestRenderedPromptSnapshots(t *testing.T) {
	scoutBriefJSON := `{"domain_read":"campaign tech","technology_options":["framework: A vs B"],"implicit_assumptions":["scale: 100k"],"watch_outs":["seasonality"]}`
	rawProposal := fixtureRawProposal(t)
	outline := fixtureOutline(t)
	concerns := []Concern{
		{AgentID: "devops_critic", Severity: "medium", Kind: "devops", Text: "CI/CD missing."},
		{AgentID: "sre_critic", Severity: "medium", Kind: "sre", Text: "observability tooling not named."},
	}

	cases := []renderedPromptCase{
		{
			name:    "triage",
			stepID:  "triage",
			agentMD: "spec_revision_triager.md",
			snap: StateSnapshot{
				Prompt:      "Build it.",
				RawProposal: rawProposal,
				Concerns:    concerns,
			},
			goldenAs: "triage.txt",
		},
		{
			name:    "revise_features",
			stepID:  "revise_features (feat-dashboard)",
			agentMD: "spec_feature_elaborator.md",
			snap: StateSnapshot{
				Prompt:              "Build it.",
				OriginalRawProposal: rawProposal,
				FanoutItem:          mustMarshal(t, NodeRevision{NodeID: "feat-dashboard", Concerns: []string{"add PII encryption"}}),
			},
			goldenAs: "revise_features.txt",
		},
		{
			name:    "revise_strategies",
			stepID:  "revise_strategies (strat-frontend)",
			agentMD: "spec_strategy_elaborator.md",
			snap: StateSnapshot{
				Prompt:              "Build it.",
				OriginalRawProposal: rawProposal,
				FanoutItem:          mustMarshal(t, NodeRevision{NodeID: "strat-frontend", Concerns: []string{"name the IaC tool"}}),
			},
			goldenAs: "revise_strategies.txt",
		},
		{
			name:    "revise_feature_additions",
			stepID:  "revise_feature_additions (feat-export)",
			agentMD: "spec_feature_elaborator.md",
			snap: StateSnapshot{
				Prompt:              "Build it.",
				OriginalRawProposal: rawProposal,
				FanoutItem:          mustMarshal(t, AddedNode{Kind: "feature", SourceConcern: "missing data export feature"}),
			},
			goldenAs: "revise_feature_additions.txt",
		},
		{
			name:    "revise_strategy_additions",
			stepID:  "revise_strategy_additions (strat-iac)",
			agentMD: "spec_strategy_elaborator.md",
			snap: StateSnapshot{
				Prompt:              "Build it.",
				OriginalRawProposal: rawProposal,
				FanoutItem:          mustMarshal(t, AddedNode{Kind: "strategy", SourceConcern: "missing infrastructure-as-code strategy"}),
			},
			goldenAs: "revise_strategy_additions.txt",
		},
		{
			name:    "elaborate_features",
			stepID:  "elaborate_features (feat-dashboard)",
			agentMD: "spec_feature_elaborator.md",
			snap: StateSnapshot{
				Prompt:     "Build it.",
				ScoutBrief: scoutBriefJSON,
				Outline:    outline,
				FanoutItem: mustMarshal(t, OutlineFeature{ID: "feat-dashboard", Title: "Dashboard", Summary: "Campaign-health view."}),
			},
			goldenAs: "elaborate_features.txt",
		},
		{
			name:    "elaborate_strategies",
			stepID:  "elaborate_strategies (strat-frontend)",
			agentMD: "spec_strategy_elaborator.md",
			snap: StateSnapshot{
				Prompt:     "Build it.",
				ScoutBrief: scoutBriefJSON,
				Outline:    outline,
				FanoutItem: mustMarshal(t, OutlineStrategy{ID: "strat-frontend", Title: "Stack", Kind: "foundational", Summary: "Vite + React."}),
			},
			goldenAs: "elaborate_strategies.txt",
		},
		{
			name:    "reconcile_greenfield",
			stepID:  "reconcile",
			agentMD: "spec_reconciler.md",
			snap: StateSnapshot{
				Prompt:      "Build it.",
				RawProposal: rawProposal,
			},
			goldenAs: "reconcile_greenfield.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := loadAgentDefForSnapshot(t, tc.agentMD)
			got := renderPrompt(t, def, tc.stepID, tc.snap)
			assertGolden(t, tc.goldenAs, got)
		})
	}
}

// mustMarshal is a test-side json.Marshal that fails the test on
// error. Equivalent to json.Marshal-with-ignored-error but explicit.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

// TestRenderedPromptHasNoContradictions — a coarse-grained smoke check
// that the rendered prompt for each council step doesn't carry the
// specific contradiction patterns that broke DJ-095. Snapshot diffs
// catch this for any specific case; this test catches it as a
// permanent invariant so a future "let's add a soft hint to the
// projection" PR fails loudly.
func TestRenderedPromptHasNoContradictions(t *testing.T) {
	// Triager rendered prompt must not say findings can be omitted
	// when the system prompt mandates routing-completeness.
	def := loadAgentDefForSnapshot(t, "spec_revision_triager.md")
	snap := StateSnapshot{
		Prompt:      "Build it.",
		RawProposal: fixtureRawProposal(t),
		Concerns:    []Concern{{AgentID: "devops_critic", Kind: "devops", Text: "x"}},
	}
	got := renderPrompt(t, def, "triage", snap)

	// The rendered prompt must NOT carry both "every finding" /
	// "must equal" language AND "simply omitted" language. The
	// projection-side directive was the bug; if a future edit
	// reintroduces it, this assertion fires.
	if strings.Contains(got, "simply omitted") || strings.Contains(got, "non-actionable") {
		// Only a problem if the routing-completeness mandate is also
		// present, which it must be (system prompt).
		if strings.Contains(got, "every finding") || strings.Contains(got, "Total entries") {
			t.Errorf("triager rendered prompt contradicts itself: contains BOTH routing-completeness mandate AND 'simply omitted' language. The projection must not carry the omit instruction; rules belong in the agent .md only. See DJ-097.")
		}
	}
}

// TestRenderedPromptSchemaTerminologyCoherence — when an agent's
// prose refers to a structured-output field by name, the prose must
// use the EXACT field name from the schema example, not a synonym.
//
// The May-4 reconciler failure: prompt prose said "pick a winner"
// while the schema field is `canonical`. The model emitted
// `"winner": {...}` for resolve_conflict actions; Genkit's schema
// validator rejected the call ("Additional property winner is not
// allowed") and the workflow died. Prose-vs-schema drift is the same
// class of bug as DJ-097's projection-vs-system-prompt drift, just
// inside a single .md file.
//
// This test enumerates known-bad synonyms and fails when any of them
// appears in a council agent's rendered prompt where the schema uses
// a different name. New aliases get added here as they're discovered;
// the test is a backstop, not a comprehensive solution.
func TestRenderedPromptSchemaTerminologyCoherence(t *testing.T) {
	// (agent .md, prose synonym, schema field) — fail if synonym is
	// present in the rendered prompt without the schema field also
	// being present in the same context.
	cases := []struct {
		agentMD    string
		stepID     string
		snap       StateSnapshot
		badSynonym string
		schemaName string
		bugRef     string
	}{
		{
			agentMD:    "spec_reconciler.md",
			stepID:     "reconcile",
			snap:       StateSnapshot{Prompt: "Build it.", RawProposal: fixtureRawProposal(t)},
			badSynonym: "pick a winner",
			schemaName: "canonical",
			bugRef:     "May-4 reconciler emitted `\"winner\": {...}` instead of `\"canonical\": {...}` because the prose said 'winner' while the schema example said 'canonical'. Genkit rejected the call.",
		},
	}

	for _, c := range cases {
		def := loadAgentDefForSnapshot(t, c.agentMD)
		got := renderPrompt(t, def, c.stepID, c.snap)
		if strings.Contains(got, c.badSynonym) {
			t.Errorf("%s: rendered prompt contains prose synonym %q for schema field %q. The model picks the prose word over the schema example. Prior bug: %s",
				c.agentMD, c.badSynonym, c.schemaName, c.bugRef)
		}
	}
}
