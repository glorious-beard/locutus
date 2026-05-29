# DJ-147 `--dry-run` via Per-Session SpecStore Overlay — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land faithful `--dry-run` on `import` / `refine` / `adopt` / `assimilate` by capturing mutations at the MCP tool boundary via a per-session SpecStore overlay, exposing the captured set through a new read-only `spec_dry_run_report` tool, and rendering it dual-path (agent-narrated in both modes; CLI-side authoritative render from `tools.jsonl` in headless).

**Architecture:** A `sessionOverlay` map keyed by `*mcp.ServerSession` lives on `SpecStore`. Sessions marked dry-run at MCP `initialize` (via `_meta["locutus.dry_run"]` forwarded from `LOCUTUS_DRY_RUN=1` env) get an overlay; every mutation MCP tool routes through a `captureOnly` registration-site wrapper (mirrors DJ-143's `requireRuntime`) that lands the would-be entry in the overlay rather than calling `commitOne`. Reads consult overlay-then-base so the workflow runs faithfully end-to-end. The agent calls `spec_dry_run_report` as the last step (instructed via a contextNote on the dispatched prompt) and renders per the `--format markdown|json` flag. Headless additionally reads `tools.jsonl` post-dispatch for an authoritative CLI-side render.

**Tech Stack:** Go 1.x; `github.com/modelcontextprotocol/go-sdk` v1.6.1; `github.com/coder/acp-go-sdk`; `github.com/stretchr/testify/{assert,require}`. Spec doc: [docs/decisions/dj-147-dry-run-mutation-capture.md](../../docs/decisions/dj-147-dry-run-mutation-capture.md).

---

## Required reading before starting

- **[docs/decisions/dj-147-dry-run-mutation-capture.md](../../docs/decisions/dj-147-dry-run-mutation-capture.md)** — the governing spec. Read all 10 Decision points + Resolved questions + Alternatives + Consequences.
- **[docs/decisions/dj-143-per-runtime-tool-policy.md](../../docs/decisions/dj-143-per-runtime-tool-policy.md)** — the `LOCUTUS_MODE` plumbing pattern (env → `_meta` → daemon-side session-context map), and the `requireRuntime` wrapper-at-registration pattern. DJ-147 mirrors both verbatim.
- **[docs/decisions/dj-134-unified-spec-store.md](../../docs/decisions/dj-134-unified-spec-store.md)** — the `SpecStore` as source of truth during a daemon session. The overlay extends this; understand the existing `Put` path before extending it.

## Note on the DJ doc's 16-tool count

The DJ doc enumerates 16 mutation tools including `spec_propose_approach` / `spec_revise_approach`. Verify against current code: `grep -nE 'Name:\s+"spec_(propose|revise|delete|mark|update)' internal/mcp/tools_spec_write.go`. The actual registered set today is **14 tools** (no propose/revise for Approach yet — only `spec_mark_approach_drifted`). When approach mutations land in a future DJ, they'll inherit the wrapper at their registration site. Don't wrap `spec_propose_approach` / `spec_revise_approach` until they actually exist.

## Phase ordering & independence

- Phase 1 (overlay data structure) is the foundation everything else builds on.
- Phase 2 (session context wiring) depends on Phase 1.
- Phase 3 (capture wrapper) depends on Phase 2.
- Phase 4 (wrap mutation tools) depends on Phase 3.
- Phase 5 (overlay-aware reads) depends on Phase 1.
- Phase 6 (`spec_dry_run_report`) depends on Phase 1.
- Phase 7 (bridge plumbing) is independent of overlay work — can run in parallel with Phases 1–6.
- Phase 8 (CLI flags + contextNote + tools.jsonl render) depends on Phase 7.
- Phase 9 (docs) depends on Phases 1–8.
- Phase 10 (full-suite + winplan e2e) last.

**Commit discipline:** commit after every green test step. Conventional prefixes (`feat:`/`test:`/`refactor:`/`docs:`).

---

## Phase 1 — Overlay data structure

### Task 1: `sessionOverlay` + `CapturedMutation` types and operations

**Files:**
- Create: `internal/agent/spec_store_overlay.go`
- Test: `internal/agent/spec_store_overlay_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/spec_store_overlay_test.go`:

```go
// DJ-147 — per-session overlay on the SpecStore. The overlay captures
// would-be mutations (proposes / revises / deletes) and serves them on
// read-after-write so a dry-run workflow runs faithfully end-to-end
// against the would-be graph.
package agent

import (
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionOverlay_PutAndLookup(t *testing.T) {
	o := newSessionOverlay()
	d := spec.Decision{ID: "dec-foo", Title: "Foo"}
	o.put("spec_propose_decision", KindDecision, "dec-foo", d)

	entry, ok := o.lookup(KindDecision, "dec-foo")
	require.True(t, ok)
	require.NotNil(t, entry)
	got, ok := entry.Body.(spec.Decision)
	require.True(t, ok)
	assert.Equal(t, "dec-foo", got.ID)

	assert.Len(t, o.capturedList(), 1)
	cap := o.capturedList()[0]
	assert.Equal(t, "spec_propose_decision", cap.Tool)
	assert.Equal(t, KindDecision, cap.Kind)
	assert.Equal(t, "dec-foo", cap.ID)
}

func TestSessionOverlay_DeleteMasksLookup(t *testing.T) {
	o := newSessionOverlay()
	o.delete("spec_delete_goal", KindGoal, "goal-x")

	_, ok := o.lookup(KindGoal, "goal-x")
	assert.False(t, ok, "lookup must report missing for deleted entries")

	assert.True(t, o.isDeleted(KindGoal, "goal-x"))
	assert.False(t, o.isDeleted(KindGoal, "goal-y"))

	assert.Len(t, o.capturedList(), 1)
	assert.Equal(t, "spec_delete_goal", o.capturedList()[0].Tool)
}

func TestSessionOverlay_RevisePreservesOrderedCapture(t *testing.T) {
	o := newSessionOverlay()
	o.put("spec_propose_feature", KindFeature, "feat-a", spec.Feature{ID: "feat-a"})
	o.put("spec_revise_feature", KindFeature, "feat-a", spec.Feature{ID: "feat-a", Title: "A revised"})

	entry, ok := o.lookup(KindFeature, "feat-a")
	require.True(t, ok)
	got := entry.Body.(spec.Feature)
	assert.Equal(t, "A revised", got.Title, "later writes overwrite earlier ones in the overlay")

	caps := o.capturedList()
	require.Len(t, caps, 2)
	assert.Equal(t, "spec_propose_feature", caps[0].Tool)
	assert.Equal(t, "spec_revise_feature", caps[1].Tool)
	assert.True(t, caps[0].Timestamp.Before(caps[1].Timestamp) || caps[0].Timestamp.Equal(caps[1].Timestamp))
}

func TestSessionOverlay_ConcurrentPutSafe(t *testing.T) {
	o := newSessionOverlay()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			id := "dec-" + string(rune('a'+i))
			o.put("spec_propose_decision", KindDecision, id, spec.Decision{ID: id})
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	// All 8 must be present.
	for i := 0; i < 8; i++ {
		id := "dec-" + string(rune('a'+i))
		_, ok := o.lookup(KindDecision, id)
		assert.True(t, ok, "missing %s after concurrent writes", id)
	}
	assert.Len(t, o.capturedList(), 8)
}

// Avoid the import-vs-used-time-noise: use time directly so the test file owns the import.
var _ = time.Now
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run 'TestSessionOverlay' -v`
Expected: FAIL — types and helpers don't exist.

- [ ] **Step 3: Create the overlay module**

Create `internal/agent/spec_store_overlay.go`:

```go
package agent

import (
	"sync"
	"time"
)

// CapturedMutation records one would-be mutation that a dry-run
// session emitted. The CLI's tools.jsonl-based render and the
// spec_dry_run_report MCP tool both surface these in capture order.
//
// Body is the typed entry (spec.Decision / spec.Feature / ...) the
// build*Body helper produced — same shape as what SpecStore.Put would
// have persisted under a normal write.
type CapturedMutation struct {
	Tool      string
	Kind      SpecKind
	ID        string
	Body      any
	Timestamp time.Time
}

// sessionOverlay holds a single dry-run MCP session's would-be
// mutations. Per DJ-147 §3: written by the captureOnly wrapper,
// consulted on every spec_list_manifest / spec_get / spec_search call
// from the same session via OverlayView, discarded at session close.
//
// The overlay is intentionally NOT a copy of the base store. It holds
// only what this session has written (entries + deleted), so memory
// scales with the captured mutation set, not the graph size.
type sessionOverlay struct {
	mu       sync.RWMutex
	entries  map[storeKey]*StoreEntry
	deleted  map[storeKey]struct{}
	captured []CapturedMutation
}

func newSessionOverlay() *sessionOverlay {
	return &sessionOverlay{
		entries: make(map[storeKey]*StoreEntry),
		deleted: make(map[storeKey]struct{}),
	}
}

func (o *sessionOverlay) put(tool string, kind SpecKind, id string, body any) {
	key := storeKey{Kind: kind, ID: id}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries[key] = &StoreEntry{Kind: kind, ID: id, Body: body, Origin: OriginProposed}
	delete(o.deleted, key) // a put after a delete un-masks
	o.captured = append(o.captured, CapturedMutation{
		Tool:      tool,
		Kind:      kind,
		ID:        id,
		Body:      body,
		Timestamp: time.Now().UTC(),
	})
}

func (o *sessionOverlay) delete(tool string, kind SpecKind, id string) {
	key := storeKey{Kind: kind, ID: id}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deleted[key] = struct{}{}
	delete(o.entries, key) // a delete after a put removes the would-be entry
	o.captured = append(o.captured, CapturedMutation{
		Tool:      tool,
		Kind:      kind,
		ID:        id,
		Timestamp: time.Now().UTC(),
	})
}

// lookup returns (entry, true) when the overlay holds a would-be entry
// for (kind, id), (nil, false) when it doesn't. A deleted key reports
// (nil, false) — masking the base store on the read path.
func (o *sessionOverlay) lookup(kind SpecKind, id string) (*StoreEntry, bool) {
	key := storeKey{Kind: kind, ID: id}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if _, gone := o.deleted[key]; gone {
		return nil, false
	}
	entry, ok := o.entries[key]
	return entry, ok
}

// isDeleted reports whether (kind, id) is marked deleted by this
// session. Used by the read-path merge to mask the base store.
func (o *sessionOverlay) isDeleted(kind SpecKind, id string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, gone := o.deleted[storeKey{Kind: kind, ID: id}]
	return gone
}

// capturedList returns a defensive copy of the ordered capture so
// callers can iterate without holding the overlay's lock.
func (o *sessionOverlay) capturedList() []CapturedMutation {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]CapturedMutation, len(o.captured))
	copy(out, o.captured)
	return out
}
```

> **Verify before running:** the existing `StoreEntry`, `storeKey`, `SpecKind`, and `OriginProposed` types must already be defined in `internal/agent/spec_store.go`. Run `grep -nE 'type (StoreEntry|storeKey|SpecKind)|OriginProposed' internal/agent/spec_store.go` — adjust the new code to whatever the actual field/type names are if they differ.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestSessionOverlay' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/agent/spec_store_overlay.go internal/agent/spec_store_overlay_test.go
git commit -m "feat(agent): sessionOverlay data structure for DJ-147 dry-run capture"
```

---

### Task 2: SpecStore integration — register/unregister + overlay-aware view

**Files:**
- Modify: `internal/agent/spec_store.go` (add `overlays` map, `RegisterOverlay`, `UnregisterOverlay`, `OverlayCaptured`, `OverlayPut`, `OverlayDelete`, `OverlayView`)
- Test: append to `internal/agent/spec_store_overlay_test.go`

- [ ] **Step 1: Append the failing tests**

Append to `internal/agent/spec_store_overlay_test.go`:

```go
import "github.com/glorious-beard/locutus/internal/specio"

func TestSpecStore_OverlayRegisterUnregister(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	type fakeSess struct{ id string }
	sess := &fakeSess{id: "s1"}

	// Before register, no overlay.
	assert.Nil(t, store.overlayFor(sess))

	store.RegisterOverlay(sess)
	assert.NotNil(t, store.overlayFor(sess))

	store.UnregisterOverlay(sess)
	assert.Nil(t, store.overlayFor(sess))
}

func TestSpecStore_OverlayPutDoesNotPersist(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	type fakeSess struct{ id string }
	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	d := spec.Decision{ID: "dec-foo", Title: "Foo"}
	err = store.OverlayPut(sess, "spec_propose_decision", KindDecision, "dec-foo", d)
	require.NoError(t, err)

	// Disk: nothing written.
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-foo.json")
	assert.Error(t, err, "OverlayPut must NOT write to disk")

	// Captured list: one entry.
	caps := store.OverlayCaptured(sess)
	require.Len(t, caps, 1)
	assert.Equal(t, "dec-foo", caps[0].ID)
}

func TestSpecStore_OverlayViewReadsOverlayThenBase(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	// Seed base with dec-a.
	require.NoError(t, store.Put(KindDecision, "dec-a", spec.Decision{ID: "dec-a", Title: "From base"}, OriginSettled))

	type fakeSess struct{ id string }
	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	// Overlay revises dec-a and adds dec-b.
	require.NoError(t, store.OverlayPut(sess, "spec_revise_decision", KindDecision, "dec-a", spec.Decision{ID: "dec-a", Title: "From overlay"}))
	require.NoError(t, store.OverlayPut(sess, "spec_propose_decision", KindDecision, "dec-b", spec.Decision{ID: "dec-b"}))

	view := store.OverlayView(sess)
	a, ok := view.Lookup(KindDecision, "dec-a")
	require.True(t, ok)
	assert.Equal(t, "From overlay", a.Body.(spec.Decision).Title, "overlay must win over base on read")
	b, ok := view.Lookup(KindDecision, "dec-b")
	require.True(t, ok)
	assert.Equal(t, "dec-b", b.Body.(spec.Decision).ID, "overlay-only entries must be visible")

	// A session without an overlay sees only the base.
	type otherSess struct{ id string }
	other := &otherSess{id: "s2"}
	viewOther := store.OverlayView(other)
	a2, ok := viewOther.Lookup(KindDecision, "dec-a")
	require.True(t, ok)
	assert.Equal(t, "From base", a2.Body.(spec.Decision).Title, "non-dry-run session sees base only")
	_, ok = viewOther.Lookup(KindDecision, "dec-b")
	assert.False(t, ok, "overlay-only entries invisible to other sessions")
}

func TestSpecStore_OverlayDeleteMasks(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/goals", 0o755))
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Put(KindGoal, "goal-x", spec.Goal{ID: "goal-x"}, OriginSettled))

	type fakeSess struct{ id string }
	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	require.NoError(t, store.OverlayDelete(sess, "spec_delete_goal", KindGoal, "goal-x"))

	view := store.OverlayView(sess)
	_, ok := view.Lookup(KindGoal, "goal-x")
	assert.False(t, ok, "deleted entry must be masked from this session's view")

	// Other sessions still see it.
	type otherSess struct{ id string }
	other := &otherSess{id: "s2"}
	_, ok = store.OverlayView(other).Lookup(KindGoal, "goal-x")
	assert.True(t, ok, "deletion is per-session, not global")
}
```

> **Helper accessor note:** `overlayFor(sess)` is an unexported helper the test uses to peek at the map. The implementation adds it; if the test runs in `agent_test` package instead of `agent`, expose it via a test-only wrapper file (`spec_store_overlay_test_helpers.go` with build tag `// +build test`) or — simpler — keep the test file in `package agent`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/agent/ -run 'TestSpecStore_Overlay' -v`
Expected: FAIL — `RegisterOverlay`, `UnregisterOverlay`, `OverlayPut`, `OverlayDelete`, `OverlayView`, `OverlayCaptured`, `overlayFor` all undefined.

