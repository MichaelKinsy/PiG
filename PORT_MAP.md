# PORT_MAP

Minimal upstream to pig file map.

- `PORT_MAP.md` answers where code lives
- `parity/coverage.md` answers what is verified and at what quality
- status here means code mapping only, not parity proof

Status: `✅` ported, `🟡` partial, `⬜` not started, `⏸` deferred, `🔴` broken, `n/a` not applicable.

Verification quality is tracked by the parity system, not by this status column. A row can be `✅` and still weak-only or untested in `parity/coverage.md`. Boot-only, smoke-only, and deferred scenarios do not count as behavioral verification. Registration-only scenarios count only for registry/catalog/auth-wiring paths. Provider implementation rows (for example `providers/openai-completions.ts`, `providers/openai-responses*.ts`, `providers/transform-messages.ts`) require stream/payload/history behavior tests; `--list-models` can only cover registry/catalog wiring.

## Version and gates

`coding/pigversion/pigversion.go` is the sole version pin; `coding/upstream.go` re-exports it as `coding.UpstreamVersion` and `coding.UpstreamCommit`. `tests/upstream-parity/mirror_version_test.go` rejects a `.upstream/current` mirror with a different package version. This map intentionally does not copy the version number.

`make port-map-drift` rejects source files missing from the map and any row, whatever its status, whose upstream file disappeared. `make upstream-delta` rejects changed source files without an explicit disposition and durable evidence. `make async-contracts` rejects Promise/async source files without a reviewed ordering, cancellation, error, concurrency, and loop-ownership contract. `make coverage-strict` rejects partial, broken, not-started, weak-only, and untested intended ports. `make family-gaps STRICT=1` rejects intended ports with no behavior family and family entries without acceptable behavioral evidence. `make foundation-check` runs those completeness gates after the normal parity gate.

Release history and open work belong in the tracked sync plan and generated coverage report, not this map.

## Package scope

The current PiG coding-agent release scope covers upstream `packages/agent`,
`packages/ai`, `packages/coding-agent`, and `packages/tui`. The sections below
map every tracked source file in those packages.

The following upstream monorepo packages are outside this four-package file
denominator. PiG now contains experimental adapters and selected shared
primitives from some of them; their presence does not establish complete package
parity or add their files to the percentage:

- `packages/client`, `packages/protocol`, `packages/server`, and
  `packages/session-backends` implement the experimental remote Session stack;
- `packages/chord` is an independent application-composition runtime; selected context/service implementations exist, but this denominator does not certify its complete plugin, replicated-state, RPC, or bundler surface;
- `packages/durable` currently publishes the detached Pico runtime and records the unimplemented Pico5 design. PiG tracks the active coding-agent Session behavior instead;
- `packages/telemetry` provides the standalone telemetry library;
- `packages/evals` provides upstream evaluation tooling.

Their omission is a metric scope boundary, not evidence of absence or complete
implementation. Add a package to the interface denominator and map all of its
source files before including it in a package-wide completeness claim.

The drift gate also rejects explicit production comments claiming to port an
upstream file while its row remains not-started, deferred, or n/a. Such conflicts
require reviewed mappings; code presence alone never automatically earns ✅.

**Windows port: windows/amd64 implementation mapping.** The following entries identify platform-specific code. They do not establish native execution or full Windows parity:
- `ai/auth.go`: auth.json lock via `LockFileEx`/`UnlockFileEx` (upstream `flock` equivalent).
- `tui/terminal.go`: raw mode over `CONIN$`/`CONOUT$`; ANSI via
  `ENABLE_VIRTUAL_TERMINAL_PROCESSING | DISABLE_NEWLINE_AUTO_RETURN`; terminal resize by polling
  `GetConsoleScreenBufferInfo` (a pig-internal mechanism under upstream's observable resize→re-render;
  windows has no SIGWINCH); `DrainInput` is a documented no-op.
- `internal/codingagent/interactive.go`: SIGHUP not registered (Go maps console-close to SIGTERM);
  resize watcher polls; Ctrl+Z suspend shows upstream's "not supported" status.
- `internal/codingagent/keybindings.go`: `ctrl+z` unbound on windows (upstream
  `win32 ? [] : "ctrl+z"`).
- `internal/codingagent/tools/bash_executor.go`: process-tree kill via `taskkill /F /T`
  (upstream `killProcessTree`).
- `internal/codingagent/tools/shell_config.go`: windows bash resolution: Git Bash at
  `%ProgramFiles%\Git\bin\bash.exe` (then x86), then `bash.exe` on PATH, else a helpful
  "install Git for Windows" error (upstream `getShellConfig` win32 branch).
- `coding/extension/exec.go`: `CREATE_NEW_PROCESS_GROUP` for detach.

The tmux scenario harness cannot drive a Windows `.exe`. Windows verification requires tests executed on Windows for auth locking, terminal start/stop and resize, keybindings, local-socket extension hosting, and child-process cancellation, plus an interactive pass covering render, input, resize, Ctrl+C, Ctrl+Z, and clean exit. Cross-compilation proves build compatibility only. Record native execution evidence against the tested commit; this map does not assert that those checks have passed. Unix scenarios cover only their exercised shared behavior.

## `packages/agent/src/`

