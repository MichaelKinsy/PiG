# K-fold rows (lane K-fold then N-PLUG, Pi e7a0df27..6d4b7443; branch `fix/follow-kfold`)

Delete this file after transcribing. Base `fix/follow-pi-next` @ 65411f70dd; Pi at `6d4b7443`. Each section below is one agent's rows in the plan's (a)-(e) format (the integrator transcribes them; per-part files were merged here).

## Lead notes (design decisions and open questions)

1. **Route (Pi `route.ts`, slice 10 R2, `path.ts`):** no backoff; `Host.Route` returns `RouteHandle{Retry, Suspend}`; a rejected `resolve` or a link closed before its handshake fails the address's holders `failed { waiting }` (`path.unreachable`, `PathEvent`). Forced Go form: Pi's `#resolve` calls `options.resolve` synchronously inside the triggering section; the Go `Resolve` is lent that section (`Loop.Lend`, the method-handler first-suspension handoff of DESIGN-go-layout.md section 10) and must call `chordctx.Suspend(ctx)` before it waits for anything that is not a host section. `RouteHandle.Retry` returns `bool` (a resolve started): a Go-only addition, since Pi's caller reads what its synchronous `resolve` recorded. Tests: host_route_test.go (all upstream cases + TestRouteResolveRunsItsSynchronousPartInsideTheSectionThatTriggeredIt).
2. **Plugins (N-PLUG, Pi `next-node/plugins`):** Go cannot evaluate JS/TS in process (AGENTS.md forbids an embedded JS/WASM runtime), so esbuild `build.ts`, `vm` `evaluate.ts` and JS source-map `stack.ts` are replaced by two consumer-owned seams, `Builder` and `Evaluator` (package `chord/nextnode/plugins`, files `plugins_<stem>.go`). Everything else is ported 1:1: discovery, install, watcher marks with generations, runtime (`NewPluginRuntime`), component (`CreatePlugins`: kinds, scripts/current, build, load, reload, step, rollout, install, close). `RolloutStep.Reload` receives `applied map[string][]PluginScript` (the sets this rollout shipped) because a Go step cannot read `plugins.current(kind)` atomically with the ship as Pi's synchronous continuation does. Designed out: `build.test.ts` esbuild cases, `evaluate.test.ts` vm cases, `stack.test.ts`, examples 37 and 39 (esbuild/vm); their data-flow equivalents are seam tests. Tests use a small facet language read by a test Builder/Evaluator (plugins_seam_test.go).
3. **Group commit (Pi e5c6182b, chord part):** `ChordStore.CommitSeries` (several commits, one backend transaction); Pi's microtask-based batching lives in Durable's writer (N-DUR), not in chord's store.
4. **Link wire break:** `retry` message carries `hops` (1..16) instead of `operation`/`through` (both ends from one revision; changelog `chord-kfold-reload-7b.md`).
5. **Open questions:** (a) `registries.sameFunc` compares closure identity with `unsafe` because Go cannot compare funcs (JS `===`); an alternative is an identity-carrying provider type. (b) A plugin left out at startup fails again on every reload (Pi's runtime.ts implies it; kept). (c) `Facet` carries an identity set only by `DefineFacet` (Pi compares facet objects). (d) Examples 37 and 39 and the esbuild-dependent N-PLUG cases are designed-out with the seam rationale; if the owner wants a Go build toolchain for plugins (e.g. an external esbuild binary behind the Builder seam) that is a new feature. (e) bench rows: `bench/links.ts` and `bench/store.ts` are measurement-method changes (see KReload/KRem parts). (f) `TestRemoteObjectAGetAcrossADrop` hung once in a loaded race run (not reproduced in 30 clean runs; unchanged upstream test): see Remote part.

## (e) interface-mapping gaps (mapping-v1.4.0+e7a0df27.json, `pkg:chord/next#...` pending IDs)

The 329 pending `pkg:chord/next` IDs are 104 top-level names plus their members (`::call:n`, `::construct:n`, `::property:x`). Proposed Go targets for the top-level names (first declaration found by name in `chord/next/**` and `chord/nextnode/**`; members follow the type's mapping by the detector's name rules; the names the delta touches are marked in the parts below). `NO Go declaration`: AddedColumn/TableTailEditor (store `table_evolve.go` editors, Go names differ), ArchiveManifest (store archive, Go: `store/archive.go`), Bounds/Defaulted (contracts schema.go), Columns (store), Contribution (registries), Factory (host `LocalFactory`/`RemoteFactory`), JsonColumnSchema (store json_column.go), KindShape (contracts `HostKind`), Static (type-level, designed-out class: TypeScript static type computation).

```
pkg:chord/next#AcceptsTable | chord/next/contracts/accepts.go#AcceptsTable
pkg:chord/next#AddedColumn | NO Go declaration of that name (see note)
pkg:chord/next#Address | chord/next/contracts/host_kind.go#Address, chord/next/store/engine.go#Address
pkg:chord/next#ArchiveManifest | NO Go declaration of that name (see note)
pkg:chord/next#Bounds | NO Go declaration of that name (see note)
pkg:chord/next#ChordError | chord/next/data/errors.go#ChordError
pkg:chord/next#ChordErrorCode | chord/next/data/errors.go#ChordErrorCode
pkg:chord/next#ChordErrorReason | chord/next/data/errors.go#ChordErrorReason
pkg:chord/next#ChordStore | chord/next/store/store.go#ChordStore
pkg:chord/next#Claimant | chord/next/store/claims.go#Claimant
pkg:chord/next#ColumnRef | chord/next/store/expression.go#ColumnRef
pkg:chord/next#ColumnRefs | chord/next/store/expression.go#ColumnRefs
pkg:chord/next#Columns | NO Go declaration of that name (see note)
pkg:chord/next#Contract | chord/next/contracts/contract.go#Contract
pkg:chord/next#Contribution | NO Go declaration of that name (see note)
pkg:chord/next#Ctx | chord/next/host/host_ctx.go#Ctx
pkg:chord/next#Defaulted | NO Go declaration of that name (see note)
pkg:chord/next#EncodedRow | chord/next/store/rows.go#EncodedRow
pkg:chord/next#Expression | chord/next/store/expression.go#Expression
pkg:chord/next#Facet | chord/next/host/host_facet.go#Facet
pkg:chord/next#FacetHost | chord/next/host/host_facet.go#FacetHost
pkg:chord/next#FacetSelf | chord/next/host/host_facet.go#FacetSelf
pkg:chord/next#Factory | NO Go declaration of that name (see note)
pkg:chord/next#Held | chord/next/documents/persistence.go#Held, chord/next/host/host_reference.go#Held, chord/next/store/claims.go#Held
pkg:chord/next#Host | chord/next/host/host_host.go#Host
pkg:chord/next#HostError | chord/next/host/host_error.go#HostError
pkg:chord/next#HostKind | chord/next/contracts/host_kind.go#HostKind
pkg:chord/next#Implementation | chord/next/contracts/contract.go#Implementation
pkg:chord/next#IpcPort | chord/next/link/ipc.go#IpcPort
pkg:chord/next#JobAccepts | chord/next/host/jobs_definition.go#JobAccepts
pkg:chord/next#JobDefinition | chord/next/host/jobs_definition.go#JobDefinition
pkg:chord/next#JobHandle | chord/next/documents/job.go#JobHandle, chord/next/host/jobs_job.go#JobHandle
pkg:chord/next#JsonColumnSchema | NO Go declaration of that name (see note)
pkg:chord/next#KeyedHandle | chord/next/documents/handles.go#KeyedHandle
pkg:chord/next#KeyedWatchable | chord/next/contracts/contract.go#KeyedWatchable
pkg:chord/next#KindShape | NO Go declaration of that name (see note)
pkg:chord/next#Native | chord/next/host/host_native.go#Native
pkg:chord/next#OnHandler | chord/next/host/host_routines.go#OnHandler
pkg:chord/next#OnLiveHandler | chord/next/host/host_routines.go#OnLiveHandler
pkg:chord/next#OnNativeHandler | chord/next/host/host_routines.go#OnNativeHandler
pkg:chord/next#OwnerTable | chord/next/store/adapter.go#OwnerTable
pkg:chord/next#Presence | chord/next/host/host_presence.go#Presence
pkg:chord/next#Principal | chord/next/data/principal.go#Principal, chord/next/host/host_ctx.go#Principal
pkg:chord/next#Query | chord/next/store/query.go#Query
pkg:chord/next#Registry | chord/next/host/host_attempt.go#Registry, chord/next/host/host_host.go#Registry, chord/next/host/host_staging.go#Registry, chord/next/registries/registry.go#Registry
pkg:chord/next#RegistryAccepts | chord/next/registries/registry.go#RegistryAccepts
pkg:chord/next#RoutineDeclarations | chord/next/host/host_routines.go#RoutineDeclarations
pkg:chord/next#RunState | chord/next/host/jobs_job.go#RunState
pkg:chord/next#Self | chord/next/host/host_attempt.go#Self
pkg:chord/next#SingletonHandle | chord/next/documents/handles.go#SingletonHandle
pkg:chord/next#StagedWrite | chord/next/store/step.go#StagedWrite
pkg:chord/next#StateDefinition | chord/next/documents/definition.go#StateDefinition
pkg:chord/next#Static | NO Go declaration of that name (see note)
pkg:chord/next#Store | chord/next/host/host_object.go#Store, chord/next/store/store.go#Store
pkg:chord/next#StoreSelf | chord/next/host/host_attempt.go#StoreSelf
pkg:chord/next#StoreWrite | chord/next/store/writes.go#StoreWrite
pkg:chord/next#Table | chord/next/host/host_attempt.go#Table, chord/next/host/host_staging.go#Table, chord/next/store/claims.go#Table, chord/next/store/step.go#Table, chord/next/store/store.go#Table
pkg:chord/next#TableDefinition | chord/next/store/table.go#TableDefinition
pkg:chord/next#TableEditor | chord/next/store/table_evolve.go#TableEditor
pkg:chord/next#TableOptions | chord/next/store/table.go#TableOptions
pkg:chord/next#TableTailEditor | NO Go declaration of that name (see note)
pkg:chord/next#Tx | chord/next/documents/documents.go#Tx, chord/next/documents/transaction.go#Tx, chord/next/host/host_attempt.go#Tx, chord/next/host/host_staging.go#Tx
pkg:chord/next#WebSocketLike | chord/next/link/websocket.go#WebSocketLike
pkg:chord/next#WhereNode | chord/next/store/expression.go#WhereNode
pkg:chord/next#add | chord/next/documents/documents.go#add, chord/next/host/host_presence.go#add, chord/next/host/host_unbound.go#add, chord/next/host/jobs_records.go#add, chord/next/host/jobs_running.go#add, chord/next/registries/record.go#Add, chord/next/state/set.go#Add, chord/next/state/watchable.go#Add, chord/next/store/expression.go#Add, chord/next/store/table_evolve.go#Add
pkg:chord/next#arr | chord/next/contracts/schema.go#Arr
pkg:chord/next#bool | chord/next/contracts/schema.go#Bool, chord/next/store/columns.go#Bool
pkg:chord/next#bytes | chord/next/contracts/schema.go#Bytes, chord/next/link/part.go#Bytes
pkg:chord/next#coalesce | chord/next/store/expression.go#Coalesce
pkg:chord/next#concat | chord/next/store/expression.go#Concat
pkg:chord/next#constant | chord/next/state/watchable.go#Constant
pkg:chord/next#decodeJsonl | chord/next/store/jsonl.go#DecodeJsonl
pkg:chord/next#encodeJsonl | chord/next/store/jsonl.go#EncodeJsonl
pkg:chord/next#findDocument | chord/next/store/engine.go#FindDocument
pkg:chord/next#findDocumentEntry | chord/next/store/engine.go#FindDocumentEntry
pkg:chord/next#hostKind | chord/next/contracts/host_kind.go#HostKind
pkg:chord/next#inArray | chord/next/store/expression.go#InArray
pkg:chord/next#int | chord/next/contracts/schema.go#Int, chord/next/store/columns.go#Int
pkg:chord/next#json | chord/next/link/binary.go#json, chord/next/store/json_column.go#Json, chord/next/store/table_evolve.go#Json, chord/nextnode/plugins/plugins_watcher.go#json
pkg:chord/next#listDocumentEntries | chord/next/store/engine.go#ListDocumentEntries
pkg:chord/next#listDocuments | chord/next/store/engine.go#ListDocuments
pkg:chord/next#lit | chord/next/contracts/schema.go#Lit
pkg:chord/next#loadContent | chord/next/store/engine.go#LoadContent
pkg:chord/next#loadDocument | chord/next/store/engine.go#LoadDocument
pkg:chord/next#local | chord/next/contracts/declare.go#Local
pkg:chord/next#map | chord/next/contracts/schema.go#Map
pkg:chord/next#max | chord/next/contracts/schema.go#Max, chord/next/store/expression.go#Max
pkg:chord/next#method | chord/next/contracts/evolve.go#Method, chord/next/contracts/schema.go#Method, chord/next/host/remote_hold.go#method
pkg:chord/next#min | chord/next/contracts/schema.go#Min, chord/next/store/expression.go#Min
pkg:chord/next#nul | chord/next/contracts/schema.go#Nul
pkg:chord/next#num | chord/next/contracts/schema.go#Num, chord/next/store/columns.go#Num
pkg:chord/next#obj | chord/next/contracts/schema.go#Obj
pkg:chord/next#oneOf | chord/next/contracts/schema.go#OneOf
pkg:chord/next#opt | chord/next/contracts/schema.go#Opt, chord/next/store/columns.go#Opt
pkg:chord/next#query | chord/next/store/query.go#Query
pkg:chord/next#readDocument | chord/next/store/engine.go#ReadDocument
pkg:chord/next#readDocumentEntry | chord/next/store/engine.go#ReadDocumentEntry
pkg:chord/next#remote | chord/next/contracts/declare.go#Remote
pkg:chord/next#replacePrefix | chord/next/store/expression.go#ReplacePrefix
pkg:chord/next#str | chord/next/contracts/schema.go#Str, chord/next/store/columns.go#Str
pkg:chord/next#sub | chord/next/store/expression.go#Sub
pkg:chord/next#union | chord/next/contracts/schema.go#Union
pkg:chord/next#unknown | chord/next/contracts/schema.go#Unknown
pkg:chord/next#when | chord/next/store/expression.go#When
```

---

# Part: Foundation packages (contracts, data, link, registries, state, documents) (from kfold-foundA.md)

# K-fold foundA rows (contracts, data, link, registries, state, documents)

## (a) upstream src files
chord/next/contracts/accepts.ts | ported | contracts/accepts.go | test:chord/next/contracts/accepts_test.go#TestAcceptsKeyedMappings | KeyedMapping, VersionMappings.Keyed, needs.keyed, checkKeyedMapping
chord/next/contracts/contract.ts | ported | contracts/contract.go (VariantsAt) | test:contracts/contract_test.go#TestVariantsAt (PiG-own; no upstream test) | variantsAt; unknown version is an error (TS `!`)
chord/next/contracts/evolve.ts | ported | contracts/evolve.go | test:contracts/evolve_test.go#TestEvolveKeyedEdits | LocalEditor.Keyed/AddKeyed/RemoveKeyed; draft clones Keyed; refuseDeclared sees keyed; type-level PutKeyed/DropKeyed designed out
chord/next/contracts/history.ts | ported | contracts/history.go | SideKeyed via the two tests above | Change side "keyed"
chord/next/contracts/internal.ts | designed-out | - | - | barrel; Go imports the package
chord/next/data/args.ts | ported | data/args.go | test:data/args_test.go#TestBinds (PiG-own) | Provider.Accepts, Binds
chord/next/data/internal.ts | designed-out | - | - | barrel
chord/next/link/binary.ts, text.ts, message.ts | ported | link/{binary,text,message}.go | test:link/message_test.go#TestMessages,TestTheBinaryCodec,TestTheTextCodec; test:link/oracle_test.go#TestWireFormatsMatchPi (golden regenerated from Pi T with link_wire.mjs; only retry entries changed) | RetryMessage{Hops}, RETRY_HOPS, schema 1..16
chord/next/link/internal.ts | designed-out | - | - | barrel
chord/next/registries/contribution.ts | ported | registries/contribution.go | test:registries/record_test.go#TestAReloadsNewContributionAdapter | adapter identity in adapt cache; Reads()/cycle edge removed
chord/next/registries/record.ts | ported | registries/record.go | test:registries/record_test.go#TestTheTable (ring tests), #TestProviderCheck | CheckProvider, Serve (no error, replaces, batch), Unserve, ComposeServed; RegistryEdge/AssertNoCycle/reads removed
chord/next/registries/value.go (value.ts) | ported | registries/value.go | test:registries/value_test.go#TestAReloadsNewValueAdapter | mapped/mapFailure keyed by adapter; Record() removed
chord/next/registries/internal.ts | ported | registries/internal.go | - | sameFunc helper (JS === on adapters, closure-pointer compare via unsafe)
chord/next/state/batch.ts | ported | state/batch.go | test:state/batch_own_test.go#TestBatchCount | CounterContractAdapts
chord/next/state/watchable.ts | unchanged-observable | state/watchable.go | - | lazy watch/subscriber sets: Go orderedSet zero value is already lazy; doc comments updated. Exports needed by host reference-source (Batch, Dependent, DeriveSource, HandedOut, Source, Subscriber) already exist.
chord/next/documents/address-source.ts, keyed-slot.ts | ported | documents/address_source.go, keyed_slot.go | test:documents/address_source_test.go | AddressImpl.Owner and KeySource.Owner removed (SingletonSlot keeps its own owner)
chord/next/documents/documents.ts | ported | documents/documents.go | test:documents/documents_test.go#TestDocumentsCheckChecksADeclarationAsDeclareWouldChangingNothing | Documents.Check
chord/next/documents/transaction.ts | ported | documents/transaction.go | covered by existing transaction tests | handle() calls store TableHandle.AssertVersion

## (c) upstream tests
contracts/accepts.test › keyed mappings (slice 7b) | ported | TestAcceptsKeyedMappings
contracts/evolve.test › keyed edits (2 cases) | ported | TestEvolveKeyedEdits
documents/address-source.test (owner removed) | ported | address_source_test.go (testAddress.Owner dropped)
link/message.test › EVERY_KIND retry cases, binary retry bytes, text retry tuple, refuses retry without hops | ported | link/message_test.go
registries/record.test › ring at first composition; later recompositions bounded | ported at record level | TestTheTable subtests
registries/record.test › ring through a derive / within one factory | pending host (needs attempts, self.derive, host.get)
registries/record.test › removed cycle-at-contribute cases | designed-out (removed upstream); Go twin removed
registries/record.test › reload's new contribution adapter | ported at record level (Serve replaces provider) | TestAReloadsNewContributionAdapter (host.reload part pending host lane)
registries/value.test › reload's new value adapter | ported at record level | TestAReloadsNewValueAdapter
registries/registry.test › provider without compose | CheckProvider ported | TestProviderCheck; "second serve leaves plugin out at load" (host.load failed[]) pending host lane
test/next/support.ts (loaded/loadedPlugins failed, relayed route) | host test support, not in my packages

## (d) async contracts
none (packages are synchronous; link codecs unchanged in contract).

## (e) new exported identifiers
contracts: KeyedMapping, VersionMappings.Keyed, LocalEditor.Keyed/AddKeyed/RemoveKeyed, SideKeyed, VariantsAt
data: Provider.Accepts, Binds
link: RETRY_HOPS, RetryMessage.Hops (Operation, Through removed)
registries: CheckProvider, RegistryRecord.Unserve; Serve now returns nothing; removed RegistryEdge, AssertNoCycle, RegistryContribution.Reads, RegistryValueSource.Record
state: CounterContractAdapts
documents: Documents.Check; removed AddressImpl.Owner, KeySource.Owner

## Host call-site edits (minimal, to compile)
remote_consumer.go retry sends Hops=RETRY_HOPS (no op id on the wire); remote_hold.go retry handler makes a new retryOperation; host_members.go contributionOf lost `pending`; host_attempt.go lost pendingEdges/AssertNoCycle at contribute; host_host.go serveEntry does double-serve + CheckProvider then Serve; tests: remote_relay_test, remote_hold_test, remote_remote_object_test (Hops instead of Operation), host_retry_on_test (Serve no error), host_members_test (contributionOf args).
Host tests asserting old semantics (to be reworked by host lane): host_members_test contribution refusing own registry value (invalid/cycle at contribute); registry cycle-at-contribute cases; cross-host retry operation/through dedupe (host_object_test diamond, ring) and `owns` for key sources (KeySource.Owner removed).

## Open questions
- sameFunc uses unsafe closure-pointer identity; RegistryProvider.Erase makes fresh closures per call, so re-serving an identical typed provider remaps (JS would not).
- Red evidence: new APIs absent => compile errors (see /tmp/kfold/red-foundA-contracts.txt, red-foundA-link.txt); registries tests written with their API change.

---

# Part: Store and output (from kfold-store.md)

# K-fold lane `store` rows (Pi `e7a0df27`..`6d4b7443`: `packages/chord/src/next/store/**`, `packages/chord/src/output.ts`)

Owned: `chord/next/store/**`, `chord/output/**`. Red output: `/tmp/kfold/red-store.txt` (store: build failed, `undefined` identifiers from the ported tests, the first 10 of the compiler's errors; output: `TestToolOutputBounds/removes_whole_ANSI_escape_sequences...` failing against the unchanged `SanitizeOutput`).

## (a) per upstream src file

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/chord/src/next/store/claims.ts | ported | chord/next/store/claims.go | test:chord/next/store/claims_test.go#TestClaims/assert_what_a_claim_would_refuse,_claiming_nothing | `assertTable`/`assertDocuments` -> `AssertTable`/`AssertDocuments`; `#claim`/`#assert` -> `claim`/`assert` over one `refusal(key, claimant)` (locked first, then another holder). No upstream test exercises them in the store directory (the host's staging, `staging.ts`, calls them), so the evidence is a Go-only case. |
| packages/chord/src/next/store/store.ts | ported | chord/next/store/store.go | test:chord/next/store/store_test.go#TestStore/a_commit_series; test:chord/next/store/table_handle_test.go#TestTableHandle/the_version_fence_against_a_migration_in_flight_(fix_round_1,_Sol_8) | `commitSeries` -> `CommitSeries` (+ `SeriesCommit`, `SeriesResult`, private `series`, `seriesProgress`, `watchedExecutor`); `commitInBatch` removed (no Go caller; clean cutover); `tableVersion`/`announceTable` -> `TableVersion`/`AnnounceTable`; `#write` split into `write` (writes, catalog, outbox) and `writeMeta`, which `step` and the series call; doc wording "adopt" -> "publish" for `Attachment`/`Batch()` (Pi's `store.batch`, here the existing `Batch()`). |
| packages/chord/src/next/store/table-handle.ts | ported | chord/next/store/table_handle.go | test:chord/next/store/table_handle_test.go#TestTableHandle/the_version_fence_of_old_handles_(R15,_slice_7b_§5) | `assertVersion` -> `AssertVersion() error`, called by `Page`, `Get`, `Count` after the liveness check (`Rows` reads through `Page`). |
| packages/chord/src/next/store/table-migration.ts | ported / designed-out (macrotask feature check) | chord/next/store/table_migration.go | test:chord/next/store/table_handle_test.go#TestTableHandle/the_version_fence_against_a_migration_in_flight_(fix_round_1,_Sol_8) | `migrateVersion` announces the version before its first commit and withdraws it after its last (`defer`); the commits moved to `migrateSteps`. The `macrotask` change (`setImmediate` read once and checked with `typeof`) is a JS runtime feature check: Go yields with `runtime.Gosched()` (existing designed-out note, extended in the file header). |
| packages/chord/src/next/store/README.md | unchanged-observable | n/a (Pi docs; PiG has no store README) | n/a | Prose naming `commitSeries`, `store.batch`. |
| packages/chord/src/output.ts | ported | chord/output/output.go | test:chord/output/output_test.go#TestToolOutputBounds/removes_whole_ANSI_escape_sequences,_not_only_their_ESC_bytes; test:chord/output/output_sanitize_golden_test.go#TestSanitizeOutputEscapeSequencesMatchPi | `ESCAPE_SEQUENCE` is the same regex in Go RE2 (leftmost-first alternation, same matches as JS); `SanitizeOutput` removes escape sequences first, then the control characters; a string with no ESC byte skips the regex. `OutputBuffer` sanitizes at `Snapshot` on the retained text, so sequences split across pushes are removed whole, as upstream's test requires. |

Other upstream diffs in the range touching the store directory: none (`git log e7a0df27..6d4b7443 -- packages/chord/src/next/store packages/chord/test/next/store` is `4b535d57` slice 7b, `e5c6182b` group commit, `50d9f823` output).

## (c) upstream test cases

| upstream test file > case | disposition | PiG test |
|---|---|---|
| store/store.test.ts > a commit series > stores its commits in one transaction, one seq each, each checked against the ones before it | ported | chord/next/store/store_test.go#TestStore/a_commit_series/stores_its_commits_in_one_transaction,_one_seq_each,_each_checked_against_the_ones_before_it |
| ... > stops at a check that refuses: the commits before it are stored, it and the ones after are not | ported | ...#stops_at_a_check_that_refuses:_the_commits_before_it_are_stored,_it_and_the_ones_after_are_not |
| ... > rolls the whole series back when a check's statement fails, even if the check throws its own error | ported | ...#rolls_the_whole_series_back_when_a_check's_statement_fails,_even_if_the_check_throws_its_own_error |
| ... > rolls the whole series back when a commit's writes throw, the store going on | ported | ...#rolls_the_whole_series_back_when_a_commit's_writes_throw,_the_store_going_on |
| ... > writes the metadata once per series: three statements per delta with its outbox commit, plus one (cost guard) | ported | ...#writes_the_metadata_once_per_series:_three_statements_per_delta_with_its_outbox_commit,_plus_one_(cost_guard) |
| ... > a failed %s is an unknown outcome: nothing is received, the store stops storage_failed (BEGIN IMMEDIATE, COMMIT, ROLLBACK) | ported | ...#a_failed_BEGIN_IMMEDIATE_is_an_unknown_outcome:..., ..._COMMIT_..., ..._ROLLBACK_... |
| ... > lets a later commit see the table records an earlier one changed; a rollback leaves the catalog as it was | ported | ...#lets_a_later_commit_see_the_table_records_an_earlier_one_changed;_a_rollback_leaves_the_catalog_as_it_was |
| ... > runs a commit queued behind a series after the series' receipts | ported | ...#runs_a_commit_queued_behind_a_series_after_the_series'_receipts (both calls started with `startQueued`, which returns once the call took its place in the queue, as Pi's two calls made before the first await) |
| ... > counts only commits with writes against the seq bound: empty ones at Number.MAX_SAFE_INTEGER still stand | ported | ...#counts_only_commits_with_writes_against_the_seq_bound:_empty_ones_at_Number.MAX_SAFE_INTEGER_still_stand |
| ... > stops the store when a receipt throws | ported | ...#stops_the_store_when_a_receipt_throws |
| store/table-handle.test.ts > the version fence of old handles (R15, slice 7b §5) > refuses reads through a handle older than the stored table: released/version | ported | chord/next/store/table_handle_test.go#TestTableHandle/the_version_fence_of_old_handles_(R15,_slice_7b_§5)/refuses_reads_through_a_handle_older_than_the_stored_table:_released/version |
| store/table-handle.test.ts > the version fence against a migration in flight (fix round 1, Sol 8) > refuses an old handle's read made while the migration's commit runs, rather than issuing obsolete SQL | ported | ...#TestTableHandle/the_version_fence_against_a_migration_in_flight_(fix_round_1,_Sol_8)/refuses_an_old_handle's_read_made_while_the_migration's_commit_runs,_rather_than_issuing_obsolete_SQL. `holdNextCommit` is `gatedStore` (holds the next transaction at its first statement); the old handle's read runs on a goroutine so a missing fence fails after 10 s instead of waiting for the held migration. Mutation-proven: with `AnnounceTable` replaced by a no-op the case fails ("did not settle while the migration's commit was held"). |
| output.test.ts > tool output bounds > removes whole ANSI escape sequences, not only their ESC bytes | ported | chord/output/output_test.go#TestToolOutputBounds/removes_whole_ANSI_escape_sequences,_not_only_their_ESC_bytes |
| (Go-only) `sanitizeOutput` against Pi 6d4b7443 | added | chord/output/output_sanitize_golden_test.go#TestSanitizeOutputEscapeSequencesMatchPi: 1512 inputs and the answers Pi 6d4b7443's `sanitizeOutput` returned under Node (`testdata/sanitize_escape_golden.json`; generated once, static). Reason: the live oracle `TestOutputBufferMatchesPi` reads the upstream mirror, which is not at 6d4b7443 (and not present in the CI container, where it already fails before this lane: `Cannot find module .../.upstream/current/packages/chord/src/output.ts`). Its piece list is unchanged (no ESC pieces added, because the mirror it compares with is older than the new behavior). |

`test/next/support.ts` changes in the range (`loaded`/`loadedPlugins` return `failed`, `relayed` returns `route`) concern host tests, not the store; `holdNextCommit`, `faultyStore`, `metered`, `exported`, `rejection` unchanged and already in `support_test.go` (`gatedStore`, `faultyStore`, `metered`, `exported`, `rejected`).

## (d) async-contract rows

| file | contract | evidence | rationale |
|---|---|---|---|
| store/store.ts `commitSeries` | awaited, microtask-ordering | TestStore/a_commit_series/runs_a_commit_queued_behind_a_series_after_the_series'_receipts | The call takes its place in the commit queue at the call (the FIFO ticket, as `Commit`); `CommitSeries` blocks until the series' transaction committed and its receipts ran, `receive` called per stored commit in order before the next queue entry starts (the ticket closes after the receipts). A refusal (a check's error) is a result (`Refused`), a failure of a check's own backend call or of a commit's writes is the returned error with the whole series rolled back and the store going on, any other failure (`BEGIN`, `COMMIT`, `ROLLBACK`, a receipt) stops the store `storage_failed`. No cancellation (ctx counts the step, as `Commit`). |
| store/table-migration.ts `migrateVersion` | awaited | TestTableHandle/the_version_fence_against_a_migration_in_flight... | `AnnounceTable` is synchronous (under `mu`) before the first commit is queued; the withdrawal is deferred, so it runs also when a commit fails. |

### Design note: group commit and the turn flush

Pi's chord store has no microtask-based batching: the grouping is the array a caller gives `commitSeries` (one backend transaction, one seq per commit with writes). What the Pi commit message (`e5c6182b`) describes as "commits that arrive while one storage call is in flight go out together in the next call" is Durable's Session writer (`packages/durable/src/session/writer.ts`), above the store; it calls `StoreStorage.commit(batches, receive, context)` -> `ChordStore.commitSeries`. So the store needs no flush at the end of a turn and none was added: `CommitSeries` is one queue entry, started by the caller's goroutine, and settlement order (receipts in index order, then the queue entry's end) is deterministic by construction. The Go durable lane that ports `writer.ts` owns the Go form of the turn (its explicit flush mechanism); it calls `ChordStore.CommitSeries(commits, receive, ctx)` and `ChordStore.Batch().Within(...)` for `publishing`.

## (e) new exported identifiers (chord/next/store)

`SeriesCommit{Check func(*Step) error; Writes []StagedWrite}`, `SeriesResult{Stored int; Refused error}`, `(*ChordStore).CommitSeries(commits []SeriesCommit, receive func(index int, seq int64, ids []int64) error, ctx context.Context) (SeriesResult, error)`, `(*ChordStore).TableVersion(name string) (int, bool)`, `(*ChordStore).AnnounceTable(name string, version int) (withdraw func())`, `(*TableHandle).AssertVersion() error`, `(*Claims).AssertTable(table string, claimant Claimant) error`, `(*Claims).AssertDocuments(owner, namespace string, claimant Claimant) error`. Removed: `(*ChordStore).CommitInBatch` (no callers in the tree; Pi removed `commitInBatch`). `chord/output`: no new identifier (`SanitizeOutput` behavior).

Callers edited outside the owned paths: none. (`chord/next/documents` needs `TableHandle.AssertVersion` for Pi's `transaction.ts`; lane foundA was told the name.)

## Open questions

- `chord/output/output_pi_oracle_test.go` (`TestOutputBufferMatchesPi`) needs `.upstream/current/packages/chord/src/output.ts`; it fails in the CI container before and after this lane. Once the mirror is at 6d4b7443 its `outputPieces` can gain ESC pieces (`"\x1b[31m"`, `"\x1b]0;t\x07"`, `"\x1b"`); left unchanged here because the mirror (`.upstream/current` -> v1.0.3 locally) predates the new behavior.
- The red capture in `/tmp/kfold/red-store.txt` shows the first 10 compile errors only (the compiler stops there); the series tests' `CommitSeries`/`SeriesCommit`/`AssertVersion` were undefined too.

---

# Part: Plugins package (discover, install, watcher, seams, runtime, component) (from kfold-plugA.md)

# kfold-plugA: `chord/nextnode/plugins` (Pi slice 7c plugins component)

Package `github.com/MichaelKinsy/PiG/chord/nextnode/plugins` (`chord/nextnode/plugins/`, files `plugins_<stem>.go`, in-package tests). Pi `6d4b7443`, `packages/chord/src/next-node/plugins/`.
Not written (lead): `plugins.ts` (component), `runtime.ts`, `index.ts`. Design decision (lead): esbuild build, `vm.Script` evaluation and JS source-map stack mapping are the consumer-owned seams `Builder` / `Evaluator`.

## (a) Upstream src files

path | disposition | PiG files | evidence | rationale
--- | --- | --- | --- | ---
next-node/plugins/discover.ts | ported | plugins_discover.go | test:chord/nextnode/plugins/plugins_discover_test.go#TestDiscoveryNamesAPluginByItsPackageJSONListsItsFacetFilesByKindItsDependenciesAndExternals | `DiscoverPlugins`, `Plugin`, `Discovery`, `DiscoveryProblem`, `DiscoveryOptions`; `isPluginName` is a private copy of host/staging.ts's regex (the host's is unexported, see open questions)
next-node/plugins/install.ts | ported | plugins_install.go | test:chord/nextnode/plugins/plugins_install_test.go#TestInstallDependenciesRunsNpmInstallWithoutDevDependenciesPeersScriptsAuditOrFundingInThePluginsRealDirectory | `InstallDependencies`, `InstallOptions`
next-node/plugins/watcher.ts | ported | plugins_watcher.go (+ plugins_watcher_linux_test.go) | test:chord/nextnode/plugins/plugins_watcher_test.go#TestTheWatchersMarksFromRealFileEventsPublishingTheMarksToAWatch | `pluginWatcher` is a `state.Source`; fsnotify replaces `fs.watch`
next-node/plugins/build.ts | designed-out (esbuild) / ported (data) | plugins_build.go | test:chord/nextnode/plugins/plugins_build_test.go#TestBuildingAFacetReturnsASyntaxErrorAndAMissingFacetAsFailedResultsNeverAThrow | the esbuild build, `ImportRules`, `freeGlobals`, `describe` are the `Builder` seam (Go cannot run esbuild in process). Ported: `BuildOptions`, `BuildInput`, `BuiltFacet` (plain data), `BuildFacet` (missing-facet failed result, abort, delegate), `inside`
next-node/plugins/evaluate.ts | designed-out (`vm.Script`) / ported (identity) | plugins_evaluate.go | test:chord/nextnode/plugins/plugins_evaluate_test.go#TestEvaluatingAFacetRunsAScriptUnderAStableFilenamePerPluginKindAndDigest | `require`, `interop`, `registerScript`, `evaluateFacet` are the `Evaluator` seam (the default-export-is-a-facet check is the evaluator's error). Ported: `HostPackages`, `EvaluateOptions`, `ScriptFilename` (stable filename per plugin/kind/digest), `PluginFile` (= `fileOf`: path in the plugin's directory, `..` out refused). `PLUGINS_SPECIFIER`, `packageName`, the exported throwing `pluginFile` stub are JS-evaluation/esbuild internals: designed-out (no Go caller)
next-node/plugins/stack.ts | designed-out | none | n/a | `mapStack`/`registerScript`/`inlineSourceMap` map V8 stack positions through esbuild's inline source maps of `vm.Script` scripts; there is no JS script in Go
next-node/plugins/runtime.ts | ported | plugins_runtime.go | test:chord/nextnode/plugins/plugins_runtime_test.go#TestLoadingAndReloadingWithCompleteSetsReloadsACompleteSetSameDigestsUnchangedAChangedOneAppliedALoadedOneNotShippedRemoved | `NewPluginRuntime(evaluator)`, `PluginRuntime.Load/Reload`, `PluginScript`, `RuntimeLoad`; V8 compilation-cache switch designed out; per-host reload queue in call order (`queue`/`run`); `Forget(host)` replaces the WeakMap
next-node/plugins/plugins.ts | ported | plugins_plugins.go | test:chord/nextnode/plugins/plugins_plugins_test.go, plugins_endtoend_test.go, plugins_examples_test.go | `CreatePlugins`, `Plugins`, `PluginsOptions`, `KindDefinition`, `Built`, `RolloutStep/Host/Result`, `PluginsError`; state/watcher touched only in sections of the component's own `host.Loop`
next-node/plugins/index.ts | designed-out | none | n/a | barrel: Go imports the package
next-node/plugins/README.md | read | n/a | n/a | package doc in doc.go summarizes the seam split

## (c) Upstream test cases

upstream test file › case | disposition | PiG test
--- | --- | ---
discover.test.ts › names a plugin by its package.json, lists its facet files by kind, its dependencies and externals | ported | plugins_discover_test.go#TestDiscoveryNamesAPluginByItsPackageJSONListsItsFacetFilesByKindItsDependenciesAndExternals
discover.test.ts › reports a directory without package.json, with an invalid one, an invalid name or two files of a kind | ported | ...#TestDiscoveryReportsADirectoryWithoutPackageJSONWithAnInvalidOneAnInvalidNameOrTwoFilesOfAKind
discover.test.ts › keeps the first root's plugin of a duplicate name and reports the other; a missing root is empty | ported | ...#TestDiscoveryKeepsTheFirstRootsPluginOfADuplicateNameAndReportsTheOtherAMissingRootIsEmpty
discover.test.ts › follows a symlinked plugin directory to its real path | ported | ...#TestDiscoveryFollowsASymlinkedPluginDirectoryToItsRealPath
install.test.ts › runs npm install without dev dependencies, peers, scripts, audit or funding, in the plugin's real directory | ported (fake npm is a `sh` script recording cwd and args, not a Node script) | plugins_install_test.go#TestInstallDependenciesRunsNpm...
install.test.ts › rejects with the command's output when it fails, and when the context aborts | ported; Node's `name: "AbortError"` is the abort reason (`errors.Is(err, stop)`, `context.Cause`) | ...#TestInstallDependenciesRejectsWithTheCommandsOutputWhenItFailsAndWhenTheContextAborts
(own, not upstream) discovery returns the abort's reason before each directory, nothing at entry | added for `throwIfAborted` | plugins_discover_test.go#TestDiscoveryReturnsTheAbortsReasonBeforeEachDirectoryAndNothingForNoDirectory
(own, not upstream) empty `npmCommand` refused; aborted ctx refuses before running | added for the source branches | ...#TestInstallDependenciesRefusesAnEmptyCommandAndAnAbortedContextBeforeRunningAnything
watcher.test.ts › the watcher's marks › dirties every plugin whose facets used a changed file, and the plugin whose directory contains it | ported | plugins_watcher_test.go#TestTheWatchersMarksDirtiesEveryPlugin...
watcher.test.ts › ... › dirties the plugin of a file no facet read yet; asks for an install for a package.json | ported | ...#TestTheWatchersMarksDirtiesThePluginOfAFileNoFacetReadYet...
watcher.test.ts › ... › asks for discovery for a directory under a root and a facet file at a plugin's top; base for base code | ported | ...#TestTheWatchersMarksAsksForDiscoveryForADirectoryUnderARoot...
watcher.test.ts › ... › ignores node_modules and .git | ported | ...#TestTheWatchersMarksIgnoresNodeModulesAndGit
watcher.test.ts › ... › keeps the mark of a change during a build: only an unchanged mark clears when applied | ported | ...#TestTheWatchersMarksKeepsTheMarkOfAChangeDuringABuild...
watcher.test.ts › ... › marks from real file events, publishing the marks to a watch | ported (a Go watch: `Source.Watch` + `chord.WatchHandle.Start`, delivery waited with `DeliveryScheduler.Wait`) | ...#TestTheWatchersMarksFromRealFileEventsPublishingTheMarksToAWatch
watcher.test.ts › fix round 1 › never lets an older build's applied snapshot clear a newer edit's mark (Astra) | ported | ...#TestTheWatcherFixRound1NeverLetsAnOlderBuildsAppliedSnapshot...
watcher.test.ts › fix round 1 › drops the install mark of a plugin the next discovery no longer has (example 38) | ported | ...#TestTheWatcherFixRound1DropsTheInstallMark...
watcher.test.ts › fix round 1 › puts no watch on node_modules or .git (Opus N2): 200 packages add no inotify watch | ported; `skipIf(platform !== "linux")` is a `//go:build linux` file, no `t.Skip` | plugins_watcher_linux_test.go#TestTheWatcherFixRound1PutsNoWatchOnNodeModulesOrGit...
watcher.test.ts › fix round 1 › watches a directory that appears later, and marks the files in it | ported | plugins_watcher_test.go#TestTheWatcherFixRound1WatchesADirectoryThatAppearsLater...
watcher.test.ts › the watcher and links › survives a link loop created under a root (Sol) | ported | ...#TestTheWatcherAndLinksSurvivesALinkLoop...
build.test.ts › returns a syntax error and a missing facet as failed results, never a throw | ported at the seam: the missing facet half is `BuildFacet`'s own; the syntax error half is the reference builder's failed result passed through unchanged (esbuild message text is not reproduced) | plugins_build_test.go#TestBuildingAFacetReturnsASyntaxErrorAndAMissingFacetAsFailedResultsNeverAThrow
(own, not upstream) `BuildFacet` throws only when ctx aborts | added for `throwIfAborted` | plugins_build_test.go#TestBuildingAFacetThrowsOnlyWhenItsContextAborts
evaluate.test.ts › runs a script under a stable filename per plugin, kind and digest | ported at the seam: the filename (`ScriptFilename`), not the V8 stack text | plugins_evaluate_test.go#TestEvaluatingAFacetRunsAScriptUnderAStableFilenamePerPluginKindAndDigest
evaluate.test.ts › gives each facet pluginFile for its own plugin's directory and refuses .. out of it | ported at the seam: `PluginFile` path and the refusal message; the `createHost` load of the evaluated facet and "outside an evaluated facet it has none" (`pluginFile("x")` throws) are JS-only | plugins_evaluate_test.go#TestEvaluatingAFacetGivesEachFacetPluginFile...
build.test.ts › inlines the plugin's own files and another plugin's file (copied); keeps host packages external | designed-out | esbuild import table and inline source maps (Builder seam)
build.test.ts › hashes each input as esbuild read it; the digest is the source's hash | designed-out | the hashes and the digest are computed by the Builder (a Builder is free to hash; the reference builder in plugins_seam_test.go uses sha256 hex and is checked in plugins_build_test.go only for `built.Digest == sha256(source)` of itself); production code never hashes
build.test.ts › gives the same script and digest from two checkout paths (relative source maps) | designed-out | source maps relative to the plugin are esbuild output
build.test.ts › inlines a package of its dependencies; keeps chord.external packages and Node builtins external on Node | designed-out | esbuild
build.test.ts › refuses a chord.external package and a Node builtin in a browser build | designed-out | esbuild
build.test.ts › collects a browser build's CSS into one stylesheet and its assets into content-hashed files | designed-out | esbuild; `CSS`/`Asset` are the data it fills
build.test.ts › refuses CSS and assets in a Node build | designed-out | esbuild
build.test.ts › refuses import.meta.url, __dirname, __filename, a dynamic import() and top-level await, pointing to pluginFile | designed-out | esbuild + source-map positions of JS
build.test.ts › refuses a path outside the plugin, a package not in its dependencies, and a package in a copied file | designed-out | esbuild resolution rules
build.test.ts › imports another plugin's file by name from any root; refuses relative paths into it, names a missing plugin | designed-out | esbuild resolution rules
evaluate.test.ts › gives host packages as the host's instances: a ChordError crosses the plugin as one | designed-out | `vm.Script` `require` (Evaluator seam)
evaluate.test.ts › answers default and named imports of an ESM and a CommonJS host package | designed-out | JS module interop
evaluate.test.ts › loads chord.external packages and Node builtins from the plugin's directory | designed-out | Node `require`
evaluate.test.ts › throws an import-time throw, a non-facet export and a refused require, naming the facet | designed-out | JS evaluation (the facet check is the Evaluator's error; the reference evaluator returns the same message for an unknown script, plugins_evaluate_test.go)
evaluate.test.ts › runs a computed import() through the facet's require | designed-out | JS
evaluate.test.ts › ESM host packages' wrappers › wraps a namespace once, and keeps no namespace nobody else holds | designed-out | JS module namespaces, WeakRef/GC
stack.test.ts › mapStack › maps a plugin script's positions ...; lets a script's source map go with its facet | designed-out | stack.ts is JS source-map mapping
stack.test.ts › mapStack, fix round 1 › maps an import-time throw / maps a throw while its default export is read / keeps a script's map while any evaluation of it lives | designed-out | stack.ts
`fixtures/plugins/{contracts,notes}` are copied to `chord/nextnode/plugins/testdata/plugins/` (used by discover and watcher tests); `files` is only used by evaluate/plugins cases that are JavaScript (the `PluginFile` case builds its own plugins)

## (d) Async contracts

item | contract | evidence
--- | --- | ---
`discoverPlugins` (async) | blocking `DiscoverPlugins(roots, options, ctx)`: roots listed in order, each root fully listed before its directories are read (as `await directoriesOf`), abort checked before each directory (`ctx.Err()` -> `context.Cause(ctx)`; never at entry or after the last), a read error is the returned error | plugins_discover_test.go#TestDiscoveryReturnsTheAbortsReasonBeforeEachDirectoryAndNothingForNoDirectory (own test) and the ported cases (order, errors)
`installDependencies` (async, `spawn` with the context's signal) | blocking `InstallDependencies(plugin, ctx, options)`: aborted ctx returns its reason before starting; while running, abort sends SIGTERM and returns the reason; non-zero exit returns `plugin "<name>": <cmd> install failed (exit <code>): <output>`; stdout and stderr share one buffer (written order) | plugins_install_test.go (fail text, abort kills a `sleep 30` and returns the cause, pre-aborted never runs the command)
`buildFacet` (async) | blocking `BuildFacet`: missing facet -> failed result without abort check (upstream order), then abort check, then the Builder; failed builds are results, errors only for abort | plugins_build_test.go
`PluginWatcher` microtask publication (`queueMicrotask` + `batch.within`) | `Batch.Soon` (same host microtask queue), at most one pending (`scheduled`); published only when watched or derived from | TestTheWatchersMarksFromRealFileEventsPublishingTheMarksToAWatch (frames delivered after the marks change)
`fs.watch` callbacks (event loop) | one goroutine owned by the watcher reads fsnotify's events/errors and runs each as a host section via `Batch.Section` (`Loop.Run`); `close` stops fsnotify without waiting, `join` waits for the goroutine (call outside sections); events after close are dropped | race run: `-race -count=5` ok

## (e) New exported identifiers

`Plugin`, `DiscoveryProblem`, `Discovery`, `DiscoveryOptions`, `DiscoverPlugins`; `InstallOptions`, `InstallDependencies`; `Platform`, `PlatformNode`, `PlatformBrowser`, `BuildOptions`, `BuildInput`, `CSS`, `Asset`, `BuiltKind`, `BuiltFacetScript`, `BuiltFacetFailed`, `BuiltFacet`, `Builder`, `BuildFacet`; `HostPackages`, `EvaluateOptions`, `Evaluator`, `ScriptFilename`, `PluginFile`; `PluginsState`, `MarkSnapshot`, `DiscoverySnapshot`.
Unexported (Pi's `PluginWatcher`, `WatcherOptions`, `FACET_EXTENSIONS`, `isPluginName`, `inside`): `pluginWatcher`, `newPluginWatcher`, `watcherOptions`, `facetExtensions`, `isPluginName`, `inside`. Watcher members used by the component (all unexported, Pi names): `know`, `used`, `snapshot`, `applied`, `installed`, `mark`, `close`, `join` (Go-only, see below), `needsDiscovery`, `discoverySnapshot`; JSON watchable via `Value`/`Deliver`/promoted `Watch`.

Go forced differences: `BuiltFacet` is a struct with `Kind` (`BuiltFacetScript`/`BuiltFacetFailed`) instead of a union; `Plugin.Facets` is a Go map (Pi's record keeps insertion = `Kinds` order: iterate the app's kinds in order and look up, do not range the map); `MarkSnapshot` carries `Names` for Pi's `Map` key order; `PublicPath` is `*string` (undefined vs `""`); `InstallOptions.NpmCommand` nil = undefined (npm), empty non-nil = refused; `ctx` is last as everywhere in Chord's Go port.

## How the component should use it

- Build the watcher inside the host's batch: `newPluginWatcher(watcherOptions{Roots, Base, Kinds, Failed}, batch)` where the batch has `UseMicrotasks(loop.Queue)` and `UseSection(loop.Run)` (the host's already does). Every method runs in a section; file events enter through `Batch.Section`.
- `Failed` runs in a section; wrap it into the `watch_failed` report.
- `close()` in the component's `Close()`; `join()` (outside sections) when it must drain.
- `Plugins.State()` is the watcher itself as a `state.SourceHandle` (its `Value()` is the JSON object `{dirty, install, set, base}`; `PluginsState.json()` is the encoder, a typed reader is not exported).
- A `Builder` and `Evaluator` come from the consumer; the plugins component owns `BuildFacet(builder, ...)` use. Test doubles: `referenceBuilder`, `referenceEvaluator` in plugins_seam_test.go (in-package, the component's tests can use them).

## Open questions

1. `isPluginName` (host/staging.ts exports it, "the plugins component checks it too"): the Go host keeps its regex unexported (`host_host.go: pluginName`). I copied the regex into the plugins package (same pattern); when the host lane exports `IsPluginName`, replace the copy.
2. Node reports an event with no file name (the watched directory itself removed/moved/chmod'd) as `join(dir, basename(dir))`; fsnotify reports the directory's own path. For a watched root or base directory that is removed Pi marks `set` or `base` (by that accident), the Go port marks nothing. I did not port the translation because I could not run Node on Linux to confirm the libuv behavior (`uv__basename_r` for nameless events): `[INFERENCE]`. Decide whether to port it.
3. After `close()` the Go watcher never watches again (a later `know` does not re-add watches); Pi has no closed flag and a `know` after `close` would add watches again (a leak). Behavior difference intended (resource guard), observable only after `close`.
4. No test for `watch_failed` (`failed`): no upstream case and no portable way to make fsnotify's Add fail other than ENOENT in a test container.
5. Error texts that embed `JSON.parse` messages (`package.json: <message>`) carry Go's `encoding/json` message; only the `package.json: ` prefix is common (the upstream test matches the prefix only).
6. A `Builder` is trusted to return `BuiltFacet{Plugin, Facet}` set to the plugin and kind it built; `BuildFacet` does not rewrite them.
7. fsnotify v1.5.4 on macOS (kqueue) opens a descriptor per file of a watched directory; Linux (inotify, the CI container) is one watch per directory as upstream. The `node_modules`/`.git` exclusion keeps the large trees out on both.
8. foundA/state: nothing needed; only `state.Source`, `Batch.Section`, `Batch.Soon`, `Source.Publish/Changed/Watched/DependentSet` are used.

## Second round: runtime.ts, plugins.ts, plugins.test.ts, runtime.test.ts, examples

Plugins, hosts and edits of these tests are written in a small facet language (plugins_seam_test.go, documented there) read by `referenceBuilder` and `dslEvaluator`, instead of TypeScript; upstream's layouts, edits, plugin names and expectations are kept. Contract names are `test.<dir>` (plugins.test) and `example.*` (examples) as upstream's.

upstream test file › case | disposition | PiG test
--- | --- | ---
runtime.test.ts › loads a set and leaves out a plugin whose build or evaluation failed, reported with the host's report | ported | plugins_runtime_test.go#TestLoadingAndReloadingWithCompleteSetsLoadsASetAndLeavesOut...
runtime.test.ts › reloads a complete set: same digests unchanged, a changed one applied, a loaded one not shipped removed | ported | ...#TestLoadingAndReloadingWithCompleteSetsReloadsACompleteSet...
runtime.test.ts › keeps the loaded facet of a plugin whose new build or evaluation failed; an added failing one is left out | ported | ...#TestLoadingAndReloadingWithCompleteSetsKeepsTheLoadedFacet...
runtime.test.ts › the runtime's imports › loads no esbuild | designed-out | an import hook run in a Node process; a PluginRuntime takes an Evaluator and has no Builder
(own) reload of a host the runtime did not load fails | added | ...#TestPluginRuntimeReloadOfAHostItDidNotLoadFails
plugins.test.ts › per plugin at startup › leaves out a plugin whose build fails, whose evaluation throws or whose setup throws; loads the others and core | ported | plugins_plugins_test.go#TestPerPluginAtStartupLeavesOut...
plugins.test.ts › per plugin at reload › rebuilds one plugin of three | ported | ...#TestPerPluginAtReloadRebuildsOnePluginOfThree...
plugins.test.ts › ... › keeps the loaded facet of a plugin whose build fails or whose evaluation throws | ported | ...#TestPerPluginAtReloadKeepsTheLoadedFacet...
plugins.test.ts › ... › adds a plugin discovered since, removes one no longer there | ported | ...#TestPerPluginAtReloadAddsAPluginDiscoveredSince...
plugins.test.ts › ... › keeps a consumer edited together with a provider whose new build fails (the closure rule through builds) | ported | ...#TestPerPluginAtReloadKeepsAConsumerEditedTogetherWithAProvider...
plugins.test.ts › rollout › runs the steps in order ... / counts a host's error ... (D12) / keeps dirty a plugin whose built kind no step shipped / stops at a step that rejects | ported (4 cases) | ...#TestRollout...
plugins.test.ts › current sets › scripts(kind): built on first use ... | ported | ...#TestCurrentSetsScriptsKind...
plugins.test.ts › installs through the component | ported (fake npm is the sh script of install_test) | ...#TestInstallsThroughTheComponent...
plugins.test.ts › GC › collects old facets over 200 reloads; the heap stays flat after warm-up | ported: `runtime.ReadMemStats` heap after GCs (< 2 MiB growth) and weak pointers for the made states | ...#TestGCCollectsOldFacetsOver200Reloads...
plugins.test.ts › end to end: a server and a worker | ported (real memory link, remote `Notes`, a count document) | plugins_endtoend_test.go#TestEndToEndAServerAndAWorker...
plugins.test.ts › fix round 1 › serializes reloads of one host | ported (two goroutines; each report keeps both changes whichever runs first) | plugins_plugins_test.go#TestFixRound1SerializesReloadsOfOneHost...
plugins.test.ts › fix round 1 › applies the removals of the build it was given | ported | ...#TestFixRound1AppliesTheRemovals...
plugins.test.ts › fix round 1 › adopts what a reload committed when its wait aborts after the commit | adapted: upstream spies `batch.commit` of the host; the Go host has no such hook, so the runtime's half (adopt of `details.committed`) is tested with a fake host returning the `aborted` error (plugins_runtime_test.go#TestRuntimeAdoptsWhatAReloadCommitted...), and the real host's abort after its commit is exercised by example 36 part 4 | both
plugins.test.ts › fix round 1 › leaves a plugin out of a host whose kind its rebuilt facets no longer have | ported | plugins_plugins_test.go#TestFixRound1LeavesAPluginOut...
plugins.test.ts › fix round 1 › reloads a structured clone of a build as the build itself | ported: a copy of the `Built` struct (`clone := *built`) | ...#TestFixRound1ReloadsACopyOfABuild...
plugins.test.ts › fix round 1 › reads a changed package.json again at the next build | ported (the new package in `node_modules` is esbuild's resolution and is not written) | ...#TestFixRound1ReadsAChangedPackageJSONAgain...
plugins.test.ts › the reload queues and garbage collection › lets a rejected new facet go once its report is dropped | ported (weak pointers) | ...#TestTheReloadQueuesAndGarbageCollection...
examples/35-plugins | ported | plugins_examples_test.go#TestExample35PluginsLoadEditMarkBuildRollOut
examples/36-plugin-failures | ported (mapped JavaScript stacks, `mapStack`, are designed-out: failures asserted by message; §4's abort window is a microtask race in Pi (attempt.ts settles the swap's first check at the first parked check; host.ts awaits exactly it), which a Go watch on its own goroutine cannot order: the test asserts what holds either way (commit stands, `aborted` names it when returned, a later reload keeps it), the abort branch is the fake-host runtime test) | ...#TestExample36PluginFailures
examples/37-plugin-imports | designed-out | esbuild's import rules and refusals, `?url` assets, JSON imports, native and CommonJS packages, `fake-npm.mjs` (the Builder seam); the install through the component is covered by TestInstallsThroughTheComponent...
examples/38-watch-and-rollout | ported | ...#TestExample38WatchAndRollout
examples/39-browser-build | designed-out | esbuild's browser build: stylesheet and assets (`CSS`, `Asset` are the data a Builder fills)

Expected values of examples 35/36/38 are derived from Pi's code (`watcher.ts`, `plugins.ts`, `runtime.ts`): Pi's examples could not be run (Node on macOS only; FSEvents' events differ from Linux's inotify, and the output of the unmodified script on this machine was not the example's). Example 38 reproduces: marks after each edit, the three-step rollout reports, the stopped rollout, the failed plugin staying dirty, the edit during a build keeping its mark.

Test helpers that Go forces: file edits of the examples go through a temporary file and a rename and wait for the watcher's events (`edit`/`quiet`), because a write in place is several events and exact marks cannot be asserted after the first one; Pi's examples print the marks after the first event and are racy by design (`until`).

### Component notes (Go forms)

- `CreatePlugins(options, ctx) (Plugins, error)`; `Plugins` methods: `State() chord.Watchable[delta.JsonValue]`, `Marks() PluginsState`, `Discovery()`, `Build`, `Scripts`, `Current`, `Load`, `Reload`, `Step`, `Rollout`, `InstallDependencies`, `Forget(host)`, `Close()`. `Rollout` returns `(RolloutResult, error)` with the stopped step in `Stopped` (a rejecting step is a result, as upstream); the error is for the component's own failures (a Go forced channel).
- `Built` returned by `Build` is a `*Built`; its unexported `self` pointer and `snapshot` replace upstream's `WeakMap<Built, MarkSnapshot>`: a copy of the struct has another address and clears no marks.
- `PluginsOptions.Kinds` is a slice of `KindDefinition{Name, Platform, HostPackages}` (upstream's record keeps its order; a Go map does not). Facet files per plugin are `Plugin.Facets[kind]`; iterate the kinds in the options' order.
- Concurrency (AGENTS.md async parity): `Build` runs one build per facet concurrently (`Promise.all`: owned, joined, results by index, the first error by index, a Builder panic is a failed result) and calls the Builder from several goroutines; `Reload` of one host are serialized in the order their sets were decided (a ticket taken in the same section that applies the build and reads the set); everything else of the component is touched in sections of its own loop.
- `Forget(host)` (Plugins and PluginRuntime): Go has no weak table keyed by a host; a closed host is forgotten by its owner.
- New exported identifiers of the second round: `PluginScript`, `RuntimeLoad`, `PluginRuntime`, `NewPluginRuntime`, `KindDefinition`, `PluginsErrorKind` (+ `PluginsListenerError`, `PluginsDeliveryFailed`, `PluginsWatchFailed`), `PluginsError`, `PluginsOptions`, `Built`, `RolloutHostKind` (+ `RolloutReported`, `RolloutStopped`, `RolloutErrored`), `RolloutHost`, `StepResult`, `RolloutStep`, `RolloutStepResult`, `StoppedStep`, `RolloutResult`, `PluginsLoad`, `Plugins`, `CreatePlugins`.

### Async contracts (second round)

item | contract | evidence
--- | --- | ---
`PluginRuntime.reload` queue (`state.reloading.then`) | blocking `Reload`; queued in call order per host by a ticket (`queue`), the ticket's turn waits for the previous reload to end (not for ctx); a rejected reload does not break the chain | TestFixRound1SerializesReloadsOfOneHost..., mutation: no wait on the previous turn fails it
`plugins.reload` (`ship` + `runtime.reload` in one tick) | `ship`, the set's copy and the ticket are one section | same
`plugins.build` `Promise.all` | `buildAll`: one goroutine per facet in a `sync.WaitGroup`, results by index | every build test
`plugins.rollout` | steps run in order, each after shipping its kinds; a step's error stops it (result, not error); marks applied once after the last step, from the snapshot of the built pointer | TestRollout..., TestExample38...
`committed` after an aborted reload | the runtime adopts `details.committed.failed` of the host's `aborted` error | TestRuntimeAdopts..., TestExample36 part 4 (real host)

### Open questions (second round)

1. Pi reports the plugin left out at startup (example 36's weather, whose setup throws) failed again by every reload, since the runtime passes its script again and the host stages its throwing setup again; the Go port does the same (asserted in example 36). Confirm this is what Pi does (it follows from `runtime.ts` `reload`: loaded has no entry for it, so it is `prepare`d and passed as an added plugin).
2. `Plugins.State()` is a `chord.Watchable[delta.JsonValue]` of the component's own loop; its `Watch` opens the watch in a section. The typed reader is `Marks()`.
3. Tests that need the host were run against a snapshot of the working tree's host with one line of `host_reference.go` (`ReferenceKeyed.Get`, line 1171) patched locally, because the worktree's host did not compile (the patched copy is /tmp/kfold/plugA-live; the worktree is untouched).

---

# Part: Host: reference side (from kfold-kref.md)

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

---

# Part: Host: instance side (from kfold-kinst.md)

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

---

# Part: Host: staging, reload, host API, support, examples (from kfold-kreload.md)

# K-fold KReload rows (staging, host, facet, root, destination, report types, test support, host tests, examples 12/33/34, reload bench)

## (a) upstream src files
chord/next/host/staging.ts | ported | host/host_staging.go | test:chord/next/host/host_staging_test.go#TestStagingAThrowKeepsThePluginOnItsOldCodeReported (+ the per plugin cases) | stagePlugin, addStaged, Staged (`staged`), StagedPlugin, Recorded, recordFactory + recording self, migratesTables, IsPluginName (exported: the plugins package needs it). Pi's `Map` order is `namedMap`. Forced differences below.
chord/next/host/host.ts | ported | host/host_host.go | test:host_host_test.go#TestHostReload* , #TestHostLoadLeavesOut... | load per plugin (LoadReport), reload (staging, closure fixpoint, one commit, fates, report), CommittedReload, rewait, bind's anonymous refusal, unboundCause via data.Binds
chord/next/host/facet.ts | ported | host/host_facet.go | test:host_facet_test.go | ServeOptions.Accepts; a Facet carries an identity (see (e))
chord/next/host/root.ts | ported | host/host_root.go | test:host_root_test.go#TestHostRootRunsAnonymous..., #TestHostRootRefusesAnAnonymousScope... | no linked signal: the scope's own cancel + one AfterFunc on the caller; RootOptions.Anonymous (same form as lane NHttp)
chord/next/host/destination.ts | ported | host/host_destination.go | test:host_staging_test.go (closure rule, host address) | placedHere
chord/next/host/README.md | ported (intent) | host/doc.go | - | "Plugins, load and reload" section
chord/next/host/index.ts, internal.ts | designed-out | - | - | barrels
test/next/support.ts | ported | host_next_support_test.go | - | loaded/loadedPlugins return the failed plugins (hostErrors.Failed), relayed.Route (already), mustLoad

## (c) upstream tests
host.test › load › leaves out and reports invalid and duplicate plugin names ... | ported | TestHostLoadLeavesOutAndReportsInvalidAndDuplicatePluginNamesAndDoubleServesTheLaterRefusesASecondLoad
host.test › principals › a host without a principal / an address serves no remote type | ported (report) | TestHostPrincipals...
host.test › a load whose setup throws (per plugin) | ported | TestHostALoadWhoseSetupThrowsLeavesThatPluginOut...
host.test › reload: the API (7 cases) | ported | TestHostReloadTheAPI*
host.test › reload: the commit (2 cases) | ported | TestHostReloadTheCommit*
host.test › reload: registries' older readers | ported | TestHostReloadRegistriesOlderReaders...
host.test › reload across hosts, in the app's order | ported | TestHostReloadAcrossHostsInTheAppsOrder...
host.test › reload: an abort while staging | ported | TestHostReloadAnAbortWhileStaging...
staging.test (all cases) | ported | host_staging_test.go
facet.test › contribute at most one value per registry | ported | TestFacetsContributeAtMostOneValuePerRegistryOnlyWhileTheirSetupRuns
root.test › 2 new cases | ported | host_root_test.go
examples 12 (comment only), 33-reload, 34-versions | ported | TestExample33Reload, TestExample34Versions
bench/reload.ts | ported | bench_reload_test.go#BenchmarkReload
bench/links.ts, store.ts | not ported here (links bench measurement method; store bench is Durable's) | -

## (d) async contracts
host.reload serialization | `reload` chains behind the previous reload's end (a once-only promise), which Go waits on outside a section; the tail settles whatever the reload's result | test:TestHostReloadTheAPIRunsReloadsOneAfterAnother
reload abort | before the commit: `aborted`, nothing changed; after it: the wait for the report (select on every swap/retry's `checked` and ctx) ends `aborted` with details.committed | test:TestHostReloadTheAPIAnAbortBeforeTheCommit..., #AnAbortAfterTheCommit...
load/reload entry | blocking calls, entering the loop themselves (never inside a section)

## (e) new exported identifiers
host: LoadReport, PluginFailure, ReloadOptions, ReloadReport, PluginChanges, ReloadedObject, ReloadedObjectKind (ReloadSwapped, ReloadRestarted, ReloadEnded, ReloadParked, ReloadRetried), CommittedReload, Host.Reload, Host.Load now returns (LoadReport, error), IsPluginName, ServeOptions.Accepts, RootOptions.Anonymous, HostError.State, Facet identity (DefineFacet only)

## Forced differences
- A Facet is compared by identity (Pi `===`): Go func values are not comparable, so DefineFacet allocates an identity every copy shares; a Facet literal has none and is never the same.
- A recording self's handles are typed inert values, not a Proxy; a Reference made by `self.Get` is a never-bound binding's, so `.Member(...)` and `Derive` over it work; calling the methods of a zero `*documents.Singleton`/`*store.TableHandle` while declaring panics (the plugin fails staging).
- Declarations panic (Self returns no errors) and the factory runs under tryPlugin.
- "abort lands right after the commit" test: Pi spies `batch.commit`; Go cancels the ctx from the swapped object's second factory run (the first is staging's), which runs in the commit's section.

## bench rows
test/next/bench/links.ts | unchanged-observable | - | - | measurement-method change only (warm-up commits, median/p90, burst medians): no host behavior; the Go link benches (bench_links_test.go) already report per-op costs via testing.B
test/next/bench/store.ts | designed-out | - | - | Durable's `commitWrites` helper in the Durable store bench; no Go counterpart in this package

## report ordering (example 36 §4)
Host.reloadReport: after every check settled, runs the host's batch deliveries (`enter; scheduler.Wait; enter`) and then reads ctx, standing for Pi's >= 3 microtask ticks between a check's settle and the report; a watcher that aborts the ctx from its callback wins deterministically.

---

# Part: Host: jobs and plugins benchmark (from kfold-kjobs.md)

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

---

# Part: Host: remote (from kfold-krem.md)

# KFold.KRemote rows (Pi e7a0df27 -> 6d4b7443, `src/next/remote`)

## (a) Upstream source files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| remote/hold.ts | ported | remote_hold.go | test:chord/next/host/remote_hold_reload_test.go#TestHoldsAcrossAReload | `callable`, `contract`, `#request`, `retry(hops)`, `moved` (rebind / unbind / follow the instance), `#unbind`, `#rebind`, `removed`, `retarget`/`following`, boundEntry at the hold's version (`liveMember`), `bindTo` at `VariantsAt`, `method` by `previous` names |
| remote/consumer.ts | ported | remote_consumer.go | test:remote_remote_object_test.go (retry hops 16), remote_hold_reload_test.go | `retry(hold, hops)`; `failed` rejects the hold's calls still waiting for a slot |
| remote/host-link.ts | ported | remote_host_link.go | test:remote_host_link_test.go#TestHostLinkOriginsOnlyFromRelays | origin on a child link is a protocol error `<kind>: an origin on a child link`; `drop(provider)` |
| remote/provider.ts | ported | remote_provider.go | test:remote_provider_test.go#TestProviderOrigins, remote_host_link_test.go | `settle`, `unbound`, `callable`, flush skips unbound holds |
| remote/relay.ts | ported | remote_relay.go | test:remote_relay_test.go#TestRelayIDsAndOrigins | retry forwarded with its hops as they are (`unreachable` was done by the lead) |
| remote/remote-object.ts | ported | remote_remote_object.go | test:remote_remote_object_test.go | `followed`, `attachable` (`attachedVariant` on the binding), `instance` nil, `keyedMapping`, `retry(walk)` with `walk.hops > 0`, `failed` no longer ignored when live, `move` retargets in a batch, `heldOnLink`/`lastValues` removed, no `dependencyMoved` on attach/drop |
| remote/internal.ts | designed-out | - | - | barrel |
| remote/path.ts | ported (lead) | remote_path.go | test:remote_path_test.go | not redone |

## (c) Upstream test cases

| case | disposition | PiG test |
|---|---|---|
| hold.test.ts retry messages carry `hops: 16` | ported | remote_hold_test.go (the retry cases) |
| hold.test.ts retries across links: the hop budget | ported | remote_hold_test.go#TestHoldRetriesAcrossLinksQuiesceAtTheirBudget |
| hold.test.ts holds across a reload (nine cases) | ported | remote_hold_reload_test.go#TestHoldsAcrossAReload |
| hold.test.ts holds across more than one swap | ported | remote_hold_reload_test.go#TestHoldsAcrossMoreThanOneSwap... |
| hold.test.ts a hold after its object's live failure | ported | remote_hold_reload_test.go#TestAHoldAfterItsObjectsLiveFailure... |
| hold.test.ts a hold across a swap whose mapping fails | ported | remote_hold_reload_test.go#TestAHoldAcrossASwapWhoseMappingFails... |
| host-link.test.ts origins only from relays (7 + 2 cases) | ported | remote_host_link_test.go#TestHostLinkOriginsOnlyFromRelays |
| provider.test.ts origins at the provider (a relay's: on its parent link) | ported | remote_provider_test.go (`notesServer`/`slowConsumer` with a parent link) |
| relay.test.ts retry hops forwarded; reconnect without timer; race 13; parent link down (route); origin gone -> `closed` | ported | remote_relay_test.go |
| remote-object.test.ts bound diff frame; retry hops 16; watch waits through a failure; parked_long when dropped | ported | remote_remote_object_test.go |
| bench/links.ts warm-up commits, mean/median/p90, 1 MiB median/p90 | ported (measurement) | bench_links_test.go |

## (d) Async contracts

- `providerSide.settle` / `hostLink.drop`: synchronous in the section of the message; `drop` calls `closed` of the side (aborts its calls, releases its holds). Evidence: TestHostLinkOriginsOnlyFromRelays.
- `provider.unbound`: rejects in-flight served calls with the cause: `call.abort(err)` then the error reply, once per slot (`reply` deletes the call first). Evidence: "a hold unbound while the window is full drops its pending ops".

## (e) New exported identifiers

None (all unexported).

## Forced differences / open questions

- `HostFacet`-based `Reload` returns `(ReloadReport, error)`; tests ignore the report except where Pi does.
- host-link.test.ts "keeps no record of an origin that holds nothing": the heap-growth bound (4 MiB) is checked only when the binary is not built with `-race` (`remote_race_test.go`/`remote_norace_test.go` `raceDetector`): the detector's runtime keeps freed memory past a collection (measured 15-45 MB under `-race`, within the bound without it). The structural check (no provider side left on any link) runs always.
- hold reload tests wait for the activation Pi's event loop drains in `transported()` (a restart's new attempt) with a polling `rhWaitFor` on the observable state; `rhLinks` also waits `untilEnded` and `deliveredOn`.
- test goroutine writes to a host writable go through a section of the host's loop (`rhSet`), as Pi's call from the event loop.
- Tests ran via `/tmp/kfold/kreload.sh test -race -count=3` (scratch copy with other lanes' Load call sites patched): ok.
- bench: Pi's median/p90 over bursts is reported as `testing.B` metrics (`median-ms`, `p90-ms`).