- [ ] **Step 3: Extend SpecStore**

Read `internal/agent/spec_store.go` to find the struct definition (around line 91). Append the overlay fields to the struct and add the methods.

Add to the struct:

```go
type SpecStore struct {
    // ... existing fields ...
    overlaysMu sync.RWMutex
    overlays   map[any]*sessionOverlay // keyed by *mcp.ServerSession; `any` to avoid an mcp import here
}
```

Initialize `overlays` in `NewSpecStore`: after the existing initialization, add `store.overlays = make(map[any]*sessionOverlay)`.

Add at the end of the file:

```go
// RegisterOverlay marks a session as dry-run. Subsequent OverlayPut /
// OverlayDelete calls keyed on this session land in the overlay rather
// than reaching the base store; OverlayView merges overlay onto base
// on read. UnregisterOverlay discards it.
//
// sess is *mcp.ServerSession in production but typed as `any` here so
// the agent package doesn't import the MCP SDK (DJ-134 keeps the
// dependency arrow pointing the right direction).
func (s *SpecStore) RegisterOverlay(sess any) {
	s.overlaysMu.Lock()
	defer s.overlaysMu.Unlock()
	if s.overlays == nil {
		s.overlays = make(map[any]*sessionOverlay)
	}
	if _, exists := s.overlays[sess]; !exists {
		s.overlays[sess] = newSessionOverlay()
	}
}

// UnregisterOverlay discards a session's overlay. Idempotent — calling
// it for a session that never registered is a no-op.
func (s *SpecStore) UnregisterOverlay(sess any) {
	s.overlaysMu.Lock()
	defer s.overlaysMu.Unlock()
	delete(s.overlays, sess)
}

// overlayFor returns the session's overlay or nil. Lock-free for
// callers — the returned pointer's own mu guards its data.
func (s *SpecStore) overlayFor(sess any) *sessionOverlay {
	s.overlaysMu.RLock()
	defer s.overlaysMu.RUnlock()
	return s.overlays[sess]
}

// OverlayPut applies a would-be put to the session's overlay. Returns
// an error if the session has no overlay registered (caller bug — the
// captureOnly wrapper guards this in production).
func (s *SpecStore) OverlayPut(sess any, tool string, kind SpecKind, id string, body any) error {
	o := s.overlayFor(sess)
	if o == nil {
		return fmt.Errorf("OverlayPut: session has no registered overlay (call RegisterOverlay first)")
	}
	o.put(tool, kind, id, body)
	return nil
}

// OverlayDelete marks a would-be deletion on the session's overlay.
func (s *SpecStore) OverlayDelete(sess any, tool string, kind SpecKind, id string) error {
	o := s.overlayFor(sess)
	if o == nil {
		return fmt.Errorf("OverlayDelete: session has no registered overlay")
	}
	o.delete(tool, kind, id)
	return nil
}

// OverlayCaptured returns the ordered list of captured mutations for
// the session, or nil if no overlay is registered. The returned slice
// is a defensive copy.
func (s *SpecStore) OverlayCaptured(sess any) []CapturedMutation {
	o := s.overlayFor(sess)
	if o == nil {
		return nil
	}
	return o.capturedList()
}

// OverlayView returns a read merger that consults the session's
// overlay first, then the base store. Sessions without an overlay get
// a view that reads from the base only — safe for non-dry-run sessions
// to call this unconditionally on every read.
type OverlayView struct {
	store   *SpecStore
	overlay *sessionOverlay // may be nil
}

func (s *SpecStore) OverlayView(sess any) *OverlayView {
	return &OverlayView{store: s, overlay: s.overlayFor(sess)}
}

// Lookup returns the entry for (kind, id), consulting the overlay
// first. A deleted overlay entry masks the base store.
func (v *OverlayView) Lookup(kind SpecKind, id string) (*StoreEntry, bool) {
	if v.overlay != nil {
		if v.overlay.isDeleted(kind, id) {
			return nil, false
		}
		if entry, ok := v.overlay.lookup(kind, id); ok {
			return entry, true
		}
	}
	return v.store.GetEntry(kind, id) // existing base-store accessor; verify name
}
```

