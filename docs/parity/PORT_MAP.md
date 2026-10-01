# PORT_MAP

Minimal upstream to pig file map.

- `docs/parity/PORT_MAP.md` answers where code lives
- `test/parity/coverage.md` answers what is verified and at what quality
- status here means code mapping only, not parity proof

Status: `✅` ported, `🟡` partial, `⬜` not started, `⏸` deferred, `🔴` broken, `n/a` not applicable.

Verification quality is tracked by the parity system, not by this status column. A row can be `✅` and still weak-only or untested in `test/parity/coverage.md`. Boot-only, smoke-only, and deferred scenarios do not count as behavioral verification. Registration-only scenarios count only for registry/catalog/auth-wiring paths. Provider implementation rows (for example `providers/openai-completions.ts`, `providers/openai-responses*.ts`, `providers/transform-messages.ts`) require stream/payload/history behavior tests; `--list-models` can only cover registry/catalog wiring.

## Version and gates

`internal/coding/pigversion/pigversion.go` is the sole version pin; `coding/upstream.go` re-exports it as `coding.UpstreamVersion` and `coding.UpstreamCommit`. `test/upstream-parity/mirror_version_test.go` rejects a `.upstream/current` mirror with a different package version. This map intentionally does not copy the version number.

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
- `packages/mcp` is the Model Context Protocol client library and `packages/codemode` is the QuickJS-sandboxed code-execution runtime. Both back the built-in `packages/coding-agent/src/extensions/{mcp,codemode}` extensions, whose files are mapped in the coding-agent section. The two library packages are in the interface and test ledgers (`upstream-v0.99.1.json`, `test-mapping-v0.99.1.json`) but not in this file denominator; `docs/plan/upgrade-0.99.1.md` D-B records the decision.

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

## `packages/ai/scripts/`

| upstream | pig | status |
|---|---|---|
| `packages/ai/scripts/models-dev-reasoning-options.ts` | `internal/modelgen/reasoning_options.go` | ✅ |
| `packages/ai/scripts/openrouter-reasoning-options.ts` | `internal/modelgen/reasoning_options.go` | ✅ |
| `packages/ai/scripts/generate-models.ts` | `cmd/gen-models/main.go + cmd/gen-models/reasoning.go + cmd/gen-models/models_dev.go (catalog emission, raw vendor reasoning snapshots, models.dev Fireworks/Qwen stages and strict JSON publication; other source-provider stages and TypeScript publication remain unported)` | 🟡 |
| `packages/ai/scripts/check-model-data.ts` | `cmd/check-model-data/main.go (validation command)` | 🟡 |
| `packages/ai/scripts/model-data.ts` | `cmd/check-model-data/model_data.go + model_data_json.go (identity, shard, manifest, hash and metadata validation; Node supplies Date.parse and JSON syntax diagnostics)` | 🟡 |

## `packages/agent/src/`

| upstream | pig | status |
|---|---|---|
| `packages/agent/src/agent.ts` | `agent/agent.go + agent/lifecycle.go + agent/queue.go (StreamFunction exposes the caller override; nil denotes the stock path, whose DefaultStreamFn is stored separately because Go cannot compare functions)` | ✅ |
| `packages/agent/src/agent-loop.ts` | `partial: agent/agent_loop.go + agent/agent.go + agent/message_json.go + agent/tool_execution.go + agent/system_state.go; shallow stream views use ai/assistant_observation.go and ai/stream_observation.go` | 🟡 |
| `packages/agent/src/types.ts` | `agent/types.go + agent/agent.go + agent/message_json.go` | ✅ |
| `packages/agent/src/proxy.ts` | `agent/proxy.go (partial: request/event reconstruction covered by agent/proxy_test.go; malformed terminal-event suppression remains under MA-007 review)` | 🟡 |
| `packages/agent/src/index.ts` | `(Go has no barrel exports)` | n/a |
| `packages/agent/src/node.ts` | `(Node-only package entrypoint)` | n/a |
| `packages/agent/src/harness/messages.ts` | `agent/messages.go + agent/transform.go + agent/harness/messages.go (unnormalized harness conversion)` | ✅ |
| `packages/agent/src/harness/prompt-templates.ts` | `agent/harness/prompt_templates.go` | 🟡 |
| `packages/agent/src/harness/system-prompt.ts` | `agent/harness/system_prompt.go` | ✅ |
| `packages/agent/src/harness/types.ts` | `agent/harness/types.go (Skill, PromptTemplate, resources, AgentHarnessTool + invocation/update contracts, stream options, FileSystem/Shell/ExecutionEnv, file/execution/compaction/branch-summary errors, shell-output view/update JSON; Result/ok/err/getOrThrow/toError are Go (T, error); unit-tested)` | ✅ |
| `packages/agent/src/harness/agent-harness.ts` | `coding/session.go + coding/runtime.go + agent/harness/runtime/models.go + agent/harness/runtime/harness.go (runtime options, constructor and provider boundary)` | ✅ |
| `packages/agent/src/harness/tools/bash.ts` | `agent/harness/tools/tools.go + internal/codingagent/tools/harness_bash.go (environment-backed factory, preparation, bounded capture and checkpoints)` | 🟡 |
| `packages/agent/src/harness/tools/edit-diff.ts` | `internal/codingagent/tools/edit_diff.go + unified_patch.go + diff_string.go` | ✅ |
| `packages/agent/src/harness/tools/edit.ts` | `agent/harness/tools/tools.go + internal/codingagent/tools/harness_files.go + edit.go (environment-backed factory sharing edit argument and diff primitives)` | 🟡 |
| `packages/agent/src/harness/tools/file-mutation-queue.ts` | `internal/codingagent/tools/harness_context.go (environment-scoped registration and canonical-path mutation settlement)` | 🟡 |
| `packages/agent/src/harness/tools/image.ts` | `internal/codingagent/tools/image_detect.go + internal/imageprocessing/images.go (detectSupportedImageMimeType, isBmp)` | ✅ |
| `packages/agent/src/harness/tools/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/tools/path-utils.ts` | `internal/codingagent/tools/harness_context.go + path_utils.go (execution-environment path resolution and shared filename variants)` | 🟡 |
| `packages/agent/src/harness/tools/read.ts` | `agent/harness/tools/tools.go + internal/codingagent/tools/harness_files.go + read.go (environment-backed reads sharing text truncation; injected image processing)` | 🟡 |
| `packages/agent/src/harness/tools/tool-context.ts` | `agent/harness/tools/tools.go + internal/codingagent/tools/harness_context.go (embeddable ExecutionToolContext carries the environment while preserving application context identity)` | 🟡 |
| `packages/agent/src/harness/tools/write.ts` | `agent/harness/tools/tools.go + internal/codingagent/tools/harness_files.go (environment-backed write factory)` | 🟡 |
| `packages/agent/src/harness/skills.ts` | `agent/harness/skills.go + internal/ignorerules/ignorerules.go` | 🟡 |
| `packages/agent/src/harness/compaction/compaction.ts` | `agent/harness/compaction/compaction.go + agent/harness/compaction/generation.go` | ✅ |
| `packages/agent/src/harness/compaction/utils.ts` | `partial: agent/harness/compaction/utils.go + agent/harness/compaction/file_operations.go; retained-tail suite covers ASCII serialization and file details; complete argument insertion order and isolated UTF-16 surrogate serialization remain unverified` | 🟡 |
| `packages/agent/src/harness/compaction/branch-summarization.ts` | `agent/harness/compaction/branch_summarization.go` | 🟡 |
| `packages/agent/src/harness/utils/shell-output.ts` | `internal/codingagent/tools/truncate.go + internal/codingagent/tool_render.go` | ✅ |
| `packages/agent/src/harness/utils/truncate.ts` | `internal/codingagent/tools/truncate.go` | ✅ |
| `packages/agent/src/harness/env/nodejs.ts` | `agent/harness/env/{env,paths,errors,shell,exec,line_reader,process_unix,process_windows}.go and internal/nodeurl/fileurl.go (NodeExecutionEnv: host-OS FileSystem + Shell; ~ and file:// path resolution, errno to FileErrorCode with Node-shaped messages, LF TextLineReader, temp dirs/files, bash exec with timeout/env layering/bounded OutputCapture/raw-byte spill/process-group kill/ctx abort/cleanup; every nodejs-env.test.ts and text-line-reader.test.ts case ported)` | ✅ |
| `packages/agent/src/harness/session/session.ts` | `agent/harness/session/session.go (StorageBackedSession synchronous mutation admission, owned async completion, mutation barrier and Branch API)` | ✅ |
| `packages/agent/src/harness/result.ts` | `agent/harness/result.go (LaneBusy, OperationMismatch, NoActiveRun, NoActiveOperation, NothingToResume, NothingToCompact, InvalidMessage, InvalidNavigation, UnknownSkill, UnknownTemplate, UnknownTarget, InvalidLane, Closed with the upstream toJSON shape; HarnessFault, HarnessClosed; the Result monad and matchError are Go (T, error) with errors.As; JSON shapes probed against upstream under Node 24)` | ✅ |
| `packages/agent/src/harness/session/context.ts` | `agent/harness/session/context.go (latest compaction, retained tail, filtered assistant responses, ordered projectors; unit tests)` | ✅ |
| `packages/agent/src/harness/session/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/session/jsonl/codec.ts` | `agent/harness/session/jsonl_codec.go (header validation and transaction roundtrip; TestJsonlHeaderValidation, TestJsonlStorageRoundTripAndWholeListDeletion)` | ✅ |
| `packages/agent/src/harness/session/jsonl/repo.ts` | `agent/harness/session/jsonl_repo.go (cwd discovery, exclusive handles, forks; TestJsonlSessionRepoConformance, TestJsonlRepositoryCwdDiscoveryAndEncodedIDs)` | ✅ |
| `packages/agent/src/harness/session/jsonl/storage.ts` | `agent/harness/session/jsonl_storage.go (atomic transaction persistence and recovery; TestJsonlStorageConformance, TestJsonlTornTailAndCompleteCorruption, TestJsonlAppendFailureDoesNotAdvanceLiveState)` | ✅ |
| `packages/agent/src/harness/session/jsonl/types.ts` | `agent/harness/session/jsonl_types.go (format headers and repo options; TestJsonlHeaderValidation, TestJsonlRepositoryCwdDiscoveryAndEncodedIDs)` | ✅ |
| `packages/agent/src/harness/session/memory.ts` | `agent/harness/session/memory.go (in-memory storage/repo; facade-owned async mutation admission; current Storage and SessionRepo conformance)` | ✅ |
| `packages/agent/src/harness/session/testing/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/session/testing/types.ts` | `agent/harness/session/testing/types.go (type-only StorageFixture and ConformanceCase contracts; no executable upstream body; backend conformance does not independently prove this row)` | ✅ |
| `packages/agent/src/harness/session/types.ts` | `agent/harness/session/{types,operation,session}.go (current session and operation unions; upstream JSON fixtures and conformance)` | ✅ |
| `packages/agent/src/harness/telemetry.ts` | `agent/harness/telemetry.go + agent/harness/telemetry_schema.go + agent/harness/telemetry_schema_data.go (partial: span parenting, outcomes and schema fixtures covered by agent/harness/telemetry_test.go; full runtime integration not yet certified)` | 🟡 |
| `packages/agent/src/harness/config.ts` | `agent/harness/config.go (DefaultRetryPolicy, ValidateToolNames, ValidateRetryPolicy, ValidateCompactionSettings; unit-tested)` | ✅ |
| `packages/agent/src/harness/context.ts` | `agent/harness/context.go + agent/harness/telemetry_memory.go + telemetry/context.go + telemetry/memory.go (partial: shared telemetry carriers, in-memory recorder and context values; runtime qualification pending)` | 🟡 |
| `packages/agent/src/harness/events.ts` | `agent/harness/agentharness/events.go + agent/harness/agentharness/clone.go + agent/harness/agentharness/event_types.go (partial: event delivery and cloning covered by events_test.go and clone_test.go; full event decoding and error-reporting closure not certified, MA-006)` | 🟡 |
| `packages/agent/src/harness/execution/assistant.ts` | `agent/harness/execution/assistant.go (partial: curated options, ordered lifecycle and cancellation settlement; runtime qualification pending)` | 🟡 |
| `packages/agent/src/harness/execution/effect-gate.ts` | `agent/harness/execution/effect_gate.go (partial: admission and abort lifecycle covered by agent/harness/execution/effect_gate_test.go; full execution pipeline integration not yet certified)` | 🟡 |
| `packages/agent/src/harness/execution/tools.ts` | `agent/harness/execution/tools.go (partial: preparation, admission, execution and finalization primitives; runtime qualification pending)` | 🟡 |
| `packages/agent/src/harness/hooks.ts` | `agent/harness/agentharness/hooks.go + agent/harness/agentharness/hook_types.go (partial: admission, mutation and ordering covered by hooks_test.go; full dependent runtime integration not yet certified)` | 🟡 |
| `packages/agent/src/harness/pico3/bash.ts` | `agent/harness/pico3/bash.go, bash_unix.go, bash_windows.go; tool_bounds_test.go` | ✅ |
| `packages/agent/src/harness/pico3/bounded.ts` | `agent/harness/pico3/bounded.go; tool_bounds_test.go` | ✅ |
| `packages/agent/src/harness/pico3/chord.ts` | `agent/harness/pico3/{chord,chord_service,chord_ops}.go (typed service tokens, scoped forwarding, bounded caller-supplied state publication; chord_test.go, chord_lifecycle_test.go)` | ✅ |
| `packages/agent/src/harness/pico3/context.ts` | `agent/harness/pico3/context.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/harness.ts` | `agent/harness/pico3/harness.go, conversation.go; lifecycle_test.go, transactions_test.go, scheduler_resume_test.go` | ✅ |
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
| `packages/agent/src/harness/pico3/scheduler.ts` | `agent/harness/pico3/scheduler.go; lifecycle_test.go, waiters_test.go, spec_scheduler_process_test.go, scheduler_resume_test.go` | ✅ |
| `packages/agent/src/harness/pico3/session.ts` | `agent/harness/pico3/session.go, session_docs.go, tx.go, tx_writes.go; transactions_test.go` | ✅ |
| `packages/agent/src/harness/pico3/system.ts` | `agent/harness/pico3/system.go; turn_test.go` | ✅ |
| `packages/agent/src/harness/pico3/types.ts` | `agent/harness/pico3/types.go, kind.go, runtime.go, json.go; transactions_test.go, lifecycle_test.go` | ✅ |
| `packages/agent/src/harness/pico3/view.ts` | `agent/harness/pico3/view.go; watch_test.go` | ✅ |
| `packages/agent/src/harness/runtime/drive.ts` | `agent/harness/runtime/drive.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/boundary.ts` | `agent/harness/runtime/drive_boundary.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/checkpoint.ts` | `agent/harness/runtime/drive_checkpoint.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/deferred.ts` | `agent/harness/runtime/drive_deferred.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/generation.ts` | `agent/harness/runtime/drive_generation.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/reconcile.ts` | `agent/harness/runtime/drive_reconcile.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/recovery.ts` | `agent/harness/runtime/drive_recovery.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/response.ts` | `agent/harness/runtime/drive_response.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/retry.ts` | `agent/harness/runtime/drive_retry.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/structural.ts` | `agent/harness/runtime/drive_structural.go + agent/harness/runtime/structural_preparation.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/terminal.ts` | `agent/harness/runtime/drive_terminal.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/tool-placement.ts` | `agent/harness/runtime/drive_tool_placement.go` | 🟡 |
| `packages/agent/src/harness/runtime/drive/tools.ts` | `agent/harness/runtime/drive_tools.go` | 🟡 |
| `packages/agent/src/harness/runtime/harness.ts` | `agent/harness/runtime/harness.go + agent/harness/runtime/harness_config.go (lane acquisition, configuration, global metadata and close; runtime qualification pending)` | 🟡 |
| `packages/agent/src/harness/runtime/index.ts` | `(barrel)` | n/a |
| `packages/agent/src/harness/runtime/lane.ts` | `agent/harness/runtime/lane.go + agent/harness/runtime/lane_state.go + agent/harness/runtime/lane_queue.go + agent/harness/runtime/accept.go + agent/harness/runtime/lane_watch.go + agent/harness/runtime/lane_drive.go + agent/harness/runtime/lane_public.go + agent/harness/runtime/types.go (concrete lane, admission, commands, drive/control, convenience APIs and watches; runtime qualification pending)` | 🟡 |
| `packages/agent/src/harness/runtime/progress.ts` | `agent/harness/runtime/progress.go` | 🟡 |
| `packages/agent/src/harness/runtime/reducer.ts` | `agent/harness/runtime/reducer.go` | 🟡 |
| `packages/agent/src/harness/runtime/restore.ts` | `agent/harness/runtime/restore.go` | 🟡 |
| `packages/agent/src/harness/runtime/transcript.ts` | `agent/harness/runtime/transcript.go (entry chaining, lifecycle events and bounded context/queue reads)` | 🟡 |
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
| `packages/agent/src/harness/utils/output-capture.ts` | `agent/harness/utils/output_capture.go + utf8_stream.go + internal/codingagent/tools/harness_output.go (OutputCapture, shared ApplyShellOutputUpdate, SanitizeShellOutput; WHATWG-style streaming UTF-8 decode; slide offsets in UTF-16 code units; upstream cases plus a -race wall-clock fold test)` | ✅ |
| `packages/agent/src/harness/utils/usage.ts` | `agent/harness/utils/usage.go (EmptyUsage, AddUsage with optional-field absence; unit-tested)` | ✅ |
| `packages/agent/src/search/index.ts` | `agent/search/search.go (API/type correspondence only; compile-signature checks in agent/search/search_test.go; upstream defines no backend behavior)` | ✅ |
| `packages/agent/src/harness/pico3/legacy-tracker.ts` | `agent/harness/pico3/delta.go (Tracker: State, Flush, Rebase over the ported chord operation primitives; legacy_tracker_test.go)` | 🟡 |

