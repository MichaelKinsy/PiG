<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# D77 narrowing: ordered Node admission and culprit-only recovery

This follow-up implements the owner-directed `D77-NARROWING.md` contract on `fin-runtime-surface`, after `f04db0b7c3`. Completed loader predecessor `749215aeab` is already included. D77 remained `SCRUTINIZED:pending` during implementation; the owner approval is recorded below. The current ledger has no D72 section; the existing crash-supervision record is D56, so this change updates D20/D56 rather than inventing a D72 approval. The integrator was notified of that discrepancy through Herdr.

## Observable contract

Pi 0.87.1 loads factories serially in configured order (`packages/coding-agent/src/core/extensions/loader.ts:599-642`) and supplies one event bus from the resource loader. Its bus passes the original JavaScript object, calls synchronous listener bodies before returning, and returns an unsubscribe function (`core/event-bus.ts:11-31`). Pi has no separate extension-process crash recovery; the owner-directed D20/D56 policy governs that boundary.

- `[Node A, Go B, Node C]` invokes factories A, B, C while A and C occupy one Node process and share the actual Pi event bus.
- The host sends each Node member's name over the process's private stdin admission channel. The name must match the next immutable manifest entry. Every member retains its own socket and ordinary registration handshake. This is not a second extension registration plane or a versioned wire format.
- Host action slots remain unbound until the complete factory transaction finishes and the registry is committed. A's successful registration does not permit C's factory to call A's host actions through the bus.
- Preparation remains concurrent. Admission and registration completion remain ordered. A blocked Node factory cannot hold a native preparation slot needed by the next configured native factory.
- A crash attributable to the admitted factory, the runtime stack, or the last dispatched owner quarantines only that member. Healthy members restart together.
- An unknown crash restarts the whole group once. Recurrence bisects diagnostic groups. Identifying a singleton culprit rejoins all healthy members. An explicit reload also ends inconclusive diagnostic partitions while preserving known culprit quarantine.
- Recovery reruns factories but never replays an interrupted tool or callback. A new named capability invocation can find the replacement; an already-admitted callback stays bound to its dead connection. Old-generation frames and queued host calls are rejected.
- Parentless timer host calls still belong to the connection generation. Disconnect cancels them. Admission/recovery inherits original owner cancellation, and Host shutdown cancels and drains startup, recovery and process watchers.
- Node's host-copy stderr writer keeps its log descriptor until `Cmd.Wait` drains output. The previous registration-scoped close discarded runtime stack evidence. Stack attribution reads only the existing bounded 64 KiB tail.
- Restart and quarantine notices use the existing crash handler. Reload reports describe actual processes, not individual admission steps, and include culprit quarantine.

D77 now covers only explicitly isolated Node extensions (`isolation: strict`) and exact standalones. Ordinary interleaving is not a reason to split a Node bus. Diagnostic recovery is documented under D20/D56, not presented as an ordinary placement exception.

## Red and mutation evidence

Artifacts are retained at `/tmp/d77-logs/` and `/tmp/d77-baseline/` on this machine.

