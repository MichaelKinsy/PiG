# plugfull-callbacks: plugnode differences 4 and 7 closed (`chord/nextnode/plugins`, branch fix/follow-plugfull-callbacks)

Design: docs/plan/chord-plugins-node-evaluator.md 10.1 (call, sync), 10.4 (host packages), 10.6 (telemetry, principalKey, revokedBy). Commits: 565b2164d3 (difference 4), 57795268cb (difference 7).

## Difference 4: host packages

| | |
|---|---|
| Pi | `HostPackages` are module instances of the app's own process, handed to a plugin's `require`. |
| PiG now | `RuntimeLoad.GoPackages` / `PluginsLoad.GoPackages` take `GoPackage{Specifier, Exports map[string]GoExport}`; an export is `GoFunc(func(ctx, JSON) (JSON, error))` or `GoValue(JSON)`. The Node host makes an ES module per specifier (`node/gopackage.mjs`, `module.registerHooks`): functions are `async (args, context?) => result` calling Go over a `call` event (answered in any order; `context` is the request's chord Context or an AbortSignal: its abort rejects the call and cancels the Go ctx; the process's end cancels it too), a Go error is a thrown Error (a *data.ChordError a ChordError), values are parsed JSON. Plugins `require` it, app modules `import` it; `HostPackages` files keep working next to it. Identity and live state do not cross (stated in plugins_node_gopackage.go). |
| Tests | `TestGoPackageAPluginCallsAGoFunctionAndReadsAValue`, `...AnAppModuleImportsItBySpecifier`, `...AGoErrorIsAThrownError`, `...ACancelledCallRejectsAndCancelsTheGoContext`, `...TheAbortOfThePluginsRequestCancelsTheGoContext`, `...CallsRunConcurrentlyAndAnswerInAnyOrder`, `...TheProcessEndingCancelsARunningGoFunction`, `...ThatCannotBeOfferedFailsTheLoad` (plugins_node_callbacks_test.go) |
| Red (baseline bfbda3e54d + the tests + declarations only) | the failure text below |

Async contract of `call`: the caller (Promise) waits for its answer; calls are concurrent, each on its own goroutine, answers in any order; cancellation: an abort of the passed signal rejects at once with the signal's reason and sends `cancel`, the Go ctx is cancelled; a call aborted before it starts is never sent; process end cancels every running call and drops answers; errors: a Go error rejects the same call.

```
--- FAIL: TestGoPackageAPluginCallsAGoFunctionAndReadsAValue (0.64s)
    plugins_node_callbacks_test.go:113: probe greet: plugin "@test/cb" (worker): "@acme/go" is not a host package, a Node builtin or one of its chord.external packages
--- FAIL: TestGoPackageAnAppModuleImportsItBySpecifier (0.61s)
    plugins_node_callbacks_test.go:156: Cannot find package '@acme/go' imported from /tmp/TestGoPackageAnAppModuleImportsItBySpecifier2709567177/001/core.mjs
--- FAIL: TestGoPackageAGoErrorIsAThrownError (0.63s)
    plugins_node_callbacks_test.go:193: fail = map[chord:false message:plugin "@test/cb" (worker): "@acme/go" is not a host package, a Node builtin or one of its chord.external packages name:Error], want map[chord:false message:the Go side failed nam
--- FAIL: TestGoPackageACancelledCallRejectsAndCancelsTheGoContext (0.65s)
    plugins_node_callbacks_test.go:256: probe abort: plugin "@test/cb" (worker): "@acme/go" is not a host package, a Node builtin or one of its chord.external packages
--- FAIL: TestGoPackageTheAbortOfThePluginsRequestCancelsTheGoContext (15.64s)
    plugins_node_callbacks_test.go:294: the Go function never started
--- FAIL: TestGoPackageCallsRunConcurrentlyAndAnswerInAnyOrder (0.63s)
    plugins_node_callbacks_test.go:334: probe both: plugin "@test/cb" (worker): "@acme/go" is not a host package, a Node builtin or one of its chord.external packages
--- FAIL: TestGoPackageTheProcessEndingCancelsARunningGoFunction (15.63s)
    plugins_node_callbacks_test.go:349: the Go function never started
--- FAIL: TestGoPackageThatCannotBeOfferedFailsTheLoad (4.36s)
        plugins_node_callbacks_test.go:379: error = Cannot find module '/x.mjs' imported from /home/kinsy/.cache/pig/chord-plugins-node-945ee9e3b1a0b8cf/host.mjs, want it to contain "both a GoPackage and a HostPackages file"
```

