# Session selector scope loading

Reference: Pi 0.87.1 `packages/coding-agent/src/modes/interactive/components/session-selector.ts`, `loadScope`, `toggleScope`, `cancelLoads`, and `SessionList.setSessions`.

A scope change updates the selected scope before it starts a load. Each scope owns at most one active load and one cache. Revisiting an active scope uses its partial cache without starting another load. A completion updates its own cache but does not change the selected scope. A successful empty result remains cached. A visible rejection clears the rows and reports the loader error with the `Failed to load sessions: ` prefix.

The loader begins on the input owner and returns one retained result. This preserves progress published before the upstream Promise returns. Native file discovery runs in an owned worker. `SessionListOptions` carries its cancellation context and progress callback. The adapter copies each published snapshot before it queues progress to the input owner. The startup and interactive selector loops apply progress and completion independently of keyboard input. They continue to process terminal events and the interactive owner's queued tasks. Progressive snapshots retain a user-touched selection by path.

Refresh cancels both active loads and clears both caches. Progress from a replaced load has no effect. Selection, cancellation, terminal failure, and owner shutdown cancel active work. Completed loaders retain an un-aborted signal, as Pi removes their controllers before `cancelLoads`; those contexts own no timer or parent subscription. Teardown releases blocked progress delivery and joins every worker. Native SessionManager listing observes cancellation during discovery and joins its file-summary workers. A loader may reuse its snapshot slice after the progress callback returns; queued UI work owns a copy.

A successful deletion removes its path from both cached lists and publishes the updated visible rows before refresh begins. The query and selection rules remain unchanged: surviving rows stay selectable, and a deleted singleton leaves no resumable row. Failed deletion leaves the list intact and starts no refresh.

This change classifies as required substrate: it preserves the existing Pi Session selector and does not activate a product workflow. It adds no extension capability or wire format. The pre-existing deletion and rename handlers are separate from scope loading. This evidence does not claim whole-component async closure.

## Evidence

- `internal/codingagent/session_selector_async_test.go` ports both original asynchronous cases from `session-selector-path-delete.test.ts:187,219` and guards cancellation, replacement, empty-cache retention, and rejection.
- `internal/codingagent/session_selector_async_upstream_test.go` guards the original blocked-input defect.
- `internal/codingagent/session_selector_progress_test.go` guards native listing cancellation, snapshot ownership, and selection retention.
- `internal/codingagent/session_selector_owner_test.go` drives the startup and interactive owner loops with input, pending progress, and late completion.
- `test/parity/scenarios/session/32-session-selector-async-scopes.toml` compares the exact original scope outcomes against the published Pi 0.87.1 component without normalization.
- `internal/codingagent/session_selector_delete_refresh_test.go` holds refresh unresolved and guards Current/All rows, singleton and filtered emptiness, touched selection, child reparenting, failure, and loader snapshot ownership.
- `internal/codingagent/session_selector_delete_refresh_owner_test.go` drives Enter through both real input owners during the unresolved refresh and rejects resuming the deleted path.
- `test/parity/scenarios/session/33-session-selector-delete-refresh.toml` compares the complete observed row/selection states against Pi's actual deletion callback without normalization.
- `BenchmarkSessionSelectorScopeLoading` measures native discovery, progress, rendering, selection, and joined teardown for 10 and 1000 Session files. It makes no speedup claim.

The synchronous test fixtures now use immediately resolved results and drain them after construction. This corresponds to upstream `flushPromises`; it does not make production listing synchronous. Tests that rename a Session drain the reload before making the next direct selection assertion.
