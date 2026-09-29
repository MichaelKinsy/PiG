# AgentSession compaction suite closure

## Cases

`packages/coding-agent/test/suite/agent-session-compaction.test.ts` has 28 case sites. Its oversized-tool case expands into ordinary-setting and model-override rows. The Go ports keep both rows and adjacent upstream line references across `coding/session_compaction_suite_{upstream,active,policy}_test.go`.

The assertions cover extension summaries and usage accounting, system checkpoint replay, completion callbacks, model/auth preflight, bearer auth, standalone summary requests, persisted generated usage, automatic failure events, length and overflow recovery, large and terminating tool results, queued steering, manual cancellation and idle reuse, and stale/absent/below-threshold usage.

The suite fixture computes request usage with Pi's faux-provider serialization and cache accounting. Returning zero usage for these responses is not equivalent: it changes successful-overflow classification. This fixture does not claim coverage for the separate Go `ai/faux.go` provider.

## Source fixes

- `coding/session.go` delivers compaction completion events synchronously. Manual compaction releases its admission state before calling completion listeners. `TestManualCompactionWaitsForCompletionListener` uses `synctest` to prove the caller waits for a blocked synchronous listener.
- Manual compaction reads and validates the model after its start notification. It performs request/auth preparation before inspecting whether history is large enough to compact. Missing-model and missing-auth errors match Pi instead of reporting an empty-session error.
- `coding/session_summarization_auth.go` reuses Services-owned ModelRuntime request preparation for registry-built providers. It forwards the prepared model, headers, environment, and API key into the existing summary completer or custom stream. A directly supplied Go provider is a caller-owned stream even when it carries provider identity metadata; it keeps its request/auth implementation instead of being rebuilt through the registry. Custom streams preserve Pi's optional-auth behavior. No second credential store or auth parser is added.
- Go models carry their provider implementation directly. A native provider exposes Pi's `auth` property through `Auth() ai.ProviderAuth`. `coding/services.go` resolves that contract with the existing `ai.ResolveProviderAuth` mechanism before calling the provider. The bearer test distinguishes resolved headers from an API-key fallback.
- `internal/codingagent/compaction/compaction.go` distinguishes a rejected custom stream call from a failed assistant response. A thrown call keeps its original error. Assistant-response and tool-call diagnostics retain their operation prefixes.

## Proof

The initial suite failed on completion ordering, active manual admission during a completion callback, missing model/auth error precedence, and the extra `Turn prefix summarization failed:` prefix. Full-suite verification also exposed caller-owned provider metadata being mistaken for registry ownership: `TestCustomProviderSummaryKeepsCallerOwnedAuth` and the existing `TestPrintModePrintsAnswerAfterOverflowRecovery` failed before the ownership fix. All failures are fixed at their shared sources.

Compiling mutations restore asynchronous completion delivery, remove model/auth preflight, remove custom-call error provenance, or bypass between-turn compaction. They fail the new suite, the deterministic listener test, the pure summary-error test, and the paired scenario. The active-run mutations also prove retained large tool results and queued steering are checked before the next provider request.

`test/parity/scenarios/compaction/13-compaction-completion-auth-errors.toml` runs three exact-output pairs against unchanged Pi 0.87.1. Error fixtures select the same read-only documentation root rather than normalizing paths. The comparison includes queued-prompt completion order, model/auth diagnostics, bearer resolution, and the thrown-stream failure text.

Normal package tests, race tests, lint, and the compaction family pass. Cross-family RPC, print, and JSON checks use the same fresh binary. Run the owning scenario families with a fresh parity binary and retain their output as verification artifacts.

`BenchmarkSummarizationRequestAuth` measures native bearer preparation at about 6.4 microseconds, 1,952 bytes, and 15 allocations per operation on this host. Reproduce the profiles with `go test ./coding -run '^$' -bench '^BenchmarkSummarizationRequestAuth$' -benchmem -cpuprofile=cpu.pprof -memprofile=mem.pprof`. Request wrappers and option maps live only for the owning compaction. The change adds no background task, global provider registry, or retained cache.
