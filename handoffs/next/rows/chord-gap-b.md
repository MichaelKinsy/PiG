# chord-gap-b rows (ChordGapB, base follow/chord-int da5ae61942)

Each Pi case of NEEDS-PORTING.md (my half) -> Go test.

| Pi case | Go test |
|---|---|
| documents: transactions their context's end cancelled > cost nothing on the committed path | NEW chord/next/documents/documents_cost_test.go TestTransactionsTheirContextsEndCancelledCostNothingOnTheCommittedPathNoCheckOfTheContextBeyondTheCommitsOwn (counts ctx.Err instead of AbortSignal.aborted; committed changes read it at most once each; the cancelled change exactly 4: the commit's check and Cause, the report's check and Cause) |
| documents: commits in telemetry > times nothing when nothing records | NEW chord/next/documents/documents_telemetry_test.go TestCommitsInTelemetryTimesNothingWhenNothingRecords (reads of the `commitNow` clock seam, as host_uuid.go's setUUIDClock: none unrecorded, two for a metered commit; the timed-always mutant fails it) |
| registries/record: compose > composes every served registry once at the end of load's setups | NEW chord/next/host/host_registry_record_test.go TestComposeEveryServedRegistryOnceAtTheEndOfLoadsSetupsAnEmptyTableTooBeforeAnyObjectActivates (Pi's inputs via host.Load; deleting load's first composition fails it); record_test.go keeps the record-level half |
| registries/record: last good composition > is dropped when a contribution leaves | NEW host_registry_record_test.go TestTheLastGoodCompositionIsDroppedWhenAContributionItWasBuiltFromLeavesValueThrowsUntilComposeSucceeds (storeRestartable: fail, throws "no contributor", retry gives 2) |
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
| telemetry: metrics > keeps no adapter alive (WeakRef cache) | NEW chord/next/telemetry/telemetry_metrics_cache_test.go TestMetricsKeepsNoAdapterAliveItsInstrumentsAreCachedByItsMetricsObjectWeakly (weak.Make + nexttest.ExpectCollected; a global-cache mutant fails it) |
| telemetry: re-holds > carries the carrier only on a reference's first hold | on fix/chord-t6 (ChordT6): host_telemetry_test.go TestReHoldsCarryTheCarrierOnlyOnAReferencesFirstHold... |
| host/attempt: activation spans > starts nothing without an adapter that records | OWNED by ChordT6d (fix/chord-t6; needs its activation span code, absent on chord-int) |
| host/root: host.root's trace > starts nothing without an adapter that records | OWNED by ChordT6d (needs RootTrace, absent on chord-int) |
| host/ctx: ctx.trace rejects, never throws | skipped per assignment (ChordT6d: TestCtxTraceReturnsAnErrorNeverPanics...) |
| tui/frames (2), surfaces/connection (1) | ChordGapA's half |