| Guard | Before / mutation failure | Current obligation |
|---|---|---|
| `TestNodeCellInterleavedGoFactoryKeepsOrderAndBus` | Distinct A/C PIDs and `{"a":true}` rather than `{"a":true,"c":true}`, at startup and reload. | Compiled Go middle factory, exact A/B/C side effects, one A/C process, shared mutable bus payload, and complete/coalesced reload report. |
| `TestModelStreamingSDKsMatch/subprocess-node-packed` | Deferred Ready overwrites an already-delivered model catalog with the earlier registration snapshot; `find current = undefined`. | Refresh the model/state snapshot at activation, after all factories finish. |
| `TestFooterOwnershipChangesBeforeFrameInvalidation` | Scenario 15 exposes both custom and built-in footers after reload. A synchronous invalidation probe sees the old suppression state at paint. | Set footer ownership before publishing the frame; the existing exact-output comparator remains unchanged. |
| `TestNodeAdmissionDoesNotBindActionsBeforeFactoriesFinish` | C's factory can call A's `getActiveTools` after A's early registration, unlike Pi's still-throwing runtime action slots. | Defer Node readiness until every factory has finished; reload/recovery commit the registry before activating actions. The paired scenario also exercises this boundary. |
| `TestNodeCrashQuarantinesOnlyCulpritAndDoesNotReplay` | No automatic recovery after a member exits during its tool call. | Only culprit has a separate PID; healthy bus reaches both siblings; the original named tool remains usable for a new call; the interrupted call is recorded once; the old event callback cannot execute in the replacement; explicit reload preserves the healthy group and reports quarantine. |
| `TestNodeUnattributableCrashRestartsWholeGroup` | No automatic recovery after external process death. | First unknown death restores all members on one shared bus. |
| `TestNodeRepeatedUnknownCrashBisectsAndRejoinsHealthyMembers` | Initial implementation lost the source hash while reconstructing members, changing the group key and resetting recurrence detection. | Preserve source identity and the complete normalized config, including SDK/group metadata; process counts follow the prescribed whole-group retry, halves, narrower halves, then culprit plus reunited healthy group. |
| `TestNodeFactoryCrashKeepsHealthyMembersTogether` | An abrupt factory exit takes later members down. | Attribute the admitted factory, report its interrupted load, and recover healthy members together. |
| `TestNodeTimerCrashUsesRuntimeStackAttribution` | Added after the log-lifetime regression; not separately red-proven. | A timer throws without a host dispatch; its persisted stack identifies the culprit and healthy members retain their bus. |
| `TestNodeRecoveryKeepsOriginalOwnerCancellation` | The first recovery implementation outlived the original owner's cancellation. | Recovered processes inherit the original owner rather than only a background Host context. |
| `TestNodeRecoveryShutdownCancelsBlockedFactory`, `TestNodeShutdownCancelsInitialAdmission` | Added with the lifetime separation; not claimed red against the old implementation. | Shutdown cancels blocked admission/recovery and joins owned process watchers. |
| `TestNodeStaleRecoveryDoesNotReplaceReloadedGeneration` | Added with generation ownership. | A late old-process crash and old-member cleanup cannot remove the explicitly reloaded process. |
| `TestUnparentedHostCallEndsWithConnectionGeneration` | Timer-owned calls remain uncancelled after disconnect, and a late call is admitted. | Every host call, including an unparented one, ends with its connection. |
| `TestOldNodeGenerationCannotPublishFramesOrStartHostCalls` | A compiling Go overlay changes the generation predicate to true; both stale callback and widget assertions fail. | Both production host-call execution and incoming frame dispatch reject obsolete generations. |
| `TestNodeRuntimeStderrLogOutlivesRegistration` | Strict and packed runtime log markers never reach the log after registration closes its descriptor. | Preserve runtime diagnostics until process output drains. |
| `52-interleaved-node-admission` | A compiling `f04db0b7c3` overlay produces `order="ABC" shared={"a":true}` while Pi produces both A and C. | Three complete `output_equal` pairs compare order and synchronous reference sharing. The middle factory is Python, providing a second native transport alongside the Go production test. |

No comparator, timeout or failure policy is weakened. Existing launcher tests now supply the mandatory host admission messages. The raw nested-call fixture now registers its claimed parent request as outstanding, so it exercises a valid nested call rather than relying on executing an already-cancelled context. The old planner assertion requiring separate A/C processes is replaced by the owner-requested shared-process assertion; the real factory-order test still checks the original ordering obligation. Native Go/Rust/Python packed-cell quarantine tests retain their existing policy.

The footer finding changes `internal/codingagent/ext_ui_context.go` as a source-level cross-family fix. The published frame's invalidation callback paints synchronously, so updating suppression afterward was too late. It is not a renderer workaround or a comparator delay.

