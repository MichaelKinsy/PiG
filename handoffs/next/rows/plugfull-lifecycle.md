# plugfull-lifecycle: differences #8, #9 and #11 of plugnode-differences.md

Branch `fix/follow-plugfull-lifecycle` (from `bfbda3e54d`). Designs: docs/plan/chord-plugins-node-evaluator.md 10.7, 10.8, 10.9. All three close; none is a numbered divergence. Reference machine for every run: xd670, container `tern-ci:v6` (Node 24.22.1), `-race`, 6 cpus.

## #11 lazy attach (closed)

| | |
|---|---|
| Pi | A forked worker `connect`s its parent link when it starts; its heartbeat (30 s) runs from then. |
| PiG before | The Node host `connect`ed at `load`; the app had to attach `NodeHost.Link()` within the heartbeat or the process ended. |
| PiG now | `NodeHost.Link()` is a wrapper (`nodeLink`); its first use by the Go host (`OnMessage` or `Send`) sends the `attach` control request, once, before the use; the host `connect`s then and its heartbeat starts then. An unattached host has no link and no clock, stays alive, and `Close` works; a link attached at any later time carries the host. `Close` and `OnClose` attach nothing. `RuntimeLoad.Heartbeat` stays for the attached link. |
| Tests | `TestNodeAnUnattachedHostStaysAliveWithNoClockAndClosesWhenAsked`, `TestNodeTheFirstUseOfTheLinkAttachesItAndStartsTheHostsHeartbeat/{OnMessage,Send}`, `TestNodeALinkAttachedAfterManyHeartbeatWindowsCarriesTheHost` (replace `TestNodeALinkNotAttachedWithinTheHeartbeatEndsTheProcessAndTheLink`); `TestNodeALinkAttachedAfterLoadAndOtherRequestsStillCarriesTheHost` kept. |
| Red (before the change) | `--- FAIL: TestNodeAnUnattachedHostStaysAliveWithNoClockAndClosesWhenAsked`: `an unattached host ended after its heartbeat window: node subprocess exited`; `--- FAIL: TestNodeALinkAttachedAfterManyHeartbeatWindowsCarriesTheHost (62.08s)`: `test.worker: the link closed before its handshake`. |
| Commands | `go test -race -count=1 -run 'TestNode|TestEndToEnd' ./chord/nextnode/plugins/`; `go test -race -count=1 ./chord/nextnode/plugins/ ./chord/nextnode/workers/` (both ok). |

## #8 crash and exit (closed; the isolation itself is strictly additive and stays)

| | |
|---|---|
| Pi | In process: an uncaught exception is printed by Node and ends the app with code 1; `process.exit(n)` ends it with n; an infinite loop stalls it. |
| PiG before | Nothing was printed (the host's `uncaughtException` handler replaced Node's); `NodeExit` had no code or signal; a stalled host was killed after a fixed 3 s with no reason. |
| PiG now | An uncaught exception or rejection is printed on the process's stderr as Node prints it (`util.inspect` text, positions mapped through the plugin source maps, blank line, `Node.js <version>`; a thrown primitive with Node's hint; Node's source-line excerpt is not printed), then `fatal`, then exit 1. Unhandled rejections are Node's default (`throw`), so the same. `NodeExit.Code` (`process.exit(7)` is 7; -1 when signalled), `NodeExit.Signal` (`SIGKILL`), `NodeExit.Reason` (the Go side killed it because its link closed and it did not exit in `NodeOptions.StopGrace`, default 5 s: Pi's workers' `GRACE`). A stalled plugin ends through the Go host's heartbeat on the attached link (the host closes the link, the process is killed after the grace). |
| Tests | `TestNodeAnUncaughtExceptionIsPrintedOnStderrWithItsMappedStackAndEndsWithCode1`, `TestNodeAThrownStringIsPrintedAsNodePrintsIt`, `TestNodeAnUnhandledRejectionIsAnUncaughtExceptionAsNodesDefaultMakesIt/{an_Error,a_string}`, `TestNodeAPluginsProcessExitEndsOnlyTheHostWithItsCode`, `TestNodeAPluginsInfiniteLoopEndsWithTheGoHostsHeartbeatOnTheLink` (Pi's in-process behaviour in each comment; Node's text verified against `node -e`). |
| Red | `stderr = ""` for every print assertion (e.g. `want "just text\n(Use `node --trace-uncaught ...` ..."`), `exit = node subprocess exited: exit status 7, want code 7, no signal, no fatal, no reason`, `exit = node subprocess exited: signal: killed, want SIGKILL, code -1 and a reason that names the link`. The new fields and `NodeOptions.StopGrace` were declared (zero) first so the tests compile. |