## Difference 7: telemetry, principalKey, revokedBy

| | |
|---|---|
| Pi | `createHost({ telemetry, principalKey, revokedBy })` take the app's adapter and functions, in process. |
| PiG now | `RuntimeLoad`/`PluginsLoad` gain `Telemetry telemetry.TelemetryContext`, `PrincipalKey *data.PrincipalKeyFunction`, `RevokedBy host.RevokedBy[delta.JsonValue]`. Telemetry: the Node host's TelemetryContext is a proxy (`node/telemetry.mjs`) whose spans/metrics are ordered `telemetry` events replayed by Go into the adapter (`plugins_node_telemetry.go`); a span is an open callback of the adapter's `StartSpan` until the `end` event; `inject` of a span returns `{pig-node-telemetry: scope:id}`, which Go resolves to the adapter's own carrier (last 4096 spans kept; scopes resolve across processes); `extract` keeps the raw carrier and Go extracts it with the adapter; `NodeHost.ExtractTelemetry` is the Go form. Spans open at process end end with an error status. principalKey / revokedBy: synchronous calls on a second connection (worker thread, `Atomics.wait`; Go accepts it on the same unix socket / named pipe, token-authenticated; the Go handler's context makes NodeHost calls return `ErrSyncCallback`). `NodeHost.Revoke(r, ctx)` forwards `host.revoke`. |
| Benchmark | `BenchmarkNodeSyncCall` on xd670 (loaded box): uncached 57-121 us per call (> 20 us), so principalKey is kept in a bounded map (4096, oldest dropped) by canonical principal JSON: a hit is 0.43-0.53 us. revokedBy is never kept. |
| Tests | plugins_node_telemetry_test.go (`TestTelemetryASpansOfAPluginAreRecordedInTheAppsAdapter`, `...AThrowingSpanHasPisErrorStatus`, `...AForeignCarrierIsExtractedByTheAdapter`, `...LinksAndSettledSpansFollowTheRecorder`, `...ACarrierOfOneProcessResolvesThroughAnother`, `...ASpanOpenWhenTheProcessEndsEndsWithAnErrorStatus`); plugins_node_hostoptions_test.go (`TestPrincipalKeyOfTheAppIsTheNodeHostsForIdentities` ports host.test.ts "identity includes the principal key"; `...IsAskedOncePerCanonicalPrincipal`, `...MemoIsBounded`; `TestRevokeNodeHostEndsTheRevokedPrincipalsObjectsAndKeepsTheOwners` and `...AGoHostsRevokeAppliesTheNodeHostsRevokedBy` port revocation.test.ts "ends the principal's objects at once ... each hold gets gone forbidden", owner kept; `...AnErrorOfRevokedByIsReturned`; `TestSyncACallbackCannotCallItsNodeHost`, `...AnErrorOfTheGoFunctionIsThrownAndTheConnectionServesTheNext`) |

Async contract: `sync` blocks the Node host thread until the Go handler answered (no timeout: a pure function); requests are served one at a time in order on the worker's connection; the handler's ctx ends with the process; NodeHost calls with it return `ErrSyncCallback` and are never waited for. Telemetry events are replayed in channel order on the reader goroutine; `end` waits until the adapter settled the span.

Red failures (same method):