The first Go scenario fixture inherited the repository's module closure, causing cache-root hashing to walk concurrently generated Rust object files under `tmp/test-fixtures`. That exposed an unrelated workspace-hash traversal warning. The canonical scenario uses a self-contained Python fixture instead of coupling its source to the repository build tree; its exact comparator still fails the old Node split. The compiled Go unit fixture retains its own temporary module. The general workspace-hash issue was reported to the integrator, not normalized away or claimed fixed here.

## Startup cost

`BenchmarkNodeAdmissionStartup` uses warm immutable artifacts, creates a fresh Host for each sample, waits for registration, and shuts the Host down. The numbers therefore include orderly teardown as well as startup. The Go middle artifact is prebuilt before measurement. The baseline is `f04db0b7c3` through a Go overlay; its actual old embedded cell loader is used, not a simulated sleep. Both runs use Go 1.27.1 and Node 24.19.0 on the same shared Linux host. Three samples per row establish cost, not a statistically significant speed claim.

| Composition | Before | After | Before allocations | After allocations |
|---|---:|---:|---:|---:|
| one Node | 539.4 ms | 578.7 ms | 148,421 B / 515 allocs | 154,093 B / 545 allocs |
| three contiguous Node | 530.3 ms | 621.4 ms | 186,805 B / 815 allocs | 179,314 B / 857 allocs |
| Node / Go / Node | 1,222.4 ms | 609.8 ms | 752,784 B / 2,177 allocs | 604,501 B / 1,957 allocs |

Sequential member admission adds handshake scheduling cost for contiguous Node members. Interleaved Node members avoid a second Node runtime. CPU and allocation profiles are retained as `admission-accepted.cpu`, `admission-accepted.mem`, `cpu-accepted-top.log`, and `mem-accepted-top.log`. These samples include the final lifetime, configuration-identity and stderr guards.

## Lifetime and diagnostics

The Host serializes load, explicit reload and recovery transitions. Startup admission has a separately cancellable context; a successful registration's process lifetime is not cancelled when staging returns. Recovery lifetimes hold references for their processes, preserve the original owner, and detach Host cancellation when the last process releases them. Shutdown waits for watcher completion after releasing the transition lock, preventing both leaked work and a lock inversion.

Each crash invalidates the old process generation before replacement can register. Old connections are closed and their UI/provider state is removed. Recovery publishes the replacement set after staging completes. There is no tool/callback retry queue. The per-member admission channel is closed after all manifest members are admitted, and the Node process joins its live member runtimes.

## Verification

Focused admission, recovery, bisection, stack attribution, cancellation, stale-generation and log-lifetime tests pass under `-race`. The mixed-native scenario is red-proven against a compiling baseline and green against Pi. Final touched-package tests pass: subprocess 264.511 s, cmd/pig 236.991 s, codingagent 17.545 s, and SDK conformance 34.658 s. The full extensions-runtime family and the cross-family footer scenarios pass with declared durability. Linux/Windows vet, `golangci-lint config verify`, changed/full lint, `go fix -diff`, generated Go interface checks, and `ci-contracts` pass. The other drift gates pass independently.

D77 deliberately remained pending during implementation. The divergence-quality gate and the existing closure assertions rejected that unapproved record. No approval was inferred from implementing the narrowing.

## Owner approval — 2026-09-27

Owner Michael Kinsy approves D77 only for explicitly isolated extensions (`isolation: strict`) and exact standalones having their own `pi.events` bus. Ordinary Node factories share Pi's bus. Crash recovery remains governed by D20/D56. The approval adds no runtime warning and does not change D78.

`docs/parity/DIVERGENCES.md` records the dated ratification and `SCRUTINIZED:approved`. The embedded divergence summary and extension API matrix reflect that decision. On the runtime follow-up branch at `9f7b880f60`, before integration with the separate D78 implementation-gap record, `make ci-drift` and the three previously failing divergence-closure checks pass: `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit`. The full `go test ./test/parity/closure ./internal/pigdocs -count=1` suite also passes. Approval verification logs are retained under `/tmp/d77-approval/`.
