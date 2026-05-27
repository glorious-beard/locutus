## DJ-112: Workflows Move from External YAML to Go Values; Agents Stay External (Supersedes DJ-036 on Workflows)

**Status:** shipping

**Decision:** The three council workflows (`PlanningWorkflow`, `AssimilationWorkflow`, `SpecGenerationWorkflow`) live as Go values in [`internal/agent/workflows.go`](../internal/agent/workflows.go). `WorkflowStep` carries closures (`Conditional`, `Fanout`, `Project`, `Merge`) instead of string tags. The YAML loader, the embedded `workflows/*.yaml` files, the scaffold seed/reset paths for them, and the four parallel string-keyed switches (`shouldRunConditional`, `mergeResults`'s big switch, `extractFanoutItems`, `ProjectState`'s stepID dispatch) are deleted.

**Agents remain external.** `.borg/agents/<id>.md` is unchanged — DJ-036's reasoning ("advanced users tune prompts, change models per agent, version per project") still holds for agent files. Workflows were the part of DJ-036 that didn't pay off in practice: every novel step needs Go-side wiring (a new merge handler, conditional, projection, possibly fanout extractor), so a user could only reorder or toggle steps from YAML — a customization affordance no one used. The YAML was implicitly author-only; making that explicit removes the fiction without losing user customization that ever existed.

**Why a single source per step beats parallel string-keyed switches.** Defining a step under the YAML+switches design required edits in 4–5 files: the YAML entry plus a case in each of `shouldRunConditional`, `mergeResults`, `extractFanoutItems`, and `ProjectState`. Behavior was scattered across files keyed only by step IDs, so a typo or stale switch case failed silently at runtime. Closure fields put a step's full behavior in one struct literal next to the workflow declaration. The four switches are gone.

**The DAG executor stays a DAG executor.** `internal/executor` still does dependency ordering, bounded parallelism, and snapshot isolation — those earn their keep on fanout and parallel critic dispatch. What's gone is the conflation of "graph topology" with "control flow." The convergence loop that wraps `Run` was always *outside* the DAG (it re-runs the whole DAG per iteration), and it stays there as a Go `for`, not a graph back-edge. Sub-graph loops, when needed, can be expressed in Go control flow rather than as cyclic dependencies.

**Migration impact:**

- `LoadWorkflow` removed; callers (`Plan`, `Analyze`, `GenerateSpec`) reference the package-level workflow values directly.
- `update --reset` no longer refreshes workflow files (there are none on disk). It still refreshes agents and `models.yaml`. `ResetReport.WorkflowsReset` is dropped.
- `locutus init` no longer creates `.borg/workflows/`. Existing project copies of the YAMLs are now dead files — harmless but stale.
- The legacy substring fallback in conditionals (`shouldRunConditional` checking `state.ProposedSpec` for the condition string) is gone. Tests that depended on it (`TestPlanWithSpecialists`, the `open_questions` substring path) have been removed; their typed equivalents (`hasOpenQuestions` against `OpenConcerns`) are what production now uses.
- Tests that injected custom workflow YAMLs now build `&Workflow{Rounds: []WorkflowStep{...}}` literals. `GenerateSpec`'s tests inject a simpler `testSpecGenWorkflow` via a new package-private `generateSpecWithWorkflow` helper so each assertion can exercise a specific behavior without the full DJ-098 outline+fanout pipeline.

**Reversal criteria:** revert if (a) we ship a genuine third-party-workflow plugin model where users author topologies without recompiling — at that point a serialization format (YAML or otherwise) earns its way back in. The serialization format is the easy part; the harder constraint (Go-side handlers per step) would also have to be addressable from the plugin surface. (b) is not anticipated.
