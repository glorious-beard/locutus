## DJ-064: FastLLM Field Bounds Monitor Cost (Extends DJ-056)

**Status:** shipped

**Decision:** `SupervisorConfig` gains a `FastLLM agent.LLM` field distinct from the strong-tier `LLM`. `Supervisor.monitorCycle` uses `FastLLM`; `Supervisor.validate` and `handleInteraction` use `LLM`. When the monitor agent is configured but `FastLLM` is nil, the supervisor surfaces a clear "FastLLM is nil" error at call time rather than routing monitor prompts through the strong tier.

**Extends DJ-056.** The original plan had the monitor calling `s.cfg.LLM`, which in production would send every monitor cycle through the strong tier — defeating the "bounded cost" property that was DJ-056's whole point. The separate field makes the cost envelope explicit: monitors burn fast-tier tokens, validators/guardians burn strong-tier tokens, and callers who care (most importantly, `Dispatcher`) plumb both through explicitly.

**Missing monitor agent behavior:** when `AgentDefs["monitor"]` is unset, `monitorCycle` logs an INFO notice exactly once per supervisor (via `sync.Once`) and returns `IsCycle=false`. Silent disable — validation at attempt end still catches bad outcomes; a one-time log means misconfiguration is discoverable without noise.
