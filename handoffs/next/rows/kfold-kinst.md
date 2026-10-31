# KFold.KInst rows: the instance/provider side (Pi `host/{object,attempt,adapted,dependencies,routines,unbound}.ts` 6d4b7443)

Status: production ported and building. Tests: everything listed "run" below was run on xd670 through
`/tmp/kfold/kreload.sh test` (a scratch copy where `jobs_records_test.go`, KJobs', was removed because it did not compile),
green; the regression selection of the existing tests of my area (`TestObject*|TestAttempt*|TestHostRevoke*|TestOnLive*|
TestRoutines*|TestParked*|TestTheDependency*|TestWaitingOn*|TestTheIndex*|...`) was green except the case fixed below;
`-race -count=3` on the new and changed cases is green. PENDING: most of `adapted.test.ts` (see (c)), one race of KRef's code
(below). Nothing was skipped or weakened.

## (a) upstream src files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| host/object.ts | ported | host_object.go | test:host_object_reload_test.go#TestReloadFates*, #TestReloadThePureSwap*, #TestReloadAStuckRoutineCall*, #TestReloadGC*, #TestReloadAParkedAttemptsHandover*, #TestReloadSwapsOneAfterAnother*, #TestReloadAReferenceDetaching*, #TestALiveFailure*, #TestANativeRestart*, #TestObjectParkedWalksADiamond* | Holder (`contract`, `retarget`, `following`, `removed`), `removal`, `reloadFate`, `swap` (gate, `done`), `retire`, `restart`, `retryReloaded`, `remove`, `reload`, `followed`, `attachable`, `instance`, `liveMember`, `keyedMapping`, `pinKey`, `binds`, `checkJob`, `jobRecords` redefine, failLive/`stopFollowing` (A10), `parked`/`unparked` with swap, `ending` (end/removed), retry by `retryWalk.visited`. `Promise.all` is `promiseAll2` / `promiseAllVoid`. The retry id set (`retried`), the member sources and the keyed members of the object are removed with their Pi code. |
| host/attempt.ts | ported | host_attempt.go | same tests | `memberAt`, `keyMapping`, `pinKey` (blocking; `pinOwn` is Pi's `pinned`), `dropVersion`, `adaptedAt`, `attachedVariant`, `handOver`, `stopRoutines`, `handOverRoutines`, `drainCalls`, `startedByReload`, `reloadStarted`, `declaresJob` (by the names declared), `opensNatives`, `checked`/`settleCheck`, `seal(err, runs)`, `close` (handed-over jobs, routine calls, runs), `workSet`s (`running`, `routineRun.calls`, `runs`), `dependencies` in declaration order, `call(..., version)` (provider `checkArgs` at the consumer's version, `call` replacement, result mapped back), `park` (`settleCheck`, `parkedCause`, `blockedBy`), `pinKeys(long)`, `retryDependencies(walk)`; `prepareKeys` and the registry edges are gone. |
| host/adapted.ts | ported | host_adapted.go (new) | test:host_adapted_test.go#TestAnOlderConsumer* (4), #TestReloadAdaptedMembersFollowTheProvidersTopology... (1) | `refusal`, `adaptedMembers` (member, key, keyed, detach), `mappingsOf`, `stateMapping`. |
| host/dependencies.ts | ported | host_dependencies.go | test:host_dependencies_test.go#TestParkedLong* (7) | `blocker`, `check.blocker/loadingOn`, `parkedCause`, new `parkedLong` (clock from parking, any blocker, `state`). |
| host/routines.ts | ported | host_routines.go | n/a (no upstream test change) | `routines.empty()`. |
| host/unbound.ts | ported | host_unbound.go | test:host_unbound_test.go#TestTheIndexWakesTheRequestsOfTheNamesAReloadServesOnly... | `unboundIndex.wake(names)`. |
| host/revocation.ts, retry-on.ts, presence.ts, parked.ts | unchanged-observable | none | none | no Pi diff between e7a0df27 and 6d4b7443. |

## (c) upstream test cases

| upstream test file › case | disposition | PiG test |
|---|---|---|
| unbound.test.ts › wakes the requests of the names a reload serves only | ported | host_unbound_test.go#TestTheIndexWakesTheRequestsOfTheNamesAReloadServesOnlyCarryOverPublicationsWakePerName |
| attempt.test.ts › run no timer while parked but parked_long's | ported (edited) | host_attempt_test.go#TestAttemptParkedAttemptsRunNoTimerWhileParkedButParkedLongsNoDependencyDeadlineNoBackoffTheFactoryRunOnce |
| revocation.test.ts › ends a guest's reference over a route link that dropped | ported (edited: `Resolve` waits for the revocation once a link was made) | host_revocation_test.go#TestHostRevokeEndsAGuestsReferenceOverARouteLinkThatDroppedAndNothingHoldsItOnTheNextOne |
| dependencies.test.ts › parked_long (A11) × 7 (the 5 old parked_long cases replaced) | ported | host_dependencies_test.go#TestParkedLong* |
| dependencies.test.ts › parked_long › names a reference whose pinned key still loads (Sol) | ported, forced difference | Pi spies `ChordObject.prototype.pinKey`; Go has no hook: the pin is held where it really waits: a persisted key stored by an earlier host, its cold load held at the store (`readGateDatabase`, as reference-keys' delayed keyed pin) |
| object.test.ts › live failing › "lets a commit admitted before the failure adopt and publish once" (doubles frames now `[2, {failed: boom}]`) | ported (edited; waits for the failure frame, which an owned task settles) | host_documents_host_test.go#TestObjectALiveObjectFailingLetsACommitAdmittedBeforeTheFailureAdoptAndPublishOnceDerivesAndDependentsIncluded |
| object.test.ts › parked › walks a diamond on one host once (A5) | ported, forced difference | host_object_reload_test.go#TestObjectParkedWalksADiamondOnOneHostOnceASharedParkedDependencyPassesARetryOnOnceAudit5: Pi counts `Attempt.prototype.retryDependencies` calls (4); Go has no prototype spy, so the walk's visited set is asserted (the 5 objects reached, once each) and the outcome (cores 2, n 4) |
| object.test.ts › reload: fates × 6 | ported | host_object_reload_test.go#TestReloadFates* |
| object.test.ts › reload: the pure swap × 6 | ported | #TestReloadThePureSwap* |
| object.test.ts › reload: a stuck routine call past the handover | ported (scopeLoop for fake timers) | #TestReloadAStuckRoutineCall... |
| object.test.ts › reload: GC × 2 | ported with weak pointers (`instancesOf(Attempt/ChordObject)` spies become `weak.Make` taken in a section; the facet's code is the weak `facetIdentity`) | #TestReloadGC* |
| object.test.ts › reload: a parked attempt's handover × 2, swaps one after another, a reference detaching from a parked swap's old instance, a live failure: older version's mapping, a native restart: last values | ported | #TestReloadAParkedAttemptsHandover*, #TestReloadSwapsOneAfterAnother*, #TestReloadAReferenceDetaching*, #TestALiveFailure..., #TestANativeRestart... |
| adapted.test.ts › an older consumer of a provider's accepted version × 4 | ported | host_adapted_test.go#TestAnOlderConsumer* (the keyed case: see the race below) |
| adapted.test.ts › reload: adapted members follow the provider's topology (first case) | ported | host_adapted_test.go#TestReloadAdaptedMembersFollowTheProvidersTopology... |
| adapted.test.ts › reload: keyed members of an older version follow the provider × 2, keyed members unchanged at a consumer's version follow a later mapping × 2, keyed sources through a removal (first case), a version refusal keeps the waiting reference's last values (first case) | ported, run green except one: "re-maps a key of an older version its provider left unchanged once a later provider maps it" FAILS (the old open keyed watch stays at {n:1}; get and pin read {n:20}): reference-side (KRef told with the exact test) | host_adapted_test.go#TestReloadKeyed*, #TestReloadKeyedSourcesOfAnOlderVersionThroughARemoval..., #TestReloadAVersionRefusalKeepsTheWaitingReferencesLastValues... |
| adapted.test.ts › STILL NOT PORTED: "maps existing local watches when the consumer's current version becomes an accepted older one", "rebuilds an existing adapted member's dependencies when a later provider removes that member", "publish one coherent frame" × 2, "an older consumer across a removal", "adapted members' lifetime" (GC of `AdaptedMembers` per version), "keeps no removed instance's mappings once the reference moved", "heals a watch of the previous current version once a later provider accepts it", and every describe after line 816 of the file | PENDING (out of request budget; the lead asked for them: to be done next) | none yet |

## (d) async-contract rows

| source | contract | evidence |
|---|---|---|
| object.ts `call` (gate) | a call arriving while a swap runs waits for `swap.done` (the caller's ctx ends only that wait), then goes to the live attempt; the result is a `parkedCallResult` settled in the gate's continuation | host_object_reload_test.go#TestReloadThePureSwapLetsARunningCallFinish... |
| object.ts `#retire` / `#restart` / `#retryReloaded` | Promise chains as keyedPromise reactions; the steps that block (`Documents.settled`, `attempt.close`) are owned tasks (`awaitTask`) that settle in a section; `Promise.all` is `promiseAll2` (settled in the last reaction, a reaction on it one microtask later); errors: none reject | #TestReloadFatesRestarts..., #TestReloadAParkedAttemptsHandover* |
| attempt.ts `drainCalls` / `handOverRoutines` | called in a section: the list of work is taken then (Pi's sync part); `drained` runs on an owned task; an empty list settles at once; a timeout reports `drain_timeout`, the promise resolves either way | #TestReloadAStuckRoutineCall..., #TestReloadThePureSwapHandsItsRoutinesOver... |
| attempt.ts `checked` | lazily made promise of the first dependency check (a nil cause: it did not park), settled once by `settleCheck`: at the first parked check with `parkedCause`, `nil` when all live or the activation ended first | #TestReloadFatesParksASwap... |
| attempt.ts `pinKey` | blocking, outside a section (as the object's was) | reference tests (KRef) |

## (e) new exported identifiers

None: every new type is unexported (`reloadFate`, `removal`, `swap`, `adaptedMembers`, `blocker`, `workSet`, `pinnedKey`, ...).

## Open questions / forced differences

- `reloadFate` is lowercase per DESIGN §3 (not exported by Pi's `index.ts`).
- `retire`'s `Documents.settled` and `attempt.close` are owned tasks: their resumption against other sections is not tick-exact (Pi: microtasks). Tests wait for the effect (`scopeEventually`/`untilKind`) where Pi awaits `flushed()`.
- A factory run by a reload's staging gets a recording self: a test factory must not assert `self.(*attempt)` (it found out the hard way); handlers take the loop from their ctx.
- DATA RACE (not mine): `-race` on `TestAnOlderConsumerOfAProvidersAcceptedVersionReadsKeyedMembersMappedPerKeyGetWatchAndAFactorysAtKey` reports `referenceKeys.release` (run from `ReferenceKeyed.Watch`'s goroutine, outside a section) against `referenceKeys.end` (in a section): KRef was told.