## `packages/ai/src/`

| upstream | pig | status |
|---|---|---|
| `packages/ai/src/types.ts` | `ai/types.go + ai/model_compat_json.go + ai/provider_streams.go + ai/deferred_option.go + coding/extension/model_stream.go + coding/extension/host/subprocess/model_fetch.go + coding/extension/host/subprocess/runtime-node/model-fetch.mjs (per-request fetch carrier)` | ✅ |
| `packages/ai/src/models.generated.ts` | `ai/models_generated.go` | ✅ |
| `packages/ai/src/models.ts` | `ai/models_runtime*.go + ai/models_provider.go + ai/models_catalog_codec.go + ai/models_options.go + ai/model_utils.go (native provider collection, auth, publication, streams and model helpers; complete models-runtime.test.ts cases)` | ✅ |
| `packages/ai/src/models-store.ts` | `ai/models_store.go (ModelsStoreEntry, ModelsStore, InMemoryModelsStore; ai/models_store_test.go, ai/radius_test.go TestRadiusProviderRestoresStoredCatalogOffline) + internal/codingagent/model_registry.go (dynamic provider/model store analogue)` | ✅ |
| `packages/ai/src/model-catalog.ts` | `designed out: pig flattens the API-grouped catalog at cmd/gen-models codegen into models_generated.go (collectRows); upstream flattenModelCatalog (Object.assign of group values) runs per-provider at module load, no Go runtime equivalent` | n/a |
| `packages/ai/src/providers/all.ts` | `ai/registry.go + ai/register_builtins.go + ai/images_registry.go` | ✅ |
| `packages/ai/src/api/anthropic-messages.ts` | `ai/direct_simple.go + ai/anthropic.go + ai/anthropic_stream.go + ai/anthropic_client.go + ai/anthropic_federation.go + ai/provider_request_options.go + ai/constrained_sampling.go (createClient auth/header branches including the ai.PiUserAgent() default User-Agent from ai/user_agent.go (D65), betas, OAuth Claude Code identity, tool-name conversion, strict JSON-schema tools across current, initial, and deferred declarations, toolChoice, metadata.user_id, complete stop-reason and incomplete-stream errors, thinking_tokens usage, usage retention on error, explicit-zero temperature compatibility, managed mid-conversation effort with historical/current effort markers, adaptive drop-block binding, beta selection, and providerThinkingLevel, and native mid-conversation tool changes with tool_addition/tool_removal blocks, deferred placeholder/later tools, beta selection, and safe fallback to the current tool list; the executor-turn body pipeline with its Node 24.19/26.7 tick-order differential in ai/anthropic_microtask_trace_test.go and ai/anthropic_stream_test.go over coding/testdata/rpc33-observation/providers/anthropic-messages; Pi 0.99.2 workload identity federation in ai/anthropic_federation.go (getAnthropicFederation, token exchange, expiry refresh, the per-config federation client cache; ai/anthropic_federation_upstream_test.go, ai/anthropic_federation_sdk_upstream_test.go, ai/anthropic_token_cache_test.go) and the strict-tool keyword fallback (ai/anthropic.go anthropicStrictUnsupportedKeyword; ai/anthropic_strict_tool_schema_upstream_test.go, ai/anthropic_strict_keywords_test.go); tests in ai/anthropic_oauth_test.go, ai/anthropic_test.go, ai/anthropic_contract_0861_test.go, ai/anthropic_effort_strict_test.go, ai/constrained_sampling_test.go, ai/transcript_tool_changes_test.go, and agent/native_tool_changes_test.go); missing: server-side fallback (allowedFallbackModels fallbacks param, server-side-fallback-2026-07-01 beta, fallback content block and cost); open Pi 0.99.2 federation gaps (audit-992-ai, fix-992-federation-auth): the provider injects the ambient ANTHROPIC_AUTH_TOKEN as an Authorization header before it checks federation, so a direct stream with that token and the federation variables does not exchange the identity token (F1), and an unreachable token endpoint reports Go's *url.Error text instead of Node's "TypeError: fetch failed" (F2)` | 🟡 |
| `packages/ai/src/api/constrained-sampling.ts` | `ai/constrained_sampling.go + ai/schema_object_order.go (imported schema-key order;grammar + canonical JSON-schema strict constrained sampling; wired into Anthropic, OpenAI Completions, OpenAI Responses, Mistral, Bedrock, and Google request paths; unit and provider-wire tests in ai/constrained_sampling_test.go, ai/anthropic_test.go, ai/openai_test.go, ai/openai_responses_constrained_test.go, ai/mistral_test.go, ai/bedrock_test.go, and ai/google_test.go)` | ✅ |
| `packages/ai/src/api/azure-openai-responses.ts` | `ai/direct_simple.go + ai/azure_openai_responses.go (logical model versus deployment identity and reasoning replay)` | ✅ |
| `packages/ai/src/api/bedrock-converse-stream.ts` | `ai/bedrock.go + ai/direct_simple.go (simple API dispatch preserves the selected model and delegates optional/keyless authentication to Bedrock)` | ✅ |
| `packages/ai/src/api/google-generative-ai.ts` | `ai/google.go + ai/direct_simple.go (SDK system-instruction user role and optional/strict/explicit function-calling mode; request guards in ai/provider_wire_defaults_test.go)` | ✅ |
| `packages/ai/src/api/google-vertex.ts` | `ai/google_vertex.go` | ✅ |
| `packages/ai/src/api/mistral-conversations.ts` | `ai/mistral.go + ai/mistral_reader.go + ai/mistral_response_body.go + ai/direct_simple.go + coding/model.go + internal/nodeurl/resolve.go + internal/nodeurl/serialize.go (SDK payload lowering, WHATWG request URLs, request lifetime, keyed tool fragments, selected-model replay through TransformMessages and reasoning controls; ai/mistral_http_transport_upstream_test.go + ai/mistral_reasoning_mode_upstream_test.go + ai/mistral_regression_test.go + ai/mistral_replay_transform_test.go + ai/mistral_ticks_test.go + ai/mistral_oracle_test.go + cmd/pig/rpc_mistral_oracle_test.go + coding/mistral_replay_metadata_test.go)` | ✅ |
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
| `packages/ai/src/auth/credential-store.ts` | `ai/in_memory_credential_store.go (literal credentials, insertion order, per-provider mutation chains and independent cancellation)` | ✅ |
| `packages/ai/src/auth/helpers.ts` | `ai/auth_login.go + ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers.go + ai/auth_native_login.go + ai/oauth_*.go` | ✅ |
| `packages/ai/src/auth/resolve.ts` | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers.go + ai/oauth_*.go` | ✅ |
| `packages/ai/src/auth/types.ts` | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_interaction.go + ai/auth_providers.go + ai/credential_json.go + ai/oauth_*.go` | ✅ |
| `packages/ai/src/auth/oauth/anthropic.ts` | `ai/oauth_anthropic.go` | ✅ |
| `packages/ai/src/auth/oauth/device-code.ts` | `ai/oauth_device_code.go` | ✅ |
| `packages/ai/src/auth/oauth/github-copilot.ts` | `ai/githubcopilot.go + ai/copilot_verification_uri.go + ai/oauth_registry.go + ai/auth_providers.go (device notifications, shared RFC 8628 polling, account catalog refresh and enterprise credential routing)` | ✅ |
| `packages/ai/src/auth/oauth/load.ts` | `ai/oauth_registry.go` | ✅ |
| `packages/ai/src/auth/oauth/kimi-coding.ts` | `ai/oauth_kimi.go (device flow and refresh ported; caller-path parity pending)` | ✅ |
| `packages/ai/src/auth/oauth/openrouter.ts` | `ai/oauth_openrouter.go (OpenRouter OAuth login/exchange; registered in oauth_registry.go; unit-tested + oauth/08 behavioral scenario)` | ✅ |
| `packages/ai/src/auth/oauth/openai-codex.ts` | `ai/oauth_openai_codex.go` | ✅ |
| `packages/ai/src/auth/oauth/pkce.ts` | `ai/pkce.go` | ✅ |
| `packages/ai/src/auth/oauth/radius.ts` | `ai/oauth_radius.go + internal/codingagent/interactive_auth.go (ai/oauth_radius_test.go, internal/codingagent/radius_login_test.go)` | ✅ |
| `packages/ai/src/auth/oauth/xai.ts` | `ai/oauth_xai.go (device-code login, polling, refresh, response validation, and owner-context cancellation; ai/oauth_xai_test.go including LoginContext/RefreshTokenContext cancellation, cmd/pig/model_oauth_refresh_test.go caller cancellation, oauth/09 behavioral scenario)` | ✅ |
| `packages/ai/src/bun-oauth.ts` | `(Bun-only OAuth flow registration; Go links OAuth implementations directly)` | n/a |
| `packages/ai/src/compat.ts` | `ai/types.go` | ✅ |
| `packages/ai/src/compat/extension-oauth-types.ts` | `ai/oauth_types.go + coding/extension/provider.go (type-only legacy OAuth callback and prompt contracts; runtime OAuth behavior belongs to the auth/provider rows)` | ✅ |
| `packages/ai/src/legacy-api-aliases.ts` | `(barrel/backward-compatible TS aliases)` | n/a |
| `packages/ai/src/providers/openai-codex.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (OpenAI Codex provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/providers/openai.ts` | `ai/models_generated.go + internal/codingagent/model_registry.go (OpenAI provider metadata/catalog registration)` | ✅ |
| `packages/ai/src/utils/error-body.ts` | `ai/errors.go + ai/openai_error.go + ai/openai.go + ai/openai_responses.go + ai/bedrock.go + internal/jsonstringify/json.go (shared normalization, SDK extraction, bounded text and JSON fallback; ai/error_body_upstream_test.go + ai/openai_error_test.go)` | ✅ |
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
| `packages/ai/src/providers/cloudflare-auth.ts` | `ai/auth_providers.go + ai/provider_login.go + ai/models_generated.go + internal/codingagent/model_registry.go (provider auth, login prompts, metadata/catalog registration)` | ✅ |
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
| `packages/ai/src/providers/radius-config.ts` | `ai/radius_config.go (ai/radius_config_test.go; test/parity/scenarios/model-runtime-store-catalog/11-radius-metadata-import.toml)` | ✅ |
| `packages/ai/src/providers/radius.ts` | `ai/radius.go + internal/codingagent/radius_models.go + internal/codingagent/radius_composition.go (ai/radius_test.go, internal/codingagent/radius_models_test.go, internal/codingagent/radius_composition_test.go, coding/radius_composition_test.go, coding/model_test.go; test/parity/scenarios/model-runtime-store-catalog/11-radius-metadata-import.toml)` | ✅ |
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
| `packages/ai/src/api/lazy.ts` | `ai/provider_streams.go + ai/models_runtime_stream.go + ai/continuation.go + coding/model_stream_forwarding.go (owned setup, promise reactions, ordered reference forwarding, and result settlement; dynamic module loading is statically linked in Go)` | 🟡 |
| `packages/agent/src/stream-fn.ts` | `agent/stream_fn.go + agent/agent.go + agent/agent_loop.go (configured host stream fallback and explicit StreamFn injection; native models retain their provider runtime fallback)` | 🟡 |
| `packages/ai/src/env-api-keys.ts` | `ai/auth_env_keys.go` | ✅ |
| `packages/ai/src/session-resources.ts` | `ai/session_resources.go + coding/session.go` | ✅ |
| `packages/ai/src/oauth.ts` | `ai/auth.go` | ✅ |
| `packages/ai/src/index.ts` | `ai/doc.go` | n/a |
| `packages/ai/src/cli.ts` | `cmd/pig/auth_commands.go (login/logout)` | ✅ |
| `packages/ai/src/bedrock-provider.ts` | `ai/bedrock.go` | ✅ |
| `packages/ai/src/api/openai-completions.ts` | `ai/openai_error.go + ai/direct_simple.go + ai/openai.go + ai/provider_request_options.go + ai/openai_stream_decoder.go + ai/native_body_iterator.go (tool-choice/stream/cache/thinking-budget upstream tests, native source options, and SDK iterator boundaries; Kimi tool additions: ai/transcript_tool_changes_test.go)` | ✅ |
| `packages/ai/src/api/openai-responses.ts` | `ai/openai_error.go + ai/openai_responses.go + ai/direct_simple.go + ai/provider_request_options.go` | ✅ |
| `packages/ai/src/api/openai-responses-shared.ts` | `ai/openai_responses.go + ai/openai_stream_decoder.go + ai/native_body_iterator.go (SDK iterator and scratch lifecycle; additional_tools and synthetic tool_search tool additions: ai/transcript_tool_changes_test.go)` | ✅ |
| `packages/ai/src/api/openai-codex-responses.ts` | `ai/direct_simple.go + ai/openai_codex_responses.go + ai/openai_codex_http.go + ai/openai_codex_frames.go + ai/openai_codex_websocket.go + ai/openai_responses.go (native retries, source response observation and pointer timeouts, selected-model metadata and pricing, account/session scoped continuation; all 30 streaming case sites/39 rows, Model Runtime caller tests and exact Pi scenarios; live cache-affinity acceptance remains credential-gated)` | 🟡 |
| `packages/ai/src/api/openai-prompt-cache.ts` | `ai/openai_prompt_cache.go` | ✅ |
| `packages/ai/src/api/pi-messages.ts` | `ai/pi_messages.go + ai/pi_messages_events.go + ai/pi_messages_reader.go (ai/pi_messages_test.go, ai/pi_messages_oracle_test.go, ai/pi_messages_ticks_test.go, cmd/pig/rpc_pi_messages_oracle_test.go, coding/model_test.go TestPiMessagesModelsJSONProviderStreamsThroughModelRuntime)` | ✅ |
| `packages/ai/src/providers/anthropic.ts` | `ai/anthropic.go + ai/anthropic_client.go + ai/anthropic_federation.go + ai/oauth_anthropic.go + ai/auth_providers.go + ai/auth_env_keys.go + internal/codingagent/model_registry.go + coding/model.go + cmd/pig/model.go (catalog; the ai resolver (ai/auth_providers.go anthropicAPIKeyAuth) reads ANTHROPIC_AUTH_TOKEN as an Authorization bearer header, then ANTHROPIC_OAUTH_TOKEN, then ANTHROPIC_API_KEY, then Pi 0.99.2 workload identity federation from the five ANTHROPIC_* federation variables (ai/auth_env_keys.go, exchanged by ai/anthropic_federation.go); subscription OAuth login; stored OAuth and api_key credentials outrank ambient auth and reach serialized requests on both SDK and CLI model paths; tests in ai/anthropic_oauth_test.go, ai/anthropic_federation_upstream_test.go, internal/codingagent/anthropic_env_auth_test.go, coding/anthropic_auth_test.go, cmd/pig/model_anthropic_auth_test.go). Federation is not yet an auth source on the registry and CLI paths: internal/codingagent/model_registry.go (HasAnyKey, hasEnvAuth, HasConfiguredAuth) and cmd/pig/model.go (AuthenticatedProviders) decide availability from the env-key tables and do not count federation-only env, so "pig --list-models" and "pig -p" report no anthropic auth with only the federation variables set (audit-992-ai F3), and the Node builtin-API bridge (coding/extension/host/subprocess/runtime-node/shims/pi-ai-bridge.mjs) rejects a federation-only anthropic request (F6); fix-992-federation-auth closes both` | 🟡 |
| `packages/ai/src/providers/google.ts` | `ai/google.go` | ✅ |
| `packages/ai/src/api/google-shared.ts` | `ai/google.go` | ✅ |
| `packages/ai/src/providers/google-vertex.ts` | `ai/auth_providers.go + ai/google_vertex.go + ai/provider_login.go` | ✅ |
| `packages/ai/src/providers/amazon-bedrock.ts` | `ai/auth_providers.go + ai/bedrock.go + ai/provider_login.go` | ✅ |
| `packages/ai/src/api/cloudflare.ts` | `ai/cloudflare.go` | ✅ |
| `packages/ai/src/providers/azure-openai-responses.ts` | `ai/azure_openai_responses.go` | ✅ |
| `packages/ai/src/providers/mistral.ts` | `ai/mistral.go` | ✅ |
| `packages/ai/src/api/github-copilot-headers.ts` | `ai/githubcopilot.go` | ✅ |
| `packages/ai/src/providers/faux.ts` | `ai/faux.go + ai/faux_deferred.go (queued streaming, canonical models, model-aware factories, caller-supplied timestamps, cache and deferred submit/fetch/cancel; complete helper/model-option contracts not closed)` | 🟡 |
| `packages/ai/src/api/transform-messages.ts` | `ai/transcript.go + agent/transform.go + ai/tool_flow.go + ai/transform_messages.go (shared provider tool-flow pass and OpenAI replay normalization; Agent retains D48 recovery)` | 🟡 |
| `packages/ai/src/providers/images/register-builtins.ts` | `ai/images_registry.go` | ✅ |
| `packages/ai/src/api/openrouter-images.ts` | `ai/openrouter_images.go` | ✅ |
| `packages/ai/src/api/simple-options.ts` | `ai/simple_options.go` | ✅ |
| `packages/ai/src/utils/event-stream.ts` | `ai/event_stream.go + ai/event_stream_continuation.go + ai/assistant_observation.go + ai/assistant_publication.go + ai/stream_observation.go + ai/stream_continuation.go` | ✅ |
| `packages/ai/src/utils/provider-env.ts` | `ai/provider_env.go (Bun empty process.env /proc fallback n/a in Go)` | ✅ |
| `packages/ai/src/images.ts` | `ai/images.go` | ✅ |
| `packages/ai/src/image-models.ts` | `ai/images_registry.go` | ✅ |
| `packages/ai/src/images-api-registry.ts` | `ai/images.go + ai/images_registry.go` | ✅ |
| `packages/ai/src/utils/hash.ts` | `ai/openai.go shortHash32 (UTF-16 code units with uint32 Math.imul wrap), used by ai/openai.go normalizeCompletionsToolCallID, ai/openai_responses.go foreign fc_ ids, and ai/mistral.go deriveMistralToolCallID (upstream oracles in ai/openai_responses_foreign_toolcall_test.go and ai/mistral_test.go)` | ✅ |
| `packages/ai/src/utils/headers.ts` | `ai/githubcopilot.go + ai/provider_response.go` | ✅ |
| `packages/ai/src/utils/json-parse.ts` | `(stdlib encoding/json)` | n/a |
| `packages/ai/src/utils/diagnostics.ts` | `agent/messages.go + ai/types.go` | ✅ |
| `packages/ai/src/utils/node-http-proxy.ts` | `ai/node_http_proxy.go + ai/bedrock.go` | ✅ |
| `packages/ai/src/utils/overflow.ts` | `ai/overflow.go (IsContextOverflow, IsRecoverableLength, GetOverflowPatterns; overflow_test.go)` | ✅ |
| `packages/ai/src/utils/text.ts` | `ai/text.go + ai/transcript.go + agent/context_tokens.go + internal/codingagent/export/tool_renderer.go` | ✅ |
| `packages/ai/src/utils/uuid.ts` | `(stdlib crypto/rand UUID helpers inline)` | n/a |
| `packages/ai/src/utils/abort-signals.ts` | `(stdlib context.Context combines signals)` | n/a |
| `packages/ai/src/utils/sanitize-unicode.ts` | `ai/sanitize_unicode.go` | ✅ |
| `packages/ai/src/utils/typebox-helpers.ts` | `(no TypeBox in Go)` | n/a |
| `packages/ai/src/utils/validation.ts` | `agent/validate.go + agent/validate_arguments.go + agent/validate_convert.go + agent/validate_errors.go + agent/validate_schema.go + agent/validate_json.go` | ✅ |
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
| `packages/ai/src/api/cloudflare-workers-ai-system-one.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/cloudflare-workers-ai-system-one.ts` | `(not started: classifier system-one API; the Go classifier registry has no dispatch yet)` | ⬜ |
| `packages/ai/src/api/llama-cpp-classify.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/llama-cpp-classify.ts` | `ai/llama_cpp_classify.go` | 🟡 |
| `packages/ai/src/api/system-one-shared.ts` | `ai/system_one.go` | 🟡 |
| `packages/ai/src/api/typesafe-system-one.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/typesafe-system-one.ts` | `(not started: typesafe system-one classifier API)` | ⬜ |
| `packages/ai/src/auth/oauth/callback-server.ts` | `ai/oauth_callback_server.go` | 🟡 |
| `packages/ai/src/auth/oauth/openai-chatgpt.ts` | `ai/oauth_openai_chatgpt.go` | 🟡 |
| `packages/ai/src/providers/typesafe.models.ts` | `(not started: typesafe provider catalog shard)` | ⬜ |
| `packages/ai/src/providers/typesafe.ts` | `(not started: typesafe provider)` | ⬜ |
| `packages/ai/src/utils/model-operations.ts` | `ai/model_types.go (ModelType and the per-type Models operations)` | 🟡 |
| `packages/ai/src/utils/models-error.ts` | `ai/auth_resolve.go (ModelsErrorCode, ModelsError, NewModelsError)` | 🟡 |
| `packages/ai/src/utils/oauth-page.ts` | `ai/oauth_page.go (upstream 0.99.1 moved the file unchanged from auth/oauth/oauth-page.ts)` | ✅ |