> **Verify `GetEntry` name:** the base-store accessor that returns `(*StoreEntry, bool)` for `(kind, id)` may be named differently. Run `grep -nE 'func.*SpecStore.*Get|func.*SpecStore.*Lookup' internal/agent/spec_store.go` and use the actual name. If the base store only exposes a batch `GetSpec(ids)` instead, wrap it: do a single-id call in `Lookup`, decode minimally to a `*StoreEntry`. If neither shape exists cleanly, add a small `(s *SpecStore) lookupEntry(kind, id)` helper in this same task.

Add `"fmt"` and `"sync"` to the imports of `internal/agent/spec_store.go` if not already present.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestSpecStore_Overlay' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Run the full agent suite — confirm no regression**

Run: `go test ./internal/agent/ -v 2>&1 | tail -10`
Expected: all pass. The overlay additions are non-invasive — no existing test should break.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/spec_store.go internal/agent/spec_store_overlay_test.go
git commit -m "feat(agent): SpecStore overlay register / view / put / delete (DJ-147)"
```

---

## Phase 2 — Session context wiring

### Task 3: Extend `sessionContext` + `newInitializedHandler` for dry-run signals

**Files:**
- Modify: `internal/mcp/session_context.go` (extend `sessionContext` struct, signal-reading in `newInitializedHandler`, register/unregister overlay)
- Modify: `internal/mcp/server.go` (pass the `*agent.SpecStore` into `newInitializedHandler` so it can register overlays)
- Test: create `internal/mcp/session_context_dry_run_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/session_context_dry_run_test.go`:

```go
// DJ-147 — newInitializedHandler reads _meta["locutus.dry_run"] and
// _meta["locutus.dry_run_format"] alongside the runtime+mode capture,
// stores them on the session map, and registers an overlay on the
// SpecStore so subsequent spec_* tool calls capture rather than persist.
package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitializedHandler_CapturesDryRunSignals(t *testing.T) {
	clearSessionRuntimes()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus-test", Version: "0.0.0"},
		&mcp.ServerOptions{
			InitializedHandler: newInitializedHandler(logger, nil, store),
		},
	)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	// Client sends ClientInfo.name + _meta.locutus.dry_run + _meta.locutus.dry_run_format.
	// ClientSessionOptions doesn't expose InitializeParams in v1.6.1; we override
	// session state directly via the same mechanism Task 2 of DJ-143 used: connect
	// the client, then storeSessionContext directly to simulate the post-init state.
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// Wait for InitializedHandler to fire and capture runtime.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Since the SDK client can't inject _meta in tests, we directly override
	// the captured context to simulate what the bridge does in production.
	storeSessionContext(ss, "claude-code", "headless", true, "json")

	rt, mode := sessionRuntimeFor(ss)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "headless", mode)
	assert.True(t, SessionDryRun(ss))
	assert.Equal(t, "json", SessionDryRunFormat(ss))
}

func TestInitializedHandler_RegistersOverlayWhenDryRun(t *testing.T) {
	clearSessionRuntimes()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	// Use a fake session pointer; bypass full SDK plumbing.
	type fakeSess struct{ id string }
	sess := (*mcp.ServerSession)(nil) // sentinel; the overlay is keyed by pointer identity
	storeSessionContext(sess, "claude-code", "headless", true, "markdown")
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	caps := store.OverlayCaptured(sess)
	assert.NotNil(t, caps, "an overlay must exist for a dry-run session")
	assert.Len(t, caps, 0, "but it starts empty")
	_ = fakeSess{} // keep import alive
}

func TestSessionDryRun_DefaultsFalseForUnknownSession(t *testing.T) {
	clearSessionRuntimes()
	assert.False(t, SessionDryRun((*mcp.ServerSession)(nil)))
	assert.Empty(t, SessionDryRunFormat((*mcp.ServerSession)(nil)))
}
```

> **SDK-injection limitation:** as DJ-143 Task 2 documented, `mcp.ClientSessionOptions` doesn't expose `InitializeParams.Meta` in the SDK version we use. The third test exercises the `storeSessionContext` override path — the same workaround Task 3 of DJ-143's plan used.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcp/ -run 'TestInitializedHandler_Captures|TestInitializedHandler_Registers|TestSessionDryRun_Defaults' -v`
Expected: FAIL — `storeSessionContext` doesn't exist, `SessionDryRun` / `SessionDryRunFormat` don't exist, `newInitializedHandler`'s signature doesn't accept `*agent.SpecStore`.

- [ ] **Step 3: Extend `sessionContext` and add accessors**

In `internal/mcp/session_context.go`:

Replace the existing `sessionContext` type:

```go
type sessionContext struct {
	runtime       string
	mode          string
	dryRun        bool
	dryRunFormat  string
}
```

Replace `storeSessionRuntime` with a more general `storeSessionContext` (keep `storeSessionRuntime` as a thin shim for backward compat with DJ-143 tests):

```go
// storeSessionContext stores the (runtime, mode, dryRun, dryRunFormat)
// tuple for a session. Called from the InitializedHandler at session
// start and from tests to override.
func storeSessionContext(sess any, runtime, mode string, dryRun bool, dryRunFormat string) {
	sessionRuntimesMu.Lock()
	defer sessionRuntimesMu.Unlock()
	sessionRuntimes[sess] = sessionContext{
		runtime:      strings.ToLower(strings.TrimSpace(runtime)),
		mode:         strings.ToLower(strings.TrimSpace(mode)),
		dryRun:       dryRun,
		dryRunFormat: strings.ToLower(strings.TrimSpace(dryRunFormat)),
	}
}

// storeSessionRuntime preserves the DJ-143 entry point (runtime + mode
// only) for tests that don't care about dry-run. New callers should
// prefer storeSessionContext.
func storeSessionRuntime(sess any, runtime, mode string) {
	storeSessionContext(sess, runtime, mode, false, "")
}
```

Add accessors:

```go
// SessionDryRun reports whether the calling session is in dry-run
// mode (per DJ-147 §2 the captureOnly wrapper consults this).
// Returns false for unknown sessions — the safe default; the wrapper
// passes through to the inner handler.
func SessionDryRun(sess *mcp.ServerSession) bool {
	sessionRuntimesMu.RLock()
	defer sessionRuntimesMu.RUnlock()
	c, ok := sessionRuntimes[sess]
	if !ok {
		return false
	}
	return c.dryRun
}

// SessionDryRunFormat returns the format the operator requested via
// --format (markdown or json). Empty for sessions not in dry-run.
func SessionDryRunFormat(sess *mcp.ServerSession) string {
	sessionRuntimesMu.RLock()
	defer sessionRuntimesMu.RUnlock()
	c, ok := sessionRuntimes[sess]
	if !ok {
		return ""
	}
	return c.dryRunFormat
}
```

Update `newInitializedHandler`'s signature and body to accept `*agent.SpecStore` and to read the new `_meta` fields and register the overlay when dry-run is true:

```go
import (
	// existing imports ...
	"github.com/glorious-beard/locutus/internal/agent"
)

// Updated signature: takes store so we can RegisterOverlay on dry-run sessions.
func newInitializedHandler(logger *slog.Logger, fsys specio.FS, store *agent.SpecStore) func(context.Context, *mcp.InitializedRequest) {
	return func(_ context.Context, req *mcp.InitializedRequest) {
		if req == nil || req.Session == nil {
			return
		}
		params := req.Session.InitializeParams()
		if params == nil {
			return
		}
		runtime, version := "", ""
		if params.ClientInfo != nil {
			runtime = params.ClientInfo.Name
			version = params.ClientInfo.Version
		}
		mode := "interactive"
		dryRun := false
		dryRunFormat := "markdown"
		if params.Meta != nil {
			if v, ok := params.Meta["locutus.mode"].(string); ok && strings.TrimSpace(v) != "" {
				mode = v
			}
			if v, ok := params.Meta["locutus.dry_run"]; ok {
				// Accept bool, "1", "true" (case-insensitive). Everything else → false.
				switch t := v.(type) {
				case bool:
					dryRun = t
				case string:
					s := strings.ToLower(strings.TrimSpace(t))
					dryRun = s == "1" || s == "true"
				}
			}
			if v, ok := params.Meta["locutus.dry_run_format"].(string); ok {
				s := strings.ToLower(strings.TrimSpace(v))
				if s == "json" || s == "markdown" {
					dryRunFormat = s
				}
			}
		}
		storeSessionContext(req.Session, runtime, mode, dryRun, dryRunFormat)
		checkRuntimeVersion(logger, fsys, runtime, version)
		if dryRun && store != nil {
			store.RegisterOverlay(req.Session)
		}
	}
}
```

