## DJ-055: Executor Uses func(any) bool for Step.Conditional, Accepting Generics Leak

**Status:** shipped

**Decision:** `executor.Step.Conditional` has signature `func(state any) bool` even though the executor is generic on `State`. Callers type-assert `state.(*MyState)` in their closures.

**Why:** `Step` is not generic — making it so would require `Step[S]` everywhere and significantly complicate the API. The `any`-typed conditional is a pragmatic leak of the generic contract. Callers handle it with a small type assertion at the closure boundary. Accepted as a Go generics limitation rather than a design flaw.

**Alternatives considered:** Make `Step` generic (too invasive), use an interface with a type parameter (awkward), remove conditional from `Step` (would push conditionality into `RunStep` itself, losing the optimization of skipping before resource allocation).
