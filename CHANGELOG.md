# Changelog

## [1.1.0](https://github.com/glorious-beard/locutus/compare/v1.0.0...v1.1.0) (2026-04-28)


### Features

* add Tier 3 decision graph with blast radius and diff command ([bdffd3a](https://github.com/glorious-beard/locutus/commit/bdffd3ad15fea974c4ccc0e38efca6c72929c1cf))
* add Tier 4 LLM council infrastructure with convergence loop ([6285118](https://github.com/glorious-beard/locutus/commit/6285118def4914da522c5b179cf07295031b0bf8))
* add Tier 5 planning pipeline, artifact generation, and triage ([1cbd82e](https://github.com/glorious-beard/locutus/commit/1cbd82e92450dc08c3fef9e179d75719544c12f8))
* add Tier 6 brownfield analysis pipeline ([b92694a](https://github.com/glorious-beard/locutus/commit/b92694a31ef712b0aefd8b572dbf39ebb4fa9c88))
* add Tier 7 dispatch, supervision, and flat scaffold structure ([ef383db](https://github.com/glorious-beard/locutus/commit/ef383db6a0ecac38358e175dee7e4dc0f360da6a))
* add Tier 8 MCP server with all tools registered ([b6bb7f8](https://github.com/glorious-beard/locutus/commit/b6bb7f8fe67a4a830460803adf6f67b51662f557))
* **adopt:** Round 7 Phase B — resume plumbing in cmd layer (DJ-074) ([672b33f](https://github.com/glorious-beard/locutus/commit/672b33f9ec783f1f7cb9915364b59e1b3bff878d))
* **adopt:** Round 7 Phase C — resume policy + planner short-circuit (DJ-074) ([b8c3243](https://github.com/glorious-beard/locutus/commit/b8c32438050251c2f9e3fb6a9d2f2d483c38fee5))
* **adopt:** wire C6 dispatch integration — Phase C complete ([12db8ce](https://github.com/glorious-beard/locutus/commit/12db8ce3a8813534db194e1841ac3197d184b5c4))
* **agent:** model tier config via embedded YAML + runtime provider resolve ([e6ba25a](https://github.com/glorious-beard/locutus/commit/e6ba25a5f8f7b56c0a4b34d7c514747e7df5ef80))
* **agent:** real Genkit LLM wiring with env-driven provider detection ([8c8d29f](https://github.com/glorious-beard/locutus/commit/8c8d29f99855f9bc85aa6931681cb87d4b6983ff))
* **assimilate:** Round 1 — existing-spec-aware persistence (DJ-075) ([45f49d9](https://github.com/glorious-beard/locutus/commit/45f49d953ac9602212b2864f5b113cb7427c9708))
* **assimilate:** Round 5 followup — remediation_run history event (DJ-045) ([61634b2](https://github.com/glorious-beard/locutus/commit/61634b281de8016905c492a2d10b0695e5b63b8f))
* **cli:** consolidate to 8-verb lifecycle shape (DJ-072) ([08762ae](https://github.com/glorious-beard/locutus/commit/08762aea2abdff5373f4234e1bc476958dc6bfbc))
* **cli:** graceful shutdown — SIGINT/SIGTERM handler + ctx end-to-end ([f8c9832](https://github.com/glorious-beard/locutus/commit/f8c983258e9b02ed07b88bb63321717f36d44299))
* **cmd:** streaming supervision part 7b — mcp-perm-bridge subcommand ([93accbe](https://github.com/glorious-beard/locutus/commit/93accbe3620df07cbee4a71cc530331dedfeb887))
* council-driven spec generation, structured output, session traces, decision provenance ([9f1f421](https://github.com/glorious-beard/locutus/commit/9f1f421ae056d5bbdc8eb65c257138e6db452dec))
* **dispatch:** per-step commit, merge, and persistence (DJ-073, DJ-074) ([90ecd6d](https://github.com/glorious-beard/locutus/commit/90ecd6d20f556b0bb0f4233eaa282e1e31574a3b))
* **dispatch:** Round 7 Phase A — resume plumbing in dispatch layer (DJ-074) ([c20604e](https://github.com/glorious-beard/locutus/commit/c20604e211d573a256421ad794f07ea915191c95))
* **dispatch:** streaming supervision part 1 — AgentEvent types ([b59a86a](https://github.com/glorious-beard/locutus/commit/b59a86ad839912d09305ab41aed677b46dc4804f))
* **dispatch:** streaming supervision part 2 — streaming CommandRunner ([85ed9fd](https://github.com/glorious-beard/locutus/commit/85ed9fdf794a9d156a0e453b1962c6826e68ee70))
* **dispatch:** streaming supervision part 5 — supervisor event loop ([31dc15c](https://github.com/glorious-beard/locutus/commit/31dc15c644dcd521afdc002eac290ddc5bdca86c))
* **dispatch:** streaming supervision part 6 — monitor + fast-tier judge ([ca9322e](https://github.com/glorious-beard/locutus/commit/ca9322efbbb79ba4bd8a997c74ab159df345578f))
* **dispatch:** streaming supervision part 7a — supervisor-side perm bridge ([295e0c7](https://github.com/glorious-beard/locutus/commit/295e0c7df02ed593ca9fb94fec5fcbbbfd7ec9fa))
* **dispatch:** streaming supervision part 7c — handleInteraction + runAttempt merge ([c3305d8](https://github.com/glorious-beard/locutus/commit/c3305d89ea0db67d9d47489816bf732f80c9fd11))
* **dispatch:** streaming supervision part 8 — churn/retry sliding window ([1abeb01](https://github.com/glorious-beard/locutus/commit/1abeb01bd547baefd3a38cc04293ec0196c5b93f))
* **dispatch:** streaming supervision part 9 — MCP progress forwarding ([9d5185a](https://github.com/glorious-beard/locutus/commit/9d5185ad8764d15e574fdb50f2e160cef2176ad6))
* **dispatch:** streaming supervision parts 3+4 — claude NDJSON parser ([f8ce50a](https://github.com/glorious-beard/locutus/commit/f8ce50a56f40879a0165bd5cd67821cfab659a6c))
* **eval:** Round 4 — llm_review via eval framework (DJ-077) ([b32d1af](https://github.com/glorious-beard/locutus/commit/b32d1afd4494b09820003d236991b3df329ddf9a))
* **history:** Round 2 — historian narrative layer (DJ-026) ([b48585f](https://github.com/glorious-beard/locutus/commit/b48585f7ca7e7fa1b0ce362e4fb5a2ac8d50e3da))
* load .env at CLI startup via godotenv ([252b056](https://github.com/glorious-beard/locutus/commit/252b056372a7ac237c7e57ce68f57dd281b72ce7))
* **memory:** Pre-Round-3 — adopt memory.Service from adk-go (DJ-077) ([6428507](https://github.com/glorious-beard/locutus/commit/6428507a7fb97c49e63237da6824c6765e4850b5))
* **overlap:** Round 6 — file-overlap detection at plan time (DJ-030) ([1580da3](https://github.com/glorious-beard/locutus/commit/1580da309a23f7f24356f843fc86f235efedcd58))
* **reconcile:** DJ-073 workstream persistence + hash decoupling ([9cb89c3](https://github.com/glorious-beard/locutus/commit/9cb89c3ded026be29e8c3f708d4da3cb636d00ef))
* **reconcile:** land C3 cascade, C4 dep walk, C5 pre-flight ([3bdf2dc](https://github.com/glorious-beard/locutus/commit/3bdf2dc7bd75c7f65ee345e4339873b211f972d3))
* **refine:** Round 3 — refine for non-Decision kinds (DJ-069) ([0aad774](https://github.com/glorious-beard/locutus/commit/0aad774bcf495612cc6f9a2e92b79fc8b651cd74))
* **remediate:** Round 5 — assimilate gap remediation (DJ-045 + DJ-046) ([d3f0b6c](https://github.com/glorious-beard/locutus/commit/d3f0b6cfd4016864ae6415d4c911381c608942aa))
* **session:** Pre-Round-3 — workstream-scoped session event log (DJ-077) ([4429732](https://github.com/glorious-beard/locutus/commit/4429732c61085b7981e442c1c7f6b86a473b080e))
* **spec:** reconcile DJ-068 + DJ-069 — manifest/state separation and DAG node redesign ([ad6a52d](https://github.com/glorious-beard/locutus/commit/ad6a52dd71b80e2c0c04b40d47efda52ed5503fb))
* **status:** --in-flight discovery + DJ-074 resume proposal ([d70939a](https://github.com/glorious-beard/locutus/commit/d70939a850a48c8a8dd0a50f76fed184e8e520ca))
* wire workstream dispatch through generic executor ([f3cf263](https://github.com/glorious-beard/locutus/commit/f3cf2633c27abb8716e6ac48f95b63d74c1989da))


### Bug Fixes

* address review findings — OSFS bugs, planner context, and cleanup ([604f19e](https://github.com/glorious-beard/locutus/commit/604f19ea8e6f49d7a8d77ed2f591ff304e427e1a))
* **dispatch:** recover stale worktree state before CreateWorktree (DJ-074) ([291d3fd](https://github.com/glorious-beard/locutus/commit/291d3fd2edb0677dacf8d8c2786567baafed08cc))
* **dispatch:** three bugs surfaced by live end-to-end smoke test ([8517bae](https://github.com/glorious-beard/locutus/commit/8517bae681956045f3396612a2ce5282e26091c6))
* **persistence:** atomic writes via renameio + workstream LoadPlan/Load error propagation ([9b63d1a](https://github.com/glorious-beard/locutus/commit/9b63d1ae9e1d7d96d6331dbe4b28b1125906c2b1))
* surface state-store IO errors, plumb typed CLI exit codes, plug bridge goroutine leak, log corrupt traces ([74b3104](https://github.com/glorious-beard/locutus/commit/74b31048901144d0238357859a800d06f3d58dfe))

## 1.0.0 (2026-04-15)


### Features

* add Tier 1 core infrastructure ([32e03df](https://github.com/glorious-beard/locutus/commit/32e03dff93416dd3404dce6e03242c6132febfce))
* add Tier 2 CLI commands, CI/CD pipeline, and release-please ([c000bce](https://github.com/glorious-beard/locutus/commit/c000bce55892cbb95a0cdbbfd4da38bbf24a2a86))