## #9 start latency and ordered frames (closed)

Measured with `BenchmarkNodeStart` (`go test -run '^$' -bench BenchmarkNodeStart -benchtime 15x ./chord/nextnode/plugins/`), ms to `started` / to `load`:

| tree | V8 compile cache | started | loaded |
|---|---|---|---|
| cold | none (before the change) | 653 | 662 |
| cold | `enableCompileCache` | 713 | 721 |
| warm | none (before the change: 629 / 638; env-disabled: 617 / 625) | 617 | 625 |
| warm | `enableCompileCache` | 313 | 321 |
| warm, a spare process taken by `Load` | `enableCompileCache` | | 8 |

`node --version` discovery is 5.5 to 7 ms (inside `started`).

| | |
|---|---|
| Levers | compile cache in `host.mjs` (`<tree>/v8`); `PluginRuntime.Warm(ctx)`, `NodeOptions.KeepWarm`, `PluginRuntime.Close(ctx)`, `Plugins.Warm`, `Plugins.Close` closes the spare; ordered delivery: `host.DeliveryAwaiter` (implemented by `NodeHost.Link()`), `link.Channel.Idle`, and the wait after the report of `Load` and every reload. |
| Ordered frames: how | The reader hands each link frame to the link, which hands it to the Go host's channel, before the next frame is read, so the host's channel has handled every frame the process wrote before the reply once the reply is decoded, except a message that waits for an asynchronous inflate. The rest is the Go host's delivery scheduler; `nodeLink.delivered` runs the host's own `Reload` wait (queued sections, deliveries entered) after the report, plus `Channel.Idle` for the inflate. Additive hook outside the package: `chord/next/host/host_link_deliveries.go` and `Channel.Idle` (`chord/next/link/channel.go`); no `docs/additive-features.md` record was written because `chord/nextnode` has none (integrator's call). |
| Tests | `TestNodeTheSubprocessKeepsV8sCompileCacheInTheExtractedTree`, `TestNodeWarmStartsOneSpareTheNextLoadTakes`, `TestNodeKeepWarmStartsTheNextSpareAfterEachLoad`, `TestNodeRuntimeCloseClosesTheSpareAndRefusesLaterLoads`, `TestNodeRuntimeCloseWhileTheKeptSpareIsStartingLeavesNoProcess`, `TestNodeALoadDoesNotTakeASpareThatDied`, `TestNodeAFailedKeptSpareIsReportedAndTheNextLoadStartsItsOwn`, `TestPluginsWarmStartsTheSpareLoadTakesAndCloseClosesAnUntakenOne`, `TestEndToEndTheFramesAReloadCausedAreDeliveredWhenReloadReturnsAlsoWhenTheirPartsInflateAsynchronously`; `TestEndToEndAServerAndAWorker...` now asserts right after Rollout (no `frames.count`, no call as barrier). |
| Red | Compile cache: `the compile cache = [], open .../chord-plugins-node-950eaf3504682e4a/v8: no such file or directory`. Frames: `frames when Rollout returned = 0 frames, want the one with the new label (40960 bytes)` (the wait disabled). The Warm tests are of new API (red is the compile error `undefined: PluginRuntime.Warm`). `TestEndToEndAServerAndAWorker...` asserting at once passes `-race -count=100` also with the wait disabled (180 s, ok): the delivery goroutine wins that race on this machine, so it is not a red test; the contract is held by the wait and proven by the inflate test above. With the wait: `-race -count=100` ok (156 s). |
| Not done / by design | A `Load` never differs from a spare in `ExecArgv`: all process options belong to the runtime, so no comparison exists. A link the app wraps before handing it to the host hides `Taken` and is not waited for. |

## Gates (fresh clone on xd670, `tern-ci:v6`)

- `go test -race -count=3 ./chord/nextnode/... ./chord/next/...`: ok (plugins 285 s, host 217 s, workers 179 s, link 19 s).
- `make lint-changed LINT_BASE=bfbda3e54d` (the tree has no `main`): 0 issues after the goimports fix; `GOOS=windows go vet ./chord/nextnode/plugins/` clean; `go fix -diff ./chord/...` empty after one `errors.AsType` change (the only other diff of `go fix -diff ./...` is `third_party/jsonschema/lazy_meta_test.go`, not this slice's).
- `make divergence-consistency divergence-quality divergences-drift`: OK; `divergence-guard` needs `.upstream/current` (`make upstream-mirror`), not run. `make test-expectation-lint` is not a target of this tree. `make port-lint` reports stale baseline entries unrelated to this slice; its new findings in this slice (two `ctxbackground`) carry `//portlint:allow` with a reason.
