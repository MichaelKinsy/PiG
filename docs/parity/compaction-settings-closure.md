# Compaction settings and preparation failures

## Upstream contract

Pi 0.87.1 `packages/coding-agent/src/core/settings-manager.ts:getCompactionTokenSetting` validates an ordinary token setting before consulting the exact provider/model override. Missing values fall back by field. Null, non-numeric values, negative numbers, fractions, unsafe integers, and non-finite numbers fail when the getter selects them. An invalid override for another model does not affect the current model. The compaction toggle does not read or validate token settings.

The synchronous getter observes one settings snapshot. Project changes merge each model field before lookup. Reload and project-trust changes retain their existing persistence semantics.

`packages/agent/src/agent-loop.ts:runLoop` awaits next-turn preparation before starting another provider request. `packages/agent/src/agent.ts:runWithLifecycle` handles a rejection by emitting one failed assistant message, its turn end, and an agent end containing only that failure. The failed assistant uses empty text and zero usage. The Session persists that message before its caller completes.

## Go representation and callers

`CompactionSettingsJSON` and `CompactionModelOverride` retain numeric settings as `*float64`. This represents JavaScript fractions and non-finite runtime numbers before validation. Private raw JSON members preserve explicit null and invalid authored shapes without rejecting an unrelated settings file. Cloning and merging preserve these members. Successful getters return validated integer token counts.

Settings getters return `(value, error)`. Manual compaction, automatic policy decisions, and next-turn preparation propagate these errors. RPC state reads the independent enabled toggle. `agent.PrepareNextTurn` returns `(*AgentLoopTurnUpdate, error)` to represent Promise rejection; it does not add a wire error field or another callback mechanism. This signature change overlaps the Agent runtime lane and must be preserved when integrating its user-content carrier change.

The whole compaction getter holds one read lock and reads only the requested model entry. It does not clone or scan every model override. No task, cache, or background worker is added to production.

## Evidence

- `TestSettingsManagerCompactionUpstream` ports all 12 case sites and every nested table row from `settings-manager-compaction.test.ts` with adjacent source citations.
- `TestCompactionSettingsReadOneSnapshot` first failed with `{Enabled:false ReserveTokens:1 KeepRecentTokens:1}`, proving interleaved getter reads. The atomic getter passes.
- `TestCompactionInvalidSettingsReachCaller` asserts manual admission cleanup and provider suppression, and a changed setting during an active run suppresses the next request and persists the failure.
- `TestPrepareNextTurnRejectionFinalizesFailedAssistant` asserts the exact four failure lifecycle events, zero usage, failed transcript tail, and one provider call.
- `test/parity/scenarios/compaction/12-compaction-settings-validation.toml` compares every invalid-value diagnostic and both Session caller paths against unchanged installed Pi. It uses `output_equal` and three pairs. It does not normalize output.
- Compiling mutations disable validation, permit malformed model entries, or bypass the failed-assistant lifecycle. The corresponding unit tests and paired scenario fail.

Reproduce the measurements with `go test ./internal/codingagent -run '^$' -bench '^BenchmarkCompactionSettingsModelOverrides$' -benchmem -cpuprofile=cpu.pprof -memprofile=mem.pprof`. The 1,000-model `BenchmarkCompactionSettingsModelOverrides` measures about 1.1 microseconds, 361 bytes, and 10 allocations per selected-model read on this host. A transitional three-clone getter measured about 579 microseconds, 493 KB, and 6,031 allocations; that implementation also failed snapshot consistency. These measurements compare getter implementations during this change, not released versions.

Verification uses the focused normal and race tests, all Agent/Session/internal coding-agent package tests, the exact Pi scenario, lint, and Windows cross-vet. Full-repository verification remains part of the lane's final gate.
