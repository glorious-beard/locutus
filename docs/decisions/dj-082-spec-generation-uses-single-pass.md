## DJ-082: Spec Generation Uses Single-Pass Council, Not the Planner Workflow

**Status:** superseded by DJ-083

**Decision (historical):** `agent.GenerateSpec` runs a lightweight council inline — proposer LLM call, then 0..N critic-and-revise rounds — rather than going through the existing `agent.Plan` planning workflow (which uses `WorkflowExecutor` + `planning.yaml`). Default critique rounds = 1 from the cmd-layer entry point.

**Reversal:** DJ-083 supersedes this. Spec generation now uses externalized agent definitions and a dedicated workflow YAML, the same model the planning council uses. The triggering observation was multi-agent expansion — once the council grew to six members (scout + architect + four specialist critics), the inline approach turned every prompt into a Go string constant and every tuning knob into a recompile. The reversal criteria DJ-082 set out ("when a graph-generator workflow is added to planning.yaml") were met as soon as it was cheaper to externalize than to keep maintaining the inline shape.