## `packages/coding-agent/src/`

| upstream | pig | status |
|---|---|---|
| `packages/coding-agent/src/cli.ts` | `cmd/pig/main.go` | ✅ |
| `packages/coding-agent/src/main.ts` | `cmd/pig/main.go + cmd/pig/startup_session.go + cmd/pig/extensions.go (mode/metadata routing)` | ✅ |
| `packages/coding-agent/src/index.ts` | `coding/extension/host/subprocess/runtime-node/shims/pi-coding-agent.mjs (pinned Node barrel; independent SDK objects; main-process identity remains D73)` | 🟡 |
| `packages/coding-agent/src/config.ts` | `internal/codingagent/paths.go + internal/codingagent/selfupdate_package_command.go + internal/codingagent/selfupdate_tier.go (D2 directory opt-in; D39 native update ownership)` | ✅ |
| `packages/coding-agent/src/migrations.ts` | `internal/codingagent/migrations.go` | ✅ |
| `packages/coding-agent/src/package-manager-cli.ts` | `cmd/pig/package_commands.go + cmd/pig/cli_error.go + cmd/pig/package_command_trust.go + cmd/pig/package_model_catalogs.go + cmd/pig/config_command.go + coding/packagecontent/packagecontent.go` | ✅ |
| `packages/coding-agent/src/cli/args.ts` | `cmd/pig/args.go` | ✅ |
| `packages/coding-agent/src/cli/initial-message.ts` | `cmd/pig/main.go` | ✅ |
| `packages/coding-agent/src/cli/file-processor.ts` | `internal/codingagent/file_processor.go` | ✅ |
| `packages/coding-agent/src/cli/list-models.ts` | `internal/codingagent/settings.go` | ✅ |
| `packages/coding-agent/src/cli/config-selector.ts` | `cmd/pig/config_command.go + tui/config_selector.go` | ✅ |
| `packages/coding-agent/src/cli/credential-print.ts` | `cmd/pig/credential_print.go + internal/codingagent/request_auth_runtime.go (request-auth surface: getProviders/getAuth/listCredentials with OAuth refresh+persist) + ai/auth_resolve.go` | ✅ |
| `packages/coding-agent/src/cli/session-picker.ts` | `internal/codingagent/session_selector.go` | ✅ |
| `packages/coding-agent/src/cli/project-trust.ts` | `cmd/pig/project_trust.go` | ✅ |
| `packages/coding-agent/src/cli/startup-ui.ts` | `internal/codingagent/startup_ui.go (ShowStartupSelector/ShowStartupInput/SelectStartupSession; startStartupTui color-scheme + OSC 11 background query with reply consumption). First-time setup is designed out (owner decision 2026-09-29): upstream shouldRunFirstTimeSetup (startup-ui.ts:122-140, called from main.ts:661) returns false unless package @earendil-works/pi-coding-agent, app pi and .pi config dir, so it never runs for this binary; the four shouldRunFirstTimeSetup cases in test/first-time-setup.test.ts are designed out with it (listed as designedOutCases in the test mapping) and its four analytics cases stay ported` | ✅ |
| `packages/coding-agent/src/core/sdk.ts` | `coding/session.go + coding/resource_loader.go (resourceLoader option; DefaultResourceLoader resolves skills, prompt templates, context files and SYSTEM.md/APPEND_SYSTEM.md from the default and settings-configured locations, ~/.agents/skills, ancestor .agents/skills, configured Packages (installing missing npm and git ones) and additional paths under project trust, with Pi's resource metadata and option and override callbacks; extensions, themes, extension-discovered resources and inline extension factories stay with the Extension Host and the theme controller) + coding/session_manager.go + coding/session_tool_registry.go + coding/session_extension_hooks.go + coding/session_cache_warming.go + coding/runtime.go + coding/services.go + coding/extension/host/subprocess/runtime-node/shims/independent-session.mjs (independent Node Session composition and owned shutdown)` | 🟡 |
| `packages/coding-agent/src/core/agent-session.ts` | `coding/session.go + coding/session_boundaries.go + coding/session_bind.go + coding/session_replaced.go + coding/session_custom_messages.go + coding/session_prompt_resources.go + internal/codingagent/skills.go + coding/session_summarization_auth.go + coding/session_model_cycle.go + coding/session_prompt.go + coding/session_scoped_models.go + coding/session_preflight.go + coding/session_retry_scheduler.go + coding/session_transcript.go + coding/session_extension_hooks.go + coding/session_tools.go + coding/session_tool_registry.go + internal/codingagent/prompts/tool_definitions.go + ai/assistant_retry.go + internal/codingagent/auto_recovery.go (SDK tool-registry, abort/admission and next-turn tool prompt regressions ported; Session boundary, runtime, queue, retry, and compaction test files still partial in the reviewed test mapping)` | 🟡 |
| `packages/coding-agent/src/core/agent-session-runtime.ts` | `partial: coding/runtime.go, coding/runtime_replacement.go, coding/session_bind.go, coding/session.go, coding/session_ops.go, coding/session_extension_replacement.go, coding/session_import.go, internal/codingagent/session_selectors.go, and cmd/pig/rpc_mode.go; native factory/replacement callbacks and retirement implemented; CLI/extension factory ownership and subprocess callback transport remain partial (D30/D61)` | 🟡 |
| `packages/coding-agent/src/core/agent-session-services.ts` | `coding/services.go` | ✅ |
| `packages/coding-agent/src/core/auth-storage.ts` | `ai/auth.go + ai/auth_reload.go + ai/auth_store.go + ai/auth_mutex.go + internal/pilock/lock.go (shared directory locks and reader-owned reloads; CredentialStore, ReadOnlyAuthStorage, in-memory store; Pi shared-file round-trip/concurrent-writer tests)` | ✅ |
| `packages/coding-agent/src/core/auth-guidance.ts` | `internal/codingagent/auth_guidance.go` | ✅ |
| `packages/coding-agent/src/core/bash-executor.ts` | `internal/codingagent/tools/bash_executor.go` | ✅ |
| `packages/coding-agent/src/core/cache-stats.ts` | `internal/codingagent/cache_stats.go + internal/codingagent/session_stats.go + internal/codingagent/model_cache_price.go + coding/session_cache_warming.go` | ✅ |
| `packages/coding-agent/src/core/defaults.ts` | `internal/codingagent/defaults.go` | ✅ |
| `packages/coding-agent/src/core/diagnostics.ts` | `cmd/pig/diagnose.go` | ✅ |
| `packages/coding-agent/src/core/trust-manager.ts` | `internal/codingagent/trust_manager.go + internal/pilock/lock.go (unit-tested + project-trust/01; Pi shared-file concurrent writers)` | ✅ |
| `packages/coding-agent/src/core/project-trust.ts` | `cmd/pig/project_trust.go + internal/codingagent/trust_manager.go` | ✅ |
| `packages/coding-agent/src/core/experimental.ts` | `internal/codingagent/experimental.go` | ✅ |
| `packages/coding-agent/src/core/event-bus.ts` | `coding/extension/eventbus.go` | ✅ |
| `packages/coding-agent/src/core/http-dispatcher.ts` | `ai/http_transport.go + ai/transport_error.go + ai/http_proxy.go + internal/codingagent/settings.go + cmd/pig/main.go` | ✅ |
| `packages/coding-agent/src/core/exec.ts` | `coding/extension/exec.go` | ✅ |
| `packages/coding-agent/src/core/footer-data-provider.ts` | `internal/codingagent/status_line.go + internal/codingagent/git_watcher.go` | ✅ |
| `packages/coding-agent/src/core/keybindings.ts` | `internal/codingagent/keybindings.go` | ✅ |
| `packages/coding-agent/src/core/messages.ts` | `agent/messages.go` | ✅ |
| `packages/coding-agent/src/core/model-config.ts` | `internal/codingagent/model_registry.go` | ✅ |
| `packages/coding-agent/src/core/model-registry.ts` | `internal/codingagent/model_registry.go + internal/codingagent/extension_model_registry.go + coding/extension/host/subprocess/provider_object.go + internal/codingagent/native_model_runtime.go + coding/model_runtime_native.go + coding/model_registry_facade.go + internal/codingagent/registry_request_auth.go` | ✅ |
| `packages/coding-agent/src/core/model-runtime.ts` | `internal/codingagent/model_registry.go + internal/codingagent/provider_registration_validation.go + internal/codingagent/provider_registration_namespace.go + internal/codingagent/extension_model_registry.go + internal/codingagent/native_provider.go + coding/native_provider.go + internal/codingagent/native_model_runtime.go + internal/codingagent/native_credential_operations.go + coding/services.go + coding/model_availability.go + internal/codingagent/model_availability.go + internal/codingagent/model_refresh_background.go + internal/codingagent/model_tasks.go + coding/model_runtime_create.go + coding/model_catalog_backend.go + coding/registered_stream_provider.go + internal/codingagent/model_catalog_data.go + internal/codingagent/registry_request_auth.go + coding/model_runtime_native.go + coding/extension/host/subprocess/native_provider.go + internal/codingagent/interactive.go + internal/codingagent/request_auth_runtime.go (request-auth surface)` | ✅ |
| `packages/coding-agent/src/core/models-store.ts` | `ai/models_store.go + ai/models_store_read.go + internal/pilock/lock.go (FileModelsStore; upstream read-cache cases and Pi directory-lock/concurrent-writer regressions) + internal/codingagent/model_registry.go + internal/codingagent/radius_models.go (<agentDir>/models-store.json)` | ✅ |
| `packages/coding-agent/src/core/provider-composer.ts` | `coding/extension/provider.go + coding/registered_stream_provider.go + coding/model_stream_forwarding.go + internal/codingagent/provider_registration_namespace.go + internal/codingagent/model_registry.go + internal/codingagent/native_provider_composer.go + internal/codingagent/model_data.go + internal/codingagent/registry_request_auth.go + internal/codingagent/native_provider_oauth.go + internal/codingagent/request_auth_runtime.go (auth composition)` | ✅ |
| `packages/coding-agent/src/core/radius.ts` | `internal/codingagent/radius.go (internal/codingagent/radius_test.go)` | ✅ |
| `packages/coding-agent/src/core/remote-catalog-provider.ts` | `internal/codingagent/remote_catalog_provider.go (WithRemoteCatalog: TestRemoteCatalogProviderUpstream) + internal/codingagent/remote_catalog_registry.go (built-in provider wiring: TestRegistryAppliesTheRemoteCatalogOverlayToBuiltInProviders; TestModelCatalogProtocolWithTheCurrentClient); D64 routes the endpoint to pi-in-go.dev. The bundled catalog's generation time is not recorded, so a stored remote catalog always applies` | 🟡 |
| `packages/coding-agent/src/core/runtime-credentials.ts` | `ai/runtime_credentials.go + internal/codingagent/model_registry.go` | ✅ |
| `packages/coding-agent/src/core/usage-totals.ts` | `internal/codingagent/session_stats.go + internal/codingagent/status_line.go + internal/codingagent/slash_commands.go` | ✅ |
| `packages/coding-agent/src/core/model-resolver.ts` | `cmd/pig/model.go + cmd/pig/startup_model.go + cmd/pig/resolve_cli_model.go + coding/extension/scoped_models.go + internal/codingagent/default_models.go + internal/codingagent/model_pattern.go + internal/codingagent/model_scope.go + internal/codingagent/model_registry_runtime_models.go` | ✅ |
| `packages/coding-agent/src/core/output-guard.ts` | `internal/codingagent/output_guard.go` | ✅ |
| `packages/coding-agent/src/core/package-manager.ts` | `coding/packagecontent/packagecontent.go, coding/packagecontent/glob.go, coding/packagecontent/autoload_patterns.go, internal/resolvepath/resolvepath.go, internal/packagemanager/doc.go, internal/packagemanager/manager.go, internal/packagemanager/git.go, internal/packagemanager/npm_metadata.go, internal/packagemanager/process.go, internal/packagemanager/progress.go, internal/packagemanager/capture_unix.go, internal/packagemanager/capture_windows.go, internal/packagemanager/temporary.go, internal/packagemanager/updates.go, internal/packagemanager/resources.go, internal/packagemanager/autoload.go, cmd/pig/package_commands.go, cmd/pig/package_progress.go, cmd/pig/package_git.go, cmd/pig/package_update_operations.go, cmd/pig/package_updates.go, cmd/pig/package_npm_metadata.go, cmd/pig/package_temporary.go, cmd/pig/package_resource_selection.go, cmd/pig/configured_resources.go, internal/codingagent/settings.go, cmd/pig/config_command.go, cmd/pig/resource_source_info.go (skill entry-file metadata)` | ✅ |
| `packages/coding-agent/src/core/prompt-templates.ts` | `internal/codingagent/prompt_templates.go + prompt_diagnostics.go (file/YAML warnings reach interactive startup/reload and RPC; source labels, collision diagnostics, exact YAML error text and remaining loader semantics need correspondence)` | 🟡 |
| `packages/coding-agent/src/core/resolve-config-value.ts` | `internal/configvalue/configvalue.go + internal/configvalue/configured_shell.go + internal/configvalue/shell_unix.go + internal/configvalue/shell_windows.go` | ✅ |
| `packages/coding-agent/src/core/resource-loader.ts` | `coding/resource_loader.go (native DefaultResourceLoader for skills, prompt templates, context files and system prompt text) + coding/packagecontent/packagecontent.go + internal/codingagent/resources.go + internal/codingagent/context_files.go + cmd/pig/configured_resources.go (resolved skills precede additional --skill paths) + cmd/pig/resource_prompts.go + internal/codingagent/prompt_templates.go + internal/codingagent/skills.go + cmd/pig/config_command.go + cmd/pig/reload_resources.go + cmd/pig/resource_source_info.go + internal/codingagent/extension_host_info.go (skill provenance projection) + cmd/pig/headless_resources.go + internal/codingagent/interactive.go + internal/codingagent/reload_resources.go + internal/codingagent/extension_diagnostics.go (theme load diagnostics and first-name-wins deduplication)` | ✅ |
| `packages/coding-agent/src/core/session-cwd.ts` | `internal/codingagent/session_cwd.go + coding/runtime_replacement.go (persisted-only missing-CWD diagnostics and factory preflight)` | ✅ |
| `packages/coding-agent/src/core/session-manager.ts` | `internal/codingagent/session_manager.go + internal/codingagent/session_manager_accessors.go + coding/session_manager.go + internal/codingagent/session.go + internal/codingagent/session_resume.go + internal/codingagent/extension_session_read.go + internal/codingagent/session_restore.go + internal/codingagent/session_branch.go + internal/codingagent/session_branch_runtime.go + internal/codingagent/session_context.go + internal/codingagent/session_listing.go + internal/codingagent/session_file.go (entry identity, collision checks, clone label reconstruction, assistant-gated writes, migration, discovery, cancellable listing, explicit opening and settings-only branch restoration)` | 🟡 |
| `packages/coding-agent/src/core/settings-manager.ts` | `coding/settings.go + internal/codingagent/settings.go + internal/codingagent/settings_compaction.go + internal/codingagent/file_lock.go + internal/pilock/lock.go (file and in-memory settings; shared-file round-trip/concurrent writers)` | ✅ |
| `packages/coding-agent/src/core/skills.ts` | `internal/codingagent/skills.go + skills_loader.go + prompts/coding.go` | 🟡 |
| `packages/coding-agent/src/core/slash-commands.ts` | `internal/codingagent/slash_commands.go` | ✅ |
| `packages/coding-agent/src/core/source-info.ts` | `internal/codingagent/resource_source_info.go + cmd/pig/resource_source_info.go + cmd/pig/rpc_mode.go` | ✅ |
| `packages/coding-agent/src/core/system-prompt.ts` | `internal/codingagent/prompts/coding.go (default/explicit-empty tools and opaque forced prompt state) + internal/codingagent/prompts/custom_sections.go + coding/extension/system_prompt_options.go (collection-complete option normalization)` | ✅ |
| `packages/coding-agent/src/core/telemetry.ts` | `internal/codingagent/settings.go (IsInstallTelemetryEnabled, isTruthyTelemetryEnvFlag, TestSettingsManager_IsInstallTelemetryEnabled) gates the attribution headers (D26) and internal/codingagent/install_telemetry.go (reportInstallTelemetry, recordChangelogVersionAndMaybeReportInstall), wired at the same two call sites as interactive-mode.ts:1265-1307 in internal/codingagent/interactive.go's Run, now sends the install/update ping to PiG's own endpoint (D64) instead of pi.dev; tests in internal/codingagent/install_telemetry_test.go` | ✅ |
| `packages/coding-agent/src/core/timings.ts` | `agent/timings.go` | ✅ |
| `packages/coding-agent/src/core/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/provider-attribution.ts` | `coding/model.go (mergeProviderAttributionHeaders, newProviderAttributionProvider; coding/provider_attribution_0861_test.go, coding/model_test.go TestBuildModelGatesAttributionHeadersOnInstallTelemetrySetting): OpenRouter/NVIDIA/Cloudflare headers gated on install telemetry, OpenCode session pair unconditional, matching upstream; values are pig-branded (D26)` | ✅ |
| `packages/coding-agent/src/core/compaction/compaction.ts` | `internal/codingagent/compaction/compaction.go (internal/codingagent/compaction/compaction_summary_reasoning_test.go; coding/session_summarization_test.go)` | ✅ |
| `packages/coding-agent/src/core/compaction/branch-summarization.ts` | `internal/codingagent/compaction/branch_summarization.go (internal/codingagent/compaction/branch_summarization_test.go; coding/session_branch_summary_upstream_test.go verifies current Agent stream selection, request auth, usage and cancellation through NavigateTree)` | ✅ |
| `packages/coding-agent/src/core/compaction/utils.ts` | `internal/codingagent/compaction/utils.go + agent/harness/compaction/utils.go (shared file operations)` | ✅ |
| `packages/coding-agent/src/core/compaction/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/extensions/types.ts` | `partial: coding/extension/*.go (Layer 0, including synchronized late tool registrations in coding/extension/tool_registry.go); subprocess setEditorComponent implements the Node factory path but native SDK factories remain missing, and addAutocompleteProvider uses retained current-provider factories across all SDKs; tracked in docs/extension-api-parity.md` | 🟡 |
| `packages/coding-agent/src/core/extensions/runner.ts` | `coding/extension/host/inproc/runner.go + coding/extension/provider_runtime.go + coding/extension/host/inproc/cache_warming.go + coding/extension/host/inproc/ui_prompt.go + coding/extension/host/subprocess/runtime-node/runtime.mjs (no-op UI); SDK abort/scoped-model binding, live UI/mode getters, user-bash validation, all-extension snapshots and prompt scopes are covered; extensions-runner.test.ts cases are covered; subprocess dialogs use coding/extension/host/subprocess/ui_prompt.go` | 🟡 |
| `packages/coding-agent/src/core/extensions/loader.ts` | `cmd/pig/extensions.go + coding/extension/provider_runtime.go (shared ordered provider queue and bind actions) + coding/extension/host/subprocess/builder_node.go + coding/extension/host/subprocess/runtime-node/jiti-loader.mjs + coding/extension/host/subprocess/runtime-node/runtime.mjs + coding/extension/host/subprocess/reload_cells.go + coding/extension/host/subprocess/host.go + coding/extension/host/subprocess/tool_registration.go (synchronous late registration updates) + coding/extension/host/subprocess/ui_bridge.go (flag defaults, tool-schema validation and failed Node factory API rejection, rollback and concurrent-provider preservation covered; full cached/inline loading and factory-time shared state still partial; reload and Session replacement re-invoke factories in the retained process: retention.go, runtime-node/generations.mjs)` | 🟡 |
| `packages/coding-agent/src/core/extensions/wrapper.ts` | `coding/extension_bridge.go + coding/session_tool_registry.go` | ✅ |
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
| `packages/coding-agent/src/core/tools/bash.ts` | `internal/codingagent/tools/bash.go + internal/codingagent/tools/bash_operations.go + internal/codingagent/tools/shell_tool.go` | ✅ |
| `packages/coding-agent/src/core/tools/output-accumulator.ts` | `internal/codingagent/tools/output_accumulator.go + truncate.go` | ✅ |
| `packages/coding-agent/src/core/tools/read.ts` | `internal/codingagent/tools/read.go + internal/codingagent/tools/context_cwd.go` | ✅ |
| `packages/coding-agent/src/core/tools/write.ts` | `internal/codingagent/tools/write.go (WriteOperations; mutation_queue_upstream_test.go)` | ✅ |
| `packages/coding-agent/src/core/tools/edit.ts` | `internal/codingagent/tools/edit.go (EditOperations; mutation_queue_upstream_test.go)` | ✅ |
| `packages/coding-agent/src/core/tools/grep.ts` | `internal/codingagent/tools/grep.go` | ✅ |
| `packages/coding-agent/src/core/tools/find.ts` | `internal/codingagent/tools/find.go (FindOperations and root-relativization tests; native Windows validation pending)` | 🟡 |
| `packages/coding-agent/src/core/tools/ls.ts` | `internal/codingagent/tools/ls.go` | ✅ |
| `packages/coding-agent/src/core/tools/edit-diff.ts` | `internal/codingagent/tools/edit_diff.go + unified_patch.go (GenerateUnifiedPatch) + diff_string.go (GenerateDiffString); coding/extension/host/subprocess/runtime-node/shims/pi-coding-agent.mjs (vendored Node diff helpers)` | ✅ |
| `packages/coding-agent/src/core/tools/file-mutation-queue.ts` | `internal/codingagent/tools/mutation_queue.go` | ✅ |
| `packages/coding-agent/src/core/tools/render-utils.ts` | `internal/codingagent/tool_render.go` | ✅ |
| `packages/coding-agent/src/core/tools/truncate.ts` | `internal/codingagent/tools/truncate.go` | ✅ |
| `packages/coding-agent/src/core/tools/tool-definition-wrapper.ts` | `coding/extension_bridge.go + coding/session_tool_registry.go` | ✅ |
| `packages/coding-agent/src/core/tools/path-utils.ts` | `internal/codingagent/tools/path_utils.go` | ✅ |
| `packages/coding-agent/src/core/tools/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/export-html/ansi-to-html.ts` | `internal/codingagent/export/ansi_html.go` | ✅ |
| `packages/coding-agent/src/core/export-html/tool-renderer.ts` | `internal/codingagent/export/ansi_html.go + internal/codingagent/export/tool_renderer.go + internal/codingagent/slash_session_handlers.go` | ✅ |
| `packages/coding-agent/src/core/export-html/index.ts` | `internal/codingagent/export/export.go + internal/codingagent/export/assets/` | ✅ |
| `packages/coding-agent/src/modes/interactive/interactive-mode.ts` | `partial: internal/codingagent/interactive.go (formatResumeCommand preserves TTY/persistence gates and the default-directory decision), internal/codingagent/auth_warnings.go, internal/codingagent/interactive_input.go, internal/codingagent/startup_input.go, internal/codingagent/interactive_turn.go, internal/codingagent/interactive_user_bash.go, internal/codingagent/ext_ui_editor_snapshot.go, internal/codingagent/interactive_commands.go, internal/codingagent/interactive_reload.go, internal/codingagent/interactive_rebind.go, internal/codingagent/model_command.go, internal/codingagent/scoped_models_selection.go, internal/codingagent/interactive_auth.go, internal/codingagent/interactive_login.go, internal/codingagent/interactive_api_key_login.go, internal/codingagent/slash_auth.go, internal/codingagent/interactive_post_login.go, internal/codingagent/interactive_events.go, internal/codingagent/interactive_tui.go, internal/codingagent/startup_header.go, internal/codingagent/ext_ui_context.go, internal/codingagent/interactive_signals_unix.go, internal/codingagent/interactive_signals_windows.go, internal/codingagent/suspend.go, internal/codingagent/session_selectors.go, internal/codingagent/interactive_theme_modal.go, internal/codingagent/interactive_theme_watcher.go, internal/codingagent/interactive_models.go, internal/codingagent/interactive_extensions.go, internal/codingagent/interactive_chat.go, internal/codingagent/interactive_changelog.go, internal/codingagent/interactive_layout.go, internal/codingagent/interactive_editor.go, internal/codingagent/remote_editor.go, internal/codingagent/interactive_transcript.go, internal/codingagent/interactive_compaction.go, internal/codingagent/interactive_thinking.go, internal/codingagent/interactive_helpers.go, internal/codingagent/interactive_hotkeys.go, internal/codingagent/loaded_resources.go, internal/codingagent/extension_diagnostics.go (quiet resource diagnostics), internal/codingagent/project_trust_warning.go (startup transcript warning), internal/codingagent/resource_diagnostics.go, internal/codingagent/autocomplete_wrappers.go, internal/codingagent/extension_ui_phase.go, coding/extension/autocomplete.go, coding/extension/host/subprocess/runtime-node/widget-component.mjs; Esc does not cancel an active branch summary` | 🟡 |
| `packages/coding-agent/src/modes/interactive/external-editor.ts` | `internal/codingagent/external_editor.go + internal/codingagent/interactive_editor.go + internal/codingagent/ext_ui_context.go + internal/codingagent/session_selectors.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/trust-selector.ts` | `internal/codingagent/trust_selector.go; internal/codingagent/slash_session_handlers.go (trustHandler)` | 🟡 |
| `packages/coding-agent/src/modes/interactive/components/first-time-setup.ts` | `(designed out: upstream gates this component to the official Pi package identity; Stock PiG does not satisfy that identity)` | n/a |
| `packages/coding-agent/src/modes/interactive/theme/theme.ts` | `tui/theme.go + tui/theme_loader.go + tui/theme_watcher.go (indexed-color load/var/ANSI/CSS/export coverage in TestLoadThemeFileIndexedColors; complete optional-color fallbacks and authored key order in scrollbar_theme_test.go) + exact pinned dark/light JSON; internal/codingagent/interactive_tui.go (foreground scrollbarThumb style); internal/codingagent/extension_diagnostics.go (getCustomThemeInfos availability)` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts` | `internal/codingagent/presentation_theme.go + interactive_theme.go + interactive_theme_modal.go + startup_ui.go (terminal color query, live scheme notifications, selection, preview, persistence and lifecycle); theme_controller_native_upstream_test.go; interactive_theme_autoswitch_test.go; interactive-rendering/36–37` | ✅ |
| `packages/coding-agent/src/modes/interactive/model-search.ts` | `tui/model_search.go (ModelSearchItem, GetModelSearchText, GetModelSelectorSearchText); GetModelSelectorSearchText feeds tui/model_select.go applyFilter over ModelSelectorItem.Name (raw model name), GetModelSearchText feeds tui/scoped_models_list.go refresh and internal/codingagent/interactive_commands.go modelArgCompletions. tui/model_search_test.go proxy-provider fixture (openrouter/vercel-ai-gateway openai/gpt-5 ids) asserts the upstream 0.99.1 fuzzyFilter order for both functions and the selector` | ✅ |
| `packages/coding-agent/src/modes/print-mode.ts` | `cmd/pig/print_mode.go (text) and cmd/pig/json_mode.go (json): upstream runs both modes from one function, pig splits them by mode. json streams the session event stream as JSONL while the turn runs; text still prints only the final assistant message. Behavioral coverage: print/01-print-mode-arithmetic, json/01-json-mode-streams-events` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts` | `cmd/pig/rpc_mode.go + cmd/pig/rpc_dispatch.go + cmd/pig/rpc_admission.go + cmd/pig/rpc_json_diagnostic.go + internal/text/quote_utf16.go + cmd/pig/rpc_ui.go` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-types.ts` | `cmd/pig/rpc_types.go` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-client.ts` | `coding/rpcclient/rpc_client.go + commands.go + types.go + process_unix.go/process_windows.go (RpcClient spawns the pig executable in --mode rpc where upstream spawns node cliPath; typed wire structs replace the TypeScript imports, with raw JSON kept for entries, messages, and events). coding/rpcclient/rpc_client_test.go ports rpc-client-clear-queue/clone/process-exit tests against scripted child processes; rpc_mode_test.go ports rpc.test.ts against a pig binary built in TestMain, using test-faux in place of the live Anthropic model` | ✅ |
| `packages/coding-agent/src/modes/rpc/jsonl.ts` | `coding/rpcclient/jsonl.go; cmd/pig/rpc_types.go; internal/text/quote_utf16.go` | ✅ |
| `packages/coding-agent/src/modes/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/utils/changelog.ts` | `embed.go + internal/codingagent/changelog.go` | ✅ |
| `packages/coding-agent/src/utils/ansi.ts` | `internal/codingagent/export/ansi_html.go` | ✅ |
| `packages/coding-agent/src/utils/child-process.ts` | `internal/crossspawn (spawnProcess/spawnProcessSync Windows shebang routing and cross-spawn escaping); internal/codingagent/tools/bash_operations.go (waitForChildProcess post-exit stdio grace, EXIT_STDIO_GRACE_MS)` | ✅ |
| `packages/coding-agent/src/utils/clipboard.ts` | `internal/codingagent/clipboard.go + internal/codingagent/clipboard_copy.go + internal/codingagent/clipboard_text.go (readClipboardText command fallbacks, then the getNativeClipboard getText reader on every platform, and interactive Ctrl+V/right-click paths; internal/codingagent/clipboard_read_text_test.go)` | ✅ |
| `packages/coding-agent/src/utils/clipboard-image.ts` | `internal/codingagent/clipboard.go + internal/codingagent/clipboard_paste.go (backend read and paste caller; unsupported formats are normalized to PNG; native clipboard parity remains incomplete)` | 🟡 |
| `packages/coding-agent/src/utils/exif-orientation.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/frontmatter.ts` | `internal/codingagent/frontmatter/frontmatter.go + internal/codingagent/frontmatter/diagnostics.go` | ✅ |
| `packages/coding-agent/src/utils/fs-watch.ts` | `internal/codingagent/git_watcher.go` | ✅ |
| `packages/coding-agent/src/utils/git.ts` | `coding/source/ref.go + cmd/pig/package_commands.go (parseGitURL cases in coding/source/ref_test.go)` | ✅ |
| `packages/coding-agent/src/utils/html.ts` | `internal/codingagent/export/ansi_html.go` | ✅ |
| `packages/coding-agent/src/utils/image-convert.ts` | `internal/codingagent/image_convert.go + internal/imageprocessing/image_convert.go (ConvertImageBytesToPng = decodeAutoOriented + encodePNG in place of Photon, also behind images.go convertToolResultImageOnly as upstream normalizeImage; ConvertToPng with Node Buffer base64 decoding; maybeConvertImagesForKitty drives tui/tool_execution.go PendingKittyImageConversions/ApplyConvertedImage and the Kitty non-PNG render skip from the tool-end and transcript-replay paths). Unit-tested by internal/codingagent/image_convert_test.go (upstream image-processing.test.ts convertToPng cases, every decodable format, failure, EXIF locators) and tui/tool_execution_kitty_convert_test.go (upstream tool-execution-component.test.ts late-conversion race) under a faked Kitty capability; tmux cannot report Kitty, so clipboard-images/02 stays deferred` | ✅ |
| `packages/coding-agent/src/utils/image-resize.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/mime.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/paths.ts` | `internal/resolvepath/resolvepath.go; internal/codingagent/paths.go, canonical_path_other.go, canonical_path_win32.go, canonical_path_windows.go (IsLocalPath is consumed by cmd/pig/main.go resolveCLIResourceFlags; ResolvePath is shared by skills_loader.go; CanonicalizePath preserves Node volume-mount identity and resolves rooted link targets on the link's device through the Node realpathSync walk)` | ✅ |
| `packages/coding-agent/src/utils/pi-user-agent.ts` | `internal/codingagent/bug_report.go (codingAgentUserAgent supplies the owner-approved Q4/D65 pig/<coding.Version> identity to exported report metadata; TestBugReportEnvironmentUsesPiGUserAgent). Other upstream call sites are accounted by their owning rows: version-check.ts and package-manager-cli.ts self-update are replaced by D39, remote-catalog-provider.ts sends the same User-Agent through internal/codingagent/remote_catalog_provider.go (see its row), and the interactive report-install call is ported under core/telemetry.ts (internal/codingagent/install_telemetry.go), which sends this same User-Agent (ai.PiUserAgent()) with the ping.` | ✅ |
| `packages/coding-agent/src/utils/photon.ts` | `(not ported: WASM)` | n/a |
| `packages/coding-agent/src/utils/shell.ts` | `internal/codingagent/tools/shell_config.go + shell_config_unix.go + shell_config_windows.go + bash_session_env.go (getShellEnv) + sanitize.go (sanitizeBinaryOutput) + bash_group_*.go + taskkill.go (killProcessTree; native Windows validation pending)` | 🟡 |
| `packages/coding-agent/src/utils/syntax-highlight.ts` | `tui/highlight.go` | ✅ |
| `packages/coding-agent/src/utils/sleep.ts` | `ai/assistant_retry.go (sleepContext is the awaited, context-cancellable backoff used by the Session-owned retry loop; TestRetryAssistantCallAbortsBackoffSleepViaContext covers cancellation before the timer wait)` | ✅ |
| `packages/coding-agent/src/utils/tools-manager.ts` | `internal/codingagent/tools/tools.go + tools_manager.go (release redirects, fetchWithRetry, extraction per D14, same-manager additive download coalescing per D81, silent grep/find, bounded distinct ToolStatus causes); internal/codingagent/interactive_tools_status.go (live owner-thread status, ordered initial reports, joined cancellation); tools_manager_status_test.go + interactive_tools_status_test.go + tools/12-managed-tool-status; remaining: Linux musl asset selection and bypassing latest lookup for pinned darwin/x64 fd` | 🟡 |
| `packages/coding-agent/src/utils/version-check.ts` | `(not ported: Pig uses configured signed release manifests under D39 instead of the pi.dev version API)` | n/a |
| `packages/coding-agent/src/utils/deprecation.ts` | `(designed out: the pinned upstream helper has no imports or callers; Go warning sites retain their caller-specific output policy)` | n/a |
| `packages/coding-agent/src/utils/image-resize-core.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/image-resize-worker.ts` | `(Node worker thread: Go uses goroutines)` | n/a |
| `packages/coding-agent/src/utils/json.ts` | `(stdlib encoding/json)` | n/a |
| `packages/coding-agent/src/utils/open-browser.ts` | `internal/codingagent/interactive_auth.go (openBrowser passes Windows URLs directly to rundll32; native TestOpenBrowserPassesURLToWindowsHandlerWithoutShell); remaining: other-platform launcher/failure behavior and complete caller-path parity` | 🟡 |
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
| `packages/coding-agent/src/modes/interactive/components/custom-editor.ts` | `internal/codingagent/interactive.go, internal/codingagent/interactive_input.go, and internal/codingagent/keys.go realize CustomEditor app-keybinding precedence (extension shortcuts -> paste-image -> interrupt with autocomplete/empty guards -> exit-on-empty -> app.session actions -> editor fallthrough) as InteractiveMode dispatch rather than an Editor subclass (structural, observably equivalent). Ctrl+D exits only when the editor has zero-length text; nonempty text, including whitespace, falls through to tui.editor.deleteCharForward. Conflicting paste-image/interrupt/exit bindings use CustomEditor order rather than registry declaration order. Guarded by mutation-proven TestCustomEditorHighPriorityBindingOrder, TestCustomEditorDispatchPrecedence, and test/parity/scenarios/custom-editor-precedence.toml.` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/custom-entry.ts` | `internal/codingagent/interactive.go addCustomEntryToChat (rebuild + live-append, Spacer(1) + expand-toggle) over the entry-renderer wire in coding/extension/host/subprocess/{protocol.go,host.go,render_proxy.go} + RegisterEntryRenderer in extensions/sdk{,-rs,-py} + runtime-node. Cross-SDK verified by extension-conformance (inproc-go reference vs subprocess go/rust/python) + parity scenario extensions-runtime/18-subprocess-entry-renderer (pi 0.84.0 vs pig byte-identical, mutation-proven)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/custom-message.ts` | `tui/custom_message.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/diff.ts` | `tui/diff_component.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/dynamic-border.ts` | `tui/dynamic_border.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-editor.ts` | `tui/extension_editor.go + internal/codingagent/session_selectors.go + internal/codingagent/ext_ui_context.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-input.ts` | `tui/extension_input.go + internal/codingagent/ext_ui_context.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-selector.ts` | `tui/extension_selector.go + internal/codingagent/ext_ui_context.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/footer.ts` | `internal/codingagent/status_line.go + internal/codingagent/interactive_auth.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/keybinding-hints.ts` | `tui/keybinding_hints.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/login-dialog.ts` | `tui/login_dialog.go + internal/codingagent/interactive_api_key_login.go + internal/codingagent/interactive_auth.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/model-selector.ts` | `tui/model_select.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/oauth-selector.ts` | `tui/oauth_selector.go + internal/codingagent/interactive_login.go + internal/codingagent/slash_auth.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/scoped-models-selector.ts` | `tui/scoped_models_list.go + internal/codingagent/interactive.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/session-selector.ts` | `internal/codingagent/session_selector.go + session_selector_load.go + session_selector_status.go + session_selector_delete.go (+ _unix/_windows) + session_selectors.go + startup_ui.go + interactive.go + interactive_theme_modal.go (owned scope loads, progress, cancellation and storage await with UI-task pumping)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/session-selector-search.ts` | `internal/codingagent/session_selector.go + tui/fuzzy.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/settings-selector.ts` | `tui/settings_list.go + internal/codingagent/slash_session_handlers.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/show-images-selector.ts` | `tui/show_images_selector.go (FilterableList layout differs from upstream SelectList and onSelect/onCancel callbacks are not dispatched; red repro: test/parity/testdata/next-evidence-app-b/team_app_show_images_test.go.repro; no ordinary interactive entry point)` | 🟡 |
| `packages/coding-agent/src/modes/interactive/components/skill-invocation-message.ts` | `tui/skill_invocation.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/theme-selector.ts` | `tui/theme_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/thinking-selector.ts` | `internal/codingagent/thinking_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/tool-execution.ts` | `tui/tool_execution.go (shell elapsed Text wrapping covered for bash/powershell output, no-output, and collapsed paths by TestToolExecutionComponent_ShellElapsedFooterRows), tui/tool_execution_definition.go (registered-definition renderers, shells and fallbacks covered by TestDefinitionCard* and extensions-runtime/24-25 tool-renderer scenarios)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/tree-selector.ts` | `tui/tree_select.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/user-message.ts` | `tui/user_message_block.go + internal/codingagent/interactive_chat.go (user transform construction)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/user-message-selector.ts` | `tui/user_message_selector.go + internal/codingagent/session_selectors.go (D66 bounds list rows at unusually narrow widths instead of emitting fatal over-wide rows)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/visual-truncate.ts` | `tui/visual_truncate.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/armin.ts` | `internal/codingagent/armin.go + internal/codingagent/interactive_easter_eggs.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/daxnuts.ts` | `(easter egg: skip)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/earendil-announcement.ts` | `internal/codingagent/earendil_announcement.go + internal/codingagent/interactive_easter_eggs.go` | ✅ |
| `packages/coding-agent/src/cli/experimental/cli.ts` | `internal/experimental/commands.go (command composition; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/cli/experimental/command-options.ts` | `internal/experimental/commands_types.go, internal/experimental/command_parse.go (Unix transport options; Radius/auth branches :5-37,47-64,97-102 designed out by owner 2026-09-28, D64; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/cli/experimental/command.ts` | `internal/experimental/commands_types.go, internal/experimental/command_parse.go (parser/action composition; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/cli/experimental/commands/client.ts` | `internal/experimental/commands_types.go, internal/experimental/commands.go (Unix client options/action; relay credentials designed out by owner 2026-09-28, D64; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/cli/experimental/commands/server.ts` | `internal/experimental/commands_types.go, internal/experimental/commands.go (Unix server options/action; relay credentials designed out by owner 2026-09-28, D64; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/client/index.ts` | `(not ported: remote-session capability via A2A, spec 482; pi-client cloud SDK not portable)` | n/a |
| `packages/coding-agent/src/core/pi-manifest.ts` | `coding/packagecontent/pi_manifest.go ReadPiManifest (BOM strip, object-only package and "pi", per-field string arrays), used by coding/extension/host/subprocess/builder_node.go piPackageEntrypoints; coding/packagecontent/packagecontent.go readPackageManifest applies the same parse for package discovery (pi_manifest_test.go, builder_node_entry_test.go)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/markdown-transform.ts` | `partial: internal/codingagent/markdown_transform.go, internal/codingagent/interactive_chat.go, tui/markdown_async.go; ordinary callback order and stale-generation publication covered, exceptional abandoned-body completion remains open` | 🟡 |
| `packages/coding-agent/src/modes/interactive/components/mermaid.ts` | `internal/codingagent/mermaid_transform.go + internal/codingagent/mermaid_fence_parser.go + internal/codingagent/mermaid_definition_parser.go + internal/codingagent/mermaid_html_parser.go + internal/codingagent/mermaid_heading_parser.go (natural-layout raw fallback and display-only partial warnings: mermaid_fallback_upstream_test.go; exact transform corpus: mermaid_transform_test.go)` | ✅ |
| `packages/coding-agent/src/modes/json-event.ts` | `cmd/pig/rpc_events.go + cmd/pig/rpc_object.go + ai/assistant_observation.go + agent/messages.go (ordered JSON/RPC conversion; field-only live projections exclude partials before serialization and read toolcall_start identity)` | ✅ |
| `packages/coding-agent/src/utils/abort.ts` | `(AbortSignal/Promise racing: raceWithAbortSignal/operationSignal; pig uses context.Context cancellation + select (D3): runtime mechanics, designed out)` | n/a |
| `packages/coding-agent/src/utils/management-http.ts` | `internal/managementhttp/managementhttp.go FetchWithRetry (management-http.test.ts cases in managementhttp_test.go), used by internal/codingagent/tools/tools_manager.go getLatestVersion and downloadFile; its other upstream callers are remote-catalog-provider.ts, which internal/codingagent/remote_catalog_provider.go ports and routes through the same FetchWithRetry (see its row), and version-check.ts, which is not ported (see its row)` | ✅ |
| `packages/coding-agent/src/utils/tool-result-images.ts` | `internal/imageprocessing/images.go + coding/session_images.go: ordered text/image content, adjacent hints, unchanged-slice identity, model profile after hooks; tool_result_images_upstream_test.go ports all seven tests` | ✅ |
| `packages/coding-agent/src/bun/runtime-setup.ts` | `(Bun-only runtime setup)` | n/a |
| `packages/coding-agent/src/bun/sandbox-env-setup.ts` | `(Bun-only sandbox helper)` | n/a |
| `packages/coding-agent/src/cli/auth-check.ts` | `cmd/pig/auth_check.go + cmd/pig/auth_run.go (main.ts runAuthCommand)` | ✅ |
| `packages/coding-agent/src/cli/auth-command.ts` | `cmd/pig/auth_command.go + cmd/pig/resolve_cli_model.go (resolveCliModel for --model)` | ✅ |
| `packages/coding-agent/src/cli/setup.ts` | `cmd/pig/setup_cli.go (PI_CODING_AGENT/AI_AGENT process markers; process.title is the executable name, Node warning suppression has no Go analogue, configureHttpDispatcher is mapped under core/http-dispatcher.ts)` | ✅ |
| `packages/coding-agent/src/core/bug-report-upload.ts` | `(designed out: D62, PiG never uploads bug reports)` | n/a |
| `packages/coding-agent/src/core/bug-report.ts` | `internal/codingagent/bug_report.go (export subset: metadata, redaction, diagnostics, archive; D62) + internal/codingagent/compaction/bug_report_summary.go (coding/bug_report.go; internal/codingagent/compaction/bug_report_summary_test.go)` | ✅ |
| `packages/coding-agent/src/core/cache-warmer.ts` | `internal/codingagent/cache_warmer.go + coding/session_cache_warming.go + coding/extension/cache_warming.go (retention, lifecycle, and awaited extension decisions)` | ✅ |
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
| `packages/coding-agent/src/experimental/cli.ts` | `cmd/pig/main_experimental.go (unshipped pig_experimental build-tag entry: internal role dispatch, then server/client when PI_EXPERIMENTAL=1, else the stable CLI; the default build keeps cmd/pig/main_default.go; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/client-runtime.ts` | `internal/experimental/client_runtime.go + client_runtime_types.go + client_runtime_sources.go + client_runtime_activate.go (Unix discovery/activation/client/source/disposal composition; Radius selection/reconnection :57-68,125-133,156-169 designed out by owner 2026-09-28, D64; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/client-tui-chat.ts` | `internal/experimental/client_tui_chat.go (snapshot-driven transcript view with shared tool renderer cards; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/client-tui.ts` | `internal/experimental/client_tui.go + client_tui_sources.go (service-only fullscreen client TUI over replicated Transcript/Models/SessionDirectory and a Chord FacetHost, shared theme controller and renderer; theme resources arrive from the CLI entry; Radius-only reconnect status text is unreachable because Radius is designed out, D64; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/client.ts` | `internal/experimental/client_run.go (real list/attach/create/prompt flow, independent terminal-event barrier and ordered callback draining; runtime policy dependencies and qualification remain pending)` | 🟡 |
| `packages/coding-agent/src/experimental/commands.ts` | `cmd/pig/main_experimental.go (Unix server/client command actions; Radius relay status/output :14-29 designed out by owner 2026-09-28, D64; runtime qualification pending)` | 🟡 |
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
| `packages/coding-agent/src/experimental/plugins/bundled.ts` | `internal/experimental/facet_bundle.go + node_facets.go + node_facets_host.go + node_facets_protocol.go + coding/extension/host/subprocess/facet_bridge.go (server-selected presentation/session loaders and callable isolated environments; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/plugins/package.ts` | `internal/experimental/plugin_package_profile.go + plugin_package_build.go + plugin_package_metadata.go (server/Session profiles and real server-owned esbuild cache; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/process.ts` | `(pending: spawns detached coordinator/server/session-worker roles via __PI_INTERNAL_SPAWN for the server stack; its source-mode branch (--import source-resolver.ts) is Node-runtime-only; no PiG caller until server.ts lands)` | ⬜ |
| `packages/coding-agent/src/experimental/radius-auth.ts` | `(pending: experimental server stack; owner approved Radius Q1)` | ⬜ |
| `packages/coding-agent/src/experimental/radius-relay.ts` | `(pending: experimental server stack; owner approved Radius Q1)` | ⬜ |
| `packages/coding-agent/src/experimental/server.ts` | `internal/experimental/server_runtime.go, server_lifetime.go, server_profile.go, session_directory.go, server_directory.go, server_activation.go, server_options.go, server_process.go, internal/pilock/lock.go (partial: native Unix backend/catalog, plugins, profiles, activation, process lifetime and D2 namespace; relay construction/lifecycle :637-677 designed out by owner 2026-09-28, D64; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/services/agent-controller-provider.ts` | `(pending: AgentController facade over AgentLane; 0.87.1 durable harness runtime (AgentLane, LaneTranscriptSnapshot, LaneWatchEvent, reduceLaneSnapshot) is ⬜ under agent/src/harness/runtime, harness slice; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/agent-controller.ts` | `internal/experimental/services/agent_controller.go + agent_controller_adapters.go + agent_controller_invocation.go (contract, guarded views, remote clients, and typed invocation admission, including in-host loopback admission; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/services/connection.ts` | `internal/experimental/services/connection.go + connection_types.go + ../client_runtime_sources.go (generation-fenced sources and real client/Chord binding adapters; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/services/models-provider.ts` | `(pending: Models service facet over ModelRuntime and SettingsManager; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/models.ts` | `(pending: Chord service contract pi.models with ReplicatedState; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/plugins.ts` | `internal/experimental/services/plugins.go + plugins_adapters.go (contracts, guarded views, and remote clients; compile-only qualification)` | 🟡 |
| `packages/coding-agent/src/experimental/services/presentation-ui.ts` | `internal/experimental/services/presentation_ui.go (local Chord contract and guarded view; runtime review pending)` | 🟡 |
| `packages/coding-agent/src/experimental/services/server.ts` | `(pending: SessionDirectory/SessionManagement/PresentationPlugins server facets; Chord service/facet/ReplicatedState runtime is outside PORT_MAP package scope; pi-server/pi-client/pi-protocol are outside PORT_MAP package scope)` | ⬜ |
| `packages/coding-agent/src/experimental/services/sessions.ts` | `internal/experimental/services/sessions.go + sessions_adapters.go (contracts and typed guarded/remote views; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/services/slash-commands-provider.ts` | `internal/experimental/services/slash_commands_provider.go + slash_commands_builtin.go (registry, local runtime facet and built-in /model, /thinking, /compact, /reload facets; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/services/slash-commands.ts` | `internal/experimental/services/slash_commands.go (local service contract and guarded service view; compile-only qualification)` | 🟡 |
| `packages/coding-agent/src/experimental/services/transcript-provider.ts` | `internal/experimental/services/transcript_provider.go (partial: provider and facet ported; complete worker composition and runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/services/transcript.ts` | `internal/experimental/services/transcript.go (partial: replicated state contract and local/remote adapters; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/services/worker.ts` | `internal/experimental/services/worker.go (built-in/plugin facet composition, scoped endpoints, reload cutover and disposal; worker process integration and runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/session-worker-manager.ts` | `internal/experimental/session_worker_manager.go, session_worker_events.go (partial: coordinator-backed worker bookkeeping, demand, operations, subscriptions and shutdown; full server/worker composition and runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/session-worker.ts` | `internal/experimental/worker_lifecycle.go, session_worker_process.go, session_worker_control.go, session_worker_factory.go (partial: real coding-Harness factory, native model carrier, public Lane delegation, process control, demand and cleanup; the control socket closes at process exit as in Pi; runtime qualification pending)` | 🟡 |
| `packages/coding-agent/src/experimental/source-resolver.ts` | `(Node-runtime-only: a node:module registerHooks resolve hook that maps tsconfig @earendil-works/* path aliases to .ts sources so internal processes run from a source checkout; Go links packages at build time)` | n/a |
| `packages/coding-agent/src/modes/interactive/bug-report.ts` | `internal/codingagent/slash_bug.go, internal/codingagent/interactive_bug.go (consent flow, export-only; D62)` | ✅ |
| `packages/coding-agent/src/modes/interactive/chat-viewport.ts` | `internal/codingagent/chat_viewport.go (CreateChatViewport; mounted by buildChatViewport in internal/codingagent/interactive_tui.go)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/settings-submenu.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/coding-agent/src/modes/interactive/model-catalog-refresh.ts` | `(new upstream by 0.87.1; not mapped)` | ⬜ |
| `packages/coding-agent/src/modes/interactive/session-share.ts` | `internal/codingagent/session_share.go (createShareTrailingEntries, ExportSessionForShare, explicit /share upload with privacy notice); hosted counterpart: private PiG-platform worker/src/share.ts (bounded R2 artifact route, 30-day expiry, unlisted viewer)` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/theme-json.ts` | `tui/theme_json.go (ValidateThemeJSON, wired in tui/theme_loader.go LoadThemeFile; upstream-generated goldens in tui/testdata/theme_json_cases.json; real-loader union coverage in TestLoadThemeFileIndexedColors)` | ✅ |
| `packages/coding-agent/src/modes/interactive/tui-renderer.ts` | `internal/codingagent/interactive_tui.go (createInteractiveTui, fullscreenTuiOptions), internal/codingagent/clipboard_text.go (right-click paste); createInteractiveTuiReference designed out: the driver reads m.tuiInst at call time under rendererMu, so no proxy is needed` | ✅ |
| `packages/coding-agent/src/utils/clipboard-command.ts` | `internal/codingagent/clipboard_copy.go (shared bounded command runner for copy and clipboard.go image reads)` | ✅ |
| `packages/coding-agent/src/utils/highlight-js.d.ts` | `(TypeScript ambient declarations; no runtime behavior)` | n/a |
| `packages/coding-agent/src/utils/text.ts` | `internal/text/text.go (BOM helpers; auth, settings, models, trust, frontmatter, context, file attachments and edit callers)` | ✅ |
| `packages/coding-agent/src/utils/wsl.ts` | `internal/codingagent/wsl.go` | ✅ |
| `packages/coding-agent/src/utils/zip.ts` | `(Go archive/zip, used by internal/codingagent/bug_report.go)` | n/a |
| `packages/coding-agent/src/bun/quickjs-wasm.d.ts` | `(Bun-only type declaration for the QuickJS WASM import; not compiled by Go)` | n/a |
| `packages/coding-agent/src/core/mcp-servers.ts` | `coding/extension/mcp_servers.go (registry and change events; mcp_servers_test.go)` | 🟡 |
| `packages/coding-agent/src/core/nested-tool-calls.ts` | `coding/nested_tool_calls.go` | 🟡 |
| `packages/coding-agent/src/core/virtual-models.ts` | `coding/extension/virtual_model.go (routing types) + coding/virtual_models.go (IsVirtualModel, FindLatestResponse, GetBranchSelection, GetVirtualModelState, CreateVirtualModel; ModelRuntime.RegisterVirtualModel, UnregisterVirtualModel, ResolveModel, GetPhysicalModel and the withVirtualModels catalog overlay; Session.routeRequest and the unrouted-stream failure); tests: coding/virtual_models_upstream_test.go (virtual-models.test.ts) and coding/virtual_models_suite_upstream_test.go (suite/virtual-models.test.ts). Status stays 🟡 until a reviewer closes behavior against the 0.99.2 source` | 🟡 |
| `packages/coding-agent/src/extensions/codemode/execute.lazy.ts` | `(designed out: the sandbox is created on the first script by construction, coding/extension/builtin/codemode/execute.go)` | n/a |
| `packages/coding-agent/src/extensions/codemode/execute.ts` | `coding/extension/builtin/codemode/execute.go + run.go (nested calls, discovery globals, store/load, output truncation, header); models.* deferred until the model runtime exposes classifiers` | 🟡 |
| `packages/coding-agent/src/extensions/codemode/index.ts` | `coding/extension/builtin/codemode/tool.go (Extension, settings-driven mode and budget) + cmd/pig/builtin_extensions.go` | 🟡 |
| `packages/coding-agent/src/extensions/codemode/renderer.ts` | `internal/codingagent/tool_codemode_renderer.go (CodemodeRenderers, carried by the codemode tool definition)` | 🟡 |
| `packages/coding-agent/src/extensions/codemode/tool.ts` | `coding/extension/builtin/codemode/{tool,description,description_text}.go (description catalog, loadout, definition)` | 🟡 |
| `packages/coding-agent/src/extensions/codemode/worker.ts` | `(designed out: the sandbox runs in-process on wazero, codemode/sandbox.go; there is no worker entry)` | n/a |
| `packages/coding-agent/src/extensions/mcp/cli.lazy.ts` | `(lazy dynamic-import wrapper; Go links the command statically)` | n/a |
| `packages/coding-agent/src/extensions/mcp/cli.ts` | `coding/mcpext/cli.go` | 🟡 |
| `packages/coding-agent/src/extensions/mcp/config.ts` | `coding/mcpext/config.go (config_test.go)` | 🟡 |
| `packages/coding-agent/src/extensions/mcp/index.ts` | `coding/mcpext/extension.go + coding/mcpext/register.go (extension_test.go)` | 🟡 |
| `packages/coding-agent/src/extensions/mcp/log.ts` | `coding/mcpext/log.go` | 🟡 |
| `packages/coding-agent/src/extensions/mcp/oauth.ts` | `coding/mcpext/oauth.go + coding/mcpext/storage.go (oauth_refresh_test.go)` | 🟡 |
| `packages/coding-agent/src/extensions/mcp/resources.ts` | `coding/mcpext/resources.go` | 🟡 |
| `packages/coding-agent/src/extensions/mcp/runtime.lazy.ts` | `(lazy dynamic-import wrapper; Go links the runtime statically)` | n/a |
| `packages/coding-agent/src/extensions/mcp/runtime.ts` | `coding/mcpext/runtime.go (connection_test.go)` | 🟡 |
| `packages/coding-agent/src/extensions/mcp/tools.ts` | `coding/mcpext/tools.go (tools_test.go)` | 🟡 |
| `packages/coding-agent/src/extensions/mcp/ui.ts` | `coding/mcpext/manager.go` | 🟡 |
| `packages/coding-agent/src/extensions/tool-search/index.ts` | `coding/extension/builtin/toolsearch/tool.go (Extension)` | 🟡 |
| `packages/coding-agent/src/extensions/tool-search/tool.ts` | `coding/extension/builtin/toolsearch/{toolsearch,tool}.go (Bm25Ranker, tokenize, documents, description, tool_search)` | 🟡 |
| `packages/coding-agent/src/modes/interactive/components/pi-logo.ts` | `internal/codingagent/startup_header.go (piLogoLines, header layout); pi_logo_upstream_test.go; startup/00-startup-banner` | 🟡 |
| `packages/coding-agent/src/modes/interactive/components/themed-text.ts` | `tui/themed_text.go (ThemedText, NewDynamicBorderToken); tui/themed_text_upstream_test.go; internal/codingagent/themed_notices_upstream_test.go` | 🟡 |
| `packages/coding-agent/src/modes/interactive/theme/system-theme.ts` | `tui/system_theme.go; tui/theme_terminal_colors.go; tui/system_theme_upstream_test.go; tui/system_theme_oracle_test.go` | 🟡 |

## `packages/tui/src/`

| upstream | pig | status |
|---|---|---|
| `packages/tui/src/tui.ts` | `tui/tui.go, tui/terminal_color_query.go (FIFO background-query lifetime), tui/overlay_command.go (input-boundary focus restoration), tui/cell_size.go (CSI 16t query and response consumption), tui/mouse.go (mouse event API, Container/Box dispatch, overlay bounds and hit testing), internal/codingagent/interactive_input.go + interactive_theme.go + startup_ui.go (driver-owned terminal input routing and theme detection)` | 🟡 |
| `packages/tui/src/editor-component.ts` | `tui/editor.go` | ✅ |
| `packages/tui/src/autocomplete.ts` | `tui/autocomplete.go + tui/file_autocomplete.go` | ✅ |
| `packages/tui/src/fuzzy.ts` | `tui/fuzzy.go` | ✅ |
| `packages/tui/src/keybindings.ts` | `tui/keybindings.go` | ✅ |
| `packages/tui/src/keys.ts` | `tui/key_match.go + tui/key_parse.go + tui/keys_decode.go + tui/keybindings.go + internal/codingagent/keys.go + internal/codingagent/stdin_buffer.go + internal/codingagent/kitty_csi.go` | ✅ |
| `packages/tui/src/kill-ring.ts` | `tui/kill_ring.go` | ✅ |
| `packages/tui/src/stdin-buffer.ts` | `tui/stdin_buffer.go (shared UTF-16 parser), internal/codingagent/stdin_buffer.go (owner timer and modal routing); native unit tests pass, full cross-boundary qualification remains partial` | 🟡 |
| `packages/tui/src/terminal.ts` | `tui/terminal.go + tui/terminal_input.go + tui/terminal_reader.go + tui/input_timer.go + tui/terminal_refresh.go + tui/terminal_unix.go + tui/terminal_windows.go + tui/terminal_native_shift_enter.go; internal/codingagent/interactive_input.go + interactive_terminal_reader.go + startup_ui.go; cmd/pig/config_command.go` | 🟡 |
| `packages/tui/src/terminal-colors.ts` | `tui/terminal_colors.go; tui/terminal_color_query.go (QueryTerminalColors, ConsumeTerminalColorResponse); tui/theme.go` | ✅ |
| `packages/tui/src/terminal-image.ts` | `tui/terminal_image.go` | ✅ |
| `packages/tui/src/undo-stack.ts` | `tui/undo_stack.go` | ✅ |
| `packages/tui/src/native-modifiers.ts` | `tui/native_modifiers.go (isNativeModifierPressed) over internal/nativeplatform/modifiers_darwin.go (CGO-free lazy dlopen of CoreGraphics CGEventSourceFlagsState via libSystem trampolines), modifiers_windows.go (user32 GetAsyncKeyState), and modifiers_other.go` | ✅ |
| `packages/tui/src/word-navigation.ts` | `tui/word_navigation.go + internal/wordsegmenter + tui/editor_segments.go (shared UTF-16 word helpers; ICU 78.3 CJK and Southeast Asian dictionary engines and rule tailoring; Node differential corpus and byte-exact Editor scenario)` | ✅ |
| `packages/tui/src/utils.ts` | `tui/widthx/*.go + internal/wordsegmenter/segments.go + internal/wordsegmenter/cjk.go + internal/wordsegmenter/sea.go + internal/wordsegmenter/word_rules.go (shared ICU word dependency)` | ✅ |
| `packages/tui/src/index.ts` | `partial: coding/extension/host/subprocess/runtime-node/shims/pi-tui.mjs re-exports pinned pi-tui/index.js; TestPiTuiPublicExportsAreRealImplementations checks the exact runtime namespace and restored exports; native Windows/macOS execution remains unqualified` | 🟡 |
| `packages/tui/src/components/box.ts` | `tui/box.go` | ✅ |
| `packages/tui/src/components/cancellable-loader.ts` | `tui/cancellable_loader.go` | ✅ |
| `packages/tui/src/components/editor.ts` | `tui/editor.go + tui/editor_segments.go + tui/editor_state.go + tui/editor_autocomplete_units.go + tui/editor_async_provider.go + tui/editor_autocomplete_request.go (native UTF-16 state with byte-provider adapters;79 original sites audited on this clean base, remaining113 helper/core cases pending)` | 🟡 |
| `packages/tui/src/components/image.ts` | `tui/image.go` | ✅ |
| `packages/tui/src/components/input.ts` | `tui/text_input.go (lossless UTF-16 via internal/jsstring)` | ✅ |
| `packages/tui/src/components/loader.ts` | `tui/loader.go` | ✅ |
| `packages/tui/src/components/markdown.ts` | `tui/markdown.go + tui/markdown_options.go + tui/markdown_style.go + tui/markdown_list.go` | ✅ |
| `packages/tui/src/components/select-list.ts` | `tui/select_filterable.go` | ✅ |
| `packages/tui/src/components/settings-list.ts` | `tui/settings_list.go` | ✅ |
| `packages/tui/src/components/spacer.ts` | `tui/spacer.go` | ✅ |
| `packages/tui/src/components/text.ts` | `tui/text.go` | ✅ |
| `packages/tui/src/components/truncated-text.ts` | `tui/truncated_text.go (D66 clamps horizontal padding when the full padding cannot fit instead of emitting a fatal over-wide row)` | ✅ |
| `packages/tui/src/components/alt-screen-flash.ts` | `tui/alt_screen_flash.go (ported; default/explicit duration, timer removal, rendering, truncation, disposal, and ordering unit-tested in tui/alt_screen_flash_test.go, including TestAltScreenFlashExplicitNonPositiveDurationExpiresImmediately; composite-render wired in tui_alt_screen.go; flash("Copied!") production trigger wired via app.message.copy/Ctrl+X → confirmMessageCopied in interactive.go, unit-tested TestConfirmMessageCopied_FullscreenFlashesInsteadOfStatusLine)` | ✅ |
| `packages/tui/src/components/h-stack.ts` | `tui/stack.go (HStack, ported + unit-tested side-by-side/align compositing; Stack-base parity)` | ✅ |
| `packages/tui/src/components/scroll-view.ts` | `tui/scroll_view.go; coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/components/scroll-view.js (pinned Pi for Node extensions)` | ✅ |
| `packages/tui/src/components/stack.ts` | `tui/stack.go` | ✅ |
| `packages/tui/src/components/v-stack.ts` | `tui/stack.go` | ✅ |
| `packages/tui/src/latex.ts` | `internal/latex/latex.go, symbols.go; upstream_test.go` | ✅ |
| `packages/tui/src/layout-node.ts` | `tui/layout_node.go` | ✅ |
| `packages/tui/src/layout.ts` | `tui/layout.go` | ✅ |
| `packages/tui/src/tui-alt-screen.ts` | `tui/tui_alt_screen.go, tui/tui_alt_screen_input.go, tui/tui_alt_screen_mouse.go, tui/tui_alt_screen_selection.go` | ✅ |
| `packages/tui/src/tui-main-screen.ts` | `tui/tui.go, tui/bounded_terminal_writer.go, tui/redraw_debug.go, tui/render_overflow.go` | ✅ |
| `packages/tui/src/alt-screen-search.ts` | `tui/alt_screen_search.go` | ✅ |
| `packages/tui/src/components/mouse-region.ts` | `tui/mouse_region.go` | ✅ |
| `packages/tui/src/native-module-path.ts` | `partial: coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/native-module-path.js (pinned Pi for Node extensions); TestNodeVendoredTuiUpstreamTests/native-module-path runs the upstream lookup cases; host Go terminal needs no Node module lookup` | 🟡 |
| `packages/tui/src/native-platform.ts` | `partial: tui/native_platform.go + internal/nativeplatform/clipboard*.go + coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/native-platform.js and native/ (pinned Node helpers); Linux native clipboard behavior is covered in Go and the shipped Node helper; Windows/macOS desktop execution remains unqualified` | 🟡 |
| `packages/tui/src/colors.ts` | `tui/colors.go` | 🟡 |
| `packages/tui/src/oklab.ts` | `tui/oklab.go` | 🟡 |
| `packages/tui/src/wheel-scroll.ts` | `tui/wheel_scroll.go + tui/tui_alt_screen.go (alt-screen wiring)` | 🟡 |
