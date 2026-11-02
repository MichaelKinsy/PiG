# plugfull-store: difference 3 (persisted documents and tables)

| | |
|---|---|
| Pi | `createHost({ store })`: `openStore(sqliteBackend(await openNodeSqliteDatabase(path)))` (examples 20-tables.ts, 21-persisted-documents.ts); the app closes the store after the host; one open host per store; a `storage_failed` is reported once and closes the host (`host.test.ts` "the host's store"). |
| PiG before | The Node host had no store: a persisted document failed to activate with `persist needs a store`. |
| PiG now | `RuntimeLoad.Store`/`PluginsLoad.Store` (`StoreOptions{Path}`, plugins_node_store.go) are forwarded on `load`; `node/store.mjs` opens Pi's store over the vendored `sqlite/node.ts` (+ `sqlite/database.ts`, added to the vendor script's entries; manifest regenerated); `close` closes host then store; `storage_failed` reaches `NodeOptions.OnError`. A host loaded without Store keeps `persist needs a store`. Design and the reason the Go store is not shared: plan doc section 8 "The store" and 10.5. |

## Tests (chord/nextnode/plugins/plugins_node_store_test.go)

- `TestStoreAPluginsPersistedDocumentsAndTableSurviveClosingTheNodeHost`: a facet with a persisted singleton and keyed document and a table (examples 20 and 21 in one type); values survive `NodeHost.Close` and a new NodeHost on the same path.
- `TestStoreAFacetWithAPersistedDocumentFailsToActivateWhenTheHostIsLoadedWithoutAStore` (replaces `TestRuntimeAFacetWithAPersistedDocumentFailsToActivateTheHostHasNoStore`).
- `TestStoreWithoutAPathIsRefused`.
- `TestStoreAFailedStoreReachesOnErrorOnceAndClosesTheHost`: another connection holds the file's write lock; the store's `BEGIN` fails after SQLite's busy timeout; one `storage_failed` whose error says "locked".
- `TestStoreTwoNodeHostsOnOneFileAreTwoStoresAsInPi`: oracle run in Node on Pi's source (two `openStore`+`createHost` pairs on one file): the second reads the first's commit, its write is refused `unavailable` "the store refused the commit (UNIQUE constraint failed: chord_documents.id)" and `object_failed` is reported; PiG asserts the same.
- `TestNodeVendoredFilesAreExactlyTheManifests` passes with the two new vendored files.

## Red (before node/store.mjs and the vendored sqlite files; the Go option already forwarded)

```
--- FAIL: TestStoreAPluginsPersistedDocumentsAndTableSurviveClosingTheNodeHost
    plugins_node_store_test.go:85: probe write: test.notebook: test.notebook: state "tags": persist needs a store
--- FAIL: TestStoreAFailedStoreReachesOnErrorOnceAndClosesTheHost
    plugins_node_store_test.go:143: probe write: test.notebook: test.notebook: state "tags": persist needs a store
```

(`TestStoreTwoNodeHostsOnOneFileAreTwoStoresAsInPi` needs the store to exist at all; it was written after the driver.)

## Commands

`ssh xd670 ... run.sh --tree pf-store --image tern-ci:v6 -- go test -race -count=1 -run "TestStore|TestNodeVendored" ./chord/nextnode/plugins/`