| upstream | pig | status |
|---|---|---|
| `packages/agent/src/agent.ts` | `agent/agent.go + agent/queue.go` | ✅ |
| `packages/agent/src/agent-loop.ts` | `agent/agent_loop.go + agent/tool_execution.go + agent/system_state.go` | ✅ |
| `packages/agent/src/types.ts` | `agent/types.go + agent/agent.go` | ✅ |
| `packages/agent/src/proxy.ts` | `agent/proxy.go (partial: request/event reconstruction covered by agent/proxy_test.go; malformed terminal-event suppression remains under MA-007 review)` | 🟡 |
| `packages/agent/src/index.ts` | `(Go has no barrel exports)` | n/a |
| `packages/agent/src/node.ts` | `(Node-only package entrypoint)` | n/a |
| `packages/agent/src/harness/messages.ts` | `agent/messages.go + agent/transform.go` | ✅ |
| `packages/agent/src/harness/prompt-templates.ts` | `internal/codingagent/prompt_templates.go` | ✅ |
| `packages/agent/src/harness/system-prompt.ts` | `internal/codingagent/prompts/coding.go` | ✅ |
| `packages/agent/src/harness/types.ts` | `agent/harness/types.go (Skill, PromptTemplate, resources, AgentHarnessTool + invocation/update contracts, stream options, FileSystem/Shell/ExecutionEnv, file/execution/compaction/branch-summary errors, shell-output view/update JSON; Result/ok/err/getOrThrow/toError are Go (T, error); unit-tested)` | ✅ |
| `packages/agent/src/harness/agent-harness.ts` | `coding/session.go + coding/runtime.go` | ✅ |
| `packages/agent/src/harness/tools/bash.ts` | `internal/codingagent/tools/bash.go + shell_tool.go + bash_operations.go (SDK exec primitive; collapsed with coding-agent wrapper)` | ✅ |
| `packages/agent/src/harness/tools/edit-diff.ts` | `internal/codingagent/tools/edit_diff.go + unified_patch.go + diff_string.go` | ✅ |
| `packages/agent/src/harness/tools/edit.ts` | `internal/codingagent/tools/edit.go` | ✅ |
| `packages/agent/src/harness/tools/file-mutation-queue.ts` | `internal/codingagent/tools/mutation_queue.go` | ✅ |
| `packages/agent/src/harness/tools/image.ts` | `internal/codingagent/tools/image_detect.go + internal/imageprocessing/images.go (detectSupportedImageMimeType, isBmp)` | ✅ |
| `packages/agent/src/harness/tools/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/tools/path-utils.ts` | `internal/codingagent/tools/path_utils.go` | ✅ |
| `packages/agent/src/harness/tools/read.ts` | `internal/codingagent/tools/read.go` | ✅ |
| `packages/agent/src/harness/tools/tool-context.ts` | `(1-field ExecutionToolContext interface; pig threads ExecutionEnv directly)` | n/a |
| `packages/agent/src/harness/tools/write.ts` | `internal/codingagent/tools/write.go` | ✅ |
| `packages/agent/src/harness/skills.ts` | `internal/codingagent/skills.go` | ✅ |
| `packages/agent/src/harness/compaction/compaction.ts` | `internal/codingagent/compaction/compaction.go` | ✅ |
| `packages/agent/src/harness/compaction/utils.ts` | `internal/codingagent/compaction/utils.go` | ✅ |
| `packages/agent/src/harness/compaction/branch-summarization.ts` | `internal/codingagent/compaction/branch_summarization.go` | ✅ |
| `packages/agent/src/harness/utils/shell-output.ts` | `internal/codingagent/tools/truncate.go + internal/codingagent/tool_render.go` | ✅ |
| `packages/agent/src/harness/utils/truncate.ts` | `internal/codingagent/tools/truncate.go` | ✅ |
| `packages/agent/src/harness/env/nodejs.ts` | `agent/harness/env/{env,paths,errors,shell,exec,line_reader,process_unix,process_windows}.go and internal/nodeurl/fileurl.go (NodeExecutionEnv: host-OS FileSystem + Shell; ~ and file:// path resolution, errno to FileErrorCode with Node-shaped messages, LF TextLineReader, temp dirs/files, bash exec with timeout/env layering/bounded OutputCapture/raw-byte spill/process-group kill/ctx abort/cleanup; every nodejs-env.test.ts and text-line-reader.test.ts case ported)` | ✅ |
| `packages/agent/src/harness/session/session.ts` | `agent/harness/session/session.go (StorageBackedSession mutation barrier and Branch API; caller and race tests)` | ✅ |
| `packages/agent/src/harness/result.ts` | `agent/harness/result.go (LaneBusy, OperationMismatch, NoActiveRun, NoActiveOperation, NothingToResume, NothingToCompact, InvalidMessage, InvalidNavigation, UnknownSkill, UnknownTemplate, UnknownTarget, InvalidLane, Closed with the upstream toJSON shape; HarnessFault, HarnessClosed; the Result monad and matchError are Go (T, error) with errors.As; JSON shapes probed against upstream under Node 24)` | ✅ |
| `packages/agent/src/harness/session/context.ts` | `agent/harness/session/context.go (latest compaction, retained tail, filtered assistant responses, ordered projectors; unit tests)` | ✅ |
| `packages/agent/src/harness/session/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/session/jsonl/codec.ts` | `agent/harness/session/jsonl_codec.go (header validation and transaction roundtrip; TestJsonlHeaderValidation, TestJsonlStorageRoundTripAndWholeListDeletion)` | ✅ |
| `packages/agent/src/harness/session/jsonl/repo.ts` | `agent/harness/session/jsonl_repo.go (cwd discovery, exclusive handles, forks; TestJsonlSessionRepoConformance, TestJsonlRepositoryCwdDiscoveryAndEncodedIDs)` | ✅ |
| `packages/agent/src/harness/session/jsonl/storage.ts` | `agent/harness/session/jsonl_storage.go (atomic transaction persistence and recovery; TestJsonlStorageConformance, TestJsonlTornTailAndCompleteCorruption, TestJsonlAppendFailureDoesNotAdvanceLiveState)` | ✅ |
| `packages/agent/src/harness/session/jsonl/types.ts` | `agent/harness/session/jsonl_types.go (format headers and repo options; TestJsonlHeaderValidation, TestJsonlRepositoryCwdDiscoveryAndEncodedIDs)` | ✅ |
| `packages/agent/src/harness/session/memory.ts` | `agent/harness/session/memory.go (in-memory storage/repo; current Storage and SessionRepo conformance)` | ✅ |
| `packages/agent/src/harness/session/testing/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/session/testing/types.ts` | `agent/harness/session/testing/types.go (type-only StorageFixture and ConformanceCase contracts; no executable upstream body; backend conformance does not independently prove this row)` | ✅ |
| `packages/agent/src/harness/session/types.ts` | `agent/harness/session/{types,operation,session}.go (current session and operation unions; upstream JSON fixtures and conformance)` | ✅ |
| `packages/agent/src/harness/telemetry.ts` | `agent/harness/telemetry.go + agent/harness/telemetry_schema.go + agent/harness/telemetry_schema_data.go (partial: span parenting, outcomes and schema fixtures covered by agent/harness/telemetry_test.go; full runtime integration not yet certified)` | 🟡 |
| `packages/agent/src/harness/config.ts` | `agent/harness/config.go (DefaultRetryPolicy, ValidateToolNames, ValidateRetryPolicy, ValidateCompactionSettings; unit-tested)` | ✅ |
| `packages/agent/src/harness/context.ts` | `agent/harness/context.go (partial: context values, cancellation and telemetry parent carriers covered by agent/harness/context_test.go; full dependent runtime integration not yet certified)` | 🟡 |
| `packages/agent/src/harness/events.ts` | `agent/harness/agentharness/events.go + agent/harness/agentharness/event_types.go (partial: event delivery and cloning covered by events_test.go and clone_test.go; full event decoding and error-reporting closure not certified, MA-006)` | 🟡 |
| `packages/agent/src/harness/execution/assistant.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/execution/effect-gate.ts` | `agent/harness/execution/effect_gate.go (partial: admission and abort lifecycle covered by agent/harness/execution/effect_gate_test.go; full execution pipeline integration not yet certified)` | 🟡 |
| `packages/agent/src/harness/execution/tools.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/hooks.ts` | `agent/harness/agentharness/hooks.go + agent/harness/agentharness/hook_types.go (partial: admission, mutation and ordering covered by hooks_test.go; full dependent runtime integration not yet certified)` | 🟡 |
| `packages/agent/src/harness/pico3/bash.ts` | `agent/harness/pico3/bash.go, bash_unix.go, bash_windows.go; tool_bounds_test.go` | ✅ |
| `packages/agent/src/harness/pico3/bounded.ts` | `agent/harness/pico3/bounded.go; tool_bounds_test.go` | ✅ |
| `packages/agent/src/harness/pico3/chord.ts` | `agent/harness/pico3/{chord,chord_service,chord_ops}.go (typed service tokens, scoped forwarding, bounded caller-supplied state publication; chord_test.go, chord_lifecycle_test.go)` | ✅ |
| `packages/agent/src/harness/pico3/context.ts` | `agent/harness/pico3/context.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/harness.ts` | `agent/harness/pico3/harness.go, conversation.go; lifecycle_test.go, transactions_test.go` | ✅ |
| `packages/agent/src/harness/pico3/hooks.ts` | `agent/harness/pico3/hooks.go; tool_bounds_test.go, turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/index.ts` | `agent/harness/pico3/ (export surface correspondence; upstream re-exports implementations from sibling files; their behavior tests belong to the owning sibling rows, not blanket barrel coverage)` | ✅ |
| `packages/agent/src/harness/pico3/jsonl.ts` | `agent/harness/pico3/jsonl.go; atomicity_test.go, lifecycle_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/collapse.ts` | `agent/harness/pico3/kinds_collapse.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/entries.ts` | `agent/harness/pico3/kinds_common.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/frames.ts` | `agent/harness/pico3/kinds_frames.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/generation.ts` | `agent/harness/pico3/kinds_generation.go, kinds_generation_classify.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/job.ts` | `agent/harness/pico3/kinds_job.go; lifecycle_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/plugin.ts` | `agent/harness/pico3/kinds_plugin.go; lifecycle_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/post-tools.ts` | `agent/harness/pico3/kinds_post_tools.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/task-api.ts` | `agent/harness/pico3/kinds_task_api.go; lifecycle_test.go` | ✅ |
| `packages/agent/src/harness/pico3/kinds/tool.ts` | `agent/harness/pico3/kinds_tool.go; tool_bounds_test.go, atomicity_test.go` | ✅ |
| `packages/agent/src/harness/pico3/membrane.ts` | `agent/harness/pico3/membrane.go; membrane_test.go` | ✅ |
| `packages/agent/src/harness/pico3/memory.ts` | `agent/harness/pico3/memory.go; storage_test.go, turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/scheduler.ts` | `agent/harness/pico3/scheduler.go; lifecycle_test.go, waiters_test.go` | ✅ |
| `packages/agent/src/harness/pico3/session.ts` | `agent/harness/pico3/session.go, session_docs.go, tx.go, tx_writes.go; transactions_test.go` | ✅ |
| `packages/agent/src/harness/pico3/system.ts` | `agent/harness/pico3/system.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/types.ts` | `agent/harness/pico3/types.go, kind.go, runtime.go, json.go; transactions_test.go, lifecycle_test.go` | ✅ |
| `packages/agent/src/harness/pico3/view.ts` | `agent/harness/pico3/view.go; watch_test.go` | ✅ |
| `packages/agent/src/harness/runtime/drive.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/boundary.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/checkpoint.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/deferred.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/generation.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/reconcile.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/recovery.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/response.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/retry.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/structural.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/terminal.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/tool-placement.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/drive/tools.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/harness.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/runtime/lane.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/progress.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/reducer.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/restore.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/transcript.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/agent/src/harness/runtime/types.ts` | `agent/harness/runtime/types.go (partial: agent/harness/runtime/types_test.go TestDrive* tests cover context detachment with retained values, settle-once completion, abort admission and close behavior; queue/event shapes, full runtime wire decoding and callers not yet certified)` | 🟡 |
| `packages/agent/src/harness/session/commit.ts` | `agent/harness/session/commit.go (sequenced mixed-write validation; Storage conformance)` | ✅ |
| `packages/agent/src/harness/session/fork-policy.ts` | `agent/harness/session/fork_policy.go (branch ancestry and current-state projection; fork conformance)` | ✅ |
| `packages/agent/src/harness/session/fork.ts` | `agent/harness/session/fork.go (snapshot projection; TestCreateForkSnapshotMatchesUpstream executes 15 pinned-source golden cases)` | ✅ |
| `packages/agent/src/harness/session/in-memory-storage-state.ts` | `agent/harness/session/in_memory_storage_state.go (atomic entries, values, lists, usage, scans and forks; Storage conformance)` | ✅ |
| `packages/agent/src/harness/session/jsonl/fork.ts` | `agent/harness/session/jsonl_fork.go (two-pass fork projection and high-water boundaries; TestJsonlSessionRepoConformance, TestJsonlLegacyForkClosedAndUpgradeOpen)` | ✅ |
| `packages/agent/src/harness/session/jsonl/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/session/jsonl/io.ts` | `agent/harness/session/jsonl_io.go (atomic publication with cleanup; TestJsonlAtomicPublicationFailuresAndRetry)` | ✅ |
| `packages/agent/src/harness/session/jsonl/legacy-v3.ts` | `agent/harness/session/jsonl_legacy.go (streaming legacy normalization and atomic upgrade; TestJsonlLegacyUpgradeFailurePreservesStateAndUsage, TestJsonlLegacySelectedCompactionsAndCapturedPrefix, TestJsonlLegacyTailProjectsCustomAndSummaryNodes)` | ✅ |
| `packages/agent/src/harness/session/mutation-line.ts` | `agent/harness/session/mutation_line.go (FIFO mutation admission and seal/drain; race tests)` | ✅ |
| `packages/agent/src/harness/session/testing/benchmark/datasets.ts` | `agent/harness/session/testing/benchmark/datasets.go (deterministic workloads; benchmark scenario tests)` | ✅ |
| `packages/agent/src/harness/session/testing/benchmark/session-repo.ts` | `agent/harness/session/testing/benchmark/session_repo.go (repository benchmark scenarios; result assertions)` | ✅ |
| `packages/agent/src/harness/session/testing/benchmark/storage.ts` | `agent/harness/session/testing/benchmark/storage.go (storage benchmark scenarios; result assertions)` | ✅ |
| `packages/agent/src/harness/session/testing/conformance/session-repo.ts` | `agent/harness/session/testing/conformance/session_repo.go (current repository contract; Memory backend)` | ✅ |
| `packages/agent/src/harness/session/testing/conformance/storage.ts` | `agent/harness/session/testing/conformance/storage.go (current Storage contract; Memory backend)` | ✅ |
| `packages/agent/src/harness/session/testing/gating-storage.ts` | `agent/harness/session/testing/gating_storage.go (park, release and discard; decorator tests)` | ✅ |
| `packages/agent/src/harness/session/testing/instrumented-storage.ts` | `agent/harness/session/testing/instrumented_storage.go (admission recording; decorator tests)` | ✅ |
| `packages/agent/src/harness/session/testing/storage-decorator.ts` | `agent/harness/session/testing/storage_decorator.go (transparent forwarding; decorator tests)` | ✅ |
| `packages/agent/src/harness/session/values.ts` | `agent/harness/session/values.go (typed addresses, exact optional prefixes and writes; unit tests)` | ✅ |
| `packages/agent/src/harness/utils/adaptive-publisher.ts` | `agent/harness/utils/adaptive_publisher.go (size-paced latest-state publication with one trailing timer; deliveries serialized outside the state lock; upstream cases on a fake clock plus reentrancy/error/dispose cases)` | ✅ |
| `packages/agent/src/harness/utils/output-capture.ts` | `agent/harness/utils/output_capture.go + utf8_stream.go (OutputCapture, ApplyShellOutputUpdate, SanitizeShellOutput; WHATWG-style streaming UTF-8 decode; slide offsets in UTF-16 code units; upstream cases plus a -race wall-clock fold test)` | ✅ |
| `packages/agent/src/harness/utils/usage.ts` | `agent/harness/utils/usage.go (EmptyUsage, AddUsage with optional-field absence; unit-tested)` | ✅ |
| `packages/agent/src/search/index.ts` | `agent/search/search.go (API/type correspondence only; compile-signature checks in agent/search/search_test.go; upstream defines no backend behavior)` | ✅ |

## `packages/ai/src/`

