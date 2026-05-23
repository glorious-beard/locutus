package agent

import (
	"testing"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecSwapsReachInnerExecutorThroughWrapperChain locks in the
// fix to a bug that was latent since DJ-125 Phase 3: the cmd-layer
// wraps the production *Executor in LoggingExecutor (recording) and
// NotifyingExecutor (sink emission), and specgen.go's swap helpers
// (specSearchSwap, specListManifestSwap, specGetSwap) type-assert
// the outer wrapper to find the swappable. Without pass-through
// methods on the wrappers the assertions fail silently — specgen's
// `if listSwap := specListManifestSwap(exec); listSwap != nil`
// guard short-circuits and the council never applies its in-flight
// overlay swap. The result was every spec_* tool call falling
// through to the FS-backed default for the entire run, with the
// in-flight overlay invisible to the model.
//
// This test exercises the full wrapper chain — production *Executor
// → LoggingExecutor → NotifyingExecutor — and asserts that the
// three swappables thread through the outer interface assertions.
// Production wrappers MUST implement SpecSearch / SpecListManifest /
// SpecGet that delegate to their inner executor.
//
// The bug surfaced as "spec_list_manifest came back empty `{}`" in
// the May 2026 winplan traces. The signature lived in the wrapper
// boundary, not in the InFlightSpecStore implementation (which
// works correctly when called directly, as TestRepro_LiveWiring_
// ManifestVisibleAfterMerge demonstrates).
func TestSpecSwapsReachInnerExecutorThroughWrapperChain(t *testing.T) {
	// Build the chain. We use MockExecutor as the inner executor —
	// it implements the same Spec*-swap accessors the production
	// *Executor does, so the pass-through path through the wrappers
	// is identical. Constructing the real *Executor needs LLM
	// provider config that's not available in unit tests; the
	// MockExecutor is the test-side analogue and is what the
	// production wrapper sees through its inner interface.
	exec := NewMockExecutor()

	// Wire the swappables — same pattern cmd/llm.go does at startup.
	fsys := specio.NewMemFS()
	fsys.MkdirAll(".borg/spec/decisions", 0o755)
	fsys.MkdirAll(".borg/spec/features", 0o755)
	fsys.MkdirAll(".borg/spec/strategies", 0o755)
	fsys.MkdirAll(".borg/spec/bugs", 0o755)
	fsys.MkdirAll(".borg/spec/approaches", 0o755)
	defaultProvider := NewFSSpecProvider(fsys)
	searchSwap := NewSwappableSpecSearch(nil)
	listSwap := NewSwappableSpecListManifest(defaultProvider)
	getSwap := NewSwappableSpecGet(defaultProvider)
	exec.SetSpecSearch(searchSwap)
	exec.SetSpecListManifest(listSwap)
	exec.SetSpecGet(getSwap)

	// Production wrapper chain. The recorder is nil because we're
	// not exercising recording; the trace-recording path doesn't
	// affect the wrapper's pass-through behaviour.
	logExec := NewLoggingExecutor(exec, nil)
	notExec := &NotifyingExecutor{Inner: logExec, Sink: SilentSink{}}

	// The swappables must reach the outermost wrapper. This is what
	// specgen.go does via its swap helpers — the type assertion has
	// to succeed and return the SAME swappable instance the cmd-
	// layer wired in, otherwise the swap to InFlightSpecStore later
	// in the council bootstrap silently no-ops.
	t.Run("LoggingExecutor passes through", func(t *testing.T) {
		got := specSearchSwap(logExec)
		assert.Same(t, searchSwap, got, "LoggingExecutor must pass through SpecSearch")
		got2 := specListManifestSwap(logExec)
		assert.Same(t, listSwap, got2, "LoggingExecutor must pass through SpecListManifest")
		got3 := specGetSwap(logExec)
		assert.Same(t, getSwap, got3, "LoggingExecutor must pass through SpecGet")
	})

	t.Run("NotifyingExecutor passes through", func(t *testing.T) {
		got := specSearchSwap(notExec)
		assert.Same(t, searchSwap, got, "NotifyingExecutor must pass through SpecSearch")
		got2 := specListManifestSwap(notExec)
		assert.Same(t, listSwap, got2, "NotifyingExecutor must pass through SpecListManifest")
		got3 := specGetSwap(notExec)
		assert.Same(t, getSwap, got3, "NotifyingExecutor must pass through SpecGet")
	})

	t.Run("Swap applied to outer wrapper reaches the inner swappable", func(t *testing.T) {
		// The council bootstrap does this swap on the outer wrapper.
		// The swap must mutate the SAME swappable instance the tool
		// handlers were registered with — that's the load-bearing
		// requirement. If the type assertion returned a copy or a
		// different swappable, the swap would mutate the wrong thing
		// and tool calls would still see the default backing.
		inflight := NewInFlightSpecStore()
		swap := specListManifestSwap(notExec)
		require.NotNil(t, swap, "the council bootstrap requires this swap to be reachable through the wrapper chain")

		prev := swap.Swap(inflight)
		t.Cleanup(func() { swap.Swap(prev) })

		// Now read through the *registered* swappable (the one the
		// cmd-layer wired into the tool registry). Reading from
		// listSwap directly is the same as a tool call resolving
		// through the registered handler.
		gotProvider := listSwap.Current()
		assert.Same(t, inflight, gotProvider,
			"the swap applied to the outer wrapper must mutate the cmd-registered swappable; if it doesn't, the tool handler will keep reading from the FS-backed default and the in-flight overlay is invisible to the model")
	})
}

// TestSpecSwapAccessorsReturnNilSafely confirms the wrapper pass-
// throughs handle the nil-inner case (defensive: a future caller
// constructing a wrapper without an inner executor shouldn't panic).
func TestSpecSwapAccessorsReturnNilSafely(t *testing.T) {
	var logExec *LoggingExecutor
	assert.Nil(t, logExec.SpecSearch())
	assert.Nil(t, logExec.SpecListManifest())
	assert.Nil(t, logExec.SpecGet())

	var notExec *NotifyingExecutor
	assert.Nil(t, notExec.SpecSearch())
	assert.Nil(t, notExec.SpecListManifest())
	assert.Nil(t, notExec.SpecGet())

	// Non-nil wrapper around an executor that doesn't expose the
	// swappables (e.g. MockExecutor without the test-side setters
	// wired) should also return nil rather than panic.
	emptyLog := NewLoggingExecutor(nil, nil)
	assert.Nil(t, emptyLog.SpecSearch())
	assert.Nil(t, emptyLog.SpecListManifest())
	assert.Nil(t, emptyLog.SpecGet())
}
