# ChordServerStart rows (Pi 284e63d9, chord server start off the build path), branch fix/pi-chord-server-start

Pin: Pi 284e63d9 is between c23b9ec1 and T=77959d667edd855d2f008924296650c888653916.

## async-contracts (packages/chord/src/node/workers/workers.ts, app/app.ts, surfaces/surface-build.ts)
- workers `#built()` (the worker base build): ordering: one build goroutine started at the first Build/Inputs/start, shared by all waiters (Go: Workers.Build, startBuild, awaitBuild); a start waits for it only until the component closes (awaitWithContext(#built(), #closingContext) = awaitBuild + closingCtx, returns `closed`); Close does not wait for it; a build ending after the close is dropped (commitBase under baseMu checks isClosing; the staged directory is removed). Cancellation: closingCtx for starts; the copy itself cannot be cancelled. Error: a failed build is stored and fails every start (`worker failed to start`), Build never panics. Drain: the build goroutine is not joined (portlint golifetime allow recorded); its late result is dropped; the base directory is removed by the component that wrote it, after every worker exited (workers.close). Evidence: workers_base_held_linux_test.go (TestTheWorkerBaseACloseDoesNotWait..., TestTheWorkerBaseAStartWaitsForAHeldBuild...), workers_base_close_test.go (TestTheWorkerBaseCloseKeepsTheBasesCopy...), app_bases_workers_linux_test.go.
- app `#scanBaseCode` / `buildBases` after the listen: evidence app_bases_scan_test.go (TestAppAnswersBeforeTheScanOfItsOwnCodeFinished..., TestAppClosesWithoutWaitingForAScanOfItsOwnCodeStalled...), app_bases_workers_linux_test.go (TestAppAnswersBeforeAWorkerKindsBaseIsBuilt..., TestAppAClosedStartsLateWorkerBaseBuildNeverTouches...).

## test-mapping (packages/chord/test/node, Pi 284e63d9; all ported, Go tests under chord/nextnode)
- workers/workers.test.ts "a start waits for the base's build, and starts it when nothing did" -> workers/workers_base_held_linux_test.go TestTheWorkerBaseAStartWaitsForAHeldBuildAndForksOnceItEnded... (+ workers_workers_test.go TestTheWorkerBaseAStartWaitsForTheBasesBuildAndStartsItWhenNothingDid)
- workers/workers.test.ts "close keeps the base's files until its workers exited..." -> workers/workers_base_close_test.go TestTheWorkerBaseCloseKeepsTheBasesCopyUntilItsWorkersExited...
- workers/base.test.ts (named directory, writes nothing before the first file) -> workers_workers_test.go TestTheWorkerBaseInANamedDirectory..., TestTheWorkerBaseAComponentThatClosedBeforeItsBuildWritesNothing...
- app/app.test.ts "bases built after the listen" worker kinds -> app/app_bases_workers_linux_test.go (2 tests); scan tests -> app/app_bases_scan_test.go (2 tests)
- Pi-only (no Go form): the close-time lazy chunk import is the fixture's ProgramOnClose; the held pipe is the entry (D600/D603).

## divergences
- No new numbered divergence: D600 (worker base is a copy of the executable), D603 (server code is its executable), D604/D608 unchanged. The worker base is staged beside the named directory and renamed in while open (Go mechanic: a program cannot be held in memory; Pi writes it at the first fork).