| upstream | pig | status |
|---|---|---|
| `packages/ai/src/types.ts` | `ai/types.go` | ✅ |
| `packages/ai/src/models.generated.ts` | `ai/models_generated.go` | ✅ |
| `packages/ai/src/image-models.generated.ts` | `ai/image_models_generated.go` | ✅ |
| `packages/ai/src/models.ts` | `ai/registry.go + ai/model_utils.go` | ✅ |
| `packages/ai/src/models-store.ts` | `ai/models_store.go (ModelsStoreEntry, ModelsStore, InMemoryModelsStore; ai/models_store_test.go, ai/radius_test.go TestRadiusProviderRestoresStoredCatalogOffline) + internal/codingagent/model_registry.go (dynamic provider/model store analogue)` | ✅ |
| `packages/ai/src/model-catalog.ts` | `designed out: pig flattens the API-grouped catalog at cmd/gen-models codegen into models_generated.go (collectRows); upstream flattenModelCatalog (Object.assign of group values) runs per-provider at module load, no Go runtime equivalent` | n/a |
| `packages/ai/src/providers/all.ts` | `ai/registry.go + ai/register_builtins.go + ai/images_registry.go` | ✅ |
| `packages/ai/src/api/anthropic-messages.ts` | `ai/anthropic.go + ai/anthropic_client.go + ai/constrained_sampling.go (createClient auth/header branches including the ai.PiUserAgent() default User-Agent from ai/user_agent.go (D65), betas, OAuth Claude Code identity, tool-name conversion, strict JSON-schema tools across current, initial, and deferred declarations, toolChoice, metadata.user_id, complete stop-reason and incomplete-stream errors, thinking_tokens usage, usage retention on error, explicit-zero temperature compatibility, managed mid-conversation effort with historical/current effort markers, adaptive drop-block binding, beta selection, and providerThinkingLevel, and native mid-conversation tool changes with tool_addition/tool_removal blocks, deferred placeholder/later tools, beta selection, and safe fallback to the current tool list; tests in ai/anthropic_oauth_test.go, ai/anthropic_test.go, ai/anthropic_contract_0861_test.go, ai/anthropic_effort_strict_test.go, ai/constrained_sampling_test.go, ai/transcript_tool_changes_test.go, and agent/native_tool_changes_test.go); missing: server-side fallback (allowedFallbackModels fallbacks param, server-side-fallback-2026-07-01 beta, fallback content block and cost)` | 🟡 |
| `packages/ai/src/api/constrained-sampling.ts` | `ai/constrained_sampling.go (grammar + canonical JSON-schema strict constrained sampling; wired into Anthropic, OpenAI Completions, OpenAI Responses, Mistral, Bedrock, and Google request paths; unit and provider-wire tests in ai/constrained_sampling_test.go, ai/anthropic_test.go, ai/openai_test.go, ai/openai_responses_constrained_test.go, ai/mistral_test.go, ai/bedrock_test.go, and ai/google_test.go)` | ✅ |
| `packages/ai/src/api/azure-openai-responses.ts` | `ai/azure_openai_responses.go` | ✅ |
| `packages/ai/src/api/bedrock-converse-stream.ts` | `ai/bedrock.go` | ✅ |
| `packages/ai/src/api/google-generative-ai.ts` | `ai/google.go` | ✅ |
| `packages/ai/src/api/google-vertex.ts` | `ai/google_vertex.go` | ✅ |
| `packages/ai/src/api/mistral-conversations.ts` | `ai/mistral.go` | ✅ |
| `packages/ai/src/api/anthropic-messages.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/azure-openai-responses.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/bedrock-converse-stream.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/google-generative-ai.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/google-vertex.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/mistral-conversations.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/openai-codex-responses.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/openai-completions.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/openai-responses.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/openrouter-images.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/pi-messages.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/auth/context.ts` | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers.go + ai/oauth_*.go` | ✅ |
| `packages/ai/src/auth/credential-store.ts` | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers.go + ai/oauth_*.go` | ✅ |
| `packages/ai/src/auth/helpers.ts` | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers.go + ai/oauth_*.go` | ✅ |
| `packages/ai/src/auth/resolve.ts` | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers.go + ai/oauth_*.go` | ✅ |
| `packages/ai/src/auth/types.ts` | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers.go + ai/oauth_*.go` | ✅ |
| `packages/ai/src/auth/oauth/anthropic.ts` | `ai/oauth_anthropic.go` | ✅ |
| `packages/ai/src/auth/oauth/device-code.ts` | `ai/oauth_device_code.go` | ✅ |
| `packages/ai/src/auth/oauth/github-copilot.ts` | `ai/githubcopilot.go` | ✅ |
| `packages/ai/src/auth/oauth/load.ts` | `ai/oauth_registry.go` | ✅ |
| `packages/ai/src/auth/oauth/kimi-coding.ts` | `ai/oauth_kimi.go (device flow and refresh ported; caller-path parity pending)` | ✅ |
| `packages/ai/src/auth/oauth/openrouter.ts` | `ai/oauth_openrouter.go (OpenRouter OAuth login/exchange; registered in oauth_registry.go; unit-tested + oauth/08 behavioral scenario)` | ✅ |
| `packages/ai/src/auth/oauth/oauth-page.ts` | `ai/oauth_page.go` | ✅ |
| `packages/ai/src/auth/oauth/openai-codex.ts` | `ai/oauth_openai_codex.go` | ✅ |
| `packages/ai/src/auth/oauth/pkce.ts` | `ai/pkce.go` | ✅ |
| `packages/ai/src/auth/oauth/radius.ts` | `ai/oauth_radius.go + internal/codingagent/interactive_auth.go (ai/oauth_radius_test.go, internal/codingagent/radius_login_test.go)` | ✅ |
| `packages/ai/src/auth/oauth/xai.ts` | `ai/oauth_xai.go (device-code login, polling, refresh, response validation, and owner-context cancellation; ai/oauth_xai_test.go including LoginContext/RefreshTokenContext cancellation, cmd/pig/model_oauth_refresh_test.go caller cancellation, oauth/09 behavioral scenario)` | ✅ |
| `packages/ai/src/bun-oauth.ts` | `(Bun-only OAuth flow registration; Go links OAuth implementations directly)` | n/a |
| `packages/ai/src/compat.ts` | `ai/types.go` | ✅ |
| `packages/ai/src/compat/extension-oauth-types.ts` | `ai/oauth_types.go + coding/extension/provider.go (type-only legacy OAuth callback and prompt contracts; runtime OAuth behavior belongs to the auth/provider rows)` | ✅ |
| `packages/ai/src/images-models.ts` | `ai/images_registry.go` | ✅ |
| `packages/ai/src/legacy-api-aliases.ts` | `(barrel/backward-compatible TS aliases)` | n/a |
| `packages/ai/src/providers/openai-codex.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (OpenAI Codex provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/openai.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (OpenAI provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/openrouter-images.ts` | `ai/images_registry.go + ai/openrouter_images.go` | ✅ |
| `packages/ai/src/utils/error-body.ts` | `ai/errors.go + ai/openai.go + ai/openai_responses.go` | ✅ |
| `packages/ai/src/utils/estimate.ts` | `ai/estimate.go (EstimateContextTokens, EstimateMessageTokens, CalculateContextTokens; estimate_test.go) + ai/simple_options.go (ClampMaxTokensToContext)` | ✅ |
| `packages/ai/src/utils/retry.ts` | `ai/assistant_retry.go (RetryAssistantCall, RetryDelayMs, IsRetryableAssistantError; assistant_retry_test.go)` | ✅ |
| `packages/ai/src/utils/provider-retry.ts` | `ai/provider_retry.go` | ✅ |
| `packages/ai/src/providers/amazon-bedrock.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/ant-ling.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/anthropic.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/azure-openai-responses.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/cerebras.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/cloudflare-ai-gateway.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/cloudflare-workers-ai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/deepseek.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/fireworks.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/github-copilot.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/google-vertex.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/google.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/groq.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/huggingface.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/kimi-coding.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/minimax-cn.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/minimax.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/mistral.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/moonshotai-cn.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/moonshotai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/nvidia.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/openai-codex.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/openai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/opencode-go.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/opencode.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/openrouter.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/qwen-token-plan.models.ts` | `ai/models_generated.go (0.81 catalog shard input)` | ✅ |
| `packages/ai/src/providers/qwen-token-plan-cn.models.ts` | `ai/models_generated.go (0.81 catalog shard input)` | ✅ |
| `packages/ai/src/providers/together.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/vercel-ai-gateway.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/xai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-ams.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-cn.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-sgp.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/xiaomi.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/zai-coding-cn.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/zai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)` | ✅ |
| `packages/ai/src/providers/ant-ling.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/cerebras.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/cloudflare-ai-gateway.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/cloudflare-auth.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/cloudflare-stream.ts` | `ai/cloudflare.go` | ✅ |
| `packages/ai/src/providers/cloudflare-workers-ai.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/data-json.d.ts` | `(TypeScript JSON import declaration only)` | n/a |
| `packages/ai/src/providers/deepseek.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/fireworks.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/github-copilot.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/groq.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/huggingface.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/kimi-coding.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/minimax-cn.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/minimax.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/moonshotai-cn.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/moonshotai.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/nvidia.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/opencode-go.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/opencode.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/openrouter.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/qwen-token-plan.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/qwen-token-plan-cn.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/radius-config.ts` | `ai/radius_config.go (ai/radius_config_test.go; parity/scenarios/model-runtime-store-catalog/11-radius-metadata-import.toml)` | ✅ |
| `packages/ai/src/providers/radius.ts` | `ai/radius.go + internal/codingagent/radius_models.go + internal/codingagent/radius_composition.go (ai/radius_test.go, internal/codingagent/radius_models_test.go, internal/codingagent/radius_composition_test.go, coding/radius_composition_test.go, coding/model_test.go; parity/scenarios/model-runtime-store-catalog/11-radius-metadata-import.toml)` | ✅ |
| `packages/ai/src/providers/together.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/vercel-ai-gateway.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/xai.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-ams.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-cn.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-sgp.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/xiaomi.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/zai-coding-cn.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/zai.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalog registration)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/status-indicator.ts` | `internal/codingagent/interactive_status.go + interactive_events.go + tui/status_indicator.go + tui/editor_status.go (working/compaction/retry border; branchSummary, summarization retry replacement, and session-clear lifecycle pending)` | 🟡 |
| `packages/coding-agent/src/rpc-entry.ts` | `(npm package export wrapper for --mode rpc; pig exposes the CLI mode directly)` | n/a |
| `packages/coding-agent/src/utils/image-process.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/ai/src/api/lazy.ts` | `(Go providers are statically linked; async setup errors surface through Provider.Stream errors)` | n/a |
| `packages/agent/src/stream-fn.ts` | `agent/agent.go + agent/agent_loop.go (StreamFn injection and model-provider fallback; PiG carries the provider runtime on each model instead of exposing Pi's package-global setDefaultStreamFn/getDefaultStreamFn fallback registry)` | 🟡 |
| `packages/ai/src/env-api-keys.ts` | `ai/auth_env_keys.go` | ✅ |
| `packages/ai/src/session-resources.ts` | `ai/session_resources.go + coding/session.go` | ✅ |
| `packages/ai/src/oauth.ts` | `ai/auth.go` | ✅ |
| `packages/ai/src/index.ts` | `ai/doc.go` | n/a |
| `packages/ai/src/cli.ts` | `cmd/pig/auth_commands.go (login/logout)` | ✅ |
| `packages/ai/src/bedrock-provider.ts` | `ai/bedrock.go` | ✅ |
| `packages/ai/src/api/openai-completions.ts` | `ai/openai.go (Kimi mid-conversation tool additions in tool-bearing system messages: ai/transcript_tool_changes_test.go)` | ✅ |
| `packages/ai/src/api/openai-responses.ts` | `ai/openai_responses.go` | ✅ |
| `packages/ai/src/api/openai-responses-shared.ts` | `ai/openai_responses.go (additional_tools and synthetic tool_search tool additions: ai/transcript_tool_changes_test.go)` | ✅ |
| `packages/ai/src/api/openai-codex-responses.ts` | `ai/openai_codex_responses.go + ai/openai_codex_frames.go + ai/openai_codex_websocket.go + ai/openai_responses.go (Codex request shape, zstd SSE body, SSE/WS event mapping, account/session headers, PiUserAgent precedence, auto WebSocket transport, session/account-scoped continuation cache, cancellation, pre-output SSE fallback; request/terminal/transport regressions under ai/openai_codex_*_test.go); missing: Codex-specific SSE retry policy, friendly usage-limit errors, and request service-tier fallback` | 🟡 |
| `packages/ai/src/api/openai-prompt-cache.ts` | `ai/openai_prompt_cache.go` | ✅ |
| `packages/ai/src/api/pi-messages.ts` | `ai/pi_messages.go + ai/pi_messages_events.go (ai/pi_messages_test.go, coding/model_test.go TestPiMessagesModelsJSONProviderStreamsThroughModelRuntime)` | ✅ |
| `packages/ai/src/providers/anthropic.ts` | `ai/anthropic.go + ai/anthropic_client.go + ai/oauth_anthropic.go + ai/auth_providers.go + internal/codingagent/model_registry.go + coding/model.go + cmd/pig/model.go (catalog; env auth in upstream order: ANTHROPIC_AUTH_TOKEN as an Authorization bearer header, then ANTHROPIC_OAUTH_TOKEN, then ANTHROPIC_API_KEY; subscription OAuth login; stored OAuth and api_key credentials outrank ambient auth and reach serialized requests on both SDK and CLI model paths; tests in ai/anthropic_oauth_test.go, internal/codingagent/anthropic_env_auth_test.go, coding/anthropic_auth_test.go, cmd/pig/model_anthropic_auth_test.go)` | ✅ |
| `packages/ai/src/providers/google.ts` | `ai/google.go` | ✅ |
| `packages/ai/src/api/google-shared.ts` | `ai/google.go` | ✅ |
| `packages/ai/src/providers/google-vertex.ts` | `ai/google_vertex.go` | ✅ |
| `packages/ai/src/providers/amazon-bedrock.ts` | `ai/bedrock.go` | ✅ |
| `packages/ai/src/api/cloudflare.ts` | `ai/cloudflare.go` | ✅ |
| `packages/ai/src/providers/azure-openai-responses.ts` | `ai/azure_openai_responses.go` | ✅ |
| `packages/ai/src/providers/mistral.ts` | `ai/mistral.go` | ✅ |
| `packages/ai/src/api/github-copilot-headers.ts` | `ai/githubcopilot.go` | ✅ |
| `packages/ai/src/providers/faux.ts` | `ai/faux.go` | ✅ |
| `packages/ai/src/api/transform-messages.ts` | `agent/transform.go` | ✅ |
| `packages/ai/src/providers/images/register-builtins.ts` | `ai/images_registry.go` | ✅ |
| `packages/ai/src/api/openrouter-images.ts` | `ai/openrouter_images.go` | ✅ |
| `packages/ai/src/api/simple-options.ts` | `ai/simple_options.go` | ✅ |
| `packages/ai/src/utils/event-stream.ts` | `ai/openai.go (inline SSE)` | ✅ |
| `packages/ai/src/utils/provider-env.ts` | `ai/provider_env.go (Bun empty process.env /proc fallback n/a in Go)` | ✅ |
| `packages/ai/src/images.ts` | `ai/images.go` | ✅ |
| `packages/ai/src/image-models.ts` | `ai/images_registry.go` | ✅ |
| `packages/ai/src/images-api-registry.ts` | `ai/images.go + ai/images_registry.go` | ✅ |
| `packages/ai/src/utils/hash.ts` | `ai/openai.go shortHash32 (UTF-16 code units with uint32 Math.imul wrap), used by ai/openai.go normalizeCompletionsToolCallID, ai/openai_responses.go foreign fc_ ids, and ai/mistral.go deriveMistralToolCallID (upstream oracles in ai/openai_responses_foreign_toolcall_test.go and ai/mistral_test.go)` | ✅ |
| `packages/ai/src/utils/headers.ts` | `ai/githubcopilot.go` | ✅ |
| `packages/ai/src/utils/json-parse.ts` | `(stdlib encoding/json)` | n/a |
| `packages/ai/src/utils/diagnostics.ts` | `agent/messages.go + ai/types.go` | ✅ |
| `packages/ai/src/utils/node-http-proxy.ts` | `ai/http_transport.go (http.ProxyFromEnvironment)` | ✅ |
| `packages/ai/src/utils/overflow.ts` | `ai/overflow.go (IsContextOverflow, IsRecoverableLength, GetOverflowPatterns; overflow_test.go)` | ✅ |
| `packages/ai/src/utils/text.ts` | `agent/context_tokens.go + internal/codingagent/export/tool_renderer.go` | ✅ |
| `packages/ai/src/utils/uuid.ts` | `(stdlib crypto/rand UUID helpers inline)` | n/a |
| `packages/ai/src/utils/abort-signals.ts` | `(stdlib context.Context combines signals)` | n/a |
| `packages/ai/src/utils/sanitize-unicode.ts` | `internal/codingagent/tools/sanitize.go` | ✅ |
| `packages/ai/src/utils/typebox-helpers.ts` | `(no TypeBox in Go)` | n/a |
| `packages/ai/src/utils/validation.ts` | `agent/validate.go` | ✅ |
| `packages/ai/src/providers/baseten.models.ts` | `(not ported: Baseten provider declined)` | n/a |
| `packages/ai/src/providers/baseten.ts` | `(not ported: Baseten provider declined)` | n/a |
| `packages/ai/src/utils/abort.ts` | `(AbortSignal/Promise racing: raceWithAbortSignal/operationSignal; pig uses context.Context cancellation + select (D3): runtime mechanics, designed out)` | n/a |
| `packages/ai/src/api/cloudflare-ai-binding.ts` | `(designed out: fetch adapter over a Cloudflare Workers env.AI binding; PiG runs as a native process, not inside a Worker, so no AI binding exists; HTTPS AI Gateway stays in ai/cloudflare.go)` | n/a |
| `packages/ai/src/auth/oauth/meta.ts` | `ai/oauth_meta.go (device flow + Muse Code key mint, registered in ai/oauth_registry.go; meta-oauth.test.ts cases in ai/oauth_meta_test.go)` | ✅ |
| `packages/ai/src/providers/meta.models.ts` | `ai/models_generated.go (0.87.1 catalog shard; ai/meta_provider_test.go)` | ✅ |
| `packages/ai/src/providers/meta.ts` | `ai/models_generated.go + ai/openai_responses.go + ai/oauth_meta.go + ai/auth_env_keys.go (catalog, openai-responses request shape, Muse subscription OAuth, META_API_KEY discovery; ai/meta_provider_test.go + internal/codingagent/model_registry_test.go)` | ✅ |
| `packages/ai/src/providers/opencode-headers.ts` | `coding/model.go mergeProviderAttributionHeaders (x-opencode-session from StreamOptions.SessionID for opencode/opencode-go, case-insensitive caller override; coding/opencode_headers_0871_test.go)` | ✅ |
| `packages/ai/src/providers/qwen-token-plan-individual.models.ts` | `ai/models_generated.go (0.87.1 catalog shard; qwen-token-plan-models.test.ts cases in ai/qwen_token_plan_models_test.go)` | ✅ |
| `packages/ai/src/providers/qwen-token-plan-individual.ts` | `ai/models_generated.go + ai/openai.go + ai/auth_env_keys.go (catalog base URL, openai-completions, qwen enable_thinking + reasoning_effort payload; shared QWEN_TOKEN_PLAN_API_KEY discovery tested in ai/auth_env_keys_test.go + internal/codingagent/model_registry_test.go)` | ✅ |
| `packages/ai/src/providers/radius.models.ts` | `ai/models_generated.go (0.87.1 radius shard, served for the default gateway by ai/radius.go: ai/registry_test.go TestRuntimeDiscoveryIncludesRadiusCatalog, ai/radius_test.go TestRadiusProviderShipsPublicCatalogForDefaultGateway)` | ✅ |
| `packages/ai/src/utils/assistant-message-frame.ts` | `ai/assistant_message_frame.go (AssistantMessageFrameEncoder, ReduceAssistantMessageFrames; upstream test cases in ai/assistant_message_frame_test.go)` | ✅ |
| `packages/ai/src/utils/pi-user-agent.ts` | `ai/user_agent.go (ai.PiUserAgent(): pig/<coding.Version> (<platform> <release>; <arch>), PiG's product name over upstream's "pi"/"pi (browser)" (D65); ai/user_agent_unix.go + ai/user_agent_windows.go derive release from uname/RtlGetVersion; wired as the default User-Agent in ai/anthropic_client.go, ai/openai.go, ai/openai_responses.go (also used by azure-openai-responses.ts and openai-codex-responses.ts), ai/google.go (also used by google-vertex.ts), and ai/mistral.go; tests in ai/user_agent_test.go and ai/anthropic_oauth_test.go)` | ✅ |
| `packages/ai/src/utils/sleep.ts` | `ai/provider_retry.go abortableSleep (context-cancelled timer: immediate rejection when already aborted, rejection on abort mid-wait; ai/sleep_test.go)` | ✅ |
| `packages/ai/src/utils/transcript.ts` | `ai/transcript.go (system-message-replay.test.ts cases and provider fold payloads for anthropic-messages, openai-responses, openai-completions in ai/system_message_replay_test.go; resolveTranscriptTools and the native transcript-tool-changes.test.ts cases in ai/transcript_tool_changes_test.go)` | ✅ |

## `packages/coding-agent/src/`

| upstream | pig | status |
|---|---|---|
| `packages/coding-agent/src/cli.ts` | `cmd/pig/main.go` | ✅ |
| `packages/coding-agent/src/main.ts` | `cmd/pig/main.go` | ✅ |
| `packages/coding-agent/src/index.ts` | `(Go has no barrel exports; public APIs are mapped at their defining source rows)` | n/a |
| `packages/coding-agent/src/config.ts` | `internal/codingagent/paths.go` | ✅ |
| `packages/coding-agent/src/migrations.ts` | `internal/codingagent/migrations.go` | ✅ |
| `packages/coding-agent/src/package-manager-cli.ts` | `cmd/pig/package_commands.go + cmd/pig/config_command.go + coding/packagecontent/packagecontent.go` | ✅ |
| `packages/coding-agent/src/cli/args.ts` | `cmd/pig/args.go` | ✅ |
| `packages/coding-agent/src/cli/initial-message.ts` | `cmd/pig/main.go` | ✅ |
| `packages/coding-agent/src/cli/file-processor.ts` | `internal/codingagent/file_processor.go` | ✅ |
| `packages/coding-agent/src/cli/list-models.ts` | `internal/codingagent/settings.go` | ✅ |
| `packages/coding-agent/src/cli/config-selector.ts` | `cmd/pig/config_command.go + tui/config_selector.go` | ✅ |
| `packages/coding-agent/src/cli/credential-print.ts` | `cmd/pig/credential_print.go + internal/codingagent/request_auth_runtime.go (request-auth surface: getProviders/getAuth/listCredentials with OAuth refresh+persist) + ai/auth_resolve.go` | ✅ |
| `packages/coding-agent/src/cli/session-picker.ts` | `internal/codingagent/session_selector.go` | ✅ |
| `packages/coding-agent/src/cli/project-trust.ts` | `cmd/pig/project_trust.go` | ✅ |
| `packages/coding-agent/src/cli/startup-ui.ts` | `internal/codingagent/startup_ui.go (ShowStartupSelector/ShowStartupInput/SelectStartupSession; startStartupTui color-scheme + OSC 11 background query with reply consumption). First-time setup is designed out: upstream shouldRunFirstTimeSetup returns false unless package @earendil-works/pi-coding-agent, app pi and .pi config dir, so it never runs for this binary` | ✅ |
| `packages/coding-agent/src/core/sdk.ts` | `coding/session.go + coding/runtime.go + coding/services.go` | ✅ |
| `packages/coding-agent/src/core/agent-session.ts` | `coding/session.go + ai/assistant_retry.go + internal/codingagent/auto_recovery.go (coding/session_zero_usage_compaction_test.go; coding/session_dns_retry_test.go; ai/assistant_retry_test.go; internal/codingagent/classifier_parity_test.go)` | ✅ |
| `packages/coding-agent/src/core/agent-session-runtime.ts` | `partial: coding/runtime.go, coding/session.go, and cmd/pig/rpc_mode.go; Session replacement retains the host-scoped extension runner (D30) and startup-project Services and Resources (D61)` | 🟡 |
| `packages/coding-agent/src/core/agent-session-services.ts` | `coding/services.go` | ✅ |
| `packages/coding-agent/src/core/auth-storage.ts` | `ai/auth.go + ai/auth_store.go (CredentialStore, ReadOnlyAuthStorage, in-memory store)` | ✅ |
| `packages/coding-agent/src/core/auth-guidance.ts` | `internal/codingagent/auth_guidance.go` | ✅ |
| `packages/coding-agent/src/core/bash-executor.ts` | `internal/codingagent/tools/bash_executor.go` | ✅ |
| `packages/coding-agent/src/core/cache-stats.ts` | `internal/codingagent/cache_stats.go` | ✅ |
| `packages/coding-agent/src/core/defaults.ts` | `internal/codingagent/defaults.go` | ✅ |
| `packages/coding-agent/src/core/diagnostics.ts` | `cmd/pig/diagnose.go` | ✅ |
| `packages/coding-agent/src/core/trust-manager.ts` | `internal/codingagent/trust_manager.go (unit-tested + project-trust/01 behavioral scenario covers it)` | ✅ |
| `packages/coding-agent/src/core/project-trust.ts` | `cmd/pig/project_trust.go + internal/codingagent/trust_manager.go` | ✅ |
| `packages/coding-agent/src/core/experimental.ts` | `internal/codingagent/experimental.go` | ✅ |
| `packages/coding-agent/src/core/event-bus.ts` | `coding/extension/eventbus.go` | ✅ |
| `packages/coding-agent/src/core/http-dispatcher.ts` | `ai/http_transport.go + internal/codingagent/settings.go + cmd/pig/main.go` | ✅ |
| `packages/coding-agent/src/core/exec.ts` | `coding/extension/exec.go` | ✅ |
| `packages/coding-agent/src/core/footer-data-provider.ts` | `internal/codingagent/status_line.go` | ✅ |
| `packages/coding-agent/src/core/keybindings.ts` | `internal/codingagent/keybindings.go` | ✅ |
| `packages/coding-agent/src/core/messages.ts` | `agent/messages.go` | ✅ |
| `packages/coding-agent/src/core/model-config.ts` | `internal/codingagent/model_registry.go` | ✅ |
| `packages/coding-agent/src/core/model-registry.ts` | `internal/codingagent/model_registry.go` | ✅ |
| `packages/coding-agent/src/core/model-runtime.ts` | `internal/codingagent/model_registry.go + coding/services.go + internal/codingagent/interactive.go + internal/codingagent/request_auth_runtime.go (request-auth surface)` | ✅ |
| `packages/coding-agent/src/core/models-store.ts` | `ai/models_store.go (FileModelsStore; ai/models_store_test.go) + internal/codingagent/model_registry.go + internal/codingagent/radius_models.go (<agentDir>/models-store.json)` | ✅ |
| `packages/coding-agent/src/core/provider-composer.ts` | `coding/extension/provider.go + internal/codingagent/model_registry.go + internal/codingagent/request_auth_runtime.go (auth composition)` | ✅ |
| `packages/coding-agent/src/core/radius.ts` | `internal/codingagent/radius.go (internal/codingagent/radius_test.go)` | ✅ |
| `packages/coding-agent/src/core/remote-catalog-provider.ts` | `(not implemented: owner-approved remote catalog refresh through PiG's own endpoint; runtime refresh, caching, and fallback remain unverified)` | ⬜ |
| `packages/coding-agent/src/core/runtime-credentials.ts` | `ai/runtime_credentials.go + internal/codingagent/model_registry.go` | ✅ |
| `packages/coding-agent/src/core/usage-totals.ts` | `internal/codingagent/session_stats.go + internal/codingagent/status_line.go + internal/codingagent/slash_commands.go` | ✅ |
| `packages/coding-agent/src/core/model-resolver.ts` | `cmd/pig/model.go + cmd/pig/startup_model.go + cmd/pig/resolve_cli_model.go + internal/codingagent/model_registry_runtime_models.go` | ✅ |
| `packages/coding-agent/src/core/output-guard.ts` | `internal/codingagent/output_guard.go` | ✅ |
| `packages/coding-agent/src/core/package-manager.ts` | `coding/packagecontent/packagecontent.go, cmd/pig/package_commands.go, cmd/pig/package_updates.go, cmd/pig/configured_resources.go, internal/codingagent/settings.go, cmd/pig/config_command.go` | ✅ |
| `packages/coding-agent/src/core/prompt-templates.ts` | `internal/codingagent/prompt_templates.go + prompt_diagnostics.go (file/YAML warnings reach interactive startup/reload and RPC; source labels, collision diagnostics, exact YAML error text and remaining loader semantics need correspondence)` | 🟡 |
| `packages/coding-agent/src/core/resolve-config-value.ts` | `internal/configvalue/configvalue.go` | ✅ |
| `packages/coding-agent/src/core/resource-loader.ts` | `coding/packagecontent/packagecontent.go + internal/codingagent/resources.go + cmd/pig/configured_resources.go + internal/codingagent/prompt_templates.go + internal/codingagent/skills.go + cmd/pig/config_command.go + cmd/pig/reload_resources.go + cmd/pig/resource_source_info.go + internal/codingagent/interactive.go + internal/codingagent/reload_resources.go` | ✅ |
| `packages/coding-agent/src/core/session-cwd.ts` | `internal/codingagent/session.go` | ✅ |
| `packages/coding-agent/src/core/session-manager.ts` | `internal/codingagent/session_manager.go + internal/codingagent/session_resume.go (session discovery, including directory symlinks)` | ✅ |
| `packages/coding-agent/src/core/settings-manager.ts` | `internal/codingagent/settings.go` | ✅ |
| `packages/coding-agent/src/core/skills.ts` | `internal/codingagent/skills.go` | ✅ |
| `packages/coding-agent/src/core/slash-commands.ts` | `internal/codingagent/slash_commands.go` | ✅ |
| `packages/coding-agent/src/core/source-info.ts` | `internal/codingagent/resource_source_info.go + cmd/pig/resource_source_info.go + cmd/pig/rpc_mode.go` | ✅ |
| `packages/coding-agent/src/core/system-prompt.ts` | `internal/codingagent/prompts/coding.go` | ✅ |
| `packages/coding-agent/src/core/telemetry.ts` | `internal/codingagent/settings.go (IsInstallTelemetryEnabled, isTruthyTelemetryEnvFlag, TestSettingsManager_IsInstallTelemetryEnabled) gates the attribution headers (D26) and internal/codingagent/install_telemetry.go (reportInstallTelemetry, recordChangelogVersionAndMaybeReportInstall), wired at the same two call sites as interactive-mode.ts:1265-1307 in internal/codingagent/interactive.go's Run, now sends the install/update ping to PiG's own endpoint (D64) instead of pi.dev; tests in internal/codingagent/install_telemetry_test.go` | ✅ |
| `packages/coding-agent/src/core/timings.ts` | `agent/timings.go` | ✅ |
| `packages/coding-agent/src/core/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/provider-attribution.ts` | `coding/model.go (mergeProviderAttributionHeaders, newProviderAttributionProvider; coding/provider_attribution_0861_test.go, coding/model_test.go TestBuildModelGatesAttributionHeadersOnInstallTelemetrySetting): OpenRouter/NVIDIA/Cloudflare headers gated on install telemetry, OpenCode session pair unconditional, matching upstream; values are pig-branded (D26)` | ✅ |
| `packages/coding-agent/src/core/compaction/compaction.ts` | `internal/codingagent/compaction/compaction.go (internal/codingagent/compaction/compaction_summary_reasoning_test.go; coding/session_summarization_test.go)` | ✅ |
| `packages/coding-agent/src/core/compaction/branch-summarization.ts` | `internal/codingagent/compaction/branch_summarization.go (internal/codingagent/compaction/branch_summarization_test.go)` | ✅ |
| `packages/coding-agent/src/core/compaction/utils.ts` | `internal/codingagent/compaction/utils.go` | ✅ |
| `packages/coding-agent/src/core/compaction/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/extensions/types.ts` | `partial: coding/extension/*.go (Layer 0); subprocess setEditorComponent carries decoration instead of a full editor factory, and addAutocompleteProvider accepts only the async suggestion-source bridge; tracked in docs/extension-api-parity.md` | 🟡 |
| `packages/coding-agent/src/core/extensions/runner.ts` | `coding/extension/host/inproc/runner.go + coding/extension/host/inproc/ui_prompt.go (ui_prompt_start/ui_prompt_end; subprocess dialogs open the scope through coding/extension/host/subprocess/ui_prompt.go)` | ✅ |
| `packages/coding-agent/src/core/extensions/loader.ts` | `cmd/pig/extensions.go + coding/extension/host/subprocess/builder_node.go + coding/extension/host/subprocess/reload_cells.go (reload re-invokes every factory in a fresh process, so module-level state also restarts: D70)` | ✅ |
| `packages/coding-agent/src/core/extensions/wrapper.ts` | `coding/extension_bridge.go` | ✅ |
| `packages/coding-agent/src/core/extensions/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/extensions/index.ts` | `cmd/pig/llama.go + internal/codingagent/llama/host.go (built-in llama.cpp provider and /llama registered natively in every mode; Stock PiG has no inline-extension runtime)` | ✅ |
| `packages/coding-agent/src/extensions/llama/client.ts` | `internal/codingagent/llama/client.go + internal/codingagent/llama/fetch.go` | ✅ |
| `packages/coding-agent/src/extensions/llama/huggingface.ts` | `internal/codingagent/llama/huggingface.go` | ✅ |
| `packages/coding-agent/src/extensions/llama/index.ts` | `internal/codingagent/llama/index.go + internal/codingagent/interactive_llama.go + internal/codingagent/slash_commands.go + cmd/pig/rpc_mode.go` | ✅ |
| `packages/coding-agent/src/extensions/llama/provider.ts` | `internal/codingagent/llama/provider.go + internal/codingagent/llama/host.go` | ✅ |
| `packages/coding-agent/src/extensions/llama/ui.ts` | `internal/codingagent/llama/ui.go` | ✅ |
| _(downstream: subprocess host bridge)_ | `coding/extension/host/subprocess/*.go` | 🔀 D19 |
| _(downstream: packed runtime cells)_ | `coding/extension/host/subprocess/cell_plan.go`, `coding/extension/host/runtimecell/*.go`, `coding/extension/host/cellpack/*.go`, `coding/extension/host/fusepack/*.go` | 🔀 D20 |
| _(downstream: Go SDK bridge)_ | `extensions/sdk/...` | 🔀 D19 |
| _(downstream: Rust SDK bridge)_ | `extensions/sdk-rs/...` | 🔀 D19 |
| _(downstream: Python SDK bridge)_ | `extensions/sdk-py/...` | 🔀 D19 |
| `packages/coding-agent/src/core/tools/bash.ts` | `internal/codingagent/tools/bash.go` | ✅ |
| `packages/coding-agent/src/core/tools/output-accumulator.ts` | `internal/codingagent/tools/output_accumulator.go + truncate.go` | ✅ |
| `packages/coding-agent/src/core/tools/read.ts` | `internal/codingagent/tools/read.go` | ✅ |
| `packages/coding-agent/src/core/tools/write.ts` | `internal/codingagent/tools/write.go` | ✅ |
| `packages/coding-agent/src/core/tools/edit.ts` | `internal/codingagent/tools/edit.go` | ✅ |
| `packages/coding-agent/src/core/tools/grep.ts` | `internal/codingagent/tools/grep.go` | ✅ |
| `packages/coding-agent/src/core/tools/find.ts` | `internal/codingagent/tools/find.go` | ✅ |
| `packages/coding-agent/src/core/tools/ls.ts` | `internal/codingagent/tools/ls.go` | ✅ |
| `packages/coding-agent/src/core/tools/edit-diff.ts` | `internal/codingagent/tools/edit_diff.go + unified_patch.go (GenerateUnifiedPatch) + diff_string.go (GenerateDiffString)` | ✅ |
| `packages/coding-agent/src/core/tools/file-mutation-queue.ts` | `internal/codingagent/tools/mutation_queue.go` | ✅ |
| `packages/coding-agent/src/core/tools/render-utils.ts` | `internal/codingagent/tool_render.go` | ✅ |
| `packages/coding-agent/src/core/tools/truncate.ts` | `internal/codingagent/tools/truncate.go` | ✅ |
| `packages/coding-agent/src/core/tools/tool-definition-wrapper.ts` | `coding/extension_bridge.go` | ✅ |
| `packages/coding-agent/src/core/tools/path-utils.ts` | `internal/codingagent/tools/path_utils.go` | ✅ |
| `packages/coding-agent/src/core/tools/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/export-html/ansi-to-html.ts` | `assets/template.js` | ✅ |
| `packages/coding-agent/src/core/export-html/tool-renderer.ts` | `internal/codingagent/export/ansi_html.go + internal/codingagent/export/tool_renderer.go + internal/codingagent/slash_session_handlers.go` | ✅ |
| `packages/coding-agent/src/core/export-html/index.ts` | `internal/codingagent/export/export.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/interactive-mode.ts` | `partial: internal/codingagent/interactive.go, internal/codingagent/auth_warnings.go, internal/codingagent/interactive_input.go, internal/codingagent/interactive_turn.go, internal/codingagent/interactive_commands.go, internal/codingagent/interactive_auth.go, internal/codingagent/interactive_events.go, internal/codingagent/interactive_tui.go, internal/codingagent/interactive_signals_unix.go, internal/codingagent/interactive_signals_windows.go, internal/codingagent/interactive_models.go, internal/codingagent/interactive_extensions.go, internal/codingagent/interactive_chat.go, internal/codingagent/interactive_layout.go, internal/codingagent/interactive_editor.go, internal/codingagent/interactive_transcript.go, internal/codingagent/interactive_thinking.go, internal/codingagent/interactive_helpers.go; Esc does not cancel an active branch summary, and automatic Copilot reauthentication cannot prompt for an enterprise domain` | 🟡 |
| `packages/coding-agent/src/modes/interactive/external-editor.ts` | `ported+wired: OpenExternalEditor (internal/codingagent/external_editor.go) called interactive.go:7452, unit-tested` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/trust-selector.ts` | `internal/codingagent/slash_session_handlers.go (trustHandler via ShowExtensionSelector; unit+mutation-tested: selected decision persisted / cancel saves nothing / unavailable-selector graceful)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/first-time-setup.ts` | `(designed out: upstream gates this component to the official Pi package identity; Stock PiG does not satisfy that identity)` | n/a |
| `packages/coding-agent/src/modes/interactive/theme/theme.ts` | `tui/theme.go + tui/theme_loader.go (indexed-color load/var/ANSI/CSS/export coverage in TestLoadThemeFileIndexedColors; complete optional-color fallbacks and authored key order in scrollbar_theme_test.go) + exact pinned dark/light JSON; internal/codingagent/interactive_tui.go (foreground scrollbarThumb style)` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts` | `tui/theme.go (0.79.10 Automatic option; AC-6 / Stage 2)` | ✅ |
| `packages/coding-agent/src/modes/interactive/model-search.ts` | `tui/model_search.go (ModelSearchItem, GetModelSearchText, GetModelSelectorSearchText); GetModelSelectorSearchText feeds tui/model_select.go applyFilter over ModelSelectorItem.Name (raw model name), GetModelSearchText feeds tui/scoped_models_list.go refresh and internal/codingagent/interactive_commands.go modelArgCompletions. tui/model_search_test.go proxy-provider fixture (openrouter/vercel-ai-gateway openai/gpt-5 ids) asserts the Pi 0.87.1 fuzzyFilter order for both functions and the selector` | ✅ |
| `packages/coding-agent/src/modes/print-mode.ts` | `cmd/pig/print_mode.go (text) and cmd/pig/json_mode.go (json): upstream runs both modes from one function, pig splits them by mode. json streams the session event stream as JSONL while the turn runs; text still prints only the final assistant message. Behavioral coverage: print/01-print-mode-arithmetic, json/01-json-mode-streams-events` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts` | `cmd/pig/rpc_mode.go + cmd/pig/rpc_ui.go` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-types.ts` | `cmd/pig/rpc_types.go` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-client.ts` | `coding/rpcclient/rpc_client.go + commands.go + types.go + process_unix.go/process_windows.go (RpcClient spawns the pig executable in --mode rpc where upstream spawns node cliPath; typed wire structs replace the TypeScript imports, with raw JSON kept for entries, messages, and events). coding/rpcclient/rpc_client_test.go ports rpc-client-clear-queue/clone/process-exit tests against scripted child processes; rpc_mode_test.go ports rpc.test.ts against a pig binary built in TestMain, using test-faux in place of the live Anthropic model` | ✅ |
| `packages/coding-agent/src/modes/rpc/jsonl.ts` | `(inlined)` | ✅ |
| `packages/coding-agent/src/modes/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/utils/changelog.ts` | `embed.go + internal/codingagent/changelog.go` | ✅ |
| `packages/coding-agent/src/utils/ansi.ts` | `internal/codingagent/export/ansi_html.go` | ✅ |
| `packages/coding-agent/src/utils/child-process.ts` | `internal/codingagent/tools/bash_operations.go (waitForChildProcess post-exit stdio grace, EXIT_STDIO_GRACE_MS); spawnProcess/spawnProcessSync are the cross-spawn Windows shim, which os/exec covers` | ✅ |
| `packages/coding-agent/src/utils/clipboard.ts` | `partial: internal/codingagent/clipboard.go + internal/codingagent/clipboard_copy.go + internal/codingagent/clipboard_text.go (readClipboardText command fallbacks and interactive Ctrl+V/right-click paths; internal/codingagent/clipboard_read_text_test.go); PiG has no Linux native getText adapter after wl-paste/xclip/xsel failures` | 🟡 |
| `packages/coding-agent/src/utils/clipboard-image.ts` | `internal/codingagent/clipboard.go + internal/codingagent/clipboard_paste.go (backend read and paste caller; unsupported-format PNG conversion and native clipboard parity remain incomplete; red repro: parity/testdata/next-evidence-app-b/team_app_clipboard_blocked_test.go.repro)` | 🟡 |
| `packages/coding-agent/src/utils/exif-orientation.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/frontmatter.ts` | `internal/codingagent/frontmatter/frontmatter.go` | ✅ |
| `packages/coding-agent/src/utils/fs-watch.ts` | `internal/codingagent/git_watcher.go` | ✅ |
| `packages/coding-agent/src/utils/git.ts` | `coding/source/ref.go + cmd/pig/package_commands.go (parseGitURL cases in coding/source/ref_test.go)` | ✅ |
| `packages/coding-agent/src/utils/html.ts` | `internal/codingagent/export/ansi_html.go` | ✅ |
| `packages/coding-agent/src/utils/image-convert.ts` | `internal/codingagent/image_convert.go + internal/imageprocessing/image_convert.go (ConvertImageBytesToPng = decodeAutoOriented + encodePNG in place of Photon, also behind images.go convertToolResultImageOnly as upstream normalizeImage; ConvertToPng with Node Buffer base64 decoding; maybeConvertImagesForKitty drives tui/tool_execution.go PendingKittyImageConversions/ApplyConvertedImage and the Kitty non-PNG render skip from the tool-end and transcript-replay paths). Unit-tested by internal/codingagent/image_convert_test.go (upstream image-processing.test.ts convertToPng cases, every decodable format, failure, EXIF locators) and tui/tool_execution_kitty_convert_test.go (upstream tool-execution-component.test.ts late-conversion race) under a faked Kitty capability; tmux cannot report Kitty, so clipboard-images/02 stays deferred` | ✅ |
| `packages/coding-agent/src/utils/image-resize.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/mime.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/paths.ts` | `internal/codingagent/paths.go` | ✅ |
| `packages/coding-agent/src/utils/pi-user-agent.ts` | `internal/codingagent/bug_report.go (codingAgentUserAgent supplies the owner-approved Q4/D65 pig/<coding.Version> identity to exported report metadata; TestBugReportEnvironmentUsesPiGUserAgent). Other upstream call sites are accounted by their owning rows: version-check.ts and package-manager-cli.ts self-update are replaced by D39, remote-catalog-provider.ts is not ported, and the interactive report-install call is ported under core/telemetry.ts (internal/codingagent/install_telemetry.go), which sends this same User-Agent (ai.PiUserAgent()) with the ping.` | ✅ |
| `packages/coding-agent/src/utils/photon.ts` | `(not ported: WASM)` | n/a |
| `packages/coding-agent/src/utils/shell.ts` | `internal/codingagent/tools/shell_config.go + shell_config_unix.go + shell_config_windows.go + bash_session_env.go (getShellEnv) + sanitize.go (sanitizeBinaryOutput) + bash_group_*.go (killProcessTree)` | ✅ |
| `packages/coding-agent/src/utils/syntax-highlight.ts` | `tui/highlight.go` | ✅ |
| `packages/coding-agent/src/utils/sleep.ts` | `ai/assistant_retry.go (sleepContext is the awaited, context-cancellable backoff used by the Session-owned retry loop; TestRetryAssistantCallAbortsBackoffSleepViaContext covers cancellation before the timer wait)` | ✅ |
| `packages/coding-agent/src/utils/tools-manager.ts` | `internal/codingagent/tools/tools.go + tools_manager.go (release redirects, fetchWithRetry, extraction per D14, silent grep/find, bounded distinct ToolStatus causes); internal/codingagent/interactive_tools_status.go (live owner-thread status, ordered initial reports, joined cancellation); tools_manager_status_test.go + interactive_tools_status_test.go + tools/12-managed-tool-status; remaining: Linux musl asset selection and bypassing latest lookup for pinned darwin/x64 fd` | 🟡 |
| `packages/coding-agent/src/utils/version-check.ts` | `(not ported: Pig uses configured signed release manifests under D39 instead of the pi.dev version API)` | n/a |
| `packages/coding-agent/src/utils/deprecation.ts` | `(designed out: the pinned upstream helper has no imports or callers; Go warning sites retain their caller-specific output policy)` | n/a |
| `packages/coding-agent/src/utils/image-resize-core.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/image-resize-worker.ts` | `(Node worker thread: Go uses goroutines)` | n/a |
| `packages/coding-agent/src/utils/json.ts` | `(stdlib encoding/json)` | n/a |
| `packages/coding-agent/src/utils/open-browser.ts` | `(stdlib os/exec)` | n/a |
| `packages/coding-agent/src/utils/windows-self-update.ts` | `internal/codingagent/windows_self_update.go (the running pig.exe is the loaded native image an npm update quarantines; TestQuarantineNativeDependenciesMovesLoadedImagesAndCopiesThemBack, TestWindowsNpmSelfUpdateReplacesTheRunningInstallation)` | ✅ |
| `packages/coding-agent/src/bun/cli.ts` | `(Bun-only entrypoint)` | n/a |
| `packages/coding-agent/src/bun/restore-sandbox-env.ts` | `(Bun-only sandbox helper)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/assistant-message.ts` | `tui/assistant_message_block.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/bash-execution.ts` | `tui/bash_execution.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/bordered-loader.ts` | `tui/bordered_loader.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts` | `tui/branch_summary.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts` | `tui/compaction_summary.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/config-selector.ts` | `tui/config_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/countdown-timer.ts` | `tui/countdown_timer.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/custom-editor.ts` | `internal/codingagent/interactive.go, internal/codingagent/interactive_input.go, and internal/codingagent/keys.go realize CustomEditor app-keybinding precedence (extension shortcuts -> paste-image -> interrupt with autocomplete/empty guards -> exit-on-empty -> app.session actions -> editor fallthrough) as InteractiveMode dispatch rather than an Editor subclass (structural, observably equivalent). Ctrl+D exits only when the editor has zero-length text; nonempty text, including whitespace, falls through to tui.editor.deleteCharForward. Conflicting paste-image/interrupt/exit bindings use CustomEditor order rather than registry declaration order. Guarded by mutation-proven TestCustomEditorHighPriorityBindingOrder, TestCustomEditorDispatchPrecedence, and parity/scenarios/custom-editor-precedence.toml.` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/custom-entry.ts` | `internal/codingagent/interactive.go addCustomEntryToChat (rebuild + live-append, Spacer(1) + expand-toggle) over the entry-renderer wire in coding/extension/host/subprocess/{protocol.go,host.go,render_proxy.go} + RegisterEntryRenderer in extensions/sdk{,-rs,-py} + runtime-node. Cross-SDK verified by extension-conformance (inproc-go reference vs subprocess go/rust/python) + parity scenario extensions-runtime/18-subprocess-entry-renderer (pi 0.84.0 vs pig byte-identical, mutation-proven)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/custom-message.ts` | `tui/custom_message.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/diff.ts` | `tui/diff_component.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/dynamic-border.ts` | `tui/dynamic_border.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-editor.ts` | `tui/text_input.go + internal/codingagent/session_selectors.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-input.ts` | `tui/extension_input.go + internal/codingagent/ext_ui_context.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-selector.ts` | `tui/select_filterable.go + internal/codingagent/ext_ui_context.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/footer.ts` | `internal/codingagent/status_line.go + internal/codingagent/interactive_auth.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/keybinding-hints.ts` | `tui/keybinding_hints.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/login-dialog.ts` | `cmd/pig/auth_commands.go + interactive.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/model-selector.ts` | `tui/model_select.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/oauth-selector.ts` | `internal/codingagent/slash_commands.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/scoped-models-selector.ts` | `tui/scoped_models_list.go + internal/codingagent/interactive.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/session-selector.ts` | `internal/codingagent/session_selector.go + session_selectors.go + interactive.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/session-selector-search.ts` | `internal/codingagent/session_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/settings-selector.ts` | `tui/settings_list.go + internal/codingagent/slash_session_handlers.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/show-images-selector.ts` | `tui/show_images_selector.go (FilterableList layout differs from upstream SelectList and onSelect/onCancel callbacks are not dispatched; red repro: parity/testdata/next-evidence-app-b/team_app_show_images_test.go.repro; no ordinary interactive entry point)` | 🟡 |
| `packages/coding-agent/src/modes/interactive/components/skill-invocation-message.ts` | `tui/skill_invocation.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/theme-selector.ts` | `tui/theme_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/thinking-selector.ts` | `internal/codingagent/thinking_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/tool-execution.ts` | `tui/tool_execution.go (shell elapsed Text wrapping covered for bash/powershell output, no-output, and collapsed paths by TestToolExecutionComponent_ShellElapsedFooterRows)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/tree-selector.ts` | `tui/tree_select.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/user-message.ts` | `tui/user_message_block.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/user-message-selector.ts` | `tui/user_message_selector.go + internal/codingagent/session_selectors.go (D66 bounds list rows at unusually narrow widths instead of emitting fatal over-wide rows)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/visual-truncate.ts` | `tui/visual_truncate.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/armin.ts` | `(easter egg: skip)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/daxnuts.ts` | `(easter egg: skip)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/earendil-announcement.ts` | `(easter egg: skip)` | n/a |
| `packages/coding-agent/src/cli/experimental/cli.ts` | `(not ported: remote-session capability via A2A, spec 482; pi-client cloud SDK not portable)` | n/a |
| `packages/coding-agent/src/cli/experimental/command-options.ts` | `(not ported: remote-session capability via A2A, spec 482; pi-client cloud SDK not portable)` | n/a |
| `packages/coding-agent/src/cli/experimental/command.ts` | `(not ported: remote-session capability via A2A, spec 482; pi-client cloud SDK not portable)` | n/a |
| `packages/coding-agent/src/cli/experimental/commands/client.ts` | `(not ported: remote-session capability via A2A, spec 482; pi-client cloud SDK not portable)` | n/a |
| `packages/coding-agent/src/cli/experimental/commands/server.ts` | `(not ported: remote-session capability via A2A, spec 482; pi-client cloud SDK not portable)` | n/a |
| `packages/coding-agent/src/client/index.ts` | `(not ported: remote-session capability via A2A, spec 482; pi-client cloud SDK not portable)` | n/a |
| `packages/coding-agent/src/core/pi-manifest.ts` | `coding/packagecontent/pi_manifest.go ReadPiManifest (BOM strip, object-only package and "pi", per-field string arrays), used by coding/extension/host/subprocess/builder_node.go piPackageEntrypoints; coding/packagecontent/packagecontent.go readPackageManifest applies the same parse for package discovery (pi_manifest_test.go, builder_node_entry_test.go)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/markdown-transform.ts` | `internal/codingagent/markdown_transform.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/mermaid.ts` | `internal/codingagent/mermaid_transform.go` | ✅ |
| `packages/coding-agent/src/modes/json-event.ts` | `cmd/pig/rpc_events.go realizes toJsonEvent: pig constructs delta-only message_update wire events directly (no cumulative partial snapshot) instead of build-then-strip. Both --mode json (json_mode.go) and --mode rpc (rpc_mode.go) forward the same shapes. pig's internal MessageUpdateEvent carries deltas only, so rpc_events.go synthesizes the run delimiters (text_start/text_end, toolcall_start/toolcall_end) upstream receives from the provider stream. Behavioral coverage: json/01-json-mode-streams-events and rpc/06-rpc-chat-faux assert the delimited stream and never a partial field` | ✅ |
| `packages/coding-agent/src/utils/abort.ts` | `(AbortSignal/Promise racing: raceWithAbortSignal/operationSignal; pig uses context.Context cancellation + select (D3): runtime mechanics, designed out)` | n/a |
| `packages/coding-agent/src/utils/management-http.ts` | `internal/managementhttp/managementhttp.go FetchWithRetry (management-http.test.ts cases in managementhttp_test.go), used by internal/codingagent/tools/tools_manager.go getLatestVersion and downloadFile; its other upstream callers, version-check.ts and remote-catalog-provider.ts, call pi.dev and are not ported (see their rows)` | ✅ |
| `packages/coding-agent/src/utils/tool-result-images.ts` | `internal/imageprocessing/images.go + coding/session_images.go: model-profile normalization after extension hooks, failed-image retention; unit/mutation-tested` | ✅ |
| `packages/coding-agent/src/bun/runtime-setup.ts` | `(Bun-only runtime setup)` | n/a |
| `packages/coding-agent/src/bun/sandbox-env-setup.ts` | `(Bun-only sandbox helper)` | n/a |
| `packages/coding-agent/src/cli/auth-check.ts` | `cmd/pig/auth_check.go + cmd/pig/auth_run.go (main.ts runAuthCommand)` | ✅ |
| `packages/coding-agent/src/cli/auth-command.ts` | `cmd/pig/auth_command.go + cmd/pig/resolve_cli_model.go (resolveCliModel for --model)` | ✅ |
| `packages/coding-agent/src/cli/setup.ts` | `cmd/pig/setup_cli.go (PI_CODING_AGENT/AI_AGENT process markers; process.title is the executable name, Node warning suppression has no Go analogue, configureHttpDispatcher is mapped under core/http-dispatcher.ts)` | ✅ |
| `packages/coding-agent/src/core/bug-report-upload.ts` | `(designed out: D62, PiG never uploads bug reports)` | n/a |
| `packages/coding-agent/src/core/bug-report.ts` | `internal/codingagent/bug_report.go (export subset: metadata, redaction, diagnostics, archive; D62) + internal/codingagent/compaction/bug_report_summary.go (coding/bug_report.go; internal/codingagent/compaction/bug_report_summary_test.go)` | ✅ |
| `packages/coding-agent/src/core/cache-warmer.ts` | `internal/codingagent/cache_warmer.go + coding/session_cache_warming.go (sdk.ts streamFn restart, agent-session.ts status/mode/settle/dispose); the cache_warming_decision extension dispatch is tracked by known-gaps api:OnCacheWarmingDecision` | ✅ |
| `packages/coding-agent/src/core/crash-log.ts` | `internal/codingagent/crash_log.go + interactive_crash.go (crash persistence, startup notice, /bug attachment and cleanup, fatal create/resume/import reporting, and loaded-extension stack attribution; permissive preservation of malformed and unknown crash-record fields remains)` | 🟡 |
| `packages/coding-agent/src/core/extensions/jiti-loader.ts` | `(barrel re-export)` | n/a |
| `packages/coding-agent/src/core/extensions/jiti-static-loader.ts` | `(barrel re-export)` | n/a |
| `packages/coding-agent/src/core/extensions/virtual-modules.ts` | `partial: coding/extension/host/subprocess/runtime-node/loader.mjs shim table + runtime-node/shims/pi-ai-oauth.mjs serve the typebox, pi-coding-agent, pi-tui, pi-ai, pi-ai/compat, and pi-ai/oauth specifiers (TestNodeRuntimeLoaderCoversPiVirtualModules, TestNodeRuntimeLoaderServesPiAiCompatAndOAuth); pi-agent-core and pi-ai/providers/all have no shim and resolve only from the extension node_modules` | 🟡 |
| `packages/coding-agent/src/core/session-export.ts` | `internal/codingagent/session_export.go (/export <file>.jsonl, /bug transcript)` | ✅ |
| `packages/coding-agent/src/core/settings-diagnostics.ts` | `internal/codingagent/settings_diagnostics.go (wired in cmd/pig/main.go: stderr for print/json/rpc/--list-models, chat warnings in interactive)` | ✅ |
| `packages/coding-agent/src/core/tools/powershell.ts` | `internal/codingagent/tools/powershell.go + shell_tool.go (shared createShellToolDefinition) + tools.go (CreateAllTools, BuiltinToolActive: registered everywhere, active only when named)` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/bash.ts` | `tui/shell_renderers.go (formatShellCall, formatDuration) + tui/tool_execution.go (live elapsed Text wrapping) + internal/codingagent/tool_render_shell.go (rebuildBashResultRenderComponent); width-boundary coverage in TestToolExecutionComponent_ShellElapsedFooterRows` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/edit.ts` | `partial: tui/tool_execution.go (FormatEditHeader) + internal/codingagent/tool_render.go (renderDiffString). Remaining: renderShell "self" call box with the async argsComplete preview diff and its pending/success/error header background, str() invalid-arg path, error text de-duplication against the preview error` | 🟡 |
| `packages/coding-agent/src/core/tools/renderers/find.ts` | `tui/tool_execution.go (FormatFindHeader) + internal/codingagent/tool_render_list.go` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/grep.ts` | `tui/tool_execution.go (FormatGrepHeader) + internal/codingagent/tool_render_list.go` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/index.ts` | `tui/tool_renderers.go (createAllToolRenderers by name, withBuiltInRenderers fallback) + tui/tool_execution.go FormatBuiltinToolHeader + internal/codingagent/tool_render.go toolBodyRenderer` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/ls.ts` | `tui/tool_execution.go (FormatLsHeader) + internal/codingagent/tool_render_list.go` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/read.ts` | `partial: tui/tool_execution.go (FormatReadHeader) + internal/codingagent/tool_render.go (renderReadLines). Remaining: compact read call for SKILL.md/AGENTS.md/CLAUDE.md/Pi docs, str() invalid-arg path, expanded result without the pig line-number gutter and path header, truncation warning rows, rendering persisted results from call args` | 🟡 |
| `packages/coding-agent/src/core/tools/renderers/write.ts` | `partial: tui/tool_execution.go (FormatWriteHeader) + internal/codingagent/tool_render.go (renderWriteContent). Remaining: content preview in the call while arguments stream (incremental highlight cache), no pig "@@ path created @@" header, toolOutput styling of unhighlighted lines, invalid content-arg marker, error-only result` | 🟡 |
| `packages/coding-agent/src/experimental/cli.ts` | `(pending implementation: owner-approved separate unshipped experimental entrypoint; source-only: tsconfig.build.json excludes src/experimental from Pi's npm package and binaries; this development entrypoint dispatches server/client when PI_EXPERIMENTAL=1, else falls through to main(). The published pi bin never dispatches them, pinned by cmd/pig/experimental_entry_test.go. A PiG analog needs a separate unshipped binary whose only targets are the pending server/client rows)` | ⬜ |
| `packages/coding-agent/src/experimental/client-runtime.ts` | `(pending: opens unix or Radius server connections and Chord remote service sources for a presentation; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope; pi-server/pi-client/pi-protocol are outside PORT_MAP package scope; its radius:// route is approved under Q1 Radius but remains unimplemented here)` | ⬜ |
| `packages/coding-agent/src/experimental/client-tui-chat.ts` | `(pending: snapshot-driven transcript view over LaneTranscriptSnapshot entries; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/client-tui.ts` | `(pending: service-only alt-screen client TUI over replicated Transcript/Models/SessionDirectory services and a Chord FacetHost; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice; Radius reconnect status is approved under Q1 Radius but remains unimplemented here)` | ⬜ |
| `packages/coding-agent/src/experimental/client.ts` | `(pending: non-interactive "client" command (discover servers, list/attach/create Session, one-shot prompt) over client-runtime.ts; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope; pi-server/pi-client/pi-protocol are outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/commands.ts` | `(pending implementation: owner-approved separate unshipped experimental entrypoint; source-only: tsconfig.build.json excludes src/experimental from Pi's npm package and binaries; dispatches "server"/"client" for the development entrypoint only; "server" always starts RadiusRelayHost (owner approved Q1 Radius; this server integration remains unimplemented); parser rows cli/experimental/* are n/a under spec 482)` | ⬜ |
| `packages/coding-agent/src/experimental/coordinator-entry.ts` | `(pending: entry for the internal "coordinator" process role, spawned only by server.ts ensureCoordinator; no PiG caller until server.ts lands)` | ⬜ |
| `packages/coding-agent/src/experimental/coordinator.ts` | `(pending: stable unix-socket endpoint and JSON-line message router between replaceable server generations and session workers; only callers are server.ts, session-worker*.ts, which are pending)` | ⬜ |
| `packages/coding-agent/src/experimental/micro/api.ts` | `(pending: MicroView/MicroController boundary over Pico3 types; micro is a standalone source-only program (tsx micro/main.ts), not reachable from pi; Pico3 implementation maps to agent/harness/pico3, but this micro adapter remains unimplemented)` | ⬜ |
| `packages/coding-agent/src/experimental/micro/main.ts` | `(pending: standalone source-only "micro" program entry, not reachable from pi; Pico3 implementation maps to agent/harness/pico3, but this micro adapter remains unimplemented)` | ⬜ |
| `packages/coding-agent/src/experimental/micro/models.ts` | `(pending: Pico3 model adapter over ModelRuntime; Pico3 implementation maps to agent/harness/pico3, but this micro adapter remains unimplemented)` | ⬜ |
| `packages/coding-agent/src/experimental/micro/runtime.ts` | `(pending: owns ModelRuntime, Pico3 harness, JSONL storage; default model openai-codex/gpt-5.6-sol; Pico3 implementation maps to agent/harness/pico3, but this micro adapter remains unimplemented)` | ⬜ |
| `packages/coding-agent/src/experimental/micro/sessions.ts` | `(pending: locked session directories under <agentDir>/experimental/micro-sessions/<cwd-hash>/; only consumer is micro/runtime.ts; Pico3 implementation maps to agent/harness/pico3, but this micro adapter remains unimplemented)` | ⬜ |
| `packages/coding-agent/src/experimental/micro/tools.ts` | `(pending: adapts coding tools to Pico3 tool definitions; Pico3 implementation maps to agent/harness/pico3, but this micro adapter remains unimplemented)` | ⬜ |
| `packages/coding-agent/src/experimental/micro/tui.ts` | `(pending: alt-screen micro TUI over MicroView; Pico3 implementation maps to agent/harness/pico3, but this micro adapter remains unimplemented)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/main.ts` | `(pending: standalone source-only "mini" program entry (node mini/main.ts), not reachable from pi; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/server/entry.ts` | `(pending: mini server process entry; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/server/run.ts` | `(pending: mini router, worker supervision, event fan-out over <agentDir>/experimental/mini.sock; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/shared/protocol.ts` | `(pending: mini service tokens and wire types carrying LaneSnapshot/LaneWatchEvent; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/shared/rpc.ts` | `(pending: harness-free call/emit RPC peer, but its only consumers are mini server/worker/tui; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/shared/transport.ts` | `(pending: harness-free newline-delimited JSON connection, but its only consumers are mini server/worker/tui; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/tui/run.ts` | `(pending: mini presentation host that spawns the detached mini server; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/tui/session.ts` | `(pending: attach/watch/rebase over reduceLaneSnapshot; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/tui/view.ts` | `(pending: alt-screen mini view over the replicated LaneSnapshot; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/worker/entry.ts` | `(pending: mini session worker process entry; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/worker/lane-service.ts` | `(pending: Lane watch subscriptions and lane commands; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/worker/models-service.ts` | `(pending: catalog, accounts, interactive login service; only consumer is mini/worker/run.ts; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/mini/worker/run.ts` | `(pending: opens the Session and builds the AgentHarness for one mini worker; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice)` | ⬜ |
| `packages/coding-agent/src/experimental/plugin.ts` | `(Node-runtime-only: the source-only "@earendil-works/pi-coding-agent/experimental/plugin" import specifier that plugins/bundled.ts resolvePluginExternal hands to in-process TypeScript facet bundles; PiG has no in-process JS module loader (AGENTS.md forbids an embedded-JS or in-process extension runtime))` | n/a |
| `packages/coding-agent/src/experimental/plugins/bundled.ts` | `(Node-runtime-only: loads Chord facet bundles (esbuild-built ESM) into the server, worker, and client processes by dynamic import; AGENTS.md forbids an embedded-JS or in-process extension runtime)` | n/a |
| `packages/coding-agent/src/experimental/plugins/package.ts` | `(Node-runtime-only: builds TypeScript plugin packages (src/session.ts, src/tui.ts) with @earendil-works/chord/bundler (esbuild) into facet bundles for in-process import; its plugin-package profile JSON only selects those bundles; AGENTS.md forbids an embedded-JS or in-process extension runtime)` | n/a |
| `packages/coding-agent/src/experimental/process.ts` | `(pending: spawns detached coordinator/server/session-worker roles via __PI_INTERNAL_SPAWN for the server stack; its source-mode branch (--import source-resolver.ts) is Node-runtime-only; no PiG caller until server.ts lands)` | ⬜ |
| `packages/coding-agent/src/experimental/radius-auth.ts` | `(pending: experimental server stack; owner approved Radius Q1)` | ⬜ |
| `packages/coding-agent/src/experimental/radius-relay.ts` | `(pending: experimental server stack; owner approved Radius Q1)` | ⬜ |
| `packages/coding-agent/src/experimental/server.ts` | `(pending implementation: owner approved Q1 Radius; startServer always creates and starts RadiusRelayHost, which connects to radius.pi.dev whenever a Radius credential resolves; the rest (server profile under PI_SERVER_DIR or ~/.pi/server, coordinator, worker manager, plugin selection) needs Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope and pi-server/pi-client/pi-protocol are outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/agent-controller-provider.ts` | `(pending: AgentController facade over AgentLane; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/agent-controller.ts` | `(pending: Chord service contract pi.agent-controller; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/connection.ts` | `(pending: server and selected-Session remote service sources over pi-client; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope; pi-server/pi-client/pi-protocol are outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/models-provider.ts` | `(pending: Models service facet over ModelRuntime and SettingsManager; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/models.ts` | `(pending: Chord service contract pi.models with ReplicatedState; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/plugins.ts` | `(pending: Chord service contracts pi.presentation-plugins and pi.session-plugins for TypeScript facet bundles; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/presentation-ui.ts` | `(pending: local Chord service contract pi.local.presentation-ui; only consumer is client-tui.ts; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/server.ts` | `(pending: SessionDirectory/SessionManagement/PresentationPlugins server facets; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope; pi-server/pi-client/pi-protocol are outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/sessions.ts` | `(pending: Chord service contracts pi.session-directory and pi.session-management; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope; pi-server/pi-client/pi-protocol are outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/slash-commands-provider.ts` | `(pending: SlashCommandRegistry and built-in /model, /thinking, /compact, /reload, /hello facets; only consumer is client-tui.ts; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/slash-commands.ts` | `(pending: local Chord service contract pi.local.slash-commands; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/transcript-provider.ts` | `(pending: publishes the main-lane transcript as ReplicatedState; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/transcript.ts` | `(pending: Chord service contract pi.transcript over LaneTranscriptSnapshot; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/worker.ts` | `(pending: assembles a Session worker's built-in and plugin facets; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/session-worker-manager.ts` | `(pending: server-side worker process bookkeeping over the coordinator; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope; pi-server/pi-client/pi-protocol are outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/session-worker.ts` | `(pending: per-Session worker process hosting the AgentHarness behind Chord services; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/source-resolver.ts` | `(Node-runtime-only: a node:module registerHooks resolve hook that maps tsconfig @earendil-works/* path aliases to .ts sources so internal processes run from a source checkout; Go links packages at build time)` | n/a |
| `packages/coding-agent/src/modes/interactive/bug-report.ts` | `internal/codingagent/slash_bug.go, internal/codingagent/interactive_bug.go (consent flow, export-only; D62)` | ✅ |
| `packages/coding-agent/src/modes/interactive/chat-viewport.ts` | `internal/codingagent/chat_viewport.go (CreateChatViewport; mounted by buildChatViewport in internal/codingagent/interactive_tui.go)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/settings-submenu.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/coding-agent/src/modes/interactive/model-catalog-refresh.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/coding-agent/src/modes/interactive/session-share.ts` | `internal/codingagent/session_share.go (createShareTrailingEntries, ExportSessionForShare, explicit /share upload with privacy notice); hosted counterpart: private PiG-platform worker/src/share.ts (bounded R2 artifact route, 30-day expiry, unlisted viewer)` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/theme-json.ts` | `tui/theme_json.go (ValidateThemeJSON, wired in tui/theme_loader.go LoadThemeFile; upstream-generated goldens in tui/testdata/theme_json_cases.json; real-loader union coverage in TestLoadThemeFileIndexedColors)` | ✅ |
| `packages/coding-agent/src/modes/interactive/tui-renderer.ts` | `internal/codingagent/interactive_tui.go (createInteractiveTui, fullscreenTuiOptions), internal/codingagent/clipboard_text.go (right-click paste); createInteractiveTuiReference designed out: the driver reads m.tuiInst at call time under rendererMu, so no proxy is needed` | ✅ |
| `packages/coding-agent/src/utils/clipboard-command.ts` | `internal/codingagent/clipboard_copy.go` | ✅ |
| `packages/coding-agent/src/utils/highlight-js.d.ts` | `(TypeScript ambient declarations; no runtime behavior)` | n/a |
| `packages/coding-agent/src/utils/text.ts` | `internal/text/text.go (BOM helpers; auth, settings, models, trust, frontmatter, context, file attachments and edit callers)` | ✅ |
| `packages/coding-agent/src/utils/wsl.ts` | `internal/codingagent/wsl.go` | ✅ |
| `packages/coding-agent/src/utils/zip.ts` | `(Go archive/zip, used by internal/codingagent/bug_report.go)` | n/a |

## `packages/tui/src/`

| upstream | pig | status |
|---|---|---|
| `packages/tui/src/tui.ts` | `tui/tui.go, tui/cell_size.go (CSI 16t query and response consumption), tui/mouse.go (mouse event API, Container/Box dispatch, overlay bounds and hit testing), internal/codingagent/interactive_input.go (driver-owned terminal input routing)` | ✅ |
| `packages/tui/src/editor-component.ts` | `tui/editor.go` | ✅ |
| `packages/tui/src/autocomplete.ts` | `tui/autocomplete.go + tui/file_autocomplete.go` | ✅ |
| `packages/tui/src/fuzzy.ts` | `tui/fuzzy.go` | ✅ |
| `packages/tui/src/keybindings.ts` | `tui/keybindings.go` | ✅ |
| `packages/tui/src/keys.ts` | `tui/key_match.go + tui/key_parse.go + tui/keys_decode.go + tui/keybindings.go + internal/codingagent/keys.go + internal/codingagent/stdin_buffer.go + internal/codingagent/kitty_csi.go` | ✅ |
| `packages/tui/src/kill-ring.ts` | `tui/kill_ring.go` | ✅ |
| `packages/tui/src/stdin-buffer.ts` | `internal/codingagent/stdin_buffer.go` | ✅ |
| `packages/tui/src/terminal.ts` | `tui/terminal.go + tui/terminal_native_shift_enter.go (isAppleTerminalSession, normalizeNativeShiftEnterInput, normalizeAppleTerminalInput, and forwardInputSequence's normalization, applied after StdinBuffer splitting in internal/codingagent/interactive_input.go, startup_ui.go, and cmd/pig/config_command.go)` | ✅ |
| `packages/tui/src/terminal-colors.ts` | `tui/theme.go: ParseOsc11BackgroundColor, parseOscHexChannel, DetectTerminalBackground (COLORFGBG + luminance), ansi256ToHex, GetThemeForRgbColor, GetDefaultTheme; unit-tested in terminal_colors_test.go` | ✅ |
| `packages/tui/src/terminal-image.ts` | `tui/terminal_image.go` | ✅ |
| `packages/tui/src/undo-stack.ts` | `tui/undo_stack.go` | ✅ |
| `packages/tui/src/native-modifiers.ts` | `tui/native_modifiers.go (isNativeModifierPressed) over internal/nativeplatform/modifiers_darwin.go (CGO-free lazy dlopen of CoreGraphics CGEventSourceFlagsState via libSystem trampolines), modifiers_windows.go (user32 GetAsyncKeyState), and modifiers_other.go` | ✅ |
| `packages/tui/src/word-navigation.ts` | `tui/editor.go (prevWordStart/nextWordEnd editor word navigation; unit-tested TestEditor_WordBoundaryHelpers; grapheme-class classification is approved divergence D27 vs Intl.Segmenter, identical for ASCII/Latin/code)` | ✅ |
| `packages/tui/src/utils.ts` | `tui/widthx/*.go` | ✅ |
| `packages/tui/src/index.ts` | `(barrel)` | n/a |
| `packages/tui/src/components/box.ts` | `tui/box.go` | ✅ |
| `packages/tui/src/components/cancellable-loader.ts` | `tui/cancellable_loader.go` | ✅ |
| `packages/tui/src/components/editor.ts` | `tui/editor.go + tui/graphemes.go` | ✅ |
| `packages/tui/src/components/image.ts` | `tui/image.go` | ✅ |
| `packages/tui/src/components/input.ts` | `tui/text_input.go` | ✅ |
| `packages/tui/src/components/loader.ts` | `tui/loader.go` | ✅ |
| `packages/tui/src/components/markdown.ts` | `tui/markdown.go` | ✅ |
| `packages/tui/src/components/select-list.ts` | `tui/select_filterable.go` | ✅ |
| `packages/tui/src/components/settings-list.ts` | `tui/settings_list.go` | ✅ |
| `packages/tui/src/components/spacer.ts` | `tui/spacer.go` | ✅ |
| `packages/tui/src/components/text.ts` | `tui/text.go` | ✅ |
| `packages/tui/src/components/truncated-text.ts` | `tui/truncated_text.go (D66 clamps horizontal padding when the full padding cannot fit instead of emitting a fatal over-wide row)` | ✅ |
| `packages/tui/src/components/alt-screen-flash.ts` | `tui/alt_screen_flash.go (ported; default/explicit duration, timer removal, rendering, truncation, disposal, and ordering unit-tested in tui/alt_screen_flash_test.go, including TestAltScreenFlashExplicitNonPositiveDurationExpiresImmediately; composite-render wired in tui_alt_screen.go; flash("Copied!") production trigger wired via app.message.copy/Ctrl+X → confirmMessageCopied in interactive.go, unit-tested TestConfirmMessageCopied_FullscreenFlashesInsteadOfStatusLine)` | ✅ |
| `packages/tui/src/components/h-stack.ts` | `tui/stack.go (HStack, ported + unit-tested side-by-side/align compositing; Stack-base parity)` | ✅ |
| `packages/tui/src/components/scroll-view.ts` | `tui/scroll_view.go` | ✅ |
| `packages/tui/src/components/stack.ts` | `tui/stack.go` | ✅ |
| `packages/tui/src/components/v-stack.ts` | `tui/stack.go` | ✅ |
| `packages/tui/src/latex.ts` | `internal/latex/` | ✅ |
| `packages/tui/src/layout-node.ts` | `tui/layout_node.go` | ✅ |
| `packages/tui/src/layout.ts` | `tui/layout.go` | ✅ |
| `packages/tui/src/tui-alt-screen.ts` | `tui/tui_alt_screen.go, tui/tui_alt_screen_input.go, tui/tui_alt_screen_mouse.go, tui/tui_alt_screen_selection.go` | ✅ |
| `packages/tui/src/tui-main-screen.ts` | `tui/tui.go` | ✅ |
| `packages/tui/src/alt-screen-search.ts` | `tui/alt_screen_search.go` | ✅ |
| `packages/tui/src/components/mouse-region.ts` | `tui/mouse_region.go` | ✅ |
| `packages/tui/src/native-module-path.ts` | `(lists install locations of prebuilt Node N-API .node helpers: the npm package, the bundle, and the node executable directory; pig is one Go binary that loads no Node addons)` | n/a |
| `packages/tui/src/native-platform.ts` | `(loads per-platform Node N-API helpers; pig loads no Node addons and covers each capability natively: clipboard via command-line tools in internal/codingagent/clipboard.go, clipboard_text.go, and slash_helpers.go; Windows VT input via golang.org/x/term MakeRaw in tui/terminal.go; isModifierPressed via the same macOS and Windows system calls in internal/nativeplatform)` | n/a |
