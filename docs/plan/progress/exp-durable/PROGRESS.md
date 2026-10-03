# exp-durable: coding-agent experimental on pi-durable (Pi 1.0.0)

STATUS: READY for what is done (see READY.md). The remaining items moved to lanes xd-models-iface, xd-coding-harness, xd-transcript-bridge and xd-harness-retire.

Lane exp-durable. Branch `smc1/exp-durable` from `agg-100`, pushed to `team/smc1/exp-durable`. Upstream `.upstream/v1.0.0/packages/coding-agent/src/experimental` and `test/experimental-*`. Merged staging `d-foundation` (durable root types) once.

## HANDOFF

### Done
- Deleted `internal/experimental/mini` (Pi removed mini and micro); upstream-sync rows and the `experimental-client` family updated.
- `session_catalog.go`: `meta.json` catalog (`ListSessions`, `ReadSession`, `CreateSession`, `DeleteSession`, `SessionStoragePath`) with unit tests; server lists, resolves, creates and deletes through it (server.ts:383-397). `routing.SessionMetadata` is now an interface (id only), as `packages/server` 1.0.0; routing tests run on an in-memory map.
- Services: AgentController contract (`abort`, `waitForPrompt`; `requestAbort`, `nextRun`, `resume`, `navigate` removed), provider over consumer-owned boundaries (`AgentConversation`, `AgentHarness`, `PromptSubmission`) using `durable.SubmissionId/WhenBusy/SubmissionAbortResult/ConversationBusy`; Models service follows the conversation's `pi.agent` document (`SyncConfiguration`); Transcript serves the durable view (`ConversationView`, stand-in types in `services/conversation_view.go`); worker services take `Harness` + `Conversation`.
- Worker (`session_worker_process.go`): opens the Harness by storage path, activity from the task graph (`TaskGraphActivity`), `Resume()`, no `fault` hook; lock stays on the Session directory.
- `client_run.go` prompts through `WaitForPrompt`; `commands.go` path in `cmd/pig/main_experimental.go` prints the answer. `client_tui_chat.go`, `client_tui.go` rewritten for the view (live/inbox/agent docs, abort status).
- Tests ported to the 1.0.0 cases over `internal/experimental/durabletest` (stand-in for pi-durable's MemoryStorage Harness + faux provider; file-backed `OpenFile`): agent-controller (3), transcript-provider, plugin-reload, client-tui (3 rows), remote-runtime prompt cases and duplicate Session ID, session-worker lifecycle and manager (earlier commit), worker task-graph retention. Services package is green except Node-oracle tests that also fail on the base (node lacks `--experimental-transform-types`).

### State at the last push (`34e22359d`)
- Merged into this branch: d-foundation, d-storage, d-session, chord-100, d-harness-a, d-harness-b, d-env-tools (rerere + `/tmp/exp/mergejson.py`-style per-entry merge for `test-mapping-v1.0.0.json`). `team/smc1/d-harness-c` conflicts with d-harness-b in `durable/harness/{stubs,task_graph,tool}.go` and `harness_task_graph_test.go` (their lanes' files), so it is not merged.
- The services, worker, client TUI and remote-runtime tests now run on the real `durable/harness` Harness: `internal/experimental/durableadapter.Session` implements `services.AgentHarness`, `AgentConversation`, `ModelsServiceConversation`, `TranscriptConversation`, `AgentDocument` (via `DocumentStateErased(AgentDoc)`) and `TaskGraphActivity`; `services.ConversationView` is `harness.ConversationView`; `durabletest` opens `OpenHarness` over `storage.NewMemoryStorage` or `sqlitenode.OpenNodeSqliteStorage` with the faux provider.
- `go test ./internal/experimental/...` is green (the Node-oracle tests now pass too); `-race` clean for the worker/client/remote-runtime tests; `golangci-lint` clean. `make test-porting-release` fails only on the chord-100 and 2860 owner-approval rows.
- Test mapping: agent-controller, transcript-provider, plugin-reload and client-tui are `ported`; remote-runtime stays `partial` (production `openCodingHarness`).

### Open: production `openCodingHarness` (the only blocker to `session-worker.ts` ported)
`createCodingAgentHarness` still ends in `openCodingHarness`, a package var that returns `errDurableHarnessUnavailable` (the test entry installs `openStandInCodingHarness`, which uses the real Harness and SQLite). Writing the real one needs:
1. Resolved on staging `xd-models-iface` (see `docs/plan/progress/xd-models-iface/PROGRESS.md`): `HarnessOptions.Models` is `durable.Models` and `*coding.ModelRuntime` satisfies it; merge that branch and pass `Models: collaborators.modelRuntime`. Original note: `harness.HarnessOptions.Models` is the concrete `*ai.Models`, but Pi passes the ModelRuntime (`models: modelRuntime`), and `coding.ModelRuntime` exposes no `*ai.Models`. Proposal for the harness lane: make `Models` an interface with `GetModel`, `StreamSimple`, `CompleteSimple`, `FetchDeferred` (what `generation.go`/`compaction.go` call), which `coding.ModelRuntime` satisfies (the removed `workerHarnessModels` in git history passed request options through).
2. A registry with `tools.CodingTools` (exists in `durable/tools`) and the pi system prompt (`experimental/durable/prompt.ts` `createPiPrompt`), `HarnessSettings` from the settings manager (`createHarnessSettings`), HTTP setup (`configureHarnessHttp`), `ExecutionEnvs` over `durable/env` `NodeExecutionEnv` keyed by cwd (cleanup after Close), `Root` with the initial model only when the root is new (`findInitialAgentModel`, ported, tested, no production caller yet).
3. d-harness-c's tool task graph (stubs in `durable/harness` today) for tools to run.

### Next (split by the lead)
- xd-models-iface: `HarnessOptions.Models` as an interface the ModelRuntime satisfies.
- xd-coding-harness: production `openCodingHarness` (item 1 below), then flip `remote-runtime` and `session-worker.ts`, and the demo presentations.
- xd-transcript-bridge: exact-frame Transcript bridge (item 2).
- xd-harness-retire: delete `agent/harness/**` and the harness tools, R1 (item 3).

Original list:
1. Resolve the open item above once the harness lane exposes the Models interface; then drop the `openCodingHarness` var and `openStandInCodingHarness`, flip `remote-runtime` and `session-worker.ts` rows, port `experimental/durable/*` and `vacation/*` (or record them).
2. Done (lane xd-transcript-bridge): `durableadapter.Session.ViewState` attaches an `internal/chord` state to `viewFrames`, a `ReplicatedStateSource` fed by `Conversation.Watch` exact frames; each revision publishes its own operations and the strict JSON value advances by them. Tests: `durableadapter/view_state_test.go#TestViewState`, and the transcript-provider port asserts the consumer's views are the watch frames.
3. Done in lane `xd-harness-retire`: `agent/harness/**`, `agent/search`, `internal/codingagent/tools/harness_*.go` and their callers are deleted (the agg-100 R1 divergence-guard hits and invalid `// upstream:` markers went with them; `make divergence-guard` is green). The `pico3` aliases the experimental services used are `internal/chord`. UUIDv7 is `ai.UUIDv7` (ai/uuid.go, uuid.test.ts ported), `combineUsage` is `internal/usagetotals`, the file-operation code follows `compaction/utils.ts`, and the skill listing follows `core/skills.ts`.
4. Ledgers updated: PORT_MAP, test-mapping (uuid, harness-submissions), upstream-sync v1.0.0 (112 removed rows designed-out), unit evidence, generated inventories and coverage. `make generate` stops at `known-gaps` on the chord-100 and 2860 owner-approval rows; the later steps were run by hand.
5. Not ported (recorded): `source-resolver.ts`/`process.ts` file-URL import (Node-only).

## Decisions
- Seams are Go interfaces owned by `services`; `durableadapter.Session` is their only implementation.
- Two Chord state implementations coexist: the old `internal/chord` (service host, used by `services`) and the new top-level `chord` (publication-only state used by `durable`); `durableadapter` bridges them. Converging them is the chord and durable leads' decision.
- `internal/experimental/durabletest` is the test helper over the real Harness (no longer a stand-in).
- Commits are SSH-signed (`git -c gpg.format=ssh commit -s -S` with the GitHub signing key).
