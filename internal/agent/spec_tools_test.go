package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFeatureForTools(t *testing.T, fs specio.FS, id, title, desc string) {
	t.Helper()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/"+id, spec.Feature{
		ID:          id,
		Title:       title,
		Status:      spec.FeatureStatusProposed,
		Description: desc,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, desc))
}

func writeStrategyForTools(t *testing.T, fs specio.FS, id, title, kind, body string) {
	t.Helper()
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/"+id, spec.Strategy{
		ID:     id,
		Title:  title,
		Kind:   spec.StrategyKind(kind),
		Status: "proposed",
	}, body))
}

func writeDecisionForTools(t *testing.T, fs specio.FS, id, title, rationale string) {
	t.Helper()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/"+id, spec.Decision{
		ID:        id,
		Title:     title,
		Status:    spec.DecisionStatusProposed,
		Rationale: rationale,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, rationale))
}

// TestBuildSpecManifestGreenfieldEmpty — a project with no .borg/spec/
// returns an empty manifest, not an error. Tools must be safe to call
// during greenfield runs where the reconciler has nothing to look up.
func TestBuildSpecManifestGreenfieldEmpty(t *testing.T) {
	fs := specio.NewMemFS()
	m := BuildSpecManifest(fs)
	assert.Empty(t, m.Features)
	assert.Empty(t, m.Strategies)
	assert.Empty(t, m.Decisions)
	assert.Empty(t, m.Bugs)
	assert.Empty(t, m.Approaches)
}

// TestBuildSpecManifestPopulated — features, strategies, and decisions
// appear in the manifest with id + title + summary, where summary is
// truncated to keep the index scannable.
func TestBuildSpecManifestPopulated(t *testing.T) {
	fs := specio.NewMemFS()
	writeFeatureForTools(t, fs, "feat-dashboard", "Dashboard", "Provides a campaign-health view aggregating outreach progress and resource allocation.")
	writeStrategyForTools(t, fs, "strat-frontend", "Stack", "foundational", "Use Next.js on Vercel for the frontend; React Server Components for data-heavy views.")
	writeDecisionForTools(t, fs, "dec-postgres", "Use PostgreSQL", "Mid-market scale; PostGIS for geospatial; serverless for cost.")

	m := BuildSpecManifest(fs)
	require.Len(t, m.Features, 1)
	assert.Equal(t, "feat-dashboard", m.Features[0].ID)
	assert.Equal(t, "Dashboard", m.Features[0].Title)
	assert.Contains(t, m.Features[0].Summary, "campaign-health")

	require.Len(t, m.Strategies, 1)
	assert.Equal(t, "strat-frontend", m.Strategies[0].ID)
	assert.Equal(t, "foundational", m.Strategies[0].Kind, "strategy entries must carry kind so the reconciler can filter")

	require.Len(t, m.Decisions, 1)
	assert.Equal(t, "dec-postgres", m.Decisions[0].ID)
	assert.Contains(t, m.Decisions[0].Summary, "PostGIS")
}

// TestBuildSpecManifestTruncatesLongSummaries — the summary field is
// the index's load-bearing scannable column. A multi-paragraph
// description must collapse to one line and truncate at the cap.
func TestBuildSpecManifestTruncatesLongSummaries(t *testing.T) {
	fs := specio.NewMemFS()
	long := "First sentence with detail. Second sentence with even more detail.\n\nA third paragraph that exists only to push past the 200-rune cap so we can verify the truncation logic actually fires when summaries are longer than the cap permits."
	writeFeatureForTools(t, fs, "feat-long", "Long", long)

	m := BuildSpecManifest(fs)
	require.Len(t, m.Features, 1)
	assert.NotContains(t, m.Features[0].Summary, "\n",
		"newlines must collapse to single spaces so the manifest stays scannable")
	runes := []rune(m.Features[0].Summary)
	assert.LessOrEqual(t, len(runes), summaryMaxRunes+1,
		"summary must respect the cap (+1 for the trailing ellipsis)")
}