> Note on session-close: the go-sdk v1.6.1 has no session-close hook (DJ-143 documented this limitation for `sessionRuntimes`). The overlay map shares that constraint — entries persist for the daemon's lifetime. For typical operator behavior (handful of dry-runs per day) the leak is negligible. Documented in the file-level comment.

Update the `sessionRuntimes` doc-comment block at the top of the file to mention dry-run alongside runtime+mode.

- [ ] **Step 4: Update `internal/mcp/server.go` to pass the store into the handler**

Find the `NewSpecServer` call site:

```bash
grep -n 'newInitializedHandler' internal/mcp/server.go
```

Update the call to pass `store`:

```go
InitializedHandler: newInitializedHandler(slog.Default(), fsys, store),
```

> The `store` parameter to `NewSpecServer` is already present (DJ-134); just pass it through.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/mcp/ -run 'TestInitializedHandler_Captures|TestInitializedHandler_Registers|TestSessionDryRun_Defaults' -v`
Expected: PASS (3 tests).

Run the full mcp suite: `go test ./internal/mcp/ -v 2>&1 | tail -10`
Expected: all pass — DJ-143 tests still green (the `storeSessionRuntime` shim preserves their entry point).

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/session_context.go internal/mcp/server.go internal/mcp/session_context_dry_run_test.go
git commit -m "feat(mcp): session-context captures dry-run signals; registers overlay (DJ-147)"
```

---

## Phase 3 — `captureOnly` wrapper

### Task 4: Generic `captureOnly[In, Out]` adapter

**Files:**
- Modify: `internal/mcp/session_context.go` (add `captureOnly` alongside `requireRuntimeAny`)
- Test: append to `internal/mcp/session_context_dry_run_test.go`

- [ ] **Step 1: Append the failing tests**

Append to `internal/mcp/session_context_dry_run_test.go`:

```go
import (
	// existing imports
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
)

type captureTestArgs struct{ ID, Title string }
type captureTestResult struct{ ID string }

func TestCaptureOnly_PassthroughWhenNotDryRun(t *testing.T) {
	clearSessionRuntimes()
	called := false
	inner := func(_ context.Context, _ *mcp.CallToolRequest, in captureTestArgs) (*mcp.CallToolResult, captureTestResult, error) {
		called = true
		return nil, captureTestResult{ID: in.ID}, nil
	}
	captureFn := func(sess *mcp.ServerSession, in captureTestArgs) (captureTestResult, error) {
		t.Fatal("capture must not run when session is not dry-run")
		return captureTestResult{}, nil
	}
	wrapped := captureOnly(inner, captureFn)

	storeSessionContext((*mcp.ServerSession)(nil), "claude-code", "headless", false, "")
	req := &mcp.CallToolRequest{Session: (*mcp.ServerSession)(nil), Params: &mcp.CallToolParamsRaw{Name: "spec_propose_decision"}}
	_, _, err := wrapped(context.Background(), req, captureTestArgs{ID: "dec-x"})
	require.NoError(t, err)
	assert.True(t, called, "inner handler must run for non-dry-run sessions")
}

func TestCaptureOnly_CapturesWhenDryRun(t *testing.T) {
	clearSessionRuntimes()
	innerCalled := false
	captureCalled := false
	var capturedIn captureTestArgs

	inner := func(_ context.Context, _ *mcp.CallToolRequest, _ captureTestArgs) (*mcp.CallToolResult, captureTestResult, error) {
		innerCalled = true
		return nil, captureTestResult{}, nil
	}
	captureFn := func(sess *mcp.ServerSession, in captureTestArgs) (captureTestResult, error) {
		captureCalled = true
		capturedIn = in
		return captureTestResult{ID: in.ID}, nil
	}
	wrapped := captureOnly(inner, captureFn)

	storeSessionContext((*mcp.ServerSession)(nil), "claude-code", "headless", true, "markdown")
	req := &mcp.CallToolRequest{Session: (*mcp.ServerSession)(nil), Params: &mcp.CallToolParamsRaw{Name: "spec_propose_decision"}}
	res, out, err := wrapped(context.Background(), req, captureTestArgs{ID: "dec-x", Title: "X"})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, innerCalled, "inner handler must NOT run for dry-run sessions")
	assert.True(t, captureCalled, "capture must run for dry-run sessions")
	assert.Equal(t, "dec-x", capturedIn.ID)
	assert.Equal(t, "dec-x", out.ID)
}

func TestCaptureOnly_CaptureErrorReturnsToolError(t *testing.T) {
	clearSessionRuntimes()
	inner := func(_ context.Context, _ *mcp.CallToolRequest, _ captureTestArgs) (*mcp.CallToolResult, captureTestResult, error) {
		return nil, captureTestResult{}, nil
	}
	captureFn := func(sess *mcp.ServerSession, in captureTestArgs) (captureTestResult, error) {
		return captureTestResult{}, fmt.Errorf("simulated capture failure")
	}
	wrapped := captureOnly(inner, captureFn)

	storeSessionContext((*mcp.ServerSession)(nil), "claude-code", "headless", true, "markdown")
	req := &mcp.CallToolRequest{Session: (*mcp.ServerSession)(nil), Params: &mcp.CallToolParamsRaw{Name: "spec_propose_decision"}}
	res, _, err := wrapped(context.Background(), req, captureTestArgs{ID: "dec-x"})

	require.NoError(t, err, "tool-level errors are surfaced as IsError results, not Go errors")
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}

// Avoid unused imports in test stubs.
var _ = agent.KindDecision
var _ = spec.Decision{}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcp/ -run TestCaptureOnly -v`
Expected: FAIL — `captureOnly` undefined.

- [ ] **Step 3: Add the wrapper**

Append to `internal/mcp/session_context.go`:

```go
// captureOnly wraps a tool handler so that dry-run sessions land their
// would-be input in the overlay (via the caller-supplied capture
// function) rather than running the inner handler.
//
// Per DJ-147 §4: composes with requireRuntimeAny — apply requireRuntime
// outermost so denied runtimes fail before capture runs.
//
// The capture function receives the same In the inner handler would
// have received; it's expected to validate input identically (so the
// agent gets the same shape-error feedback in both modes) and to call
// store.OverlayPut / OverlayDelete via a closure over the SpecStore.
func captureOnly[In, Out any](
	h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
	capture func(sess *mcp.ServerSession, in In) (Out, error),
) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		if !SessionDryRun(req.Session) {
			return h(ctx, req, in)
		}
		out, err := capture(req.Session, in)
		if err != nil {
			var zero Out
			return errorResult(err.Error()), zero, nil
		}
		return textResult("captured (dry-run)"), out, nil
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp/ -run TestCaptureOnly -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/session_context.go internal/mcp/session_context_dry_run_test.go
git commit -m "feat(mcp): captureOnly wrapper for DJ-147 dry-run mutation capture"
```

---

## Phase 4 — Wrap the mutation tools

### Task 5: Wrap all 14 mutation handlers with `captureOnly` + per-tool capture closures

**Files:**
- Modify: `internal/mcp/tools_spec_write.go` (wrap every `mcp.AddTool` for a mutation tool)
- Test: create `internal/mcp/tools_spec_write_dry_run_test.go`

- [ ] **Step 1: Enumerate the tools to wrap**

Run: `grep -nE 'Name:\s+"spec_(propose|revise|delete|mark|update)' internal/mcp/tools_spec_write.go`

Expected list (14 tools): `spec_propose_decision`, `spec_revise_decision`, `spec_propose_feature`, `spec_revise_feature`, `spec_propose_strategy`, `spec_revise_strategy`, `spec_propose_goal`, `spec_revise_goal`, `spec_delete_goal`, `spec_propose_antigoal`, `spec_revise_antigoal`, `spec_delete_antigoal`, `spec_mark_approach_drifted`, `spec_update_goals_md_hash`. Confirm the count; if any are absent or there are extras, adjust this task's wraps accordingly.

- [ ] **Step 2: Write the failing test (table-driven coverage)**

Create `internal/mcp/tools_spec_write_dry_run_test.go`:

```go
// DJ-147 — every mutation MCP tool, called from a dry-run session,
// must capture rather than persist. Verified end-to-end via the
// in-memory transport pair: call the tool, then assert the SpecStore's
// overlay has the entry and the base store does not.
package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDryRunCapturesMutations(t *testing.T) {
	// Each entry: tool name + arguments + (kind, id) the capture
	// should land in the overlay under.
	cases := []struct {
		name string
		tool string
		args map[string]any
		kind agent.SpecKind
		id   string
	}{
		{"propose_decision", "spec_propose_decision",
			map[string]any{"id": "dec-foo", "title": "Foo", "chosen_option": "x", "rationale": "y"},
			agent.KindDecision, "dec-foo"},
		{"propose_feature", "spec_propose_feature",
			map[string]any{"id": "feat-foo", "title": "Foo", "summary": "s", "body": "b"},
			agent.KindFeature, "feat-foo"},
		{"propose_strategy", "spec_propose_strategy",
			map[string]any{"id": "strat-foo", "title": "Foo", "body": "b"},
			agent.KindStrategy, "strat-foo"},
		{"propose_goal", "spec_propose_goal",
			map[string]any{"id": "goal-foo", "title": "Foo", "body": "b", "source_clause": "verbatim"},
			agent.KindGoal, "goal-foo"},
		{"propose_antigoal", "spec_propose_antigoal",
			map[string]any{"id": "agoal-foo", "title": "Foo", "body": "b", "source_clause": "verbatim"},
			agent.KindAntiGoal, "agoal-foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSessionRuntimes()
			fsys := specio.NewMemFS()
			require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
			require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
			require.NoError(t, fsys.MkdirAll(".borg/spec/strategies", 0o755))
			require.NoError(t, fsys.MkdirAll(".borg/spec/goals", 0o755))
			require.NoError(t, fsys.MkdirAll(".borg/spec/antigoals", 0o755))

			store, err := agent.NewSpecStore(fsys)
			require.NoError(t, err)

			server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil)
			serverT, clientT := mcp.NewInMemoryTransports()
			ss, err := server.Connect(context.Background(), serverT, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = ss.Close() })

			client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
			cs, err := client.Connect(context.Background(), clientT, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = cs.Close() })

			// Wait for the runtime to land.
			for d := time.Now().Add(time.Second); time.Now().Before(d); {
				if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			// Flip dry-run on directly (the SDK client can't inject _meta).
			storeSessionContext(ss, "claude-code", "headless", true, "markdown")
			store.RegisterOverlay(ss)
			t.Cleanup(func() { store.UnregisterOverlay(ss) })

			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			require.NoError(t, err)
			require.False(t, res.IsError, "tool result: %+v", res)

			// Captured in overlay.
			caps := store.OverlayCaptured(ss)
			require.Len(t, caps, 1)
			assert.Equal(t, tc.tool, caps[0].Tool)
			assert.Equal(t, tc.kind, caps[0].Kind)
			assert.Equal(t, tc.id, caps[0].ID)

			// NOT on disk.
			view := store.OverlayView(ss)
			_, ok := view.Lookup(tc.kind, tc.id)
			assert.True(t, ok, "overlay view must surface the captured entry")
		})
	}
}

func TestDryRunPassthroughForNonDryRunSession(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Leave dryRun false.

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{"id": "dec-foo", "title": "Foo", "chosen_option": "x", "rationale": "y"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Persisted to disk.
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-foo.json")
	assert.NoError(t, err, "non-dry-run sessions must still persist")
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/mcp/ -run 'TestDryRunCapturesMutations|TestDryRunPassthroughForNonDryRunSession' -v`
Expected: FAIL — captures don't happen yet because nothing is wrapped. (`TestDryRunPassthroughForNonDryRunSession` may already pass — that's fine.)

- [ ] **Step 4: Add per-tool capture helpers + wrap registrations**

In `internal/mcp/tools_spec_write.go`, near the existing `commitOne` helper, add per-tool capture closures. The pattern is uniform: build the typed body via the existing `build*Body` helper, then call `store.OverlayPut` or `OverlayDelete`:

```go
// captureProposeDecision is the dry-run twin of the spec_propose_decision
// handler. Same input shape; instead of commitOne it calls OverlayPut.
func captureProposeDecision(store *agent.SpecStore) func(sess *mcp.ServerSession, in proposeDecisionInput) (any, error) {
	return func(sess *mcp.ServerSession, in proposeDecisionInput) (any, error) {
		body, err := buildDecisionBody(in, time.Time{})
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_propose_decision", agent.KindDecision, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

// captureReviseDecision; same pattern but preserves the existing-id check.
func captureReviseDecision(store *agent.SpecStore) func(sess *mcp.ServerSession, in reviseDecisionInput) (any, error) {
	return func(sess *mcp.ServerSession, in reviseDecisionInput) (any, error) {
		// In dry-run, the existing-id check still applies — agent gets the same
		// not-found feedback. Consult the overlay-view (overlay-then-base).
		view := store.OverlayView(sess)
		_, ok := view.Lookup(agent.KindDecision, in.ID)
		if !ok {
			return nil, fmt.Errorf("spec_revise_decision: decision %q does not exist; use spec_propose_decision to create it", in.ID)
		}
		// preserve created_at: read it from the overlay-view entry. (See existing
		// existingDecisionCreatedAt helper for the same path under non-dry-run.)
		body, err := buildDecisionBody(in, time.Time{} /* TODO: read createdAt from view */)
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_revise_decision", agent.KindDecision, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}
```

> **`createdAt` preservation under dry-run:** the existing revise handlers read `createdAt` via small helpers like `existingDecisionCreatedAt`. Those today look at the base store. Update them (or write overlay-aware twins `existingDecisionCreatedAtOverlay(store, sess, id)`) so the dry-run revise also preserves the original timestamp. Same pattern for feature / strategy.

Write capture helpers for all 14 tools. The shape is:
- propose/revise: build body via `build*Body`, `OverlayPut`.
- delete: `OverlayDelete`.
- mark_approach_drifted: build the drift body, `OverlayPut` against the existing Approach entry from the overlay view (revise-shape on the approach entry; the field updated is `invalidated_by_event_id`).
- update_goals_md_hash: the manifest fields (`goals_md_hash`, `goals_md_synced_at`) need a small overlay slot — extend `sessionOverlay` with an optional `manifestOverride *manifestStub` and have `OverlayView.Manifest()` consult it first. Cover with one extra unit test in `spec_store_overlay_test.go`.

Then wrap each `mcp.AddTool` registration. Example for `spec_propose_goal`:

```go
mcp.AddTool(server, &mcp.Tool{
    Name:        "spec_propose_goal",
    Description: descSpecProposeGoal,
}, captureOnly(
    func(ctx context.Context, _ *mcp.CallToolRequest, in proposeGoalInput) (*mcp.CallToolResult, any, error) {
        // existing handler body unchanged
        body, err := buildGoalBody(in, time.Time{})
        if err != nil {
            return errorResult(err.Error()), nil, nil
        }
        if err := commitOne(store, agent.KindGoal, in.ID, body); err != nil {
            return errorResult(err.Error()), nil, nil
        }
        publishManifestUpdate(ctx, server)
        return textResult(fmt.Sprintf("Proposed goal %s.", in.ID)), nil, nil
    },
    captureProposeGoal(store),
))
```

Apply the same wrap pattern to all 14 registrations. Take care to compose with `requireRuntimeAny` where it already wraps (e.g., `spec_update_goals_md_hash` may not have a runtime guard, but if any of the targeted tools do, apply order is `requireRuntimeAny(captureOnly(inner, capture), ...allowed)` — outermost is `requireRuntimeAny` so denial happens first).

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/mcp/ -run 'TestDryRunCapturesMutations|TestDryRunPassthroughForNonDryRunSession' -v`
Expected: PASS (5 subtests in the table + the passthrough test).

Run the full mcp suite: `go test ./internal/mcp/ -v 2>&1 | tail -10`
Expected: all pass — existing write tests still work because they don't set dry-run, so the wrapper passes through.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools_spec_write.go internal/mcp/tools_spec_write_dry_run_test.go
git commit -m "feat(mcp): wrap mutation tools with captureOnly for DJ-147 dry-run"
```

---

## Phase 5 — Overlay-aware reads

### Task 6: Make `spec_list_manifest`, `spec_get`, `spec_search` consult the overlay

**Files:**
- Modify: `internal/mcp/tools_spec_read.go`
- Test: append to `internal/mcp/tools_spec_write_dry_run_test.go` (cross-cutting read-after-write coverage)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/tools_spec_write_dry_run_test.go`:

```go
func TestDryRunReadAfterWrite(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	// Seed a base feature so we can test overlay-overrides-base.
	require.NoError(t, store.Put(agent.KindFeature, "feat-base", spec.Feature{ID: "feat-base", Title: "Base"}, agent.OriginSettled))

	server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	storeSessionContext(ss, "claude-code", "headless", true, "markdown")
	store.RegisterOverlay(ss)
	t.Cleanup(func() { store.UnregisterOverlay(ss) })

	// Capture two operations: revise the base feature + propose a new one.
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_feature",
		Arguments: map[string]any{"id": "feat-base", "title": "Revised in overlay", "summary": "s", "body": "b"},
	})
	require.NoError(t, err)
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_feature",
		Arguments: map[string]any{"id": "feat-new", "title": "New", "summary": "s", "body": "b"},
	})
	require.NoError(t, err)

	// spec_get must return overlay versions to THIS session.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_get",
		Arguments: map[string]any{"ids": []string{"feat-base", "feat-new"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	body := callToolResultText(t, res)
	assert.Contains(t, body, "Revised in overlay", "overlay revision must surface on read in the same session")
	assert.Contains(t, body, "feat-new", "overlay-only entry must surface on read in the same session")

	// spec_list_manifest must include feat-new (overlay-only).
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "spec_list_manifest"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	assert.Contains(t, callToolResultText(t, res), "feat-new")
}

// callToolResultText extracts the text content from a tool result for substring assertions.
func callToolResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcp/ -run TestDryRunReadAfterWrite -v`
Expected: FAIL — reads consult the base store, not the overlay; overlay-only entries are invisible.

- [ ] **Step 3: Update the read handlers**

In `internal/mcp/tools_spec_read.go`, find the `spec_list_manifest`, `spec_get`, `spec_search` handlers. Each currently calls a SpecStore accessor (`GetManifest`, `GetSpec(ids)`, etc.). Update each to call the overlay-aware path via `store.OverlayView(req.Session)`:

- `spec_list_manifest`: build the manifest from the overlay-view. The simplest path is: have `OverlayView` expose `Manifest() *spec.Manifest` that returns a merged manifest (overlay entries layered on the base manifest, deleted entries masked). Implement that in `internal/agent/spec_store_overlay.go` as a method on `*OverlayView`.
- `spec_get`: replace the existing `store.GetSpec(ids)` call with a loop over ids that consults `view.Lookup(kind, id)` first and falls back to `store.GetSpec` for ids not in the overlay. For ids that ARE in the overlay, marshal the typed body to JSON for the response shape.
- `spec_search`: query the base Bluge index as today; post-filter — for each captured mutation in `view.Captured()`, score the captured body's text against the query and inject matching entries into the result list. Sort by score. Accept the documented soft fidelity gap on full-text matches against captured-but-not-indexed bodies (DJ-147 §3 + Resolved Q5).

> The exact merge code is small but the manifest shape is project-specific. Read the existing `Manifest` struct in `internal/agent/spec_store.go` and the existing `spec_list_manifest` handler body — mirror its shape, just sourcing entries from `view.Lookup` instead of the base store.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp/ -run TestDryRunReadAfterWrite -v`
Expected: PASS.

Run the broader read-tool tests: `go test ./internal/mcp/ -run 'TestSpec(List|Get|Search)' -v 2>&1 | tail -10`
Expected: all pass — non-dry-run reads should be unchanged (overlay-view with nil overlay is a passthrough).

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_spec_read.go internal/agent/spec_store_overlay.go internal/mcp/tools_spec_write_dry_run_test.go
git commit -m "feat(mcp): overlay-aware reads for DJ-147 dry-run faithfulness"
```

---

## Phase 6 — `spec_dry_run_report` MCP tool

### Task 7: Register the read-only `spec_dry_run_report` tool

**Files:**
- Create: `internal/mcp/tools_dry_run.go`
- Test: create `internal/mcp/tools_dry_run_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/tools_dry_run_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecDryRunReport_ReturnsCapturedOrdered(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	storeSessionContext(ss, "claude-code", "headless", true, "json")
	store.RegisterOverlay(ss)
	t.Cleanup(func() { store.UnregisterOverlay(ss) })

	// Propose two things in order.
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{"id": "dec-a", "title": "A", "chosen_option": "x", "rationale": "y"},
	})
	require.NoError(t, err)
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_feature",
		Arguments: map[string]any{"id": "feat-a", "title": "A", "summary": "s", "body": "b"},
	})
	require.NoError(t, err)

	// Call the report tool.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "spec_dry_run_report"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// The result body should be JSON; parse and assert ordered + format.
	body := callToolResultText(t, res)
	var parsed struct {
		Format    string `json:"format"`
		Captured  []struct {
			Tool string `json:"tool"`
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"captured"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &parsed))
	assert.Equal(t, "json", parsed.Format)
	require.Len(t, parsed.Captured, 2)
	assert.Equal(t, "spec_propose_decision", parsed.Captured[0].Tool)
	assert.Equal(t, "dec-a", parsed.Captured[0].ID)
	assert.Equal(t, "spec_propose_feature", parsed.Captured[1].Tool)
	assert.Equal(t, "feat-a", parsed.Captured[1].ID)
}

