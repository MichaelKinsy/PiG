# Behavioral rigidity follow-up: BR20–BR28

## Status

BR25, BR26 and BR28 are fixed here. The active skill, extension-wire, and footer owners supply BR20–BR24 and BR27. The owned autocomplete scenarios now pass strict comparisons against Pi. No skip, normalization, divergence approval, or implementation stand-in closes these findings.

The audit commit `4b23fd195` is merged by `f616c1670`. Merge conflicts retain both changelog entries and regenerate coverage from the combined scenario set. No BR01–BR19 production code is changed by this follow-up.

| Finding | Disposition | Pi 0.87.1 contract and evidence |
|---|---|---|
| BR20 | Active skill owner fixes it in `13fdbfb46214f4006a8ff5cacadba9e2775a7ff4`; no duplicate loader edits here. | `packages/coding-agent/src/core/skills.ts:434-508` preserves insertion order. The owner records a red loader/publication regression, a compiling sort mutation, and three strict pairs in `extensions-runtime/38-discovered-skill-order`. |
| BR21 / W19 | Extension-wire owner fixes it in `cdd29705f15a7fac9dd926138307687252ca6483`; explicitly excluded from duplicate work. | `packages/coding-agent/src/main.ts:538-539,828` retains explicit tool order. The owner's complete-record scenarios 55–57 and tool-order regression accompany the commit. |
| BR22 / W13 | Same active extension-wire owner and commit. | `packages/coding-agent/src/core/extensions/runner.ts:837-884` supplies public context fields. The owner implements model projection, scoped models and idle-signal absence with cross-SDK evidence. |
| BR23 / W14 | Same active extension-wire owner and commit. | `packages/coding-agent/src/core/session-manager.ts:439-474,1288-1298` retains explicit null values. The owner adds persisted-null and complete-record guards. |
| BR24 / W20 | Same active extension-wire owner and commit. | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:151-161` omits an unspecified notification level. The owner removes the fabricated Node default. |
| BR25 | Fixed here: typed provider factories, real captured current providers, retained callback state, both editor paths, lifecycle, cancellation and all SDKs. | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:779-794,2562-2565`; `packages/tui/src/components/editor.ts:766-782,2284-2435`; `packages/tui/src/autocomplete.ts:294-386` govern construction, async queries and synchronous completion. `autocomplete/09`–`12` pass strict comparisons; `TestAutocompleteFactoriesAcrossSDKs` covers all transports and placements. |
| BR26 | Fixed for the audited named-theme lookup and synchronous named-selection defects. | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:2572-2583`; `theme/theme.ts:570-575,790-815` returns a loaded Theme or absence and the actual selection result, including the dark failure fallback. `selectors/13-extension-theme-results` compares the complete result dialog and style bytes. Cross-process selection of an authored Theme object remains the pre-existing separate partial API in the matrix. |
| BR27 | Active footer owner fixes it in `ec82a1f16066b6b3cb041cb6277f727def522d1e`; no duplicate watcher edits here. | `packages/coding-agent/src/core/footer-data-provider.ts:214-392` and `utils/fs-watch.ts:3` govern directory/reftable watches and lifecycle. The owner records all eight upstream cases, red native/idle-footer tests, a common-directory mutation, and `footer/08-reftable-branch-watch`. |
| BR28 | Fixed here. | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:2585,2623` exposes expansion before selector completion. `selectors/12-extension-dialog-remapping` is red before the fix and passes three exact escaped-output pairs after it. |

The external owner commits are inspected, not merged or re-certified here. The integration owner must merge and run them in the combined candidate.

## Root fixes

### Dialog state and receive ordering

`Host.runCall` previously published a current snapshot only after `setModel`. A dialog could change expansion without publishing it before returning its result. It now publishes state before the result of every awaited dialog, including dismissal and error payloads.

