# Rows of N-DUR slice hidle (Pi 35dfa757 tests: harness-idle-scopes.test.ts at 6d4b7443)

## (c) Tests: test/harness-idle-scopes.test.ts (591 lines) -> durable/harness/harness_idle_scopes_test.go
- ported (all 23 cases): `TestIdleWaitersByScope` (3 cases), `TestIdleKeptContextRanges` (12: busy-again expiry, reconcile-pass expiry, idle order with clock back, retention zero, kept expiry on re-read, entry commit finds expired, entry commit cannot extend, shorter/longer/zero retention, zero-retention idle, sweep without full check),
  `TestIncrementalIdleStateEqualsAFullRecomputeAfterEveryStepOfRandomNestedWork` (200 steps, fixed PCG seed, fullIdleChecks 0), `TestWakeReadsSleepingTasksAndScansLiveOnesOnlyWhileNothingRuns` (7 table cases + running invocation).
- missing: none. designedOut: none.
- Go forms: the state is `TaskScheduler.idleState()`/`SchedulerIdleState` (scheduler_idle.go); entry-row counts are Pi's (5 and 0: no bounds probe row on this branch); no `keep` helper (Pi loads the chain in the creating commit; the earlier 1.4.0 port needed one).
- Go differences: Pi's `ControlledStorage.reopen` has no Go equivalent and closing a Harness closes its MemoryStorage, so the "chain not loaded yet" case leaves the first (never resumed) Harness unclosed, as the 060c port did; `quiet` waits for the scheduler's reconcile flags plus 2x50 ms instead of four microtask flushes; the random sequence uses math/rand/v2 PCG(42,42), not Pi's LCG, so the steps differ but the property checked is the same.
- Red record: none. The production port (scheduler_idle.go) was already on the base, so the cases were committed against ported code and passed at first run (one name clash `TestKeptContextRanges` with context_kept_range_test.go fixed by naming mine `TestIdleKeptContextRanges`).
- Run: `go test -race -count=1 -timeout 200s -run 'TestIdleWaitersByScope|TestIdleKeptContextRanges|TestIncrementalIdleState|TestWakeReadsSleeping' ./durable/harness/` -> ok (20.5s).
- Open failures: none.