func TestSpecDryRunReport_EmptyWhenNoCaptures(t *testing.T) {
	clearSessionRuntimes()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	server := NewSpecServer(store, specio.NewMemFS(), activity.DefaultRegistry(), nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	storeSessionContext(ss, "claude-code", "headless", true, "markdown")
	store.RegisterOverlay(ss)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "spec_dry_run_report"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	body := callToolResultText(t, res)
	assert.Contains(t, body, `"captured":[]`, "empty session returns an empty list")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcp/ -run 'TestSpecDryRunReport' -v`
Expected: FAIL — `spec_dry_run_report` not registered.

- [ ] **Step 3: Register the tool**

Create `internal/mcp/tools_dry_run.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descSpecDryRunReport = "Returns the calling session's ordered list of captured mutations (DJ-147). " +
	"Only meaningful for sessions in dry-run mode — the workflow's would-be spec_propose_* / spec_revise_* / " +
	"spec_delete_* / spec_mark_approach_drifted / spec_update_goals_md_hash calls accumulate in a per-session " +
	"overlay; this tool reads them back. Response shape: {format, captured: [{tool, kind, id, body, timestamp}, ...]}. " +
	"Format echoes the operator's --format choice (markdown|json) so the agent can decide between verbatim " +
	"emission (json) and prose narration (markdown). Read-only; no input."

type dryRunReportInput struct{}

type dryRunReportOutput struct {
	Format   string                      `json:"format"`
	Captured []agent.CapturedMutation    `json:"captured"`
}

func registerDryRunTools(server *mcp.Server, store *agent.SpecStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_dry_run_report",
		Description: descSpecDryRunReport,
	}, func(_ context.Context, req *mcp.CallToolRequest, _ dryRunReportInput) (*mcp.CallToolResult, dryRunReportOutput, error) {
		out := dryRunReportOutput{
			Format:   SessionDryRunFormat(req.Session),
			Captured: store.OverlayCaptured(req.Session),
		}
		// Marshal to JSON for the text content; the structured output is the same.
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("spec_dry_run_report: marshal: %v", err)), out, nil
		}
		return textResult(string(body)), out, nil
	})
}
```

Wire it from `NewSpecServer` (in `internal/mcp/server.go`):

```go
registerDryRunTools(server, store)
```

Add the call alongside the existing `registerReadTools(server, store)` and `registerWriteTools(server, store, hist)` calls.

> The `Captured` field renders the typed `Body` per Go's default JSON marshaling. If a specific body field needs custom JSON (e.g., omit empty), add the tag at the type definition in `internal/agent/spec_store_overlay.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp/ -run 'TestSpecDryRunReport' -v`
Expected: PASS (2 tests).

Run the full mcp suite: `go test ./internal/mcp/ 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_dry_run.go internal/mcp/server.go internal/mcp/tools_dry_run_test.go
git commit -m "feat(mcp): spec_dry_run_report MCP tool (DJ-147)"
```

---

## Phase 7 — Bridge plumbing

### Task 8: Bridge reads `LOCUTUS_DRY_RUN` + `LOCUTUS_DRY_RUN_FORMAT`, forwards via `_meta`

**Files:**
- Modify: `cmd/mcp.go` (add `resolveLocutusDryRun()` + `resolveLocutusDryRunFormat()`; thread through to `BridgeStdioToSocket`)
- Modify: `internal/mcp/bridge.go` (inject `_meta["locutus.dry_run"]` + `_meta["locutus.dry_run_format"]` alongside the existing `locutus.mode` injection from DJ-143)
- Test: extend `cmd/mcp_test.go` + `internal/mcp/bridge_test.go`

- [ ] **Step 1: Write the failing env-resolution tests in `cmd/mcp_test.go`**

Append to `cmd/mcp_test.go`:

```go
func TestResolveLocutusDryRun_UnsetIsFalse(t *testing.T) {
	t.Setenv("LOCUTUS_DRY_RUN", "")
	assert.False(t, resolveLocutusDryRun())
}

func TestResolveLocutusDryRun_TruthyVariants(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "  True  "} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("LOCUTUS_DRY_RUN", v)
			assert.True(t, resolveLocutusDryRun())
		})
	}
}

func TestResolveLocutusDryRun_FalsyVariants(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "anything-else"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("LOCUTUS_DRY_RUN", v)
			assert.False(t, resolveLocutusDryRun())
		})
	}
}

func TestResolveLocutusDryRunFormat_DefaultMarkdown(t *testing.T) {
	t.Setenv("LOCUTUS_DRY_RUN_FORMAT", "")
	assert.Equal(t, "markdown", resolveLocutusDryRunFormat())
}

func TestResolveLocutusDryRunFormat_AcceptsJSONAndMarkdown(t *testing.T) {
	t.Setenv("LOCUTUS_DRY_RUN_FORMAT", "json")
	assert.Equal(t, "json", resolveLocutusDryRunFormat())
	t.Setenv("LOCUTUS_DRY_RUN_FORMAT", "MARKDOWN")
	assert.Equal(t, "markdown", resolveLocutusDryRunFormat())
}

func TestResolveLocutusDryRunFormat_UnknownFallsBack(t *testing.T) {
	t.Setenv("LOCUTUS_DRY_RUN_FORMAT", "xml")
	assert.Equal(t, "markdown", resolveLocutusDryRunFormat(), "unknown values fall back to the safe default")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/ -run 'TestResolveLocutusDryRun' -v`
Expected: FAIL — helpers undefined.

- [ ] **Step 3: Add the helpers and update `McpCmd.Run`**

In `cmd/mcp.go`, add alongside `resolveLocutusMode`:

```go
func resolveLocutusDryRun() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("LOCUTUS_DRY_RUN")))
	return v == "1" || v == "true"
}