// TestLookupSpecNodeRoutesByPrefix — id prefix selects the kind
// directory; greenfield returns the underlying not-exist error so
// callers can distinguish "no such id" from "invalid prefix."
func TestLookupSpecNodeRoutesByPrefix(t *testing.T) {
	fs := specio.NewMemFS()
	writeFeatureForTools(t, fs, "feat-dashboard", "Dashboard", "desc")
	writeStrategyForTools(t, fs, "strat-frontend", "Stack", "foundational", "body")
	writeDecisionForTools(t, fs, "dec-postgres", "Use PostgreSQL", "rationale")

	t.Run("feat- prefix routes to features dir", func(t *testing.T) {
		raw, err := LookupSpecNode(fs, "feat-dashboard")
		require.NoError(t, err)
		var f spec.Feature
		require.NoError(t, json.Unmarshal(raw, &f))
		assert.Equal(t, "feat-dashboard", f.ID)
	})

	t.Run("strat- prefix routes to strategies dir", func(t *testing.T) {
		raw, err := LookupSpecNode(fs, "strat-frontend")
		require.NoError(t, err)
		var s spec.Strategy
		require.NoError(t, json.Unmarshal(raw, &s))
		assert.Equal(t, "strat-frontend", s.ID)
	})

	t.Run("dec- prefix routes to decisions dir", func(t *testing.T) {
		raw, err := LookupSpecNode(fs, "dec-postgres")
		require.NoError(t, err)
		var d spec.Decision
		require.NoError(t, json.Unmarshal(raw, &d))
		assert.Equal(t, "dec-postgres", d.ID)
	})

	t.Run("unknown prefix errors clearly", func(t *testing.T) {
		_, err := LookupSpecNode(fs, "xyz-foo")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown prefix")
	})

	t.Run("missing id surfaces underlying read error", func(t *testing.T) {
		_, err := LookupSpecNode(fs, "feat-ghost")
		require.Error(t, err)
		// Don't pin the error string to OS-vs-MemFS specifics — both
		// satisfy fs.PathError and we just need the call to fail.
	})

	t.Run("empty id rejected", func(t *testing.T) {
		_, err := LookupSpecNode(fs, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty id")
	})
}

// TestBuildGenerateRequestThreadsTools — frontmatter `tools:` round-
// trips through AgentDef into GenerateRequest.Tools so the GenKit
// Generate path can attach them via ai.WithTools.
func TestBuildGenerateRequestThreadsTools(t *testing.T) {
	def := AgentDef{
		ID:           "spec_reconciler",
		Tools:        []string{"spec_list_manifest", "spec_get"},
		SystemPrompt: "you are the reconciler",
	}
	req := BuildGenerateRequest(def, []Message{{Role: "user", Content: "x"}})
	require.Len(t, req.Tools, 2)
	assert.Equal(t, "spec_list_manifest", req.Tools[0])
	assert.Equal(t, "spec_get", req.Tools[1])
}

// TestLoadAgentDefsParsesTools — the YAML `tools:` list parses into
// AgentDef.Tools as a string slice without surprise.
func TestLoadAgentDefsParsesTools(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/agents", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_reconciler.md", []byte(`---
id: spec_reconciler
role: reconcile
capability: balanced
tools:
  - spec_list_manifest
  - spec_get
---
You are the reconciler.
`), 0o644))

	defs, err := LoadAgentDefs(fsys, ".borg/agents")
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, []string{"spec_list_manifest", "spec_get"}, defs[0].Tools)
}

