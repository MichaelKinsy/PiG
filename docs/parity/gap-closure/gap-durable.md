# gap-durable: Pi 1.0.4 `packages/durable` (and the Chord parts it uses)

Scope: `packages/durable` (Pi Durable: Harness, Conversations, Documents, Tasks, Storage, Environment, Tools, the Pico runtime) and the parts of `packages/chord` that durable imports (`@earendil-works/chord`, `/context`, `/delta`). `docs/parity/PORT_MAP.md` does not count either package (see its out-of-denominator list). This file is the file map, the test map and the evidence. It does not change `PORT_MAP.md`.

Upstream mirror: the Pi 1.0.4 source tree (`.upstream/v1.0.4`). Base: `12429f15b`.

## Percentages

| Measure | Before | After |
|---|---:|---:|
| Counted by `PORT_MAP.md` / `test/parity/coverage.md` (durable only) | none of the 67 source files | none (unchanged: `packages/durable` is outside the PORT_MAP denominator) |
| `packages/durable/src` files with a Go counterpart and a `// Ports` claim | 66 / 67 (98.5%) | 66 / 67 (98.5%); the 67th, `storage/sqlite/index.ts`, is the re-export surface of Go package `durable/storage/sqlite` |
| Named exports of `index.ts`, `env`, `tools`, `testing`, `storage/jsonl`, `storage/sqlite` (249) with a Go identifier of the same name after case mapping | 237 / 249 (95.2%); the 12 others are Go-shaped, see the file map | 237 / 249; the typed `watchDoc` method gained its Go form, which is not an export name |
| Upstream `*.test.ts` files with a `ported` disposition (`test-mapping-v1.0.4.json`) | 47 / 47 durable, 21 / 21 chord | 47 / 47, 21 / 21 |
| Upstream test cases (731 `it`/`test` titles in the 47 files) accounted for in Go | 731 / 731 (629 by identical title, 102 by renamed or parametrized Go test) | 731 / 731 + 1 new case (`WatchDoc`) |
| Pocket API calls (46 rows) with a Go counterpart and a proving Pi test | not listed | 46 / 46 (11 also proven by Pocket-composition tests) |
| Upstream example scripts (`test/examples/*.ts`, 32) with a Go counterpart | 21 / 32 (65.6%) | 32 / 32 (100%) |

The upstream examples are not vitest files, so no ledger counts them. They run the public Harness API end to end (late join, subagents, child tasks, compaction, plan mode, reload and restart), which is the surface Pi Pocket uses.

## Pi Pocket drop-in readiness checklist

