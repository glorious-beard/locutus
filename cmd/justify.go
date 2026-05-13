package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// JustifyCmd defends a spec node with the advocate agent. Without
// --against, runs the advocate solo and emits a JustificationBrief.
// With --against, runs the challenger first and the advocate second
// to produce an AdversarialDefense.
type JustifyCmd struct {
	ID      string `arg:"" help:"Spec node id (dec-…, feat-…, strat-…, app-…, bug-…)."`
	Against string `help:"Free-form challenge to defend against." optional:""`
	Format  string `help:"Output format: markdown or json." enum:"markdown,json" default:"markdown"`
}

// JustifyResult is the JSON-shaped output. Exactly one of Brief,
// Adversarial, or FanOut is populated:
//
//   - Brief: solo-advocate path (no --against, any node kind).
//   - Adversarial: adversarial path against a decision target.
//   - FanOut: adversarial path against a strategy / feature / bug /
//     approach — challenge fans out to the underlying decisions
//     and a strategy-level synthesis aggregates the per-decision
//     verdicts.
type JustifyResult struct {
	ID          string                    `json:"id"`
	Challenge   string                    `json:"challenge,omitempty"`
	Brief       *agent.JustificationBrief `json:"brief,omitempty"`
	Challenger  *agent.ChallengeBrief     `json:"challenger,omitempty"`
	Research    *agent.ResearchBrief      `json:"research,omitempty"`
	Adversarial *agent.AdversarialDefense `json:"adversarial,omitempty"`
	FanOut      *agent.FanOutResult       `json:"fan_out,omitempty"`
	Markdown    string                    `json:"markdown"`
	SessionPath string                    `json:"session_path,omitempty"`
}

func (c *JustifyCmd) Run(ctx context.Context, cli *CLI) error {
	fsys, root, err := projectFS()
	if err != nil {
		return err
	}

	challenge := c.Against
	llm, rec, err := recordingLLM(fsys, root, justifyCommandLabel(c.ID, challenge))
	if err != nil {
		return err
	}
	llm, sink, closeSink := withProgressSink(cli, llm)
	defer closeSink()

	result, err := RunJustifyCommand(ctx, llm, fsys, c.ID, challenge, sink)
	if err != nil {
		return err
	}
	if rec != nil {
		_ = rec.Close()
		result.SessionPath = rec.Path()
		// Re-render markdown with session path in the footer.
		result.Markdown = renderJustifyMarkdown(result)
	}

	format := c.Format
	if cli.JSON && format == "markdown" {
		format = "json"
	}
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		fmt.Print(result.Markdown)
		if rec != nil {
			fmt.Printf("\nSession: %s/\n", rec.Path())
		}
		return nil
	}
}