The Node frame reader also resolved asynchronous call results before the runtime consumed earlier queued notifications. Results now use the same ordered receive loop. Restricted synchronous operations still complete in the IO worker's synchronous pump. Already received results survive a subsequent socket close. Asynchronous cancellation follows earlier queued frames; synchronous cancellation still releases the pump.

`TestNodeDialogPublishesExpansionBeforeCompletion` is red before the fix in isolated and packed Node modes: `stale expansion after expand`. It covers expansion, collapse, dismissal and dialog failure. Removing dialog publication through a Go build overlay reproduces the same failure and makes both Node cases of `TestUIStateBarriersAcrossSDKs` report `expanded=false`.

`TestNodeCallResultWaitsForEarlierFrames` additionally rejects a received-result bypass with `call result overtook queued state`. Closing the connection after receipt initially rejected that delivered result; the test was red before preserving it. It also checks cancellation ordering and empty pending maps. The deterministic queue guard was added after the first fix and is mutation-proven.

The imported selector scenario used a retained cancel hint as a new-output barrier. Removing that hint from the barrier corrects the synchronization input; the final exact-output and result assertions remain unchanged. The initial valid pair reports:

```text
Pi:  {"selected":"chosen","expanded":true,"entered":"first-second"}
PiG: {"selected":"chosen","expanded":false,"entered":"first-second"}
```

### Named themes

`RuntimeUI.getTheme` previously returned theme-list metadata. The host lookup also returned a Go component instead of the portable palette used for the active extension theme. The host now returns the same resolved palette representation and returns absence for missing names. Node rehydrates the pinned Pi Theme prototype and maps, without selecting it. Its Chalk styles use the host's resolved capability rather than the subprocess pipe's color detection.

