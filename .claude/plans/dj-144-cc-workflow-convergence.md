# DJ-144 — Claude Code Workflow Convergence + Per-Runtime Version Floor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Claude Code's convergence driver with a dynamic-workflow script (in-runtime parallel orchestration + loop) for the four convergent activities, take Claude Code off the harness `OuterLoopRunner`, and add a declared per-runtime minimum-version registry (warn-and-proceed) that generalizes the workflow v2.1.154 floor.

**Architecture:** Locutus does not call LLMs; it exposes the spec graph over MCP and dispatches activity playbooks to coding-agent runtimes over ACP (DJ-135). DJ-144 changes only the *Claude Code* convergence driver: instead of the harness re-dispatching a one-iteration playbook and reading a verdict line (DJ-140), Claude Code runs a dynamic workflow that loops in-runtime and reads the scout's verdict itself; Codex/Gemini are untouched (DJ-142 drivers stand). A separate, independently-shippable mechanism — a `runtimes-default.yaml` + `.borg/runtimes.yaml` version-floor registry, read from `ClientInfo.version` at MCP `initialize` — warns when a runtime is below the floor a feature needs.

**Tech Stack:** Go 1.x; `gopkg.in/yaml.v3`; `github.com/modelcontextprotocol/go-sdk` v1.6.1; `github.com/coder/acp-go-sdk`; `github.com/stretchr/testify/assert`; embedded scaffold (`//go:embed`). Spec doc: [docs/decisions/dj-144-cc-workflow-convergence.md](../../docs/decisions/dj-144-cc-workflow-convergence.md).

---

## Required reading before starting

- **[docs/decisions/dj-144-cc-workflow-convergence.md](../../docs/decisions/dj-144-cc-workflow-convergence.md)** — the governing spec. Read all 9 Decision points, the Open verification items, and Consequences.
- **[docs/agent-conventions.md](../../docs/agent-conventions.md)** — MANDATORY before editing or creating any file under `internal/scaffold/agents/` or `internal/scaffold/plans/` ([[feedback-agent-conventions-checklist-first]]). Phase 3 touches playbooks; walk this as a checklist first.
- **[docs/decisions/dj-142-idiomatic-convergence-drivers.md](../../docs/decisions/dj-142-idiomatic-convergence-drivers.md)** and **[dj-140](../../docs/decisions/dj-140-headless-convergence-unification.md)** — the drivers DJ-144 amends. Critical: Codex/Gemini behavior must NOT change.
- **[docs/decisions/dj-143-per-runtime-tool-policy.md](../../docs/decisions/dj-143-per-runtime-tool-policy.md)** — `spec_loop_*` stays denied to Claude Code; this plan must keep it that way.

## Phase ordering & independence

