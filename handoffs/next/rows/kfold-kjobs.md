# KFold.KJobs rows (jobs: Pi e7a0df27 -> 6d4b7443)

## (a) upstream src files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| src/next/jobs/internal.ts | designed-out | (CutReason is `cutReason`, unexported) | n/a | barrel |
| src/next/jobs/job.ts | ported | jobs_job.go | test:chord/next/host/jobs_records_test.go#TestRecordsJobsAcrossAReload* | `handed` state, `handOver`, `seal(err, reason)`, own `definition` for args, `outcomeOf` in finish, `admitRun` owner member |
| src/next/jobs/persisted.ts | ported | jobs_persisted.go | test:jobs_persisted_test.go#TestPersistedVersionsResolvedByAReload* | `forgetBlockedState` |
| src/next/jobs/records.ts | ported | jobs_records.go | test:jobs_records_test.go | `redefine`, `check`, `undeclare`, `retryBlocked`, `takes`, `argsOf`, `outcomeOf`, `entryOf`, `unblock` change, `reload` outcome, per-run version/fingerprint, blockedPhase |
| src/next/jobs/run-states.ts | ported | jobs_run_states.go | via records tests | `redefine`, `migrated`, `older` definitions |
| src/next/jobs/run.ts | ported | jobs_run.go | test:jobs_run_test.go#TestARunHasAnEntryPerPhase... | fingerprint, mapped, blockedPhase, `entryFor(state, args)` |
| src/next/jobs/stored.ts | ported | jobs_stored.go | via persisted tests | `storedRun(run, fields)` uses the run's fingerprint; `clearBlocked` field is the Go form of `blocked: undefined` |
| src/next/documents/transaction.ts | unchanged-observable | n/a | n/a | the +1 line is not job-related |

## (c) upstream tests
| case | disposition | PiG test |
|---|---|---|
| records.test.ts jobs across a reload (4 cases) | ported | TestRecordsJobsAcrossAReload... (x4) |
| records.test.ts reservation released after a swap (Sol) | ported | TestRecordsAReservationReleasedAfterASwap... |
| persisted.test.ts blocked for its version at its load is resumed by reload | ported | TestPersistedVersionsResolvedByAReload... |
| run.test.ts entry per phase (+ mapped args), waiters | ported | jobs_run_test.go |

## (d) async-contract rows
- `retryBlocked`/`readBlockedState` (async in Pi): blocking Go methods; store read outside a section, decode/create inside `loop.Run`; a read/decode failure leaves the runs blocked (returns), as Pi's catch. The retry transaction's failure (object ended) is ignored as in Pi.
- `void this.record(...)` in `undeclare`/`need`/`dispatch`: owned `loop.Go`.
- `argsOf` errors (adapter throws): in `start` computed in-section, delivered as the run's error from the goroutine (Pi: thrown inside the async try).

## (e) new identifiers
unexported only: `cutReason` (`cutRestart`, `cutReload`), `sealedWith`, `mappedRun`, `jobHanded`, `changeUnblock`, `jobOwner.admitRun`, records `check/redefine/undeclare/retryBlocked/takes/argsOf/outcomeOf/entryOf`, `runStates.redefine/migrated/definitionOf`. Tests: `nthTransactionDatabase` (forced: holds the 2nd backend transaction instead of replacing `store.commit`), `jobsReloaded`, `runner` fixture.

## Open
- Verified on xd670 via kreload.sh: `-race -count=3 -run 'TestRecords|TestPersisted|TestARun|TestJob|TestRunning|TestDefine'` green.

## Bench row (second assignment)
| upstream | disposition | PiG | evidence |
|---|---|---|---|
| test/next/bench/plugins.ts (cold build of 20 plugins, rebuild, evaluation, heap after 100 reloads) | ported (forced: reference Builder/dslEvaluator instead of esbuild/vm.Script; `testing.B` instead of hand medians; `runtime.GC` for `--expose-gc`; no correctness assertions upstream beyond sanity of builds/reloads, kept as fatal checks) | chord/nextnode/plugins/bench_plugins_test.go | bench:BenchmarkPluginsColdBuild/Rebuild/Evaluate/Reload ran on xd670 |