`RuntimeUI.setTheme` previously sent the wrong argument key (`name`, not the existing wire's `theme`) and immediately returned fabricated success. It now calls the host synchronously through the existing restricted pump, returns the real result, and reads the active palette before returning. The host omits the error field on success, as Pi does. No protocol version or second representation is introduced.

`TestExtensionThemeLookupUsesPortablePaletteAndAbsence` is red on the original `*tui.Theme` result. `TestNodeThemeCallsReturnHostResultsSynchronously` is red on the metadata stand-in. The real Pi scenario separately exposes fabricated selection success and stale fallback state. Replacing the host selection with constant success fails the synchronous-state guard. Hardening the loaded Theme's styles exposed another red pair (`bold:"x"` instead of the escaped bold string); the host-capability projection fixes it. Disabled styles have a separate assertion.

`TestUIStateBarriersAcrossSDKs` passes for the in-process reference, isolated Go/Node/Python/Rust, packed Go/Node/Python/Rust, and fused Go. It uses host values distinguishable from every fallback. UI-prompt event scheduling has its own conformance tests; this row compares the complete state report rather than unrelated unawaited prompt notifications.

## BR25 root fixes and red/green evidence

`autocomplete/09-extension-wrapper-state` registers a wrapper, asks its current provider for a real registered command, and accepts its overridden completion. No model request is made. Before the fix, the final dialog differs in every owned result:

```text
               Pi                 PiG
registered     true               false
base           true               false
applied        true               false
args           chosen             (empty)
```

The original factory is not constructed at registration, the supplied base returns null, and the Go editor applies its own completion instead of the extension's method. The old source also recreates the fold per query and drops file-trigger behavior. The replacement removes that additive adapter rather than appending more suggestions.

`extension.SetupAutocompleteProvider` folds factories over a fresh base and merges trigger characters in first-seen order. Go, Rust, Python and Node retain the resulting providers. Captured current-provider handles forward complete get/apply/trigger operations; they do not run a fabricated local substitute. The Node custom editor receives the same actual provider object when it shares a process with its producer. Reverse callbacks use the existing sockets. The restricted synchronous pump services sibling sockets in packed cells, without executing arbitrary asynchronous handlers reentrantly.

Queries run on owned workers and serialize after preceding queries. Directory enumeration and attachment search are captured as deferred work as well; the input loop does not enumerate the filesystem for a wrapper's current-provider call. `TestCombinedProviderDefersDirectoryQueriesAndCapturesInput` asserts the captured-input and cancellation boundary and rejects a compiling mutation that removes deferral. Natural triggers use Pi's 20 ms debounce. New text cancels old queries; a forced suggestion Promise does not hold typing. Synchronous trigger and completion callbacks hold the next input until their result applies on the owner loop. Pure virtual-time tests were red on plain-text queries, blocked forced-query input, and first-item selection instead of the best prefix. The corrected tests pass. `TestAutocompleteCompletionHoldsInputUntilOwnerApply` exercises the actual input ticket path.

Startup needed an owner-loop await phase for synchronous factory callbacks. That phase handles extension UI, but retains ordinary type-ahead until the final provider is installed. A built-in debug listener does not turn it into a general editor loop. The initial unqualified phase consumed text before factories were installed and then cancelled its popup during the final setup; the strict scenario exposed that failure. The final phase preserves the input rather than re-querying a fabricated state.

The former command-argument cache returned a synthetic first null and scheduled another query. `SlashCommand.AwaitArgumentCompletions` now carries the real awaited callback, and the captured query returns its result/error before a wrapper observes it. Scenario 11 latches the first result so a later answer cannot conceal a cache-miss stand-in. The synchronous Go callback form remains separate because Go cannot express TypeScript's value-or-Promise callback return as one function type.

A real Pi probe showed that rejected autocomplete queries terminate through `uncaughtException`, not a recoverable notification. The host now raises the error on the owner loop and retains the extension's supplied stack in its crash record. Cancellation remains quiet. A custom editor exposed an abort rejection during provider replacement; the current-provider proxy suppresses only a rejection whose supplied signal is aborted and still rejects real failures. Scenario 12 asserts the exact error headline and exit 1. Its stack frames are implementation provenance, not normalized acceptance output.

Lifetime ownership is explicit. Connection cancellation removes its factories. Reload removes the old generation. Query cancellation is reserved on the receive loop before another call lane can cancel it. Captured provider references use leases, weak host-origin keys, and release notifications; borrowed callbacks retain their lease. Disconnect clears remaining references. Queries do not add provider registrations. Tests cover foreign-owner rejection, cancellation before dispatch, cleanup, and owner-loop removal.

Compiling mutations are rejected: skipping the factory fold fails the owner-loop regression; pumping only one Node socket fails the packed-editor callback test; returning a null current result fails scenario 09; bypassing provider application fails scenario 09; replacing awaited arguments with null fails scenario 11; and a Go SDK no-op fails the fused conformance row. Removing cancellation handling fails the Node proxy regression, and removing directory deferral fails its captured-query guard. No compiler failure counts as mutation evidence. Scenarios 09 and 10 also assert retained provider state and shared custom-editor identity. A distinct second query supplies their new-output barrier; incidental resize queries cannot satisfy it.

## Performance and lifetime

`BenchmarkDialogStateSnapshot` serializes a representative current snapshot with the full active theme palette and no Session-log subscription. On Linux/amd64, Go 1.27.1 and an Intel Xeon 6746E, it measured approximately 68.9 µs/op, 16.2 KB/op and 506 allocations/op. This is a cost observation, not a speedup claim. CPU/allocation profiles identify palette construction, ANSI conversion and JSON copying. Snapshot construction remains off the TUI loop. Existing Session-log paging bounds subscribed history; no new history scan or subscription is added.

The autocomplete bridge adds no new process or transport. Query and input/factory workers belong to the mode lifetime. The existing Node IO workers also wake the shared restricted pump for sibling callbacks. `BenchmarkNodeAutocompleteRoundTrip` includes a real Node request, current-provider delegation and 32 returned items: approximately 514 µs/op, 13.2 KB/op and 157 allocations/op on the same worker. Captured-reference creation/release measured 705 ns/op, 32 B/op and one allocation. Profiles are retained; no speedup is claimed.

No new background worker, timer, transport, or queue is introduced by BR26/BR28. The existing receive queue additionally owns asynchronous results until dispatch. Connection close/cancellation releases undelivered requests, and delivered results are released when processed. Theme calls wait in the extension process while the IO worker services the connection; their host operations cannot call back into Node. Theme repaint uses the existing owner-loop path.

## Verification

Every probe and test uses a temporary `HOME`. Coding-agent directory overrides point into temporary roots or remain unset so the test's own temporary config root controls default-path checks. Go 1.27.1, Node 24.19.0, Python 3.12.3, Rust/Cargo 1.97.1 and tmux 3.7b are available. npm is 11.17.0 rather than the qualified 12.1.0; no dependency installation is needed. The initial temporary-home PATH exposed an unusable mise `uv` shim. Selecting the available native toolchain paths removes that environment failure; no test skip or production fallback is added. The TypeScript default-path fixture also correctly rejected the shell's explicit temporary coding-agent directory outside its own config root. Unsetting that override with `HOME` still isolated gives the fixture its intended default-path input.

The final Go SDK suite, Rust SDK suite and focused Go race regressions pass. Python runs through the complete cross-SDK conformance suite; its separate pytest suite is not claimed because pytest is unavailable in the checked interpreters and temporary installation approval is pending. The new owner-loop test fixture initially omitted the production render dispatcher, which let the TUI timer render concurrently; installing that dispatcher fixes the fixture and the focused race run passes without suppressions.

The cross-family slash-command run exposes one unrelated evidence limit: `05-changelog-ignores-collapse-setting` requires a release heading inside a fixed 300-row viewport, but the growing bundled changelog pushes every heading into history. Its history-based wait succeeds and its final viewport assertion fails. This is reported to the scenario/harness owner; its comparator and terminal size are not weakened here. All other slash-command scenarios pass.

The complete selectors family passes at declared durability (including both new/fixed three-pair scenarios). Full `go test` runs pass for `./coding/extension/host/subprocess`, `./internal/codingagent`, and `./tui`. The autocomplete family passes all twelve scenarios at declared durability, including the formerly red BR25 path, custom-editor sharing, awaited arguments and uncaught failure. Cross-family `36-model-registry-session-manager`, `53-node-independent-session`, `54-node-overlay-handle`, and `54-extension-context-usage-presence` pass at declared durability. Native and Windows vet, full repository lint, changed-file lint, focused race tests and cross-SDK state conformance pass. `go fix -diff` is empty. Repository contract gates retain the inherited pending/partial hot-path test release debt and unapproved D78. `go test ./test/parity/...` fails the three D78 closure/dashboard checks. The full `go test ./...` run reaches those same checks and a nested-module checksum check before the new SDK file is staged; the tracked-file checksum check passes after staging the complete SDK source. No full-repository green result is claimed while D78 remains open. One complete extension-conformance run exposes `TestProviderObjectsAcrossSDKs/go-reader-packed` failing with `Provider stream ended without a terminal event`; the failure remains in `verified-touched-tests.log` and its preserved copy `provider-creation-failure.log`. The later `conformance-all.log` run passes in 318.757 seconds. That pass does not close the race. The provider-object owner supplied the deterministic `synctest`/`net.Pipe` regression `TestProviderStreamResultDoesNotOvertakeNotifications` for `stream`, `streamSimple` and `fetchDeferred`. Its fix is consumed from `c2b46f1a6` as `7bc810bf7`; the source-compatible local SDK checksum is regenerated here. The minimal checkpoint changes no autocomplete or Go SDK protocol read-loop behavior. Twenty-five race repetitions pass after integration. The dedicated UI-state conformance row passes in every placement. No gate, denominator, comparator or approval is weakened.