- **Phase 0 (spikes)** gates **Phase 3** only. It does not gate Phases 1, 2, or 4.
- **Phase 1 (version registry)** is fully independent and independently shippable — it has no dependency on workflows. It MAY be built and merged first.
- **Phase 2 (Option A dispatch)** is independent Go work.
- **Phase 3 (workflow authoring + publishing)** depends on Phase 0 findings (saved-workflow file format, headless reachability) and on Phase 2 (single-dispatch path).
- **Phase 4 (teardown + resolution guards)** depends on Phase 3 (don't delete the `/goal` wrapper until its replacement exists).
- **Phase 5 (docs)** last.

**Commit discipline:** commit after every green test step. Conventional prefixes (`feat:`/`fix:`/`refactor:`/`test:`/`docs:`) per [[feedback-commit-conventions]].

---

## Phase 0 — Verification spikes (gating for Phase 3)

These resolve the three Open verification items in the spec. Each task produces a written finding; no production code. The findings determine concrete choices in Phases 1 and 3. **Do not fabricate the dynamic-workflow JS API — Phase 3's script authoring consumes these findings.**

Record all findings in a single scratch file `.claude/plans/dj-144-spike-findings.md` (gitignored or committed as `docs:` — operator's choice; it is a working note, not a shipped artifact).

### Task 0.1: Confirm `claude-agent-acp` triggers dynamic workflows headlessly + version floor

**Files:**
- Create: `.claude/plans/dj-144-spike-findings.md` (working note)

- [ ] **Step 1: Determine the spawned Claude Code version**

Run the ACP server binary's version and the underlying Claude Code CLI version:

```bash
claude-agent-acp --version 2>&1 || true
claude --version 2>&1 || true
```

Expected: a version string. Record it. The workflow floor is **v2.1.154** — note whether the installed version meets it.

- [ ] **Step 2: Dispatch a trivial keyword-trigger workflow headlessly and inspect the session log**

Use an existing throwaway project (or `locutus init` a temp dir) and dispatch any convergent activity, then inspect the recorded ACP event stream for evidence a workflow ran (workflow runs surface coarser events — internal subagent calls don't appear as ACP `tool_call` events; only the final answer returns):

```bash
# In a temp project with locutus initialized:
LOCUTUS_MODE=headless locutus refine 2>&1 | tee /tmp/dj144-probe.log
ls -t .locutus/sessions/*/*/*/ | head -1   # newest session dir
```

Inspect `events.jsonl` in the newest session dir. Record: did the run trigger a workflow (coarse event stream, internal subagents absent), or did it run as a normal multi-tool agent session?

- [ ] **Step 3: Record the finding**

Write to `.claude/plans/dj-144-spike-findings.md` under a heading `## 0.1 Headless workflow reachability`:
- Installed `claude-agent-acp` / `claude` version vs. v2.1.154 floor.
- Whether headless dispatch triggered a workflow (yes/no/inconclusive).
- If NO: the headless Claude Code path will fall back to a single non-looping pass — Phase 3's keyword playbook may not be enough; flag for the operator. If inconclusive, note what further check is needed.

### Task 0.2: Determine whether workflow-spawned subagent tool calls route through the ACP `Policy`

**Files:**
- Modify (read-only inspection): `internal/dispatch/acp/client.go`, `internal/dispatch/policy/policy.go`
- Append to: `.claude/plans/dj-144-spike-findings.md`

- [ ] **Step 1: Inspect the permission path**

Read `internal/dispatch/acp/client.go`'s `RequestPermission` implementation and confirm it is the only permission entry point. From the Task 0.1 probe run, grep the session's `events.jsonl` and `tools.jsonl` for `mcp__locutus__spec_*` calls made *inside* the workflow:

```bash
SESS=$(ls -dt .locutus/sessions/*/*/* | head -1)
grep -c "request_permission\|RequestPermission" "$SESS/events.jsonl" || true
grep -o "mcp__locutus__spec_[a-z_]*" "$SESS/tools.jsonl" | sort -u
```

- [ ] **Step 2: Record the finding**

Under `## 0.2 Workflow-subagent permission routing`:
- Did `spec_*` writes made from within the workflow appear in the daemon's `tools.jsonl` (proving they reached the MCP daemon)? 
- Did permission requests route through the ACP client (count > 0), or did the workflow's subagents follow Claude Code's own permission rules out-of-band (count 0 but writes succeeded)?
- **Decision recorded:** if ACP-routed → existing `policy.AllowOncePolicy`/Guardian covers them (no settings allowlist needed). If out-of-band → Phase 3 must publish a `settings.json` allowlist (`mcp__locutus__*`, `Bash`, `Read`, `Task`). Note which.

### Task 0.3: Determine what `ClientInfo.version` carries + the saved-workflow file format

**Files:**
- Append to: `.claude/plans/dj-144-spike-findings.md`

- [ ] **Step 1: Capture `ClientInfo.version` as seen by the daemon**

Add a temporary `slog.Info` at `internal/mcp/session_context.go:118-120` logging `params.ClientInfo.Name` and `params.ClientInfo.Version`, rebuild, dispatch once, and read the daemon log. (Revert the temporary log after.) Record the exact `Version` string Claude Code sends.

- [ ] **Step 2: Decide the version-floor detection point**

Under `## 0.3 ClientInfo.version semantics`:
- Is `ClientInfo.version` the **Claude Code version** (e.g. `2.1.x`) or the **MCP client-library/SDK version**?
- **Decision recorded:** if it is the Claude Code version → Phase 1 wires the floor check in `newInitializedHandler` (covers interactive + headless). If it is the SDK version → Phase 1 falls back to a `--version` subprocess probe at dispatch in `internal/runner` (headless-only). Phase 1 Task 1.4 reads this decision.

- [ ] **Step 3: Determine the saved-workflow file format**

From the Claude Code docs (https://code.claude.com/docs/en/workflows) and a local inspection (`ls ~/.claude/workflows/ .claude/workflows/ 2>/dev/null`; create one interactively if needed), record under `## 0.3b Saved-workflow file format`:
- The directory (`.claude/workflows/` confirmed?) and file extension (`.js`? markdown-with-frontmatter? other?).
- The orchestration API surface the script uses to spawn subagents, run them in parallel, and loop (function/object names, how a subagent is invoked by id, how parallel fan-out and barriers are expressed).
- Whether a saved workflow is surfaced as a `/<name>` slash command and how its name is derived from the filename.
- **This is the input Phase 3 Task 3.2 needs to author the actual scripts. If the API cannot be determined from docs, note that Phase 3 script authoring must be done interactively with the operator.**

---

## Phase 1 — Per-runtime minimum-version registry (independent)

A new `runtimes-default.yaml` + `.borg/runtimes.yaml` registry keyed by runtime with `min_version`, a pure semver-ish comparison helper, and a warn-and-proceed check at the version-detection point chosen in Task 0.3. Mirrors the existing `internal/activity` agents.yaml loader pattern exactly.

### Task 1.1: Embedded `runtimes-default.yaml` + parse/merge loader

**Files:**
- Create: `internal/runtimepolicy/runtimes-default.yaml`
- Create: `internal/runtimepolicy/registry.go`
- Create: `internal/runtimepolicy/registry_test.go`

> Package name `runtimepolicy` keeps version floors separate from the `activity` registry (activities are per-activity; floors are per-runtime). Follows the `internal/activity` two-file (`registry.go` + embedded yaml) shape.

- [ ] **Step 1: Write the embedded default YAML**

Create `internal/runtimepolicy/runtimes-default.yaml`:

```yaml
# Default per-runtime minimum-version registry shipped with Locutus (DJ-144 §9).
#
# Keyed by runtime id (matching internal/dispatch/acp/registry.go AgentSpawns
# keys: claude-code, codex, gemini). min_version is the lowest runtime version
# Locutus relies on for a version-gated feature; below it, Locutus logs a
# warn-and-proceed message (it never blocks dispatch — DJ-144 §9).
#
# Override per-project at .borg/runtimes.yaml. An empty/absent min_version
# means "no floor declared" (no warning ever fires for that runtime).
#
# claude-code's floor is the dynamic-workflows requirement (v2.1.154).

runtimes:
  claude-code:
    min_version: "2.1.154"
  codex:
    min_version: ""
  gemini:
    min_version: ""
```

- [ ] **Step 2: Write the failing test for parse + merge**

Create `internal/runtimepolicy/registry_test.go`:

```go
package runtimepolicy

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry_EmbeddedDefaults(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	floor, ok := reg.MinVersion("claude-code")
	assert.True(t, ok)
	assert.Equal(t, "2.1.154", floor)
	// Runtimes with empty floors report ok=false (no floor declared).
	_, ok = reg.MinVersion("codex")
	assert.False(t, ok)
}

func TestNewRegistry_ProjectOverride(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.WriteFile(".borg/runtimes.yaml", []byte(
		"runtimes:\n  claude-code:\n    min_version: \"2.2.0\"\n"), 0o644))
	reg, err := NewRegistry(fsys)
	require.NoError(t, err)
	floor, ok := reg.MinVersion("claude-code")
	assert.True(t, ok)
	assert.Equal(t, "2.2.0", floor, "project override replaces the default floor")
}

func TestNewRegistry_UnknownRuntimeRejected(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.WriteFile(".borg/runtimes.yaml", []byte(
		"runtimes:\n  nonsense:\n    min_version: \"1.0.0\"\n"), 0o644))
	_, err := NewRegistry(fsys)
	assert.Error(t, err, "a floor for a runtime absent from AgentSpawns is a config error")
}
```

> Confirm the MemFS constructor name first: `grep -rn "func New.*FS" internal/specio/`. Use the exact constructor (the snippet assumes `specio.NewMemFS()`; adjust if it differs).

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/runtimepolicy/ -run TestNewRegistry -v`
Expected: FAIL — `NewRegistry`/`MinVersion` undefined.

- [ ] **Step 4: Implement the registry**

Create `internal/runtimepolicy/registry.go`:

```go
// Package runtimepolicy implements DJ-144 §9's per-runtime minimum-
// version registry: a map from runtime id (claude-code / codex /
// gemini) to the lowest runtime version Locutus relies on for a
// version-gated feature. Below the floor, callers log a warn-and-
// proceed message; the registry never blocks dispatch.
//
// Lifecycle mirrors internal/activity.Registry: NewRegistry loads the
// embedded default, then layers .borg/runtimes.yaml from the project
// root on top (per-runtime replacement). Read-only after construction.
package runtimepolicy

import (
	"errors"
	"fmt"
	"io/fs"

	_ "embed"

	"github.com/glorious-beard/locutus/internal/dispatch/acp"
	"github.com/glorious-beard/locutus/internal/specio"
	"gopkg.in/yaml.v3"
)

//go:embed runtimes-default.yaml
var defaultRuntimesYAML []byte

const projectOverridePath = ".borg/runtimes.yaml"

type runtimesYAMLFile struct {
	Runtimes map[string]runtimeEntry `yaml:"runtimes"`
}

type runtimeEntry struct {
	MinVersion string `yaml:"min_version"`
}

// Registry is the merged runtime → min-version table.
type Registry struct {
	floors map[string]string // runtime → min_version (non-empty entries only)
}

func parseRuntimesYAML(data []byte) (map[string]string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("runtimes.yaml: empty input")
	}
	var file runtimesYAMLFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("runtimes.yaml: parse: %w", err)
	}
	out := map[string]string{}
	for name, raw := range file.Runtimes {
		out[name] = raw.MinVersion
	}
	return out, nil
}

// NewRegistry layers .borg/runtimes.yaml over the embedded default.
// A nil fsys loads only the defaults.
func NewRegistry(fsys specio.FS) (*Registry, error) {
	merged, err := parseRuntimesYAML(defaultRuntimesYAML)
	if err != nil {
		return nil, fmt.Errorf("runtimepolicy: parse embedded default: %w", err)
	}
	if fsys != nil {
		data, err := fsys.ReadFile(projectOverridePath)
		switch {
		case err == nil:
			override, perr := parseRuntimesYAML(data)
			if perr != nil {
				return nil, fmt.Errorf("runtimepolicy: %s: %w", projectOverridePath, perr)
			}
			for name, v := range override {
				merged[name] = v
			}
		case errors.Is(err, fs.ErrNotExist):
			// no override; defaults stand
		default:
			return nil, fmt.Errorf("runtimepolicy: read %s: %w", projectOverridePath, err)
		}
	}
	// Validate: every keyed runtime must be a real spawn target.
	floors := map[string]string{}
	for name, v := range merged {
		if _, ok := acp.AgentSpawns[name]; !ok {
			return nil, fmt.Errorf("runtimepolicy: runtime %q is not registered in acp.AgentSpawns", name)
		}
		if v != "" {
			floors[name] = v
		}
	}
	return &Registry{floors: floors}, nil
}

// MinVersion returns the declared floor for a runtime and whether one
// is declared (a non-empty min_version). Runtimes with empty/absent
// floors return ("", false) — no warning should fire for them.
func (r *Registry) MinVersion(runtime string) (string, bool) {
	v, ok := r.floors[runtime]
	return v, ok
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/runtimepolicy/ -run TestNewRegistry -v`
Expected: PASS (all three).

- [ ] **Step 6: Commit**

```bash
git add internal/runtimepolicy/
git commit -m "feat: add per-runtime minimum-version registry (DJ-144 §9)"
```

### Task 1.2: Version-comparison helper

**Files:**
- Create: `internal/runtimepolicy/version.go`
- Create: `internal/runtimepolicy/version_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/runtimepolicy/version_test.go`:

```go
package runtimepolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBelowFloor(t *testing.T) {
	cases := []struct {
		name     string
		detected string
		floor    string
		below    bool
		ok       bool // false when comparison is indeterminate (fail-open)
	}{
		{"clearly below", "2.1.100", "2.1.154", true, true},
		{"clearly above", "2.2.0", "2.1.154", false, true},
		{"equal", "2.1.154", "2.1.154", false, true},
		{"patch below", "2.1.153", "2.1.154", true, true},
		{"prefixed v", "v2.1.200", "2.1.154", false, true},
		{"trailing label", "2.1.154-beta.1", "2.1.154", false, true},
		{"unparseable detected", "weird-build", "2.1.154", false, false},
		{"empty detected", "", "2.1.154", false, false},
		{"empty floor", "2.1.0", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			below, ok := BelowFloor(c.detected, c.floor)
			assert.Equal(t, c.ok, ok, "ok mismatch")
			if c.ok {
				assert.Equal(t, c.below, below, "below mismatch")
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runtimepolicy/ -run TestBelowFloor -v`
Expected: FAIL — `BelowFloor` undefined.

- [ ] **Step 3: Implement `BelowFloor`**

Create `internal/runtimepolicy/version.go`:

```go
package runtimepolicy

import (
	"strconv"
	"strings"
)

// BelowFloor reports whether the detected version is strictly below
// the floor. The second return is false when the comparison is
// indeterminate (either string is empty or unparseable) — callers
// MUST fail open on ok==false: never warn or block on a version we
// could not read (DJ-144 §9).
//
// Parsing is deliberately lenient: a leading "v" is stripped, a
// trailing "-label"/"+build" suffix is dropped, and the dotted
// numeric prefix is compared field-by-field. Non-numeric or missing
// fields make the version unparseable (ok=false). This is not a full
// semver implementation — it is the minimum needed to compare
// coding-agent CLI version strings, and it errs toward fail-open.
func BelowFloor(detected, floor string) (below bool, ok bool) {
	d, dok := parseVersion(detected)
	f, fok := parseVersion(floor)
	if !dok || !fok {
		return false, false
	}
	return compare(d, f) < 0, true
}

func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return out, false
	}
	// Drop pre-release / build metadata: "2.1.154-beta+x" → "2.1.154".
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func compare(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/runtimepolicy/ -run TestBelowFloor -v`
Expected: PASS (all 9 sub-cases).

- [ ] **Step 5: Commit**

```bash
git add internal/runtimepolicy/version.go internal/runtimepolicy/version_test.go
git commit -m "feat: add fail-open version-floor comparison helper (DJ-144 §9)"
```

### Task 1.3: Warn formatter

**Files:**
- Create: `internal/runtimepolicy/warn.go`
- Create: `internal/runtimepolicy/warn_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/runtimepolicy/warn_test.go`:

```go
package runtimepolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckVersion_WarnsBelowFloor(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	msg, warn := reg.CheckVersion("claude-code", "2.1.100")
	assert.True(t, warn)
	assert.Contains(t, msg, "claude-code")
	assert.Contains(t, msg, "2.1.100")
	assert.Contains(t, msg, "2.1.154")
}

func TestCheckVersion_NoWarnAtOrAboveFloor(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	_, warn := reg.CheckVersion("claude-code", "2.1.154")
	assert.False(t, warn)
	_, warn = reg.CheckVersion("claude-code", "2.3.0")
	assert.False(t, warn)
}

func TestCheckVersion_FailOpen(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	// No floor declared for codex → never warn.
	_, warn := reg.CheckVersion("codex", "0.0.1")
	assert.False(t, warn)
	// Unparseable detected version → fail open, never warn.
	_, warn = reg.CheckVersion("claude-code", "weird-build")
	assert.False(t, warn)
	// Empty detected version (runtime didn't report) → fail open.
	_, warn = reg.CheckVersion("claude-code", "")
	assert.False(t, warn)
	// Unknown runtime → no floor → never warn.
	_, warn = reg.CheckVersion("unknown", "1.0.0")
	assert.False(t, warn)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runtimepolicy/ -run TestCheckVersion -v`
Expected: FAIL — `CheckVersion` undefined.

- [ ] **Step 3: Implement `CheckVersion`**

Create `internal/runtimepolicy/warn.go`:

```go
package runtimepolicy

import "fmt"

// CheckVersion compares a runtime's detected version against its
// declared floor. It returns (message, warn): warn is true ONLY when
// a floor is declared, the detected version is parseable, and it is
// strictly below the floor. In every other case — no floor declared,
// unparseable/empty detected version, unknown runtime — warn is false
// (fail-open, DJ-144 §9). The message is empty when warn is false.
//
// CheckVersion never blocks; the caller logs the message and proceeds.
func (r *Registry) CheckVersion(runtime, detected string) (message string, warn bool) {
	floor, ok := r.MinVersion(runtime)
	if !ok {
		return "", false
	}
	below, ok := BelowFloor(detected, floor)
	if !ok || !below {
		return "", false
	}
	return fmt.Sprintf(
		"runtime %q version %q is below the minimum %q Locutus relies on; "+
			"version-gated features (e.g. Claude Code dynamic workflows) may silently "+
			"degrade — see docs/runtime-affordances.md",
		runtime, detected, floor,
	), true
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/runtimepolicy/ -run TestCheckVersion -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtimepolicy/warn.go internal/runtimepolicy/warn_test.go
git commit -m "feat: add warn-and-proceed version-floor check (DJ-144 §9)"
```

### Task 1.4: Wire the check at the detection point chosen in Task 0.3

**Files (primary path — `ClientInfo.version` is the Claude Code version):**
- Modify: `internal/mcp/session_context.go:108-129` (`newInitializedHandler`)
- Modify/create test: `internal/mcp/session_context_version_test.go`

> **Read `.claude/plans/dj-144-spike-findings.md § 0.3` first.** If `ClientInfo.version` is the Claude Code version, implement the primary path below (daemon-side, covers both modes). If it is the SDK version, implement the contingency at the end of this task instead (dispatch-side `--version` probe).

- [ ] **Step 1: Write the failing test (primary path)**

Create `internal/mcp/session_context_version_test.go`:

```go
package mcp

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckRuntimeVersion_WarnsBelowFloor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	// checkRuntimeVersion loads the runtimepolicy registry and logs a
	// warning when the version is below floor. nil fsys → embedded defaults.
	checkRuntimeVersion(logger, nil, "claude-code", "2.1.100")
	assert.Contains(t, buf.String(), "below the minimum")
	assert.Contains(t, buf.String(), "2.1.154")
}

func TestCheckRuntimeVersion_SilentAtOrAboveFloor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	checkRuntimeVersion(logger, nil, "claude-code", "2.2.0")
	assert.Empty(t, buf.String())
}

func TestCheckRuntimeVersion_FailOpenOnUnparseable(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	checkRuntimeVersion(logger, nil, "claude-code", "weird")
	assert.Empty(t, buf.String())
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/mcp/ -run TestCheckRuntimeVersion -v`
Expected: FAIL — `checkRuntimeVersion` undefined.

- [ ] **Step 3: Implement `checkRuntimeVersion` and call it from `newInitializedHandler`**

Add to `internal/mcp/session_context.go` (new function + import `log/slog` and `internal/runtimepolicy`, plus `internal/specio` for the project fsys):

```go
// checkRuntimeVersion warns (never blocks) when a runtime connects
// below the minimum version Locutus relies on (DJ-144 §9). fsys is
// the project FS for the .borg/runtimes.yaml override (nil → embedded
// defaults). A nil logger is a no-op.
func checkRuntimeVersion(logger *slog.Logger, fsys specio.FS, runtime, version string) {
	if logger == nil {
		return
	}
	reg, err := runtimepolicy.NewRegistry(fsys)
	if err != nil {
		logger.Warn("runtimepolicy: registry load failed; skipping version check", "err", err)
		return
	}
	if msg, warn := reg.CheckVersion(runtime, version); warn {
		logger.Warn(msg)
	}
}
```

Then in `newInitializedHandler`, after `runtime = params.ClientInfo.Name`, capture the version and call the check. The handler needs a logger and the project fsys; thread them in from the server construction site (`internal/mcp/server.go` — `newInitializedHandler` is constructed there; pass the daemon's existing `*slog.Logger` and project fsys as closure args). Update the signature:

```go
func newInitializedHandler(logger *slog.Logger, fsys specio.FS) func(context.Context, *mcp.InitializedRequest) {
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
		if params.Meta != nil {
			if v, ok := params.Meta["locutus.mode"].(string); ok && strings.TrimSpace(v) != "" {
				mode = v
			}
		}
		storeSessionRuntime(req.Session, runtime, mode)
		checkRuntimeVersion(logger, fsys, runtime, version)
	}
}
```

> Find the call site: `grep -rn "newInitializedHandler" internal/mcp/`. Update it to pass the daemon logger and project fsys. If `server.go` lacks a handy `*slog.Logger`/fsys at that point, use `slog.Default()` and the daemon's project root fsys (check how `server.go` already holds the project root — `grep -n "projectRoot\|specio\|slog" internal/mcp/server.go`).

- [ ] **Step 4: Run the test + the existing session-context tests**

Run: `go test ./internal/mcp/ -run "TestCheckRuntimeVersion|TestSessionRuntime|Initialized" -v`
Expected: PASS, and no regression in existing DJ-143 session-context tests.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/session_context.go internal/mcp/session_context_version_test.go internal/mcp/server.go
git commit -m "feat: warn when a runtime connects below its version floor (DJ-144 §9)"
```

**Contingency (only if Task 0.3 found `ClientInfo.version` is the SDK version, not the CC version):** instead of the daemon-side check, add a `--version` probe in `internal/runner/run.go` before spawn. Run `exec.CommandContext(ctx, spawn.Cmd, "--version")` (for `claude-agent-acp`), parse stdout, and call `runtimepolicy.NewRegistry(fsys).CheckVersion(runtime, detected)` — write the warning to the `progress` writer. This covers headless only; document that interactive version-floor warnings are unavailable in that case. Write the test against a fake command runner.

### Task 1.5: Publish `.borg/runtimes.yaml` on init (parity with agents.yaml)

**Files:**
- Modify: wherever `agents-default.yaml` is copied to `.borg/` on `locutus init` / `update --reset`
- Test: alongside the existing agents.yaml scaffold test

- [ ] **Step 1: Locate the agents.yaml scaffold-copy site**

Run: `grep -rn "agents.yaml\|agents-default" internal/scaffold/ cmd/ | grep -v "_test.go"`
Identify where `.borg/agents.yaml` is written on init (so the operator has a copy to edit).

- [ ] **Step 2: Write the failing test**

Add a test asserting that after the init/scaffold routine runs against a MemFS, `.borg/runtimes.yaml` exists and contains `claude-code` + `min_version`. Mirror the existing agents.yaml scaffold assertion (find it: `grep -rn "agents.yaml" internal/scaffold/*_test.go cmd/*_test.go`).

- [ ] **Step 3: Run to verify it fails**

Run the relevant package test; expected FAIL (`.borg/runtimes.yaml` not written).

- [ ] **Step 4: Implement — embed and copy `runtimes-default.yaml` to `.borg/runtimes.yaml`**

Add the copy alongside the agents.yaml copy. Source the bytes from `runtimepolicy` (export a `DefaultYAML []byte` from the package, or re-embed in scaffold — prefer exporting from `runtimepolicy` to keep one source of truth). Add to `internal/runtimepolicy/registry.go`:

```go
// DefaultYAML is the embedded shipping default, exported so the init
// scaffold can write a project-editable copy to .borg/runtimes.yaml.
func DefaultYAML() []byte { return defaultRuntimesYAML }
```

- [ ] **Step 5: Run to verify it passes**

Run the package test; expected PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: scaffold .borg/runtimes.yaml on init (DJ-144 §9)"
```

---

## Phase 2 — Option A dispatch: take Claude Code off the outer loop + inject the cap

Claude Code becomes a single ACP dispatch; the workflow owns the loop. Codex/Gemini keep `OuterLoopRunner`. The `max_iterations` cap is templated into the playbook body so the workflow can enforce it.

### Task 2.1: `dispatchUsesOuterLoop` returns false for claude-code; `DispatchActivity` branches

**Files:**
- Modify: `internal/runner/run.go:114-158` (`DispatchActivity`, `dispatchUsesOuterLoop`)
- Test: `internal/runner/run_dispatch_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/runner/run_dispatch_test.go`:

```go
package runner

import "testing"

func TestDispatchUsesOuterLoop(t *testing.T) {
	if dispatchUsesOuterLoop("claude-code") {
		t.Fatal("claude-code must NOT use the harness outer loop (DJ-144): the dynamic workflow owns the loop")
	}
	for _, rt := range []string{"codex", "gemini"} {
		if !dispatchUsesOuterLoop(rt) {
			t.Fatalf("%s must keep the harness outer loop (DJ-142 driver unchanged)", rt)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/runner/ -run TestDispatchUsesOuterLoop -v`
Expected: FAIL — claude-code currently returns true.

- [ ] **Step 3: Implement the branch**

In `internal/runner/run.go`, change `dispatchUsesOuterLoop`:

```go
// dispatchUsesOuterLoop reports whether the runtime's headless
// dispatch is driven by the Locutus harness outer loop.
//
// Per DJ-142 this is true for Codex and Gemini (the harness re-
// dispatches a one-iteration playbook and reads the verdict line).
// Per DJ-144 it is FALSE for Claude Code: Claude Code converges via an
// in-runtime dynamic workflow that owns its own loop and reads the
// scout's verdict itself, so a single ACP dispatch is correct — the
// harness must not re-dispatch on top of the workflow's loop (that
// would stack two convergence engines and double-count the cap).
func dispatchUsesOuterLoop(runtime string) bool {
	return runtime != "claude-code"
}
```

Then in `DispatchActivity`, replace the unconditional `runOuterLoopDispatch` call (line 148) with a branch:

```go
	if dispatchUsesOuterLoop(runtime) {
		return runOuterLoopDispatch(ctx, projectRoot, runtime, spawn, playbookBody, maxIterations, out, progress)
	}
	// Claude Code (DJ-144): single dispatch; the dynamic workflow loops
	// in-runtime. maxIterations is still validated by the caller and is
	// surfaced to the workflow via the playbook body (see cap injection
	// in cmd/activity_verb.go), not enforced by the harness here.
	return runOneIteration(ctx, projectRoot, runtime, spawn, playbookBody, out, progress)
```

Update the `DispatchActivity` doc comment block (lines 90-101) to describe the DJ-144 split instead of the DJ-140 "every runtime" claim.

- [ ] **Step 4: Run to verify it passes + no regression**

Run: `go test ./internal/runner/ -v`
Expected: PASS. The existing `loop_test.go` (OuterLoopRunner unit tests) still pass — `OuterLoopRunner` is unchanged, just not invoked for claude-code.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/run.go internal/runner/run_dispatch_test.go
git commit -m "feat: take Claude Code off the harness outer loop (DJ-144 Option A)"
```

### Task 2.2: Inject the `max_iterations` cap into the playbook body

**Files:**
- Modify: `cmd/activity_verb.go:46-60`
- Test: `cmd/activity_verb_test.go` (new or existing)

> The workflow script needs the cap as text. Use a token the playbook contains, replaced at dispatch with the registry value. Substitution is harmless when the token is absent, so it can run for all runtimes uniformly.

- [ ] **Step 1: Write the failing test**

Create `cmd/activity_verb_test.go` (or add to an existing one — `ls cmd/*_test.go`):

```go
package cmd

import (
	"strings"
	"testing"
)

func TestInjectMaxIterations(t *testing.T) {
	body := "Run the workflow with cap {{max_iterations}} iterations."
	got := injectMaxIterations(body, 12)
	if !strings.Contains(got, "cap 12 iterations") {
		t.Fatalf("token not substituted: %q", got)
	}
	if strings.Contains(got, "{{max_iterations}}") {
		t.Fatal("token left in body")
	}
	// No token → unchanged.
	plain := "no token here"
	if injectMaxIterations(plain, 12) != plain {
		t.Fatal("body without token must be unchanged")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/ -run TestInjectMaxIterations -v`
Expected: FAIL — `injectMaxIterations` undefined.

- [ ] **Step 3: Implement and call it**

Add to `cmd/activity_verb.go`:

```go
// injectMaxIterations substitutes the {{max_iterations}} token in a
// playbook body with the activity's registry cap (DJ-144 §6). Claude
// Code's dynamic-workflow playbooks contain the token so the in-
// runtime workflow can enforce the same cap the harness enforces for
// Codex/Gemini. Bodies without the token are returned unchanged, so
// this is safe to run for every runtime.
func injectMaxIterations(body string, cap int) string {
	return strings.ReplaceAll(body, "{{max_iterations}}", strconv.Itoa(cap))
}
```

Add imports `strconv` and (already present) `strings` is not imported in this file — check and add. In `runActivityVerb`, after building `prompt` (line 53), insert:

```go
	prompt = injectMaxIterations(prompt, act.MaxIterations)
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/ -run TestInjectMaxIterations -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/activity_verb.go cmd/activity_verb_test.go
git commit -m "feat: inject max_iterations cap into playbook body at dispatch (DJ-144 §6)"
```

---

## Phase 3 — Workflow + keyword playbook authoring & publishing (depends on Phase 0)

**Gate:** read `.claude/plans/dj-144-spike-findings.md` (Phase 0) before starting. Tasks 3.2 and 3.4 consume the saved-workflow file format (§0.3b) and the permission-routing decision (§0.2). **Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) as a checklist before authoring any playbook or workflow file.**

The known agent ids the workflow orchestrates (must match `internal/scaffold/agents/`): confirm the exact set with `ls internal/scaffold/agents/*.md`. The convergent-activity playbooks reference (at least) `spec-scout`, `spec-decision-elaborator`, `spec-challenger`, plus the goal-diff matcher for `spec_refinement`. The four convergent activities and their CLI verbs: `spec_refinement`→`refine`, `feature_ingestion`→`import`, `code_adoption`→`adopt`, `code_assimilation`→`assimilate`.

### Task 3.1: Author the headless keyword-trigger playbook for `spec_refinement`

**Files:**
- Create: `internal/scaffold/plans/spec_refinement.claude-code.md`
- Test: `internal/scaffold/spec_refinement_dj144_test.go` (new)

> Tier-2 (provider, mode-agnostic). Resolves for Claude Code in BOTH modes — but Phase 4's resolution guard ensures interactive Claude Code prefers the saved workflow path; headless uses this. It must contain the literal word "workflow" to trigger the dynamic-workflow keyword path headlessly (Task 0.1 confirms this fires).

- [ ] **Step 1: Write the failing test (content invariants)**

Create `internal/scaffold/spec_refinement_dj144_test.go`:

```go
package scaffold_test

import (
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecRefinementClaudeCodeHeadlessPlaybook(t *testing.T) {
	body, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
	require.NoError(t, err)
	assert.Equal(t, "plans/spec_refinement.claude-code.md", src,
		"claude-code headless must resolve to the tier-2 workflow playbook")
	text := string(body)
	// Keyword trigger (DJ-144 §5): the word "workflow" must appear.
	assert.Contains(t, strings.ToLower(text), "workflow")
	// Cap-injection token (DJ-144 §6).
	assert.Contains(t, text, "{{max_iterations}}")
	// Must NOT reference the Codex/Gemini loop tools (DJ-143 denies them to CC).
	assert.NotContains(t, text, "spec_loop_begin")
	assert.NotContains(t, text, "spec_advance_iteration")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/scaffold/ -run TestSpecRefinementClaudeCodeHeadlessPlaybook -v`
Expected: FAIL — file does not exist; `ResolvePlaybook` resolves to tier-4 default.

- [ ] **Step 3: Author the playbook**

Create `internal/scaffold/plans/spec_refinement.claude-code.md`. Structure (workflow-shaped prose — describe the orchestration so Claude Code's keyword path builds the workflow; reference the named subagents by id; encode the phase-barrier model from DJ-144 §4). Skeleton to fill (replace bracketed guidance with concrete instructions modeled on the existing `spec_refinement.md` per-iteration body — read it first and mirror its step content so the phases match):

```markdown
# Spec Refinement (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives the spec
graph to convergence for the target named in your Run context (`Target:`,
default `goals`). The workflow loops until the scout reports convergence
or the iteration cap of {{max_iterations}} is reached.

## One-time preamble (before the loop)

[Goal-layer sync — for spec_refinement only. Dispatch spec-goal-diff-matcher
to reconcile the persisted goal layer against GOALS.md exactly as Step 0 of
spec_refinement.md. GOALS.md is read-only during the run, so run this ONCE,
not per iteration (DJ-144 §4).]

## Convergence loop (repeat until converged or {{max_iterations}} reached)

Each iteration, in phase order with a barrier between phases:

1. Survey / scout (serial): dispatch spec-scout. If it reports
   `converged: true`, stop the loop.
2. Fan out over DISJOINT units only (parallel): [decide-axes ‖ elaborate ‖
   critique — one subagent per independent axis/candidate, mirroring
   spec_refinement.md's per-iteration body. Never let two parallel branches
   write the same node.]
3. Reconcile (barrier): join the fan-out; dedup and serialize writes that
   land on a shared node so one coherent revision is applied.
4. Cascade (serial across shared nodes): apply downstream revisions / drift
   marks with shared-node writes serialized.

## After convergence

[Citation walk — judge .advances / .respects against the final state, as in
spec_refinement.md Step N+1. Run once, after the loop.]

## Reporting

Report the final convergence verdict in your closing summary. (The harness
does not re-dispatch you — the workflow owns the loop.)
```

> **Important:** the spec graph writes still go through the `mcp__locutus__spec_*` MCP tools. Do NOT introduce any new tool. Do NOT reference `spec_loop_*` (those are Codex/Gemini-only per DJ-143). Match the phase set to `spec_refinement.md` so Task 3.3's alignment test passes.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/scaffold/ -run TestSpecRefinementClaudeCodeHeadlessPlaybook -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/plans/spec_refinement.claude-code.md internal/scaffold/spec_refinement_dj144_test.go
git commit -m "feat: add Claude Code dynamic-workflow headless playbook for spec_refinement (DJ-144 §5)"
```

### Task 3.2: Author the pinned saved-workflow script for `spec_refinement` (spike-gated)

**Files:**
- Create: `internal/scaffold/workflows/spec_refinement.js` (extension/format per Task 0.3b finding)
- Modify: `internal/scaffold/scaffold.go` (add `//go:embed workflows/*` directive)
- Test: `internal/scaffold/workflows_dj144_test.go`

> **BLOCKED until Task 0.3b records the saved-workflow file format and JS orchestration API.** If the API could not be determined from docs (Task 0.3b said "author interactively"), pause here and author this file with the operator. Do not invent API symbols.

- [ ] **Step 1: Add the embed directive + accessor**

In `internal/scaffold/scaffold.go`, after the `//go:embed plans/*.md` directive (line ~37), add:

```go
//go:embed workflows/*
var workflowsFS embed.FS

// EmbeddedWorkflowsFS exposes the saved-workflow scripts for the
// publisher and tests.
func EmbeddedWorkflowsFS() fs.FS { return workflowsFS }
```

> Confirm `embed` and `io/fs` are already imported in scaffold.go (the plans embed uses them). Create the `internal/scaffold/workflows/` directory with at least one file before building — `//go:embed` fails on an empty/missing dir.

- [ ] **Step 2: Write the failing alignment-shape test**

Create `internal/scaffold/workflows_dj144_test.go`:

```go
package scaffold_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowScriptReferencesOnlyKnownAgents(t *testing.T) {
	body, err := fs.ReadFile(scaffold.EmbeddedWorkflowsFS(), "workflows/spec_refinement.js")
	require.NoError(t, err)
	text := string(body)
	// Must orchestrate the named subagents, not reshape them (DJ-144 §2).
	assert.Contains(t, text, "spec-scout")
	// Must NOT reference Codex/Gemini loop tools (DJ-143).
	assert.NotContains(t, text, "spec_loop_begin")
}
```

> Adjust the filename/extension to the Task 0.3b finding. Tighten the "known agents" assertion in Task 3.3 (the cross-file alignment test) — this is the smoke check.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/scaffold/ -run TestWorkflowScript -v`
Expected: FAIL — workflows dir/file missing.

- [ ] **Step 4: Author the script (with the operator if the API is undocumented)**

Write `internal/scaffold/workflows/spec_refinement.js` implementing the same preamble → barrier-separated loop → citation-walk structure as Task 3.1's playbook, using the orchestration API recorded in §0.3b. The script reads the cap from `{{max_iterations}}` (the publisher substitutes it at publish time — see Task 3.4) or from a documented workflow-input mechanism. Encode phase barriers explicitly (fan out over disjoint units, join before shared-node writes).

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/scaffold/ -run TestWorkflowScript -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/workflows/ internal/scaffold/scaffold.go internal/scaffold/workflows_dj144_test.go
git commit -m "feat: add pinned spec_refinement dynamic-workflow script (DJ-144 §5)"
```

### Task 3.3: Cross-file alignment test (workflow phases ⊇ base playbook phases; only known agent ids)

**Files:**
- Create: `internal/scaffold/workflow_alignment_dj144_test.go`

- [ ] **Step 1: Write the test**

Create `internal/scaffold/workflow_alignment_dj144_test.go`:

```go
package scaffold_test

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownAgentIDs reads the canonical agent set from the embedded agents FS.
func knownAgentIDs(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := fs.ReadDir(scaffold.EmbeddedAgentsFS(), "agents")
	require.NoError(t, err)
	ids := map[string]bool{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			ids[strings.TrimSuffix(e.Name(), ".md")] = true
		}
	}
	return ids
}

func TestWorkflowReferencesOnlyKnownAgentIDs(t *testing.T) {
	known := knownAgentIDs(t)
	body, err := fs.ReadFile(scaffold.EmbeddedWorkflowsFS(), "workflows/spec_refinement.js")
	require.NoError(t, err)
	// Agent ids are hyphenated tokens like spec-scout. Find candidate
	// "spec-*" tokens and assert each is a known agent.
	re := regexp.MustCompile(`spec-[a-z-]+`)
	for _, tok := range re.FindAllString(string(body), -1) {
		assert.Truef(t, known[tok], "workflow references unknown agent id %q", tok)
	}
	_ = path.Base // keep imports honest if trimmed
	_ = sort.Strings
}
```

> Confirm `EmbeddedAgentsFS` exists: `grep -rn "func EmbeddedAgentsFS\|agentsFS" internal/scaffold/`. If the accessor is named differently, use that. If absent, add one mirroring `EmbeddedPlansFS`.

- [ ] **Step 2: Run to verify it passes (or surfaces a real mismatch)**

Run: `go test ./internal/scaffold/ -run TestWorkflowReferencesOnlyKnownAgentIDs -v`
Expected: PASS (the Task 3.2 script references only real ids). If it fails, fix the script's agent ids — do not loosen the test.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/workflow_alignment_dj144_test.go
git commit -m "test: assert workflow scripts reference only known agent ids (DJ-144 §5)"
```

### Task 3.4: Publish the saved workflow + (conditionally) a settings allowlist

**Files:**
- Modify: `internal/publisher/claudecode.go` (`PublishActivity` and/or a new `PublishWorkflow`)
- Modify: `internal/publisher/publisher.go` (orchestration, if a new publish step is needed)
- Test: `internal/publisher/claudecode_dj144_test.go`

> Uses §0.3b (where saved workflows live + filename→slash-command mapping) and §0.2 (permission routing → whether to emit a `settings.json` allowlist).

- [ ] **Step 1: Write the failing test**

Create `internal/publisher/claudecode_dj144_test.go`:

```go
package publisher

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishWorkflow_EmitsSavedWorkflowForConvergentActivities(t *testing.T) {
	fsys := specio.NewMemFS()
	// Arrange: the publisher needs the embedded scaffold copied to .borg/plans
	// + .borg/workflows. Mirror the setup used by existing publisher tests
	// (grep TestPublish in internal/publisher/*_test.go for the helper).
	seedBorgScaffold(t, fsys) // helper to be reused/created from existing tests

	p := claudeCodePublisher{}
	act := CanonicalActivity{Name: "spec_refinement", CLIVerb: "refine"}
	require.NoError(t, p.PublishActivity(act, fsys))

	// The saved workflow lands where Claude Code reads workflows (per 0.3b).
	exists, _ := fsys.Exists(".claude/workflows/locutus-refine.js") // adjust path/ext per 0.3b
	assert.True(t, exists, "convergent activity must publish a saved workflow")
}
```

> Replace `.claude/workflows/locutus-refine.js` and `seedBorgScaffold` with the real path (0.3b) and the existing test seeding helper. Find the latter: `grep -rn "func.*MemFS\|WriteFile(\".borg" internal/publisher/*_test.go`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/publisher/ -run TestPublishWorkflow -v`
Expected: FAIL — no workflow emitted.

- [ ] **Step 3: Implement workflow publishing**

In `internal/publisher/claudecode.go`, for the four convergent activities (`spec_refinement`, `feature_ingestion`, `code_adoption`, `code_assimilation`), read the pinned script from `scaffold.EmbeddedWorkflowsFS()` (or `.borg/workflows/` if the scaffold copies it there — match the plans pattern), substitute `{{max_iterations}}` if the saved-workflow format can't read it at runtime (otherwise leave the token for the workflow input mechanism), and write it to the saved-workflow path with the slash-command-deriving filename (0.3b). Keep the existing `.claude/commands/locutus-<verb>.md` emission for non-convergent activities and as the interactive entry if 0.3b shows commands and workflows coexist.

If §0.2 found permission is out-of-band, also emit/merge a `settings.json` allowlist (`mcp__locutus__*`, `Bash`, `Read`, `Task`) — add an `EnsureSettings` step. If §0.2 found ACP-routed, skip the allowlist (add a code comment citing §0.2).

- [ ] **Step 4: Run to verify it passes + full publisher suite**

Run: `go test ./internal/publisher/ -v`
Expected: PASS, no regression in existing publisher tests.

- [ ] **Step 5: Commit**

```bash
git add internal/publisher/ 
git commit -m "feat: publish Claude Code saved workflows for convergent activities (DJ-144 §5)"
```

### Task 3.5: Replicate Tasks 3.1–3.4 for the other three convergent activities

**Files:**
- Create: `internal/scaffold/plans/feature_ingestion.claude-code.md`, `code_adoption.claude-code.md`, `code_assimilation.claude-code.md`
- Create: `internal/scaffold/workflows/feature_ingestion.js`, `code_adoption.js`, `code_assimilation.js` (format per 0.3b)
- Test: extend the Phase 3 tests to table-drive over all four activities

- [ ] **Step 1: Read each base playbook first**

Run: `ls internal/scaffold/plans/{feature_ingestion,code_adoption,code_assimilation}.md` and read each. Each has its own per-iteration body and phase set — the workflow/keyword playbook for each must mirror ITS phases, not spec_refinement's. (e.g. feature_ingestion's import-conflict detection against `agoal-*`; code_adoption's scope note.)

- [ ] **Step 2: Table-drive the Phase 3 tests over all four activities**

Edit `spec_refinement_dj144_test.go`, `workflows_dj144_test.go`, `workflow_alignment_dj144_test.go`, and the publisher test to iterate over `[]string{"spec_refinement","feature_ingestion","code_adoption","code_assimilation"}` with matching CLI verbs. Run them; expect FAIL for the three new activities.

- [ ] **Step 3: Author the three keyword playbooks + three workflow scripts**

Mirror Task 3.1 / 3.2 for each, matching each activity's own base-playbook phases. Walk agent-conventions.md again. Goal-layer preamble applies to `spec_refinement` and `feature_ingestion` (both read the goal layer); confirm whether `code_adoption`/`code_assimilation` touch the goal layer by reading their base playbooks — do not add a preamble they don't need.

- [ ] **Step 4: Run all Phase 3 tests to green**

Run: `go test ./internal/scaffold/ ./internal/publisher/ -v`
Expected: PASS for all four activities.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/plans/ internal/scaffold/workflows/ internal/scaffold/*_test.go internal/publisher/
git commit -m "feat: extend Claude Code workflow convergence to all four convergent activities (DJ-144)"
```

---

## Phase 4 — Teardown + resolution guards (depends on Phase 3)

Remove the `/goal` wrapper now that its replacement exists, and lock the resolution so Claude Code never reaches the Codex/Gemini `spec_loop_*` interactive playbook.

### Task 4.1: Guard — Claude Code interactive must NOT resolve to `spec_refinement.interactive.md`

**Files:**
- Test: `internal/scaffold/plans_overlay_dj144_test.go` (new)

> Do this BEFORE deleting the `/goal` wrapper, so the test documents the required post-deletion resolution and fails loudly if a future tier change leaks the loop-tool playbook to Claude Code.

- [ ] **Step 1: Write the guard test**

Create `internal/scaffold/plans_overlay_dj144_test.go`:

```go
package scaffold_test

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// After DJ-144, Claude Code interactive must resolve to the tier-2
// provider playbook (spec_refinement.claude-code.md), NOT the tier-3
// Codex/Gemini self-loop (spec_refinement.interactive.md), which uses
// spec_loop_* tools DJ-143 denies to Claude Code.
func TestClaudeCodeInteractiveDoesNotResolveLoopToolPlaybook(t *testing.T) {
	_, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)
	assert.NotEqual(t, "plans/spec_refinement.interactive.md", src,
		"claude-code interactive must not resolve the Codex/Gemini loop-tool playbook (DJ-143/DJ-144)")
	assert.Equal(t, "plans/spec_refinement.claude-code.md", src,
		"claude-code interactive must resolve the tier-2 workflow playbook")
}

// Codex/Gemini interactive must STILL resolve the tier-3 self-loop
// (DJ-142 unchanged).
func TestCodexInteractiveStillResolvesLoopToolPlaybook(t *testing.T) {
	_, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "codex", scaffold.ModeInteractive)
	require.NoError(t, err)
	assert.Equal(t, "plans/spec_refinement.interactive.md", src)
}
```

> Resolution check (tier order from `plans_overlay.go`): for claude-code interactive the candidates are tier-1 `spec_refinement.claude-code.interactive.md` → tier-2 `spec_refinement.claude-code.md` → tier-3 `spec_refinement.interactive.md` → tier-4 `spec_refinement.md`. After Task 4.2 deletes tier-1, tier-2 (created in Task 3.1) wins — so Claude Code never reaches tier-3. This is the mechanism the test locks.

- [ ] **Step 2: Run to verify CURRENT state**

Run: `go test ./internal/scaffold/ -run "TestClaudeCodeInteractiveDoesNotResolveLoopToolPlaybook|TestCodexInteractiveStillResolvesLoopToolPlaybook" -v`
Expected: the first test FAILS now (tier-1 `/goal` wrapper still exists and wins, so `src` is `spec_refinement.claude-code.interactive.md`, not `.claude-code.md`). The Codex test PASSES. This confirms the guard is meaningful.

- [ ] **Step 3: Commit the test (red)**

```bash
git add internal/scaffold/plans_overlay_dj144_test.go
git commit -m "test: guard Claude Code interactive resolution away from loop-tool playbook (DJ-144)"
```

### Task 4.2: Delete the `/goal` wrapper playbook

**Files:**
- Delete: `internal/scaffold/plans/spec_refinement.claude-code.interactive.md`
- Check: any test referencing it

- [ ] **Step 1: Find references**

Run: `grep -rn "spec_refinement.claude-code.interactive\|claude-code.interactive" internal/ docs/`
Note every test/doc that asserts the wrapper resolves (e.g. `plans_overlay_dj140_test.go`, `plans_overlay_test.go`, DJ-142's publisher test).

- [ ] **Step 2: Delete the file**

```bash
git rm internal/scaffold/plans/spec_refinement.claude-code.interactive.md
```

- [ ] **Step 3: Update the now-stale DJ-140/DJ-142 resolution tests**

Tests that asserted Claude Code interactive resolves to `.claude-code.interactive.md` (the `/goal` wrapper) must now assert it resolves to `.claude-code.md` (the workflow playbook). Update them — do NOT delete the assertions; retarget them. Specifically check:
- `internal/scaffold/plans_overlay_test.go` (TestResolvePlaybook_InteractiveOverlayResolvesOnlyForInteractiveMode and the provider-beats-mode test).
- `internal/scaffold/plans_overlay_dj140_test.go`.
- `internal/scaffold/plans_overlay_dj142_test.go` (the claude-code interactive case).
- The DJ-142 publisher test asserting Claude Code interactive command body is the `/goal` wrapper — retarget to assert it now references the workflow (contains "workflow", not `/goal`).

- [ ] **Step 4: Run the guard test + the full scaffold/publisher suites**

Run: `go test ./internal/scaffold/ ./internal/publisher/ -v`
Expected: PASS — including `TestClaudeCodeInteractiveDoesNotResolveLoopToolPlaybook` (now green: tier-2 wins).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: remove Claude Code /goal wrapper playbook, superseded by dynamic workflow (DJ-144)"
```

### Task 4.3: Full-suite regression + Codex/Gemini-unchanged assertion

**Files:**
- Verify only (no new code unless a regression surfaces)

- [ ] **Step 1: Run the entire test suite with the race detector**

Run: `go build ./... && go test ./... -race`
Expected: all green. Pay special attention to `internal/runner`, `internal/scaffold`, `internal/publisher`, `internal/mcp`, `internal/activity`, `internal/runtimepolicy`, `cmd`.

- [ ] **Step 2: Confirm Codex/Gemini drivers are untouched**

Run: `go test ./internal/scaffold/ -run "Codex|Gemini|dj142" -v` and `go test ./internal/runner/ -run "OuterLoop|Converged" -v`
Expected: PASS — DJ-142 self-loop resolution and the `OuterLoopRunner` unit tests still pass; only claude-code changed.

- [ ] **Step 3: `go vet`**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 4: Commit (if any regression fixes were needed)**

```bash
git add -A
git commit -m "test: full-suite regression for DJ-144 (Codex/Gemini drivers unchanged)"
```

---

## Phase 5 — Documentation

Per [[feedback-council-doc-maintenance]] (council-touching DJ) and the DJ-144 Consequences > Documentation block. No code; docs only. Maintain Locutus voice neutrality ([[feedback-locutus-voice-neutrality]]).

### Task 5.1: CLAUDE.md convergence-driver bullet → four-driver matrix

**Files:**
- Modify: `CLAUDE.md` (the DJ-140/DJ-142 "Three idiomatic convergence drivers" bullet under "Sources of Truth")

- [ ] **Step 1: Update the bullet**

Find it: `grep -n "Three idiomatic convergence drivers\|DJ-142" CLAUDE.md`. Extend "Three idiomatic convergence drivers, one outcome (DJ-142)" to note the **fourth** Claude-Code-only driver (DJ-144): Claude Code (both modes) converges via an in-runtime dynamic workflow; `dispatchUsesOuterLoop("claude-code")` is false; `spec_loop_*` stays Codex/Gemini-only. Add a sentence on the per-runtime version-floor registry (`runtimes-default.yaml` / `.borg/runtimes.yaml`, warn-and-proceed).

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update convergence-driver bullet to four-driver matrix + version floor (DJ-144)"
```

### Task 5.2: runtime-affordances.md, council.md, debugging-traces.md

**Files:**
- Modify: `docs/runtime-affordances.md`, `docs/council.md`, `docs/debugging-traces.md`

- [ ] **Step 1: runtime-affordances.md**

Document the Claude Code dynamic-workflow driver, the keyword-trigger (headless) vs saved-workflow (interactive) split, the v2.1.154 floor, and the per-runtime `min_version` registry + warn-and-proceed semantics (DJ-144 §9).

- [ ] **Step 2: council.md (REQUIRED — council-touching DJ)**

Update the convergence visualization: the Claude Code row(s) change from `/goal` + harness to the dynamic-workflow driver. The diagram must show all four drivers reaching the one outcome (converge or cap). Follow the DJ-142 council.md update as the pattern.

- [ ] **Step 3: debugging-traces.md**

Note the coarser ACP event stream for Claude Code workflow runs (internal subagent calls don't surface as ACP events; only the final answer returns) and that the daemon-side spec-mutation trail (`tools.jsonl` + history events) remains the audit surface.

- [ ] **Step 4: Commit**

```bash
git add docs/runtime-affordances.md docs/council.md docs/debugging-traces.md
git commit -m "docs: document Claude Code workflow convergence driver + version floor (DJ-144)"
```

### Task 5.3: Flip DJ-144 status + close verification items

**Files:**
- Modify: `docs/decisions/dj-144-cc-workflow-convergence.md`, `docs/DECISION_JOURNAL.md`

- [ ] **Step 1: Update the status line**

Change DJ-144 **Status:** from `settled` to `shipping`, listing which phases landed (1–5) and what end-to-end validation remains the operator's (a real Claude Code workflow convergence run). Fold the Phase 0 findings into the spec: replace the three Open verification items with their resolved answers (permission routing, headless reachability + version, saved-workflow format), or note any that remain open. Update the manifest row status in `DECISION_JOURNAL.md` to match.

- [ ] **Step 2: Run the manifest bijection test**

Run: `go test ./internal/docs/`
Expected: PASS (row ↔ file still consistent).

- [ ] **Step 3: Commit**

```bash
git add docs/decisions/dj-144-cc-workflow-convergence.md docs/DECISION_JOURNAL.md
git commit -m "docs: flip DJ-144 to shipping and close verification items"
```

---

## Self-review (completed by plan author)

**Spec coverage** (DJ-144 §1–§9 + Consequences):
- §1 four-driver matrix → Phase 2 (dispatch), Phase 5.1 (docs). §2 named subagents preserved → Task 3.2/3.3 alignment tests. §3 no `spec_loop_*` for CC → Task 3.1/4.1 guards. §4 phase-barrier orchestration → Task 3.1/3.2 authoring. §5 authoring/resolution → Phase 3 + Task 4.1/4.2. §6 Option A + cap injection → Task 2.1/2.2. §7 teardown → Task 4.2. §8 permissions → Task 0.2 + Task 3.4 (conditional allowlist). §9 version registry → Phase 1 entirely. Consequences code-add/modify/remove → Phases 1–4. Docs → Phase 5. Open verification items → Phase 0 + Task 5.3.
- **Gap noted (deliberate):** Task 3.2's exact JS API is spike-gated (Phase 0.3b), not fabricated — per CLAUDE.md "don't assume." This is a known, surfaced dependency, not a placeholder.

**Placeholder scan:** the `{{max_iterations}}` token is an intentional runtime substitution marker (defined in Task 2.2, consumed in Task 3.1), not a plan placeholder. Bracketed `[...]` guidance in the Task 3.1 playbook skeleton marks content to mirror from the existing `spec_refinement.md` — flagged as "read it first and mirror," not left as TBD. All Go tasks have complete code.

**Type/name consistency:** `dispatchUsesOuterLoop` (2.1), `injectMaxIterations` (2.2), `runtimepolicy.NewRegistry`/`MinVersion`/`BelowFloor`/`CheckVersion`/`DefaultYAML` (Phase 1), `checkRuntimeVersion` (1.4), `EmbeddedWorkflowsFS` (3.2) — used consistently across the tasks that reference them. Verb→activity mapping (refine/import/adopt/assimilate) consistent. MemFS constructor and `EmbeddedAgentsFS` accessor flagged for confirmation before use (names assumed from convention).