// RunJustifyCommand is the shared implementation backing the CLI and
// MCP handlers. challenge is empty for the solo defense path; non-
// empty triggers the adversarial dialogue.
//
// The Phase-5 workflowization wraps the existing dispatcher-driven
// helpers in three workflows (Solo / AdversarialFallback /
// AdversarialFanout) selected by parent kind + decision presence at
// dispatch time. The cmd layer remains the workflow-selection
// boundary; the workflow itself owns phase orchestration, fanout, and
// observability.
//
// sink may be nil (MCP and tests); when non-nil, each constructed
// WorkflowExecutor bridges its lifecycle events to sink.OnEvent so
// the CLI spinner UI renders the council's per-step progress. Without
// the bridge the workflow is silent because WithSuppressLLMNotify
// shuts down NotifyingExecutor inside workflow steps.
func RunJustifyCommand(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, id, challenge string, sink agent.EventSink) (*JustifyResult, error) {
	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return nil, err
	}
	stages := spec.DeriveStages(loaded, fsys)
	nodeMD, err := render.ExplainNode(loaded, stages, id)
	if err != nil {
		return nil, err
	}
	goalsBody, _ := readGoals(fsys)

	advocate, err := scaffold.LoadAgent(fsys, "spec_advocate")
	if err != nil {
		return nil, fmt.Errorf("load spec_advocate: %w", err)
	}

	dispatcher := agent.NewDispatcher(llm)
	result := &JustifyResult{ID: id, Challenge: challenge}

	// Solo path: no challenge supplied, advocate-only workflow.
	if challenge == "" {
		state := agent.JustifyState{
			NodeID:       id,
			NodeMarkdown: nodeMD,
			GoalsBody:    goalsBody,
			AdvocateDef:  advocate,
			Dispatcher:   dispatcher,
		}
		exec := &agent.WorkflowExecutor[agent.JustifyState]{
			Executor:  llm,
			AgentDefs: map[string]agent.AgentDef{"spec_advocate": advocate},
			Workflow:  agent.JustifySoloWorkflow,
		}
		defer exec.BridgeToSink(sink)()
		if _, err := exec.Run(ctx, &state); err != nil {
			return nil, err
		}
		result.Brief = state.Brief
		result.Markdown = renderJustifyMarkdown(result)
		return result, nil
	}

	// Adversarial paths: load challenger + researcher; pick fanout
	// vs. single-target by parent kind + decision presence.
	challenger, err := scaffold.LoadAgent(fsys, "spec_challenger")
	if err != nil {
		return nil, fmt.Errorf("load spec_challenger: %w", err)
	}
	researcher, err := scaffold.LoadAgent(fsys, "justify_researcher")
	if err != nil {
		return nil, fmt.Errorf("load justify_researcher: %w", err)
	}

	// Decisions go through the single-target flow directly;
	// strategies / features / bugs / approaches go through fanout
	// when they reference decisions, falling back to single-target
	// when they don't (first-class commitments like "Adopt TDD").
	kind := nodeKindOf(id)
	useFanout := kind != spec.KindDecision
	var decisionIDs []string
	if useFanout {
		ids, derr := parentDecisionIDs(loaded, kind, id)
		if derr != nil {
			return nil, derr
		}
		decisionIDs = ids
		if len(decisionIDs) == 0 {
			useFanout = false
		}
	}

	if !useFanout {
		state := agent.JustifyState{
			NodeID:        id,
			NodeMarkdown:  nodeMD,
			GoalsBody:     goalsBody,
			Challenge:     challenge,
			AdvocateDef:   advocate,
			ChallengerDef: challenger,
			ResearcherDef: researcher,
			Dispatcher:    dispatcher,
		}
		exec := &agent.WorkflowExecutor[agent.JustifyState]{
			Executor: llm,
			AgentDefs: map[string]agent.AgentDef{
				"spec_advocate":      advocate,
				"spec_challenger":    challenger,
				"justify_researcher": researcher,
			},
			Workflow: agent.JustifyAdversarialFallbackWorkflow,
		}
		defer exec.BridgeToSink(sink)()
		_, runErr := exec.Run(ctx, &state)
		// Adversarial fallback always populates partial outputs even
		// on per-step failure (challenger / research / advocate each
		// stash whatever they produced before the error). Mirror the
		// legacy RunJustifyAgainst behavior of returning the partial
		// brief alongside the error so the cmd layer can render it.
		result.Challenger = state.ChallengerOut
		result.Research = state.ResearcherOut
		result.Adversarial = state.AdversarialDefense
		if runErr != nil {
			result.Markdown = renderJustifyMarkdown(result)
			return result, runErr
		}
		result.Markdown = renderJustifyMarkdown(result)
		return result, nil
	}

	splitter, err := scaffold.LoadAgent(fsys, "justify_splitter")
	if err != nil {
		return nil, fmt.Errorf("load justify_splitter: %w", err)
	}
	synthesizer, err := scaffold.LoadAgent(fsys, "justify_synthesizer")
	if err != nil {
		return nil, fmt.Errorf("load justify_synthesizer: %w", err)
	}

	fanState, err := buildFanoutState(loaded, stages, id, nodeMD, goalsBody, challenge, kind,
		advocate, challenger, researcher, splitter, synthesizer, dispatcher)
	if err != nil {
		return nil, err
	}
	exec := &agent.WorkflowExecutor[agent.JustifyState]{
		Executor: llm,
		AgentDefs: map[string]agent.AgentDef{
			"spec_advocate":       advocate,
			"spec_challenger":     challenger,
			"justify_researcher":  researcher,
			"justify_splitter":    splitter,
			"justify_synthesizer": synthesizer,
		},
		Workflow: agent.JustifyAdversarialFanoutWorkflow,
	}
	defer exec.BridgeToSink(sink)()
	_, runErr := exec.Run(ctx, &fanState)
	fanOut := assembleFanOutResult(&fanState)
	result.FanOut = fanOut
	result.Markdown = renderJustifyMarkdown(result)
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