// TestProjectReconcileNoLongerInlinesExistingSpec — Phase 3 moves
// existing-spec navigation behind the spec_lookup tools. The reconciler
// prompt no longer carries the full snapshot; it carries a tool-usage
// hint when an existing spec is present, and nothing extra otherwise.
func TestProjectReconcileNoLongerInlinesExistingSpec(t *testing.T) {
	rawProposal := `{"features":[{"id":"feat-x"}]}`
	existing := &ExistingSpec{
		Decisions: []spec.Decision{{ID: "dec-existing", Title: "Existing decision", Rationale: "old"}},
	}

	t.Run("existing present yields tool-usage hint, not inlined snapshot", func(t *testing.T) {
		snap := StateSnapshot{
			Prompt:      "Build it.",
			RawProposal: rawProposal,
			Existing:    existing,
		}
		msgs := projectReconcile(snap)
		require.Len(t, msgs, 1)
		body := msgs[0].Content
		assert.Contains(t, body, "spec_list_manifest", "tool-usage hint must mention the manifest tool")
		assert.Contains(t, body, "spec_get", "tool-usage hint must mention the per-id fetch tool")
		assert.NotContains(t, body, "dec-existing",
			"existing decisions must NOT be inlined into the reconciler prompt — that's exactly the context-pressure problem Phase 3 fixes")
		assert.NotContains(t, body, "Existing decision",
			"titles also must not leak via inlining")
	})

	t.Run("greenfield omits the tool-usage hint", func(t *testing.T) {
		snap := StateSnapshot{
			Prompt:      "Build it.",
			RawProposal: rawProposal,
			Existing:    &ExistingSpec{}, // empty, IsEmpty() → true
		}
		msgs := projectReconcile(snap)
		body := msgs[0].Content
		assert.NotContains(t, body, "spec_list_manifest",
			"greenfield runs have nothing to look up; don't push the tool on the model when it would only return empty")
	})
}

// TestStripJSONFences — the defensive parser pre-step removes
// ```json ... ``` and ``` ... ``` wrappers Gemini emits when JSON
// mode is silently disabled by tool attachment.
func TestStripJSONFences(t *testing.T) {
	t.Run("plain JSON passes through", func(t *testing.T) {
		s := `{"a":1}`
		assert.Equal(t, s, stripJSONFences(s))
	})
	t.Run("strips ```json fence", func(t *testing.T) {
		s := "```json\n{\"a\":1}\n```"
		assert.Equal(t, `{"a":1}`, stripJSONFences(s))
	})
	t.Run("strips bare ``` fence", func(t *testing.T) {
		s := "```\n{\"a\":1}\n```"
		assert.Equal(t, `{"a":1}`, stripJSONFences(s))
	})
	t.Run("strips ```JSON (uppercase) fence", func(t *testing.T) {
		s := "```JSON\n{\"a\":1}\n```"
		assert.Equal(t, `{"a":1}`, stripJSONFences(s))
	})
	t.Run("missing closing fence still strips opening", func(t *testing.T) {
		s := "```json\n{\"a\":1}"
		assert.Equal(t, `{"a":1}`, stripJSONFences(s))
	})
	t.Run("leading whitespace before fence", func(t *testing.T) {
		s := "  \n```json\n{\"a\":1}\n```\n"
		assert.Equal(t, `{"a":1}`, stripJSONFences(s))
	})
}

// TestMergeReconcileToleratesFencedVerdict — the end-to-end merge path
// must accept a fenced verdict (the realistic Gemini-with-tools
// failure mode) without a parse error. This is the load-bearing
// assertion behind the fence-stripper: without it, the reconciler's
// output JSON would fail json.Unmarshal on a wrapping ```json fence
// and the whole reconcile step would error.
func TestMergeReconcileToleratesFencedVerdict(t *testing.T) {
	rawProposal := `{"features":[{"id":"feat-x","title":"X","decisions":[{"title":"use Y"}]}]}`
	fencedVerdict := "```json\n{\"actions\":[]}\n```"

	out, applied, err := mergeReconcile(rawProposal, fencedVerdict, nil)
	require.NoError(t, err, "fenced verdict must parse cleanly via the strip-fences pre-step")
	assert.Empty(t, applied)

	var p SpecProposal
	require.NoError(t, json.Unmarshal([]byte(out), &p))
	require.Len(t, p.Features, 1)
	require.Len(t, p.Decisions, 1, "implicit keep-separate mints one decision per inline-decision")
}