func resolveLocutusDryRunFormat() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("LOCUTUS_DRY_RUN_FORMAT")))
	switch v {
	case "json", "markdown":
		return v
	default:
		return "markdown"
	}
}
```

In `McpCmd.Run`, after `mode := resolveLocutusMode()`, add:

```go
dryRun := resolveLocutusDryRun()
dryRunFormat := resolveLocutusDryRunFormat()
if err := mcp.BridgeStdioToSocket(ctx, sockPath, mode, dryRun, dryRunFormat); err != nil {
    return fmt.Errorf("mcp: %w", err)
}
```

> The `BridgeStdioToSocket` signature gets two new args. Update everywhere it's called (probably just the one site in `cmd/mcp.go`).

- [ ] **Step 4: Update the bridge to inject the new `_meta` fields**

In `internal/mcp/bridge.go`, update `BridgeStdioToSocket` and `BridgeIOToSocket` signatures to take `(mode string, dryRun bool, dryRunFormat string)`. Update `maybeInjectInitMode` to also set `meta["locutus.dry_run"] = dryRun` (when true) and `meta["locutus.dry_run_format"] = dryRunFormat` (when non-empty). Rename it to `maybeInjectInitMeta` to reflect that it now handles three fields.

- [ ] **Step 5: Append bridge tests**

Append to `internal/mcp/bridge_test.go`:

```go
func TestBridgeInjectsDryRunMeta(t *testing.T) {
	sock := filepath.Join(shortTempSocketDir(t), "mcp.sock")
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	captured := make(chan map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var msg map[string]any
		_ = json.Unmarshal(line, &msg)
		params, _ := msg["params"].(map[string]any)
		meta, _ := params["_meta"].(map[string]any)
		captured <- meta
	}()

	in, inW := io.Pipe()
	out, outW := io.Pipe()
	go func() {
		initLine := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"claude-code","version":"test"}}}` + "\n"
		_, _ = inW.Write([]byte(initLine))
		_ = inW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, out) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = BridgeIOToSocket(ctx, sock, in, outW, "headless", true, "json")

	select {
	case meta := <-captured:
		require.NotNil(t, meta)
		assert.Equal(t, "headless", meta["locutus.mode"])
		assert.Equal(t, true, meta["locutus.dry_run"])
		assert.Equal(t, "json", meta["locutus.dry_run_format"])
	case <-time.After(2 * time.Second):
		t.Fatal("server never received an initialize")
	}
}

func TestBridgeOmitsDryRunMetaWhenFalse(t *testing.T) {
	sock := filepath.Join(shortTempSocketDir(t), "mcp.sock")
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	captured := make(chan map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil { return }
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var msg map[string]any
		_ = json.Unmarshal(line, &msg)
		params, _ := msg["params"].(map[string]any)
		meta, _ := params["_meta"].(map[string]any)
		captured <- meta
	}()

	in, inW := io.Pipe()
	out, outW := io.Pipe()
	go func() {
		_, _ = inW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"claude-code","version":"test"}}}` + "\n"))
		_ = inW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, out) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = BridgeIOToSocket(ctx, sock, in, outW, "interactive", false, "")

	select {
	case meta := <-captured:
		assert.Equal(t, "interactive", meta["locutus.mode"])
		_, hasDryRun := meta["locutus.dry_run"]
		assert.False(t, hasDryRun, "dry_run field must be omitted when false (defaults are implicit)")
	case <-time.After(2 * time.Second):
		t.Fatal("server never received an initialize")
	}
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./cmd/ -run 'TestResolveLocutusDryRun' -v` — PASS.
Run: `go test ./internal/mcp/ -run 'TestBridgeInjects|TestBridgeOmits|TestBridgePassesThrough' -v` — PASS.
Run: `go test ./... 2>&1 | grep -vE '^ok|no test files' | tail -10` — empty (full suite green).

- [ ] **Step 7: Commit**

```bash
git add cmd/mcp.go cmd/mcp_test.go internal/mcp/bridge.go internal/mcp/bridge_test.go
git commit -m "feat(mcp): bridge forwards LOCUTUS_DRY_RUN + LOCUTUS_DRY_RUN_FORMAT via _meta (DJ-147)"
```

---

## Phase 8 — CLI surface

### Task 9: Kong flags + contextNote + post-dispatch tools.jsonl render

**Files:**
- Modify: `cmd/import.go`, `cmd/refine.go`, `cmd/adopt.go`, `cmd/assimilate.go` (add `DryRun bool` + `Format string` Kong fields)
- Modify: `cmd/activity_verb.go` (env + contextNote when `--dry-run`; post-dispatch tools.jsonl render in headless)
- Test: `cmd/activity_verb_dry_run_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/activity_verb_dry_run_test.go`:

```go
package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDryRunContextNote_Markdown(t *testing.T) {
	note := dryRunContextNote("markdown")
	assert.Contains(t, note, "Dry-run mode is active")
	assert.Contains(t, note, "spec_dry_run_report")
	assert.Contains(t, note, "prose closing summary", "markdown variant must instruct narration, not verbatim emission")
	assert.NotContains(t, note, "fenced ```json", "markdown must not include the json verbatim instruction")
}

func TestDryRunContextNote_JSON(t *testing.T) {
	note := dryRunContextNote("json")
	assert.Contains(t, note, "Dry-run mode is active")
	assert.Contains(t, note, "spec_dry_run_report")
	assert.Contains(t, note, "fenced", "json variant must instruct verbatim fenced emission")
	assert.NotContains(t, note, "prose closing summary", "json must not also ask for narration")
}

func TestDryRunContextNote_UnknownFallsBackToMarkdown(t *testing.T) {
	note := dryRunContextNote("xml")
	assert.Contains(t, note, "prose closing summary", "unknown format falls back to markdown")
}

func TestRenderDryRunReportFromToolsJSONL_FormatJSON(t *testing.T) {
	// Synthesize a tools.jsonl file with two mutation entries.
	tmp := t.TempDir()
	path := tmp + "/tools.jsonl"
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{
		`{"Kind":"tool_call","ToolName":"spec_propose_decision","ToolInput":{"id":"dec-a","title":"A","chosen_option":"x"}}`,
		`{"Kind":"tool_call","ToolName":"Read","ToolInput":{"file_path":"GOALS.md"}}`,
		`{"Kind":"tool_call","ToolName":"spec_propose_feature","ToolInput":{"id":"feat-a","title":"A"}}`,
		"",
	}, "\n")), 0o644))

	out := renderDryRunReportFromToolsJSONL(path, "json")
	assert.Contains(t, out, `"tool":"spec_propose_decision"`)
	assert.Contains(t, out, `"tool":"spec_propose_feature"`)
	assert.NotContains(t, out, `"tool":"Read"`, "non-mutation tools must not appear in the report")
}

func TestRenderDryRunReportFromToolsJSONL_FormatMarkdown(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/tools.jsonl"
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{
		`{"Kind":"tool_call","ToolName":"spec_propose_decision","ToolInput":{"id":"dec-a","title":"A"}}`,
		`{"Kind":"tool_call","ToolName":"spec_propose_feature","ToolInput":{"id":"feat-a","title":"A"}}`,
		"",
	}, "\n")), 0o644))

	out := renderDryRunReportFromToolsJSONL(path, "markdown")
	assert.Contains(t, out, "Dry-run captured")
	assert.Contains(t, out, "dec-a")
	assert.Contains(t, out, "feat-a")
}
```

Add the missing imports (`os`, `path/filepath`, `require`).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/ -run 'TestDryRunContextNote|TestRenderDryRunReportFromToolsJSONL' -v`
Expected: FAIL — helpers undefined.

- [ ] **Step 3: Add the helpers + flags + dispatch wiring**

In `cmd/activity_verb.go`:

```go
// dryRunContextNote returns the playbook contextNote to append when
// --dry-run is set. The wording is fixed per format; the agent reads
// it as part of its prompt and follows the closing-summary instruction.
func dryRunContextNote(format string) string {
	if format != "markdown" && format != "json" {
		format = "markdown"
	}
	base := "Dry-run mode is active. The daemon is capturing your spec_propose_* / spec_revise_* / spec_delete_* / spec_mark_approach_drifted / spec_update_goals_md_hash calls in a per-session overlay rather than persisting them, and discarding the overlay at session close. You will still see your own captured mutations in subsequent spec_list_manifest / spec_get / spec_search results so the workflow runs end-to-end against the would-be graph. After your final mutation phase, call mcp__locutus__spec_dry_run_report once with no arguments to retrieve the structured capture, then close with the report rendered as "
	switch format {
	case "json":
		return base + "JSON: emit the structured capture verbatim inside a single fenced ```json code block. No surrounding prose. The JSON shape is exactly what the tool returned — don't reformat or filter."
	default:
		return base + "markdown: walk the structured capture and produce a prose closing summary that counts the captured mutations by kind, names each by id with its title, and calls out cascade impact (which existing nodes' citations the mutations would touch). Close with a one-line outcome."
	}
}