// buildFanoutState assembles the JustifyState for the adversarial
// fanout workflow. Resolves the parent body prose, the list of
// referenced decisions, and pre-renders explain markdown for each
// decision so the per_decision step's RunItem closures have
// NodeMarkdown ready.
//
// For bugs, decisions come from the parent feature (bugs inherit).
// For approaches, decisions come from Approach.Decisions[] (the
// audit trail of what was consulted at synthesis time).
func buildFanoutState(loaded *spec.Loaded, stages spec.StageMap, id, nodeMD, goalsBody, challenge string, kind spec.NodeKind, advocate, challenger, researcher, splitter, synthesizer agent.AgentDef, dispatcher agent.AgentDispatcher) (agent.JustifyState, error) {
	var parentBody string
	var decisionIDs []string

	switch kind {
	case spec.KindStrategy:
		n := loaded.StrategyNodeByID(id)
		if n == nil {
			return agent.JustifyState{}, fmt.Errorf("justify fan-out: strategy %q not found", id)
		}
		parentBody = n.Body
		decisionIDs = n.Spec.Decisions
	case spec.KindFeature:
		n := loaded.FeatureNodeByID(id)
		if n == nil {
			return agent.JustifyState{}, fmt.Errorf("justify fan-out: feature %q not found", id)
		}
		parentBody = n.Spec.Description
		if parentBody == "" {
			parentBody = n.Body
		}
		decisionIDs = n.Spec.Decisions
	case spec.KindBug:
		n := loaded.BugNodeByID(id)
		if n == nil {
			return agent.JustifyState{}, fmt.Errorf("justify fan-out: bug %q not found", id)
		}
		parentBody = n.Spec.Description
		if parentBody == "" {
			parentBody = n.Body
		}
		// Bugs inherit decisions from their parent feature.
		if pf := loaded.FeatureNodeByID(n.Spec.FeatureID); pf != nil {
			decisionIDs = pf.Spec.Decisions
		}
	case spec.KindApproach:
		n := loaded.ApproachNodeByID(id)
		if n == nil {
			return agent.JustifyState{}, fmt.Errorf("justify fan-out: approach %q not found", id)
		}
		parentBody = n.Spec.Body
		if parentBody == "" {
			parentBody = n.Body
		}
		decisionIDs = n.Spec.Decisions
	default:
		return agent.JustifyState{}, fmt.Errorf("justify fan-out: kind %q does not support fan-out (challenge a specific decision id instead)", kind)
	}

	if len(decisionIDs) == 0 {
		return agent.JustifyState{}, fmt.Errorf("justify fan-out: %s %q references no decisions; nothing to fan out to", kind, id)
	}

	refs := make([]agent.SplitterDecisionRef, 0, len(decisionIDs))
	perDecisionMD := make(map[string]string, len(decisionIDs))
	for _, did := range decisionIDs {
		dn := loaded.DecisionNodeByID(did)
		if dn == nil {
			// Skip dangling references; the splitter would fail
			// to classify against an absent decision and we'd
			// rather degrade to the resolvable subset than abort.
			continue
		}
		refs = append(refs, agent.SplitterDecisionRef{
			ID:        dn.Spec.ID,
			Title:     dn.Spec.Title,
			Rationale: truncateForSplitter(dn.Spec.Rationale, 400),
		})
		md, err := render.ExplainNode(loaded, stages, dn.Spec.ID)
		if err != nil {
			return agent.JustifyState{}, fmt.Errorf("render explain for %s: %w", dn.Spec.ID, err)
		}
		perDecisionMD[dn.Spec.ID] = md
	}
	if len(refs) == 0 {
		return agent.JustifyState{}, fmt.Errorf("justify fan-out: %s %q references decisions but none resolve in the loaded graph", kind, id)
	}

	return agent.JustifyState{
		NodeID:              id,
		NodeMarkdown:        nodeMD,
		GoalsBody:           goalsBody,
		Challenge:           challenge,
		AdvocateDef:         advocate,
		ChallengerDef:       challenger,
		ResearcherDef:       researcher,
		SplitterDef:         splitter,
		SynthesizerDef:      synthesizer,
		ParentBody:          parentBody,
		ParentNodeMD:        nodeMD,
		PerDecisionMarkdown: perDecisionMD,
		DecisionRefs:        refs,
		Dispatcher:          dispatcher,
	}, nil
}

