# Session rename refresh completion

Pi 0.87.1 `packages/coding-agent/src/modes/interactive/components/session-selector.ts:917-939` awaits the rename and refresh before the `finally` block exits rename mode. Progress is not refresh completion. The panel remains mounted until the result is applied, including a failed refresh.

Each rename refresh retains its completion on the load. The selector owner applies active results before invoking completion. Cancelling an active scope retains a rename continuation separately until that load produces its result. This preserves the first confirmation's `finally` when a second confirmation supersedes its refresh. An abort request alone does not settle the load.

The existing loader worker signals readiness after storing its result. Readiness signals coalesce; result values remain buffered. Both startup and interactive owners drain completed continuations. The change adds no goroutine or timer. Retained continuation state scales with outstanding cancelled rename operations, not Session history. Closing the selector cancels active work, joins owned workers, and discards remaining UI continuations.

`confirmRename` uses the existing JavaScript trim implementation. A BOM-only name remains empty and keeps the panel open. A NEXT LINE character is a valid nonempty name, matching JavaScript rather than Go whitespace rules.

## Regression evidence

- `TestSessionRenameWaitsForRefreshedSessions` fails on the reviewed source before the fix in current/all scopes and success/error cases. It also checks progress before terminal completion.
- `TestPairReviewCancelledRenameRefreshRunsFinally` retains the independently supplied pair-review regression unchanged.
- `TestCancelledRenameRefreshWaitsForActualSettlement` separates abort from actual settlement.
- `TestCancelledRenameRefreshWakesOwnerLoops` exercises both real owner loops while the replacement refresh remains pending.
- `TestClosedSelectorDropsRenameRefreshContinuation` checks disposal.
- `TestSessionRenameUsesJavaScriptTrim` was red for both BOM and NEXT LINE.
- Existing rename input-state tests drain their already-resolved refresh Promise before checking the post-await state. Held-refresh guards do not drain a fabricated result or weaken their assertions.

Compiling mutation overlays that restore immediate exit, discard cancelled continuations, run completion on abort, or omit readiness signaling each fail the corresponding guard. A direct pinned-Pi prototype probe confirms that the first refresh settlement runs the first confirmation's `finally` while the second refresh is still pending.

Run the focused guards with:

```sh
go test ./internal/codingagent -race -count=1 -run 'Test(SessionRename|SessionSelectorRename|PairReviewCancelledRenameRefreshRunsFinally|CancelledRenameRefresh|ClosedSelectorDropsRename|SessionSelectorLoadCancellationAndFailure|SessionSelectorAsyncOwnerLoops|UpstreamSessionSelectorAsyncScopes|UpstreamSessionSelectorRename|SessionSelectorProgress|SessionSelectorCompletedLoadSignal|SessionSelectorListingAdapterCancellation|SessionSelectorScopeInputDoesNotAwaitLoader|StartupPrompt)'
```

The targeted tests, owning-package lint and vet pass. The retained 64-byte-name construction/rename/refresh benchmark measured 38.14 microseconds, 13711 bytes and 222 allocations per iteration in one sample. CPU/allocation profiles are retained with the review evidence. No performance improvement is claimed. Deletion refresh and status-expiry findings remain separate.
