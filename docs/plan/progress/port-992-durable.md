# port-992-durable: upstream 0.99.2 packages/durable tests (phase 1, per-file D-H disposition)

Method: phase 1 of the owner-approved port-first method, corrected by the lead: decision D-H (`docs/plan/upgrade-0.99.1.md:58` and `:74`) puts `packages/durable` outside the four-package denominator, so its pending test rows are `designed-out` per file instead of ported. No durable infrastructure is built; no production code changed.

History: an earlier attempt ported 10 files into a new `durable/` Go package tree with signature stubs (commits `5364cb8bf`..`8e82338d8`). The lead corrected the scope; commit `1d5659e29` removes that tree together with the mapping change. Git history keeps the removed stubs and ported cases.

## Result

All 38 pending `packages/durable/test/*.test.ts` rows in `test/parity/interfaces/test-mapping-v0.99.2.json` are now `designed-out`, each with its own rationale: what the file tests, why PiG has no equivalent, and the Go tests that overlap (listed in `evidence`, verified to resolve by `make test-inventory`). The overlap credit is honest scope: those Go tests cover the same behavior family in the predecessor or sibling implementation (`agent/harness/pico3`, `agent/harness/{env,session,compaction,runtime,tools}`, `internal/codingagent/tools`), not these durable files. Nothing is claimed ported.

| upstream file | status | credited Go evidence |
|---|---|---|
| env-node-spill, env-node | designed-out | agent/harness/env/{env,exec}_test.go |
| env-truncate | designed-out | internal/codingagent/tools/truncate_head_test.go, agent/harness/tools/tools_upstream_test.go |
| harness-compaction | designed-out | agent/harness/compaction/{compaction,generation}_upstream_test.go |
| harness-context | designed-out | agent/harness/session/context_test.go, pico3/conversation_observation_test.go |
| harness-conversations | designed-out | pico3/lifecycle_test.go, kinds_fork_history_test.go |
| harness-events | designed-out | agent/harness/agentharness/events_test.go, pico3/watch_test.go |
| harness-generation, -generation-recovery | designed-out | pico3/turn_test.go, recovery_test.go, agent/harness/runtime drive tests |
| harness-inbox | designed-out | pico3/busy_test.go, agent/harness/runtime/port_wave_03_lane_test.go |
| harness-inspect | designed-out | pico3/reads_test.go |
| harness-live-deltas | designed-out | pico3/view_streaming_test.go, delta_bounds_test.go |
| harness-output | designed-out | pico3/tool_bounds_test.go, agent/harness/utils/output_capture_test.go |
| harness-ownership | designed-out | pico3/subagent_test.go, busy_test.go |
| harness-prompt | designed-out | agent/harness/system_prompt_upstream_test.go |
| harness-registry | designed-out | pico3/spec_registry_lifecycle_test.go |
| harness-structured, -tasks, -tasks-recovery | designed-out | pico3/spec_scheduler_process_test.go, waiters_test.go, scheduler_resume_test.go, recovery_test.go |
| harness-submissions | designed-out | agent/harness/runtime/port_wave_02_accept_test.go |
| harness-tools, -tools-recovery | designed-out | pico3/kinds_tools_test.go, recovery_test.go, agent/harness/execution/port_wave_01_execution_tools_test.go |
| harness-view | designed-out | pico3/view_streaming_test.go, watch_test.go |
| jsonl-storage, memory-storage | designed-out | agent/harness/session/{jsonl_test,memory_conformance_test}.go |
| session-checkpoints-migrations, -definitions, -documents, -forks, -states, -tables, -watches | designed-out | pico3/spec_storage_history_test.go, define_service_test.go, transactions/atomicity/fork-history/chord-lifecycle/watch tests |
| sqlite-facade, sqlite-migrations, sqlite-storage | designed-out | none (PiG has no SQLite durable storage) |
| storage-runtime-boundary, types | designed-out | none |
| tools | designed-out | agent/harness/tools/tools_upstream_test.go, internal/codingagent/tools/{edit_diff,bash}_test.go |

Failing ports: none (nothing is ported). Production fixes: none. Stubs for other families: none (the earlier `durable/` stubs are removed).

## Checks

`make test-inventory` (`go run ./test/parity/cmd/testinventorycheck`): OK, 80 designed-out, 169 pending (was 207 pending). `make test-porting-release` still fails on rows owned by other lanes; it names no durable row.