Acceptance target: Pi Pocket runs on PiG's Go Pi Durable (the Pi Pocket design notes, section 2, and the Pocket architecture notes, section 1). The list below is built from Pocket's source (Pocket's `src/server`, 19,197 lines): every `@earendil-works/pi-durable` and `@earendil-works/chord` import (26 + 14 + 7 sites) and every call on a Harness, Conversation, `tx`, `api` (ToolExecutionApi) or `runtime` (TaskRuntime) that the source makes. File:line is the first Pocket call site.

Legend. Status: `ready` means the Go API exists, the Pi tests that cover it are ported with their cases and pass, and nothing Pocket does with it differs. `+X` marks a row whose behavior `durable/interop` also compares with Pi Durable 1.0.4 (Node) on SQLite and JSONL stores. `ready+P` means Pocket composes it in a way no Pi test covers, so `durable/examples/pocket_dropin_test.go` also proves the composition. `Go shape` means the call keeps its meaning but its Go form differs for a language reason (`context.Context` for chord's `Context`, generics for tokens, `*T` for `T | undefined`). A Pi test is named by file and, where one case carries the proof, by title. Every Go counterpart of a Pi test is the same-named file under `durable/harness`, `durable/session`, `durable/storage` or `durable/tools`.

### Harness and Session

| Pocket call (first site) | Go | Status | Pi test that proves it |
|---|---|---|---|
| `Harness.open(storage, {models, registry, settings, env, now, conversationCreated, onReport}, context)` (`app.ts:454`) | `harness.OpenHarness(ctx, store, harness.HarnessOptions{Models, Registry, Settings, Env, Now, ConversationCreated, OnReport})` | ready+P (`conversationCreated` creating documents; reopen) +X | `harness-lifecycle.test.ts` (open, reopen, `onReport`), `harness-conversations.test.ts` (`conversationCreated`), `harness-tasks.test.ts` (`now` clock for `Now()` and `sleep`) |
| `settings` as getters read at every use (`app.ts:763`: `stream`, `compaction`, `retry`, `steeringMode`, `followUpMode`) | `HarnessOptions.Settings func() *HarnessSettings` with `Stream`, `Compaction`, `Retry`, `SteeringMode`, `FollowUpMode`, `ToolExecution`, `Extensions`, `Progress` | Go shape (a function returning the struct replaces per-field getters) | `harness-generation.test.ts` (settings read per request), `harness-compaction.test.ts` (live `backgroundTokens`); Go: example 25 changes `backgroundTokens` between requests |
| `harness.resume()` (`app.ts:760`) | `Harness.Resume()` | ready +X | `harness-tasks.test.ts`, `harness-tasks-recovery.test.ts` (`resume` after reopen) |
| `harness.commit(tx => ..., context)` (24 sites; `app.ts:482`) | `Session.Commit(ctx, func(tx) (any, error))`, `durable.Commit[T]` | ready +X | `session-documents.test.ts`, `session-tables.test.ts` |
| `harness.snapshot(Doc, [id|key], context)` for session, conversation and family documents (55 sites; `app.ts:473`) | `durable.Snapshot[T](ctx, reader, token, args...)` | ready+P (session scope next to conversation scope in one SQLite store, family member with seed) +X | `session-documents.test.ts`, `session-checkpoints-migrations.test.ts` |
| `harness.subscribeCommits(publication => ...)`: `publication.changes` of type `document` (`record.kind`, `conversationId`, `value`) and `submission` (`value`) (`app.ts:556`) | `Session.SubscribeCommits`; `durable.DocumentChange{Record.Kind, ConversationId, Value}`, `durable.SubmissionWrite{Value}`, `UsageDoc.AnyDefinition().Kind` | ready+P (busy transitions from `pi.live`, spend from `pi.usage`, submission records; the subscriber has run when `Wait` returns, finding 7) | `harness-live-deltas.test.ts`, `harness-inbox.test.ts`, `harness-events.test.ts` |
| `harness.taskGraph(context)` and its nodes (`running.ts:39`) | `Harness.TaskGraph(ctx)` returns `AttachedReplicatedState[TaskGraph]`; `TaskGraphNode{Id, Kind, ConversationId, Owner, Background, AbortRequested, State, Conversations}` | ready | `harness-task-graph.test.ts` |
| `harness.waitForTask(id, context)` (`commands.ts:1327`) | `Harness.WaitForTask(ctx, id)` | ready +X | `harness-tasks.test.ts`, `harness-ownership.test.ts` |
| `harness.abortTask(id, context)` (`schedules.ts:328`, `shell.ts`) | `Harness.AbortTask(ctx, id)` | ready | `harness-structured.test.ts`, `harness-tasks.test.ts` |
| `harness.abortSubmission(id, context, conversationId)` (`commands.ts:625`) | `Harness.AbortSubmission(ctx, id, &conversationId)` | ready | `harness-submissions.test.ts` (reports abort results and looks submissions up by conversation) |
| `harness.getTask(id, context)` (`shell.ts:345`) | `Harness.GetTask(ctx, id)` | ready +X | `harness-tasks-recovery.test.ts`, `harness-tasks.test.ts` |
| `harness.submission(id, context)` then `.status(context)` (`app.ts:1261`) | `Harness.Submission(ctx, id)`, `Submission.Status(ctx)` | ready | `harness-compaction.test.ts` (23 uses), `harness-generation-recovery.test.ts` |
| `harness.conversation(id, context)` (`alerts.ts:95`) | `Harness.Conversation(ctx, id)` | ready +X | `harness-ownership.test.ts` (21 uses), `harness-conversations.test.ts` |
| `harness.createConversation({ownership, agent, init}, context)` (`commands.ts:138`) | `Harness.CreateConversation(ctx, ConversationCreateOptions{Ownership, Agent, Init})` | ready +X | `harness-conversations.test.ts`, `harness-structured.test.ts` |
| `harness.close(context)` (24 sites) | `Session.Close(ctx)` | ready | `harness-lifecycle.test.ts` (close, pending waits rejected) |
| `tx.scanConversations({}, 256, cursor)` paging at startup (`app.ts:483`) | `Tx.ScanConversations(query, limit, cursor)` with `Page.Next *Cursor` | ready+P +X | `session-tables.test.ts`, `sqlite-storage.test.ts` |
| `storage.scanSubmissions({conversationId}, 256, cursor, context)` on the Storage the host kept (`app.ts:242`) | `Storage.ScanSubmissions(ctx, query, limit, cursor)` | ready+P (read after the Harness opened, then after reopen) | the storage conformance suite run by `sqlite-storage.test.ts` and `jsonl-storage.test.ts` (`scanSubmissions` cases) |
| `openNodeSqliteStorage(path)` (`app.ts:451`) | `sqlitenode.OpenNodeSqliteStorage(path, NodeSqliteStorageOptions{})` | ready +X | `sqlite-storage.test.ts`, `sqlite-facade.test.ts`, `sqlite-migrations.test.ts` (the schema text equals Pi's) |
| `NodeExecutionEnv({cwd})`, `ExecutionEnv` (`app.ts:411`, `shell.ts`) | `envnode.NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd})`, `env.ExecutionEnv` | ready (1.0.3 and 1.0.4 `ExecutionEnv` changes included) | `env-node.test.ts`, `env-node-conformance.test.ts`, `env-node-spill.test.ts` |
| `CodingTools`, `Registry.install`, `uninstall`, `createRegistry` (`app.ts:410`, `reload.ts:253,318`) | `tools.CodingTools`, `Registry.Install`, `Uninstall`, `harness.CreateRegistry` | ready | `harness-registry.test.ts`; example 10 |

### Conversation

| Pocket call | Go | Status | Pi test |
|---|---|---|---|
| `conversation.submit({type: "input"\|"write", content, whenBusy, requestId}, context)` and `submission.wait(context)` (`commands.ts:417`) | `Conversation.Submit(ctx, SubmissionDraft{Type, Content, WhenBusy, RequestId, Entry})`, `Submission.Wait` | ready +X | `harness-submissions.test.ts`, `harness-inbox.test.ts` |
| `conversation.abort(context)` (`commands.ts:563`, `http.ts`) | `Conversation.Abort(ctx, options)` | ready | `harness-structured.test.ts`, `harness-ownership.test.ts`, `harness-compaction.test.ts` |
| `conversation.configure({model, thinkingLevel, extensions, tools, instructions, cwd}, context)` (`commands.ts:605`) | `Conversation.Configure(ctx, AgentChange{...})` with `SetTo`, `Cleared` | Go shape (`null` is `Cleared[T]()`) +X | `harness-conversations.test.ts`, `harness-tools.test.ts` |
| `conversation.reset(handoff, context)` (`commands.ts:763`) | `Conversation.Reset(ctx, *string)` | ready +X | `harness-inbox.test.ts`, `harness-view.test.ts` |
| `conversation.fork(at, {ownership, agent, init}, context)` (`commands.ts:1298`) | `Conversation.Fork(ctx, at, ConversationCreateOptions)` | ready+P (init copies and trims documents in the forking commit, writes a session document) +X | `harness-conversations.test.ts` (10 uses), `session-forks.test.ts` |
| `conversation.compact(instructions, context)` returning the task id (`commands.ts:1323`) | `Conversation.Compact(ctx, *string) (TaskId, error)` | ready +X | `harness-compaction.test.ts` (58 uses) |
| `conversation.viewState(context)`: `.value.conversation.owner`, `.value.entries`, `.subscribe(cb)`, `.dispose()` (`room.ts:173`) | `Conversation.ViewState(ctx)` returns `AttachedReplicatedState[ConversationView]` with `Value()`, `Subscribe`, `Dispose` | ready | `harness-view.test.ts`, `harness-lifecycle.test.ts` |
| `conversation.entries(query, limit, cursor, context)` (`resend.ts`, `projection.ts`) | `Conversation.Entries(ctx, query, limit, cursor)` | ready +X | `harness-lifecycle.test.ts`, `harness-prompt.test.ts` |
| `conversation.agent(context)` (`extensions/subagents.ts`, `codemode.ts`) | `Conversation.Agent(ctx)` | ready | `harness-conversations.test.ts` |

### Transactions (`tx`) and documents

| Pocket call | Go | Status | Pi test |
|---|---|---|---|
| `tx.doc(Doc, id)`, `tx.doc(Doc)`, `tx.doc(Family, id, key, seed)` (46 draft mutations; `app.ts:464`) | `durable.TxDoc[T](tx, token, args...)`, a `*delta.Object` draft | ready+P. A typed Go struct seed was rejected as non-JSON; fixed in `session/transaction.go` (finding 5) +X | `session-documents.test.ts`, `session-forks.test.ts` |
| `defineDoc` and `defineDocFamily` with `scope` session or conversation, `history: "latest"`, `fork: "current"` or `"initial"` (20 kinds; `docs.ts`) | `durable.DefineDoc`, `DefineDocFamily`, `DocumentSemantics{Scope, History, Fork}` | ready+P (current, initial, latest, session, family) +X | `session-definitions.test.ts`, `session-forks.test.ts`, `session-checkpoints-migrations.test.ts` |
| `tx.entry(AssistantEntry, id)` (`app.ts:1391`) | `tx.Entry(id)`, `durable.AssistantEntry.Is` | ready +X | `harness-submissions.test.ts` (typed entries through tokens) |
| `tx.conversation(id)`, `tx.task(id)`, `tx.submissionByRequest(conversationId, requestId)` (`schedules.ts:46`, `shell.ts`) | `Tx.Conversation`, `Tx.Task`, `Tx.SubmissionByRequest` | ready | `session-tables.test.ts`, `harness-ownership.test.ts` |
| `tx.createConversation({ownership: {kind: "task", taskId}})` and `configure(tx, id, change)` (`extensions/subagents.ts`) | `Tx.CreateConversation`, `harness.Configure(tx, id, change)` | ready +X | `harness-ownership.test.ts`, `harness-structured.test.ts`; examples 22, 23 |
| `tx.createTask(Task, input, {ownership, background, conversationId})` (5 sites; `commands.ts`, `subagents.ts`) | `durable.CreateTask(tx, task, input, TaskOptions{Ownership, ConversationId, Background})` | ready +X | `harness-tasks.test.ts`, `harness-ownership.test.ts` (23 and 14 uses) |
| Built-in documents and entries read by Pocket: `AgentDoc`, `AgentState`, `LiveDoc`, `LiveState`, `UsageDoc`, `UsageState`, `InboxState`, `AssistantEntry`, `EntryRecord`, `ConversationView`, `ConversationRecord` | the same names under `harness` and `durable` | ready (JSON names equal Pi's; `UsageState{models, tools}` checked) | `harness-view.test.ts`, `harness-inbox.test.ts`, `harness-live-deltas.test.ts` |

### Tasks, hooks and extensions

| Pocket call | Go | Status | Pi test |
|---|---|---|---|
| `defineTask({name, version, initial, phases, abort})` for 9 task kinds (`reload.ts` `ORDER`; `shell.ts`, `schedules.ts`, `resend.ts`) | `durable.DefineTask(TaskDefinition{...})` | ready +X | `harness-tasks.test.ts`, `harness-tasks-recovery.test.ts`, `types.test.ts` |
| `runtime.commit`, `runtime.conversation`, `runtime.now`, `runtime.sleep`, `runtime.memo`, `runtime.snapshot`, `runtime.env` | `TaskRuntime.Commit`, `Conversation`, `Now`, `Sleep`, `Memo`/`MemoCandidate`, `Snapshot` through `DocumentReader`, `Env` | ready | `harness-tasks.test.ts` (memo, sleep), `harness-tasks-recovery.test.ts`, `harness-structured.test.ts` |
| `defineExtension({name, tools, tasks, sections, hooks, wraps})`, `defineTool`, `section` (`reload.ts`, 10 and 5 and 8 uses) | `harness.DefineExtension`/`durable.Extension`, `harness.DefineTool`, `harness.Section` | ready | `harness-registry.test.ts`, `harness-prompt.test.ts`, `harness-tools.test.ts` |
| `hook(ToolTask, {beforeTool})` returning `undefined` or `{block}` (`plan.ts:297`, `guard.ts:19`) | `harness.Hook(harness.ToolTask, &harness.ToolHooks{BeforeTool})`, `BeforeToolResult{Block, Arguments}` | ready+P (a blocked call reaches the model as an error result) | `harness-tools.test.ts` (7 `beforeTool` cases) |
| `hook(GenerationTask, {onYield})` returning `{continue}` (`goals.ts:41`) | `harness.Hook(harness.GenerationTask, &harness.GenerationHooks{OnYield})`, `YieldContinue` | ready | `harness-inbox.test.ts` (7 uses), `harness-tools.test.ts` |
| `extension.hooks` read by a host tool: `registration.task === ToolTask.definition.name`, `registration.handlers.beforeTool/afterTool` (`codemode.ts:68`) | `Extension.Hooks[i].Task`, `.Handlers.(*harness.ToolHooks)`, `harness.ToolTask.AnyDefinition().Name` | ready+P | no Pi test reads `Extension.hooks` directly (hooks are exercised through `runtime.hooks.each` in `types.test.ts` and `harness-tasks.test.ts`); proven by the Go Pocket test only |
| `{...api, memo}` as a `HookApi` (`codemode.ts:230`) | a struct embedding `durable.ToolExecutionApi` that overrides `Memo` and `MemoCandidate` | ready+P (`memoPrefixed` in the Pocket test) | `harness-tools.test.ts` (keeps durable hook decisions in task memos) |
| `api.agent`, `api.conversation`, `api.snapshot`, `api.commit`, `api.details`, `api.taskId`, `api.callId`, `api.memo`, tool `replay`, `executionMode`, `prepareArguments`, `outputLimits.retain` (`artifacts.ts`, `subagents.ts`, `codemode.ts`) | `ToolExecutionApi.Agent/Conversation/SnapshotErased/Commit/Details/TaskId/CallId/Memo`, `ToolRegistration{Replay, ExecutionMode, PrepareArguments, OutputLimits}` | ready | `harness-tools.test.ts`, `harness-tools-recovery.test.ts`, `harness-live-deltas.test.ts` |
| `validateToolArguments(tool, call)` (`@earendil-works/pi-ai/utils/validation`) | `agent.ValidateToolArguments` (the Harness calls it in `harness/tool.go`) | ready | `harness-tools.test.ts` (validates arguments before and after beforeTool; repairs arguments with prepareArguments before validation) |
| `BACKGROUND_CONTEXT`, `awaitWithContext`, `withAbortSignal` (`@earendil-works/chord/context`; 14 sites) | `context.Context` (`context.Background()`, derived contexts) | Go shape: every Go call takes a `ctx`; no Pi test applies | `chordctx_upstream_test.go` (`internal/chord/chordctx`) |

### What the checklist does not prove

- Pocket's own TypeScript is not run: it is a TypeScript application. A Go Pocket calls the rows above; the Go tests prove the Harness side of each call.
- Pocket pins `@earendil-works/*` 1.0.2 and the Go port follows 1.0.4. Durable changed in 20 files between them (1.0.3 `ExecutionEnv`: bounded binary readers, watch, argv exec, windowed output; 1.0.4: powershell tool, growing reads, output BOM). Pocket's own `#env` and the shell and artifact tools use `NodeExecutionEnv`, which the Go port has at 1.0.4.
- First item for the Pocket integration: a `pocket.sqlite` written by Pocket itself, with Pocket's own documents and extensions, was not opened by Go. The library-level cross-read in `durable/interop` (the previous section) is the evidence for the store format.
- Tasks that become ready together commit in the Go scheduler's order, not Pi's task order (finding 8, open).
- Rows marked `ready` rest on the ported Pi tests passing; they were not re-derived against Pocket's runtime behavior beyond the compositions in `pocket_dropin_test.go`.

Count: 46 rows, 46 with a Go counterpart, 11 `ready+P`, 22 `+X`, 3 `Go shape`, 0 missing.

## Cross-runtime proof with Pi Durable 1.0.4 (Node)

`durable/interop` runs the pinned `@earendil-works/pi-durable`, `pi-ai` and `chord` 1.0.4 (`package-lock.json`; `make durable-interop-deps` installs them through `automation/ci/npm-locked.py`, a prerequisite of `make test`; the tests do not install, and missing packages fail them) against the stores Go reads and writes. The Node program (`app.mjs`, `scenario.mjs`, `scenario2.mjs`, `resume.mjs`, `apidump.mjs`, `rawdump.mjs`) and its Go twin (`app_test.go`) are one scripted application: documents (conversation, session, rewindable `asOf`, family with seed), a tool that starts a task, a tool that runs a task-owned child conversation, an agent `configure`, a model error, a fork with an `init`, a fork as of an earlier entry, a manual compaction, a reset, and a scripted faux model.

| Test | What it proves |
|---|---|
| `TestScriptedSessionStoresMatchPiDurable`, `TestSecondScriptedSessionStoresMatchPiDurable` | The script run by Pi (Node) and by Go on fresh SQLite files leaves the same rows in all 9 tables (`durable_metadata`, `durable_schema`, `record_ids`, `conversations`, `entries`, `tasks`, `submissions`, `documents`, `document_revisions`), as JSON values, with `next_id`, `next_seq`, `commit_seq`, record IDs and document revisions equal. Only wall-clock `timestamp`, the faux provider's random `api` and the provider `sessionId` are normalized. The comparison is strict, and it fails in about 4% of runs of each script (finding 8). |
| `TestScriptedSessionJsonlFilesMatchPiDurable` | The same for JSONL stores: the same 14 file names and the same records per file; `main.jsonl` compares as an unordered set (finding 8). |
| `TestScriptedSessionsViewIdentically` | Both scripts, both storages: the Harness API shows the same conversations, entries, model context, agent, live, usage, notes and plan documents, session document and tasks for the Pi-written and the Go-written store. |
| `TestGoOpensAndResumesAStoreWrittenByPiDurable` | Pi writes a store; Go reads it through the Harness API and sees what Pi sees; Go resumes it (new input, a turn in the fork, a compaction); Pi reads what Go wrote and sees what Go sees; Pi resumes the store Go resumed. SQLite and JSONL, both scripts. |
| `TestPiDurableOpensAndResumesAStoreWrittenByGo` | The same with the roles reversed. |

This closes the cross-read gap of the previous round for the Durable library. A `pocket.sqlite` produced by the Pocket application itself is the same file format, but Pocket's own documents and extensions were not run. Differences found are findings 6 and 8: Go wrote no `sections` member for a system message whose sections were empty (Pi writes `{}`), fixed in `ai/messages.go` (review: Go's `GetCurrentSystemMessage` then also needed Pi's rule of omitting `sections` when none remain, fixed in `ai/transcript.go`); and the scheduler's commits differ in order and, in about 4% of runs, in number (finding 8).

Records are equal as JSON values, not as text. Pi writes object members in insertion order and Go writes struct fields in declaration order and map keys sorted, so the bytes of a record differ while its value is identical; both readers parse JSON, and the cross-read tests prove each reads the other's text. The SQLite schema statements are the same text as Pi's `migrations.ts`.

## File map: `packages/durable/src` to Go

Status `ported`: every exported behavior has a Go counterpart; the upstream tests covering the file are ported (see the test map). `Ports` comment: present in the Go file.

| Upstream | Go | Status |
|---|---|---|
| `documents.ts` | `durable/documents.go` | ported; `WatchDoc[T]` added (typed `watchDoc`) |
| `entries.ts` | `durable/entries.go` | ported |
| `errors.ts` | `durable/errors.go` | ported |
| `ids.ts` | `durable/ids.go` | ported |
| `index.ts` | `durable/doc.go` (package surface) | ported; `DEFAULT_*_POLICY` are `harness.DefaultRetryPolicy` etc., `HooksOf`/`HookResult` are Go generics on `Hook[...]` |
| `tasks.ts` | `durable/tasks.go` | ported |
| `truncate.ts` | `durable/truncate.go` | ported |
| `types.ts` | `durable/types.go`, `durable/harness_types.go` | ported; `durable.DocumentStateOf[T]` is the typed `documentState` (added) |
| `env/index.ts`, `env/decode.ts`, `env/line-scan.ts` | `durable/env/env.go`, `decode.go`, `line_scan.go` | ported |
| `env/node.ts` | `durable/env/node/node.go`, `exec.go`, `shell.go`, `binary_reader.go`, `line_reader.go`, `paths.go`, `process_*.go`, `open_*.go`, `fileid_*.go` | ported |
| `env/node-watch.ts` | `durable/env/node/watch.go`, `watch_fs_*.go` | ported |
| `harness/agent.ts` | `durable/harness/agent.go` | ported |
| `harness/compaction.ts` | `durable/harness/compaction.go` | ported |
| `harness/context.ts` | `durable/harness/context.go` | ported |
| `harness/define.ts` | `durable/harness/define.go` | ported |
| `harness/events.ts` | `durable/harness/events.go` | ported |
| `harness/generation.ts` | `durable/harness/generation.go` | ported |
| `harness/harness.ts` | `durable/harness/harness.go` | ported |
| `harness/inbox.ts` | `durable/harness/inbox.go` | ported |
| `harness/json.ts`, `util.ts` | `durable/harness/json.go`, `util.go` | ported |
| `harness/live.ts` | `durable/harness/live.go` | ported |
| `harness/output.ts` | `durable/harness/output.go` | ported |
| `harness/prompt.ts` | `durable/harness/prompt.go` | ported |
| `harness/provider.ts` | `durable/harness/provider.go` | ported |
| `harness/registry.ts` | `durable/harness/registry.go` | ported |
| `harness/scheduler.ts` | `durable/harness/scheduler.go` | ported |
| `harness/submissions.ts` | `durable/harness/submissions.go` | ported |
| `harness/task-graph.ts` | `durable/harness/task_graph.go` | ported |
| `harness/tool.ts` | `durable/harness/tool.go` | ported |
| `harness/types.ts` | `durable/harness/types.go`, `durable/harness_types.go`, `durable/tasks.go` | ported |
| `harness/usage.ts`, `harness/view.ts` | `durable/harness/usage.go`, `view.go` | ported |
| `session/forks.ts`, `observation.ts`, `session.ts`, `transaction.ts` | `durable/session/forks.go`, `observation.go`, `session.go`, `transaction.go` | ported |
| `storage/memory.ts` | `durable/storage/memory.go` | ported |
| `storage/jsonl/{index,storage}.ts`, `node.ts` | `durable/storage/jsonl/storage.go`, `node/node.go` | ported |
| `storage/sqlite/{database,migrations,storage,node}.ts` | `durable/storage/sqlite/{database,migrations,storage}.go`, `node/node.go` | ported |
| `storage/sqlite/index.ts` | package `durable/storage/sqlite` | ported (re-export file, no `Ports` claim) |
| `testing/{assertions,env-conformance,index,runner,storage-benchmark,storage-conformance,types}.ts` | `durable/durabletest/*.go` | ported; `ExpectLike`, `StorageConformanceRunner`, `createExpectAssertions` are vitest adapters and have no Go form (`*testing.T` is the runner) |
| `tools/{bash,edit,edit-diff,env,file-mutation-queue,image,index,path-utils,read,write}.ts` | `durable/tools/*.go` | ported |

## File map: `packages/chord/src` files that durable uses

Durable imports `@earendil-works/chord` (40 imports: replicated state, services), `/context` (10) and `/delta` (12).

| Upstream | Go | Status |
|---|---|---|
| `json.ts` | `chord/json.go`, `internal/chord/chordjson/chordjson.go` | ported |
| `context/index.ts` | `internal/chord/chordctx/chordctx.go` | ported |
| `delta/index.ts`, `delta/diff.ts`, `delta/tracker.ts`, `delta/draft.ts` | `chord/delta/{ops,handles,tracker,json}.go`, `chord/delta/{apply,diff,delta,copy_tracker}.go` | ported; every name durable imports from `/delta` (`Op`, `Path`, `NonEmptyPath`, `Change`, `Prepared`, `Tracker`, `apply`, `applyImmutable`, `applyImmutableBatches`, `overlap`, `track`) exists; only `index`, `diff` and `tracker` carry a `Ports` claim, `draft.ts` is `handles.go` |
| `delta/apply-immutable-trusted.ts`, `delta/revision-validator.ts` | folded into `chord/delta/tracker.go`, `chord/state.go` | designed out as separate files: the first is the tracker's trusted apply (Go's `ApplyImmutable` over already-validated JSON); the second validates JS object graphs (cycles, symbol keys, accessors, sparse arrays), which decoded Go JSON cannot contain, and `chord.CopyJSON` rejects non-finite numbers |
| `services/state.ts`, `state-codec.ts`, `wire.ts`, `types.ts` | `chord/state.go`, `internal/chord/state*.go` | ported |
| `services/consumer.ts`, `instances.ts`, `api.ts`, `facets/host.ts` | `internal/chord/binding_async.go`, `keyed.go`, `service.go`, `facets.go` | ported |
| `services/{errors,handle,loopback,provider,state-internals}.ts`, `facets/loader.ts`, `index.ts` | `internal/chord/{provider,facet_remote,...}.go` | ported by behavior (no `Ports` claim) |
| `bundler.ts`, `node.ts`, `node/{bundle,bundle-loader,manifest,package}.ts` | none | not used by durable: Chord application bundling and loading; deferred outside this slice |

## Test map: `packages/durable/test`

All 47 `*.test.ts` files carry a `ported` disposition in `test/parity/interfaces/test-mapping-v1.0.4.json`. The Go test names mirror the upstream titles (identical text for 629 of 731 cases; the other 102 are renamed `TestFilesystem...`/`TestShell...`/`TestRead...` functions or parametrized cases). Support files (`harness-support.ts`, `session-support.ts`, `task-support.ts`, `chat-support.ts`, `storage-memory.ts`) correspond to `durable/harness/*_support_test.go`, `durable/session/sessiontest`, `durable/durabletest`. The benchmarks (`storage.bench.ts`, `tool-output-bench.ts`) correspond to `durable/storage/storage_bench_test.go` and `durable/durabletest/storage_benchmark.go`.

| Upstream test file | Go evidence |
|---|---|
| `chord-guide`, `harness-*` (21 files: compaction, context, conversations, events, generation, generation-recovery, inbox, inspect, lifecycle, live-deltas, output, output-skip, ownership, prompt, registry, structured, submissions, task-graph, tasks, tasks-recovery, tools, tools-recovery, view) | `durable/harness/*_test.go` (one Go file per upstream file) |
| `session-*` (checkpoints-migrations, definitions, documents, forks, states, tables, watches) | `durable/session/*_test.go` |
| `jsonl-storage`, `memory-storage`, `sqlite-facade`, `sqlite-migrations`, `sqlite-storage`, `storage-runtime-boundary` | `durable/storage/**` |
| `env-line-scan`, `env-node`, `env-node-conformance`, `env-node-spill`, `env-truncate` | `durable/env/**`, `durable/truncate_upstream_test.go` |
| `tools`, `tools-read-differential` | `durable/tools/*_test.go` |
| `provider-session-cache-e2e` | `provider_session_cache_e2e_test.go` (hermetic), `..._live_test.go` (live) |
| `spec-usage`, `types` | `durable/spec_usage_upstream_test.go`, `durable/examples/spec_usage_upstream_test.go`, `durable/types_upstream_test.go`, `durable/tasks_test.go` |

## Test map: `packages/durable/test/examples`

| Script | Go test (`durable/examples/`) | Status |
|---|---|---|
| 00 to 05 | `session_examples_test.go` | ported before this slice |
| 06, 07, 08, 10 | `harness_examples_test.go` | ported before |
| 09 | `context_examples_test.go` | ported before |
| 11, 12, 13 | `task_examples_test.go` | ported before |
| 14, 15, 17 | `chat_examples_test.go` | ported before |
| 18, 19 | `print_examples_test.go` | ported before |
| 20, 30 | `inbox_override_examples_test.go` | ported before |
| 16 real model | `lifecycle_examples_test.go` `TestExample16RealModel` | ported here with the faux provider: OpenAI is outside durable and the script prints "skipped" without a key; the pi.live watch path is the content |
| 21 late join | `lifecycle_examples_test.go` `TestExample21LateJoin` | ported here |
| 22 foreground subagent | `lifecycle_examples_test.go` `TestExample22SubagentForeground` | ported here |
| 23 background subagents | `background_subagents_example_test.go` | ported here (spawn, send, stop, status, restart with an in-flight reporter) |
| 24 child tasks | `tasks_compaction_examples_test.go` `TestExample24ChildTasks` | ported here (failFast, abort cascade with refunds, task graph, restart) |
| 25 compaction | `tasks_compaction_examples_test.go` `TestExample25Compaction` | ported here; the overflow step differs from the script, see below |
| 26 coding agent | `agent_examples_test.go` | ported here (live settings getter, env follows `cwd`) |
| 27 plan mode | `agent_examples_test.go` | ported here (tool filter, `Cleared`, system prompt switches) |
| 28 reviewer | `agent_examples_test.go` | ported here (`onYield` loop, stored agent) |
| 29 sandbox per conversation | `lifecycle_examples_test.go` | ported here |
| 31 reload and restart | `lifecycle_examples_test.go` | ported here |

## Findings

1. `durable.WatchDoc[T]` was missing. `types.go` named a "typed `WatchDoc` helper" that did not exist, so Go callers of Pi's `harness.watchDoc(LiveDoc, id)` (Pocket's live-answer streaming) had only `WatchDocErased`. Added with a unit test (`TestWatchDocTypedDecodesRevisionsAndReportsRetirementAsNil`, red before the change: the symbol did not compile) and a caller path (example 16, a watch of `pi.live` decoded as `harness.LiveState`).
2. `types.go` also named a `DocumentStateOf` helper that did not exist. Added as `durable.DocumentStateOf[T]` (a typed Chord replicated state over `Session.DocumentStateErased`: snapshot, buffered frames in cursor order, nil after retirement, nil for an absent document), with `TestDocumentStateOfDecodesRevisionsAndRetirement`. A revision that does not decode as the token's type stops delivery and reaches `OnError` (`DocumentStateOfWithOptions`), as a source-contract failure of an attached state does (`TestTypedStateSourceReportsARevisionThatDoesNotDecode`); a publication at or before the snapshot cursor is not delivered again (`TestTypedStateSourceIgnoresPublicationsAtOrBeforeTheSnapshotCursor`). A typed watch refuses a document whose current value does not decode when it is acquired, ends `Start`'s listener with `listener_error` on a later mismatched revision, and its `Value` returns the latest revision that decoded instead of panicking (`TestWatchDocTypedValueDoesNotPanicOnAMismatchedRevision`).
3. Example 25: Pi Durable 1.0.4 (Node, the pinned packages) was run on the upstream script and the Go example asserts its printed numbers: after the long chat a threshold summary first, 5 messages in context and 14 entries stored; after `compact()` a manual summary first, 4 and 17; after "What should we pack?" 7 and 20; the last request ends `model_error` with a threshold summary, 4 and 24. The script's final step does not reach the overflow compaction in Pi either: the context already estimates over the blocking threshold, so generation compacts for the threshold and an overflow after that has no second compaction (`generation.go` `Compacted`, as `generation.ts`). Examples 22, 24, 26, 27, 28, 29 and 31 were run the same way and print what the Go examples assert; 21 and 23 are timer-driven and were not compared.
4. Stopping an event stream does not drain batches whose commit already finished: the script sleeps a tick before `stream.stop()` for that reason. The Go examples wait for the last expected event instead of sleeping.
5. Typed Go family seeds. `tx.Doc(familyToken, id, key, seed)` rejected a struct seed as non-JSON, while `CreateTask` input and `Snapshot` accept typed values. Pocket's `ArtifactBodyDoc` is a family with a seed. Fixed at `durable/session/transaction.go` (the seed is encoded through `durable.ToJsonValue`); regression `TestFamilyDocSeedAcceptsATypedGoValue` (red before the change).
6. Empty `sections`. After a reset Pi writes a `pi.system` message with `"sections": {}`; Go omitted the member, so the two runtimes wrote different records for the same script (found by `TestScriptedSessionStoresMatchPiDurable`, red before the change). Fixed in `ai/messages.go` (`SystemMessage.MarshalJSON` writes a present, empty sections object).
7. Waiter wake-up order. In Pi, `submission.wait()` and `waitForTask()` resolve a promise inside a commit listener, and the awaiting code resumes after the synchronous listener loop, so a host `subscribeCommits` callback has already seen the commit when `await wait()` returns. Go woke the waiter goroutine inside the first listener, which raced the host's callback (the Pocket busy-state case failed 3 runs in 5 before the change). Fixed: `SessionImpl.DeferUntilPublished` runs deferred work after the listener loop, and the submission, task and idle waiters resolve through it (`durable/session/session.go`, `harness/submissions.go`, `harness/scheduler.go`). Regressions: `TestDeferUntilPublishedRunsAfterEveryListener`, `TestPocketCommitSubscriberTracksBusySpendAndSubmissions` (60 runs under `-race`). Document watches and states are unchanged.
8. Commit order of tasks that are ready together. Pi runs on one thread: a started handler runs synchronously to its first await before the next handler starts, so tasks that become ready together commit in task order (a Pi Durable 1.0.4 probe of three tasks created in one commit prints `x y z` for the phase-a commits and again for the terminal commits; the interop script's tool task 10 and job 15 commit their sidecars 10 then 15). Go runs each invocation on its own goroutine, so the order is the Go scheduler's: two of 40 JSONL commits swap in about half the interop runs, and a three-task probe differs from Pi's order in under 1% of runs. No Pocket call reads the order, but it is a drop-in difference and it is open.
   Review (rev-gap-durable) correction: records do differ. The scheduler's reservation pass (`kick` then `drain` on a goroutine) takes its Session line position when the goroutine runs, where Pi's `queueMicrotask(drain)` takes it in the publishing turn. When a handler's commit overtakes the pass, one reservation commit reserves two tasks that Pi reserves in two commits, so Go writes one commit fewer and every later `seq`, `commit_seq`, `createdAt` and `next_seq` is one lower. Measured at `f305a7842`: 7 of 200 runs of the first script (39 commits, Pi 40) and 9 of 200 of the second (52, Pi 53), without `-race`; under `-race` more often. `TestScriptedSessionStoresMatchPiDurable`, `TestSecondScriptedSessionStoresMatchPiDurable` and `TestScriptedSessionJsonlFilesMatchPiDurable` fail when it happens, so they are flaky in `make test` until this is closed. A probe that takes the line position synchronously in `kick` and `scheduleReconcileLocked` (a `LineTicket` the drain or reconcile goroutine later commits at) matched Pi's commit count in 200 of 200 runs of each script; it changes which commit `TestTaskScheduling` (`rejects task waits cancelled or closed while queued on the line`) holds, so it needs that case re-derived against Pi before it can land. Handler-originated order (the 10/15 swap) remains after it.
   Closed in part (follow-up to the review): the reservation pass and the reconcile pass now take their Session line position synchronously where they are scheduled (`Session.TakeLineTicket`, used by `kick` and `scheduleReconcileLocked`; the pass commits at that position with `CommitWithTicket`), as `queueMicrotask` does in the publishing turn. `TestReservationPassTakesItsLinePositionInThePublishingTurn` holds the scheduler's goroutine back and starts a second task creation from a commit subscriber: it was red (one reservation commit started both tasks, 39-for-40 shape) and is green over 50 runs under `-race`. A released ticket still waits for the job ahead of it (`TestReleasedLineTicketStillWaitsForTheJobAheadOfIt`, mutation-killed): the first version let the rest of the line overtake a held commit, which `TestTaskScheduling` caught. Review (rev-gap-durable-2): taking a ticket under the scheduler mutex let Close deadlock when an admission reached the line after close began, because the admission waited for the close listeners while holding the line mutex and the scheduler's `seal` listener needs the scheduler mutex; the admission now waits without the line mutex (`TestAdmissionWaitingForCloseListenersDoesNotHoldTheLine`, red before). The reconcile pass's ticket has no guard yet (replacing it with nil passes every durable test). The handler-originated order swap described above remains.
   What was tried and why it was not kept:
   - Starting reservations in task order, waiting for each first step to hold its place on the Session line, and holding every step until the whole pass started (the safe part, about 60 lines in `scheduler.go` plus an admission callback on `Session.CommitWith`): the probe fails on 1 run in 300 instead of on run 0, and the interop order stays racy because the swapped commits come from handlers, not steps. Not kept: it leaves the difference in place and adds scheduler code.
   - A dispatch baton (a handler starts only after the previous handler yielded at its first line commit or runtime await, and resumes after a commit only in settle order): the probe passes 1000 runs, also under `-race`. Not kept: `TestHarnessInspect` hangs, because a handler blocked in user code that is not a runtime call (a channel gate, a model call, a socket read) holds every later handler back, where Pi's `await` yields. Go cannot see that a goroutine is blocked, and a time bound is a numbered divergence with a magic literal.
   - Needs an owner decision: accept a numbered divergence with a time-bounded baton, or leave the order as the scheduler's. The probe and the baton diff are reproducible from this paragraph; the strict JSONL comparison in `durable/interop` stays unordered for `main.jsonl` until then.
9. Pocket compositions are covered by `durable/examples/pocket_dropin_test.go`: open and reopen over SQLite with `conversationCreated`; a commit subscriber that tracks busy state, spend and submissions; a fork whose `init` copies and trims documents; a ToolTask hook read from `Extension.Hooks` and called with an overriding `HookApi`; storage reads on the kept Storage.
10. Not done, and the first item for the Pocket integration: open a `pocket.sqlite` written by Pocket itself (its documents, extensions and Lancet Guard state) with Go Pi Durable, resume it, and compare the Harness API view with Node. The library-level cross-read in `durable/interop` proves the store format for the Durable features its scripts use, not Pocket's own documents. Also not done: Pocket's own TypeScript against Go (later, by the lead's order); a Pocket-written `pocket.sqlite` with Pocket's own documents; the upstream vitest suite run against Go (the Go tests are ports of its cases, not the suite itself).

## Commands

```
go test ./durable/... ./chord/... ./internal/chord/... -count=1
go test ./durable/examples/ -count=10 -race
go vet ./durable/...
```
