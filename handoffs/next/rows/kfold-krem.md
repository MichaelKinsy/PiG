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