```
--- FAIL: TestPrincipalKeyOfTheAppIsTheNodeHostsForIdentities (0.66s)
    plugins_node_hostoptions_test.go:141: logs = ["guest" "guest;guest" "guest;guest;owner"], want ["guest" "guest" "guest;owner"]
--- FAIL: TestRevokeNodeHostEndsTheRevokedPrincipalsObjectsAndKeepsTheOwners (15.69s)
    plugins_node_hostoptions_test.go:216: the reference is live, want it ended
--- FAIL: TestRevokeAGoHostsRevokeAppliesTheNodeHostsRevokedBy (15.66s)
    plugins_node_hostoptions_test.go:236: the reference is live, want it ended
--- FAIL: TestRevokeAnErrorOfRevokedByIsReturned (0.66s)
    plugins_node_hostoptions_test.go:262: Revoke = <nil>, want the error of revokedBy
--- FAIL: TestSyncACallbackCannotCallItsNodeHost (30.76s)
    plugins_node_hostoptions_test.go:305: probe callSync: Cannot find module '/tmp/TestSyncACallbackCannotCallItsNodeHost868485742/001/chord-plugins-node-945ee9e3b1a0b8cf/callbacks.mjs' imported from /tmp/TestSyncACallbackCannotCallItsNodeHost8684857
    plugins_node_hostoptions_test.go:312: the synchronous callback never returned: a call to the NodeHost waited for the blocked process
--- FAIL: TestSyncAnErrorOfTheGoFunctionIsThrownAndTheConnectionServesTheNext (0.73s)
    plugins_node_hostoptions_test.go:334: callSync(test.fails) = <nil>, Cannot find module '/tmp/TestSyncAnErrorOfTheGoFunctionIsThrownAndTheConnectionServesTheN4198492711/001/chord-plugins-node-945ee9e3b1a0b8cf/callbacks.mjs' imported from /tmp/Test
--- FAIL: TestTelemetryASpansOfAPluginAreRecordedInTheAppsAdapter (0.68s)
--- FAIL: TestTelemetryASpansOfAPluginAreRecordedInTheAppsAdapter (0.66s)
    plugins_node_telemetry_test.go:91: spans = [], want [plugin.work plugin.child plugin.continued] (the adapter's recorder got the plugin's spans)
--- FAIL: TestTelemetryAThrowingSpanHasPisErrorStatus (0.68s)
    plugins_node_telemetry_test.go:153: statuses = [], want [{Status:error Error:0x18be59d284a0} {Status:error Error:<nil>} {Status:error Error:0x18be59d284c0}]
--- FAIL: TestTelemetryAForeignCarrierIsExtractedByTheAdapter (0.64s)
    plugins_node_telemetry_test.go:164: inject(extract(carrier)) = <nil>, want the carrier back
--- FAIL: TestTelemetryLinksAndSettledSpansFollowTheRecorder (0.66s)
    plugins_node_telemetry_test.go:184: plugin.second links = [], want [{Context:map[] Attributes:[{Name:why Value:after}]}]
--- FAIL: TestTelemetryASpanOpenWhenTheProcessEndsEndsWithAnErrorStatus (15.66s)
    plugins_node_telemetry_test.go:200: timed out after 15s waiting for the open span to start
--- FAIL: TestPrincipalKeyIsAskedOncePerCanonicalPrincipal (0.77s)
    plugins_node_hostoptions_test.go:180: the Go function was asked map[S1:3 S2:1] times per session, want map[S1:1 S2:1]
--- FAIL: TestPrincipalKeyMemoIsBounded (0.71s)
    plugins_node_hostoptions_test.go:207: probe memo: memoizedByPrincipal is not a function
```

(The Revoke / Sync / Telemetry red tests ran against declarations-only stubs of the new API; the tests that panicked or hung there before the helpers were hardened are the same tests as the green ones.)

## Commands (xd670, tern-ci:v6)

`go test -race -count=1 ./chord/nextnode/plugins/` (green), `go vet` and `GOOS=windows go vet` of the package, `go fix -diff`, `make lint-changed` (0 issues), `make port-lint` (no finding in the new files; the repository's baseline already lists others), see the report for the final gates.