// assembleFanOutResult flattens the workflow's per-step state into
// the FanOutResult shape the cmd-layer renderer expects.
func assembleFanOutResult(s *agent.JustifyState) *agent.FanOutResult {
	if s == nil {
		return nil
	}
	if s.Split == nil && len(s.PerDecisionResults) == 0 && s.Synthesis == nil {
		return nil
	}
	return &agent.FanOutResult{
		Split:              s.Split,
		PerDecisionResults: s.PerDecisionResults,
		Synthesis:          s.Synthesis,
	}
}

// parentDecisionIDs returns the decision IDs a fan-out justify
// would target for the given parent. Used both to detect the
// fall-back-to-single-target case (when len == 0) and as the seed
// for the richer resolution buildFanoutState performs.
//
// Bugs inherit decisions from their parent feature; approaches
// use their audit-trail Decisions[]; strategies and features use
// their own. Empty result is a legitimate "first-class commitment"
// parent (e.g. "Adopt TDD") that should fall back to single-target
// adversarial dialogue rather than failing the whole command.
func parentDecisionIDs(loaded *spec.Loaded, kind spec.NodeKind, id string) ([]string, error) {
	switch kind {
	case spec.KindStrategy:
		n := loaded.StrategyNodeByID(id)
		if n == nil {
			return nil, fmt.Errorf("justify: strategy %q not found", id)
		}
		return n.Spec.Decisions, nil
	case spec.KindFeature:
		n := loaded.FeatureNodeByID(id)
		if n == nil {
			return nil, fmt.Errorf("justify: feature %q not found", id)
		}
		return n.Spec.Decisions, nil
	case spec.KindBug:
		n := loaded.BugNodeByID(id)
		if n == nil {
			return nil, fmt.Errorf("justify: bug %q not found", id)
		}
		if pf := loaded.FeatureNodeByID(n.Spec.FeatureID); pf != nil {
			return pf.Spec.Decisions, nil
		}
		return nil, nil
	case spec.KindApproach:
		n := loaded.ApproachNodeByID(id)
		if n == nil {
			return nil, fmt.Errorf("justify: approach %q not found", id)
		}
		return n.Spec.Decisions, nil
	}
	return nil, fmt.Errorf("justify: kind %q does not support fan-out", kind)
}

// truncateForSplitter shortens long rationale text for the
// splitter prompt. The splitter is a classifier; sending the full
// rationale (sometimes paragraph-long) wastes tokens. 400 runes
// captures the headline reasoning without inflating the prompt.
func truncateForSplitter(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// nodeKindOf returns the spec NodeKind implied by the id prefix.
// Returns empty NodeKind for unknown prefixes; callers expecting
// a kind validate up-front. Mirrors nodeKindFromID in
// internal/agent/justify_fanout.go but lives at the cmd layer
// where we want spec.NodeKind values for switch matching.
func nodeKindOf(id string) spec.NodeKind {
	switch {
	case len(id) >= 4 && id[:4] == "dec-":
		return spec.KindDecision
	case len(id) >= 5 && id[:5] == "feat-":
		return spec.KindFeature
	case len(id) >= 6 && id[:6] == "strat-":
		return spec.KindStrategy
	case len(id) >= 4 && id[:4] == "app-":
		return spec.KindApproach
	case len(id) >= 4 && id[:4] == "bug-":
		return spec.KindBug
	}
	return ""
}

func renderJustifyMarkdown(r *JustifyResult) string {
	if r.FanOut != nil {
		return render.JustifyFanOutMarkdown(r.ID, r.Challenge, r.FanOut, r.SessionPath)
	}
	if r.Adversarial != nil {
		return render.JustifyAgainstMarkdown(r.ID, r.Challenge, r.Challenger, r.Research, r.Adversarial, r.SessionPath)
	}
	if r.Brief != nil {
		return render.JustifyMarkdown(r.ID, r.Brief, r.SessionPath)
	}
	return ""
}

func justifyCommandLabel(id, challenge string) string {
	if challenge == "" {
		return "justify " + id
	}
	return "justify " + id + " --against"
}