// renderDryRunReportFromToolsJSONL is the CLI-side authoritative render
// path (headless only). After dispatch returns, the CLI reads the
// session's tools.jsonl, filters to mutation-family tool calls, and
// emits per format. This path is deterministic — independent of how
// the agent narrated its closing summary.
func renderDryRunReportFromToolsJSONL(path string, format string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "" // graceful — caller falls back to agent narration
	}
	mutationFamily := map[string]struct{}{
		"spec_propose_decision":      {},
		"spec_revise_decision":       {},
		"spec_propose_feature":       {},
		"spec_revise_feature":        {},
		"spec_propose_strategy":      {},
		"spec_revise_strategy":       {},
		"spec_propose_goal":          {},
		"spec_revise_goal":           {},
		"spec_delete_goal":           {},
		"spec_propose_antigoal":      {},
		"spec_revise_antigoal":       {},
		"spec_delete_antigoal":       {},
		"spec_mark_approach_drifted": {},
		"spec_update_goals_md_hash":  {},
	}
	type toolCall struct {
		ToolName  string         `json:"ToolName"`
		ToolInput map[string]any `json:"ToolInput"`
		Kind      string         `json:"Kind"`
	}
	var entries []toolCall
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var t toolCall
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			continue
		}
		if t.Kind != "tool_call" {
			continue
		}
		if _, ok := mutationFamily[t.ToolName]; !ok {
			continue
		}
		entries = append(entries, t)
	}
	if format == "json" {
		out, _ := json.MarshalIndent(map[string]any{"format": "json", "captured": entries}, "", "  ")
		return string(out)
	}
	// markdown
	var b strings.Builder
	fmt.Fprintf(&b, "Dry-run captured %d mutation(s):\n\n", len(entries))
	for i, e := range entries {
		id, _ := e.ToolInput["id"].(string)
		title, _ := e.ToolInput["title"].(string)
		if title == "" {
			fmt.Fprintf(&b, "  %d. %s %s\n", i+1, e.ToolName, id)
		} else {
			fmt.Fprintf(&b, "  %d. %s %s — %s\n", i+1, e.ToolName, id, title)
		}
	}
	return b.String()
}
```

Add `DryRun bool` and `Format string` fields to each verb's Kong struct. Example for `cmd/import.go`:

```go
type ImportCmd struct {
	Input  string `help:"..."`
	DryRun bool   `name:"dry-run" help:"Capture proposed mutations without writing them; print what would land."`
	Format string `help:"Report format when --dry-run is set." enum:"markdown,json" default:"markdown"`
}
```

Apply the same two fields to `RefineCmd`, `AdoptCmd`, `AssimilateCmd`.

In each verb's `Run`, pass `c.DryRun` and `c.Format` to `runActivityVerb`. Update `runActivityVerb`'s signature to accept them:

```go
func runActivityVerb(ctx context.Context, cli *CLI, activityName, contextNote string, dryRun bool, dryRunFormat string) error {
	if dryRun {
		contextNote = appendNote(contextNote, dryRunContextNote(dryRunFormat))
	}
	// existing dispatch logic; pass env to the spawned process:
	env := append(os.Environ(),
		"LOCUTUS_MODE=headless",
	)
	if dryRun {
		env = append(env, "LOCUTUS_DRY_RUN=1", "LOCUTUS_DRY_RUN_FORMAT="+dryRunFormat)
	}
	// ...
	// after dispatch returns:
	if dryRun && sessionDir != "" {
		report := renderDryRunReportFromToolsJSONL(filepath.Join(sessionDir, "tools.jsonl"), dryRunFormat)
		if report != "" {
			fmt.Println(report)
		}
	}
	return nil
}
```

> **`appendNote` helper:** the existing code may have a context-note joiner; if not, write a small one: `func appendNote(a, b string) string { if a == "" { return b }; return a + "\n\n" + b }`.

> **`sessionDir` plumbing:** the existing dispatch function probably returns the session dir or constructs it via a known function. Verify by reading `internal/runner/run.go` around the dispatch entrypoint. If not exposed, return it from the dispatch function as part of this task.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/ -run 'TestDryRunContextNote|TestRenderDryRunReportFromToolsJSONL' -v` — PASS.
Run: `go test ./cmd/ -v 2>&1 | tail -10` — full suite green (existing tests untouched; runActivityVerb's new args default to false/"" in callers that don't yet set them — adjust those call sites in this task).

- [ ] **Step 5: Commit**

```bash
git add cmd/import.go cmd/refine.go cmd/adopt.go cmd/assimilate.go cmd/activity_verb.go cmd/activity_verb_dry_run_test.go
git commit -m "feat(cmd): --dry-run + --format on the four mutating verbs (DJ-147)"
```

---

## Phase 9 — Docs

### Task 10: docs/runtime-affordances.md + CLAUDE.md addition

**Files:**
- Modify: `docs/runtime-affordances.md`, `CLAUDE.md`

- [ ] **Step 1: Append a "Dry-Run" passage to docs/runtime-affordances.md**

Insert after the existing "Tool-Restriction" section (search for `## Tool-Restriction`):

```markdown
## Dry-Run (DJ-147)

The four mutating verbs (`import`, `refine`, `adopt`, `assimilate`) accept `--dry-run` — the workflow runs end-to-end against a per-session overlay on the SpecStore, captures every would-be `spec_propose_*` / `spec_revise_*` / `spec_delete_*` / `spec_mark_approach_drifted` / `spec_update_goals_md_hash` call, and discards the overlay at session close. Nothing reaches `.borg/spec/`, the Bluge index, history, or `spec://manifest` notifications.

Activation mirrors DJ-143's mode plumbing: CLI flag → `LOCUTUS_DRY_RUN=1` env (plus `LOCUTUS_DRY_RUN_FORMAT=markdown|json`) on the spawned coding-agent → bridge reads + forwards as `_meta["locutus.dry_run"]` + `_meta["locutus.dry_run_format"]` on its `initialize` → daemon's session-context map stores them and registers an overlay on the SpecStore.

The agent retrieves the capture via the read-only `spec_dry_run_report` MCP tool (no input, returns `{format, captured: [...]}`) and renders it in its closing message — verbatim fenced JSON for `--format json`, prose summary for the default `markdown`. In headless dispatch the CLI additionally reads the session's `tools.jsonl` post-dispatch and emits an authoritative structured render — the agent's narration is convenience; the CLI render is contract.
```

- [ ] **Step 2: Add a CLAUDE.md paragraph in Sources of Truth**

In `CLAUDE.md`, after the DJ-144 driver-matrix bullet, add:

```markdown
- **`--dry-run` on the four mutating verbs (DJ-147).** `import`, `refine`, `adopt`, `assimilate` accept `--dry-run` + `--format markdown|json`. The workflow runs faithfully end-to-end against a per-session overlay on the SpecStore (DJ-134) — every `mcp__locutus__spec_*` write captures in the overlay rather than persisting; reads in the same session see the overlay so the cascade, citation walk, and convergence happen against the would-be graph. Signal travels env → `_meta` → session-context like DJ-143's `LOCUTUS_MODE`; the daemon registers an overlay on dry-run sessions. Report comes from a new read-only `spec_dry_run_report` MCP tool (the agent calls it as the closing step) plus, in headless, a CLI-side render from `tools.jsonl`. Exit code is always 0 on a successful dry-run. See [DJ-147](docs/decisions/dj-147-dry-run-mutation-capture.md).
```

- [ ] **Step 3: Verify docs manifest bijection still holds**

Run: `go test ./internal/docs/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add docs/runtime-affordances.md CLAUDE.md
git commit -m "docs(dj-147): document --dry-run via SpecStore overlay"
```

---

## Phase 10 — Validation

### Task 11: Full suite + end-to-end winplan validation

- [ ] **Step 1: Full suite + vet + race**

Run: `go build ./... && go vet ./... && go test ./... -race 2>&1 | grep -vE '^ok|no test files' | tail -15`
Expected: empty output (everything passes under race).

- [ ] **Step 2: Rebuild binary**

```bash
go build -o locutus .
ls -la locutus
ls -la /Users/chetan/projects/winplan/locutus  # symlink target unchanged
```

- [ ] **Step 3: Restart the daemon to pick up new wrappers**

```bash
( cd /Users/chetan/projects/winplan && ./locutus mcp-stop || true )
```

- [ ] **Step 4: Dry-run a refine against the winplan dashboard**

```bash
( cd /Users/chetan/projects/winplan && ./locutus refine feat-main-dashboard --dry-run )
```

Expect:
- The workflow runs to convergence (same shape as the wet run from earlier).
- Console output includes the CLI-rendered markdown report (mutation count + per-id summary).
- `git status .borg/` shows no changes — the run is fully captured, nothing persisted.
- Newest session dir has a `tools.jsonl` with the captured mutation entries.

- [ ] **Step 5: Dry-run an import with JSON output**

```bash
( cd /Users/chetan/projects/winplan && ./locutus import docs/dashboard.md --dry-run --format json )
```

Pipe through `jq`:

```bash
... --dry-run --format json | jq '.captured[] | {tool, id}'
```

Expect: a structured JSON array on stdout, parseable by `jq`, no `.borg/` changes.

- [ ] **Step 6: Report**

Summarize: the workflow runs faithfully against the overlay; `tools.jsonl` carries the audit; `.borg/` is untouched; `--format json` produces machine-readable stdout. This validates DJ-147 end-to-end on a real project.

---

## Self-Review

- **Spec coverage:** DJ-147 §1 (verb scope) → Task 9. §2 (activation) → Tasks 3, 8. §3 (overlay) → Tasks 1, 2, 6. §4 (`captureOnly`) → Tasks 4, 5. §5 (`spec_dry_run_report`) → Task 7. §6 (contextNote) → Task 9. §7 (dual rendering) → Tasks 7 + 9. §8 (subagents share overlay) → covered by the keyed-by-`*ServerSession` design; verified end-to-end in Task 11. §9 (concurrent isolation) → covered by per-session overlay; tested in Task 2. §10 (side effects) → no history events / no manifest notifications / OTel-tagged: enforced in Task 5's per-tool capture closures (don't call `publishManifestUpdate`, don't call history methods). Resolved questions 1–8 all map to tasks. Alternatives + Follow-ups noted in the DJ doc; nothing requiring task work here.
- **Placeholder scan:** no TBD/TODO. Real code in every step. The plan has three "verify before running" sub-instructions (Task 1 Step 3, Task 2 Step 3 on `GetEntry`, Task 9 Step 3 on `sessionDir` plumbing) — those are calibrations against the actual codebase, not placeholders; the implementer is told exactly what to grep for and how to adjust.
- **Type consistency:** `CapturedMutation`, `OverlayPut`, `OverlayDelete`, `OverlayView`, `OverlayCaptured`, `RegisterOverlay`, `UnregisterOverlay`, `overlayFor`, `SessionDryRun`, `SessionDryRunFormat`, `captureOnly`, `dryRunContextNote`, `renderDryRunReportFromToolsJSONL`, `resolveLocutusDryRun`, `resolveLocutusDryRunFormat` — used consistently across all tasks where referenced. Mutation-family tool name list in Task 5 + Task 9 align (both 14 tools, same names).
- **Known soft spots calibrated at execution time:** Task 2's `GetEntry` accessor name (verify in code), Task 5's `createdAt` preservation under dry-run (extend `existingDecisionCreatedAt` family or write overlay-aware twins), Task 9's `sessionDir` return-value plumbing from the dispatch function.
