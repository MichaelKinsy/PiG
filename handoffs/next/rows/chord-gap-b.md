# chord-gap-b rows (ChordGapB, base follow/chord-int da5ae61942)

Each Pi case of NEEDS-PORTING.md (my half) -> Go test.

| Pi case | Go test |
|---|---|
| documents: transactions their context's end cancelled > cost nothing on the committed path | NEW chord/next/documents/documents_cost_test.go TestTransactionsTheirContextsEndCancelledCostNothingOnTheCommittedPathNoCheckOfTheContextBeyondTheCommitsOwn (counts ctx.Err instead of AbortSignal.aborted; committed changes read it at most once each) |
| documents: commits in telemetry > times nothing when nothing records | designed out. Relies on Pi documents.ts:189,193 (`timed ? performance.now() : 0`): the test observes the guard only by spying the global `performance.now`. Go's equivalent guard is `timed` at chord/next/documents/documents.go:~307, a local bool calling time.Now; Go has no spy-able global clock and the behaviour (no recording, no metric/event when nothing records) is already covered by the no-recorder commits in every other documents test. Reviewer: confirm |
| registries/record: compose > composes every served registry once at the end of load's setups | already: chord/next/registries/record_test.go TestCompose t.Run "composes every served registry once at the end of load's setups, an empty table too" |
| registries/record: last good composition > is dropped when a contribution leaves | already: record_test.go TestTheLastGoodComposition t.Run "is dropped when a contribution it was built from leaves" |
| link/message: codec mismatch > decodes the other codec's hello | already: link/message_test.go TestACodecMismatch |
| link/message: describeMessage > text tuple form | already: TestDescribeMessage |
| link/message: messages > round-trip %j (each kind) | already: TestMessages t.Run "round-trip <DescribeMessage> and pass the message schema" (name built at run time) |
| link/message: binary codec's long strings > round-trip %s in place behind a padded length | already: TestTheBinaryCodecsStrings t.Run "round-trip <name>, encoded in place behind a padded length" |
| link/values: Uint8Array subclass (Node Buffer, toJSON) | n/a in Go. Relies on Pi link/values.ts:30-33 (a Uint8Array subclass with `toJSON`, Node's Buffer, reaches the JSON replacer as what toJSON made, so the code checks the holder's original value with `instanceof Uint8Array`). Go has one bytes type, `[]byte`, no subclasses and no toJSON replacer; []byte payload carriage is tested in link/values_test.go. Reviewer: confirm |
| telemetry-ingest/facet: drops spans posing as another sender's | already: telemetryingest/ingest_upstream_test.go TestPageBatchDropsSpansPosingAsAnotherSendersOrTheServers |
| telemetry-ingest/facet: never keeps a principal the page claims | already: TestPageBatchNeverKeepsAPrincipalThePageClaims |
| telemetry-sqlite: migrates a store of version 1 | already: telemetrysqlite/telemetry_sqlite_v2_test.go TestMigratesAStoreOfVersion1ToMicrosecondsItsRowsKept |
| telemetry: nothing recorded > treats the NOOP context as untraced | already: telemetry/telemetry_upstream_test.go TestUntracedTreatsTheNoopContextAsUntraced |
| telemetry: open spans > ends once, with how its work settled | already: TestOpenSpansEndOnceWithHowTheirWorkSettled |
| telemetry: open spans > marks a span failed, fixed category, 500 chars | already: TestSpanFailedUsesAFixedCategoryAndAtMost500Characters |
| telemetry: metrics > keeps no adapter alive (WeakRef cache) | designed out. Relies on Pi telemetry.ts:521 (`instruments = new WeakMap<TelemetryMetrics, ChordMetrics>()`) and the test's WeakRef + `expectCollected`: it checks JS garbage collection of a weak cache. Go's chordtelemetry has no per-metrics instrument cache (instruments are created by the adapter's Counter/Histogram calls), so there is nothing to collect; an unreachable Go adapter is collected by the GC without a weak map. Reviewer: confirm |
| telemetry: re-holds > carries the carrier only on a reference's first hold | on fix/chord-t6 (ChordT6): host_telemetry_test.go TestReHoldsCarryTheCarrierOnlyOnAReferencesFirstHold... |
| host/attempt: activation spans > starts nothing without an adapter that records | OWNED by ChordT6d (fix/chord-t6; needs its activation span code, absent on chord-int) |
| host/root: host.root's trace > starts nothing without an adapter that records | OWNED by ChordT6d (needs RootTrace, absent on chord-int) |
| host/ctx: ctx.trace rejects, never throws | skipped per assignment (ChordT6d: TestCtxTraceReturnsAnErrorNeverPanics...) |
| tui/frames (2), surfaces/connection (1) | ChordGapA's half |
