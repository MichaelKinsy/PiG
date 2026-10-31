# kfold-kref: the reference side of `chord/next/host` (Pi `e7a0df27` -> `6d4b7443`)

## (a) upstream src files
path | disposition | PiG files | evidence | rationale
--- | --- | --- | --- | ---
host/reference.ts | ported | host_reference.go | test:chord/next/host/host_reference_test.go#TestKeyedStateMembersTwoConsumersOfOneKeyEachHoldTheirOwnPinReferencesOwnTheirKeysEachReleasedOnceWithItsReference | `target` final (`followed`, `attachable`, `instance`, `keyedMapping`, `pinKey(version)`, `call(version)`, `retry(walk)`, no `heldOnLink`/`member`/`lastValues`); `followed`/`followedKind`, `retryWalk`/`newRetryWalk(hops)`, `contractRef`, `binding.bind/retarget/removed/keepAll/memberSource/none/keyedMapping/pinKey/firstValues`, `place`; `retryOperation` deleted
host/reference-source.ts | ported | host_reference_source.go (`referenceSource`) | test:host_reference_source_test.go#TestHostReferenceSource* and TestReferenceSource* (own unit tests) | `Source` subclass as `memberSource` was; states unbound/bound/held/failed/ended; `fail/hold/vanish/end/bind`
host/reference-keys.ts | ported | host_reference_keys.go (`referenceKeys`) | test:host_reference_keys_test.go#TestReferenceKeys* | `hold` blocks outside a section (like the old `pinKey`); pins are goroutines of the loop settling a `keyedPromise` in a section
host/members.ts | unchanged-observable | host_members.go | comment only (+ `contributionOf` done in wave 1) | `Method` export has no Go counterpart (Go uses `contracts.MethodFunction`)
host/follow.ts | unchanged-observable | host_follow.go | comment only | -
host/ref-state.ts | unchanged-observable | host_ref_state.go | test:host_ref_state_test.go (changed case: last value readable while the hold is failed) | -
host/member-source.ts | designed-out (deleted upstream) | host_member_source.go removed | - | replaced by `referenceSource`
host/keyed-members.ts | designed-out (deleted upstream) | host_keyed_members.go removed; `keyedPromise`/`keyedMap` (Go-only) moved verbatim to host_promise.go; `pinOn`/`pinned`/`pinnedKey` are KInst's (attempt) | - | replaced by `referenceKeys`

## (c) upstream test cases
upstream test file › case | disposition | PiG test
--- | --- | ---
reference-source.test.ts (all 22 cases, incl. 5 new ones) | ported | host_reference_source_test.go#TestHostReferenceSource*; plus own unit tests TestReferenceSource* over `referenceSource` directly
reference-keys.test.ts (all cases incl. "a released key leaves its document", 2 "keys across a restart and a failed re-pin") | ported | host_reference_keys_test.go#TestReferenceKeys*; keyedPromise/keyedMap own tests kept there
reference.test.ts › two consumers of one key each hold their own pin | ported | host_reference_test.go
reference.test.ts › a binding released while it binds (fix round 5) | ported with forced difference | host_reference_test.go#TestABindingReleasedWhileItBindsFixRound5... (see below)
ref-state.test.ts › variant ... failed: last value readable | ported | host_ref_state_test.go

## (d) async-contract rows
- `referenceKeys.hold` (reference-keys.ts `hold`: `await awaitWithContext(#pin(entry), context)`): blocking Go call outside a section; the caller's ctx ends only its own wait (`keyedPromise.thenWithContext`, then `parking.Park`); a failed wait releases the hold once. Evidence: TestReferenceKeys* (first-pin cases).
- `referenceKeys.pin` (`#pin`): the pin is a goroutine (`target.pinKey` blocks) that settles `loading` inside a loop section; its success/failure handler is Pi's `.then` pair. Evidence: TestReferenceKeysKeysAcrossARestartAndAFailedRePin*.
- `binding.watch`/`firstValues`/`hydrated`: as before, blocking with `loop.Run` per section; `firstValues` waits through failures (Pi `#until(live)`).

## (e) new exported identifiers
none (everything new is unexported: `referenceSource`, `referenceKeys`, `followed`, `attachable`, `retryWalk`, `contractRef`).

## Forced differences
- Pi's `void cause.error.stack` (force the stack text so a kept error holds no frames) has no Go counterpart: Go errors keep no frames.
- "a binding released while it binds" spies `ChordObject.prototype.hold`; Go cannot wrap a method, so the test binds a real `binding` to a target whose `hold` releases it (same call sequence inside `bind`; GC checked with a weak pointer).
- the scenario-20 pin delay spies `ChordObject.prototype.pinKey`; the Go test delays the pin where it really waits (store read gate), as before.

## More forced differences (Go test timing; Pi's microtasks have no Go counterpart)
- Pi's `failLive(...); ref.retry()` is one synchronous block: the failed object's stop of following (a microtask there, a loop task here, `awaitTask`) cannot land between them. Go tests run the pair as one section: `failLiveThen` (host_reference_support_test.go), `msFailRetry`.
- Where Pi has `await delivered()` after a failure, the stop of following and its `failed` call are tasks of the loop: tests wait for the call with `awaitWatchCalls` (poll) before the next step, and for pins landing on loop tasks (`referenceKeys.pin`) with `scopeEventually`. Failure calls of watches opened while failed are delivered (`deliveredOn`) before the next value, as Pi's awaits give them the turn (the watch coalesces a failure superseded by a value).
- reference-keys.test.ts "a key's re-pin on a new instance that fails to load...": Pi replaces `slot.persistence.read` once to throw. A Go `Persistence` is concrete over the store: a backend error stops the store and closes the host, and a stored version newer than the code's breaks the owner object. The case is ported at `referenceKeys` level (fake target whose `pinKey` fails once: last value kept, one failed call, the next instance's pin heals it with one frame) in host_reference_keys_test.go.
- host_members_test.go own tests: a reference's member no longer throws when resolved (Pi: `member()` makes a source), its reads fail; the registry-cycle refusal at `contributionOf` was removed upstream (`wantMembersResolved`).
- host_follow_test.go: the test struct `followed` renamed `followedRuntime` (the production type `followed` is the reference's).

## Commands run
- `/tmp/kfold/kreload.sh test -race -count=5 -run 'TestHostReferenceSource|TestReferenceSource|TestReferenceKeys|TestKeyedState|TestABindingReleased|TestKeyedPromise|TestKeyedMap|TestRefState|TestFollow|TestMembers' ./chord/next/host/` ok (scratch copy with the other agents' `Load(` call sites patched).
