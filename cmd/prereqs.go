package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/prereqs"
	"github.com/chetan/locutus/internal/specio"
)

// runSpecPrereqs invokes every prereq check this verb depends on, with
// regen tied to whether the verb is actually about to mutate state.
//
// regen=true  → assertion + resolution. Walks the spec dir, dispatches
//
//	the appropriate workflow to fill any gaps. Used on the
//	normal (non-dry-run) path.
//
// regen=false → assertion only. Walks the spec dir, returns a
//
//	SummariesError when nodes are missing. Used on the
//	--dry-run path because dry-run promises not to mutate
//	anything — including prereq-driven fills — so the
//	honest report is "would fail, run `update --check-pre-reqs`
//	first."
//
// Today the prereq surface is just SummariesPresent. Future prereqs
// (traces.json writers, etc.) plug in as additional Ensure* calls in
// the same sequence.
func runSpecPrereqs(ctx context.Context, fsys specio.FS, llm agent.AgentExecutor, sink agent.EventSink, regen bool) error {
	sctx := prereqs.SummariesContext{FSys: fsys, Sink: sink}
	if regen {
		// LLM is required for resolution. The assertion-only path
		// (regen=false) doesn't construct a dispatcher — passing one
		// with a nil executor would be a latent bug, and the
		// dispatcher contract requires non-nil exec.
		if llm == nil {
			return fmt.Errorf("runSpecPrereqs: regen=true requires a non-nil LLM executor")
		}
		sctx.Executor = llm
		sctx.Dispatcher = agent.NewDispatcher(llm)
	}
	if err := prereqs.EnsureSpecsContainSummaries(ctx, sctx, regen); err != nil {
		return mapPrereqError(err)
	}
	return nil
}

// mapPrereqError translates a typed prereq error into a CLI-facing
// message that points at the actionable remedy. Returns the original
// error unchanged if it's not a known prereq type.
func mapPrereqError(err error) error {
	var sErr *prereqs.SummariesError
	if errors.As(err, &sErr) {
		return fmt.Errorf("prereq: %s", sErr.Error())
	}
	return err
}
