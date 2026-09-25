# Runtime-cell reload and operations

This page covers atomic replacement, crash quarantine/fission, diagnostics, and
the protocol boundary. Start with the
[runtime-cell overview](extension-runtime-cells.md).

## Atomic reload

`Host.Reload` treats a new extension set as a transaction:

1. resolve the requested extension configs/specs;
2. plan isolated and packed cells;
3. reuse or build cell artifacts;
4. start replacement cells beside current cells, for unchanged extensions too
   (upstream reload invokes every extension factory again);
5. complete per-extension protocol registration;
6. validate identities/contributions and construct the replacement registry;
7. atomically publish the new registry;
8. stop old cells only after the swap.

Like upstream reload, every extension factory runs again even when its source and cached artifact are unchanged. Each extension loads on its own. An extension that fails to resolve, build, start, or register is not loaded: its previous runtime stops, and `ReloadReport.Issues` records `<path>: Failed to load extension: <error>`, which `/reload` lists under Extension issues. Every other extension loads. When a packed cell fails, each member is staged again in its own isolated cell, so only the failing members are reported. Only a config loader failure fails the whole reload.

## Quarantine and fission

A packed process can fail without identifying the member at fault. Pig records
that packed composition as quarantined. On a later reload, the planner fissions
its members into isolated cells:

```text
packed cell: A + B + C
        crash/quarantine
next plan:  A | B | C
```

A member that still fails can then be isolated without taking healthy members
down. Quarantine is host runtime state and never changes the author's source.

## Reload report

`Host.LastReloadReport()` returns the last placement transaction.
`/reload --explain` renders the same facts for a user.

| Fact | Examples |
|---|---|
| strategy/key/members | packed or isolated placement |
| reason | source command, shared factory, quarantine/fission |
| artifact | resolved runner path |
| cache/build | hit or cold-build duration |
| result | success/failure and wall duration |

## Protocol boundary

Every extension, including each member of a packed cell, uses the same current
wire contract over its own connection. The wire has no independent version. Required behavior includes:

- registration with stable extension identity;
- request/response correlation;
- cancellation and shutdown;
- host calls and results;
- tool updates and UI notifications;
- frame-size enforcement;
- startup/register timeouts;
- error propagation without crashing the host.

Packed mode must be observationally identical to isolated mode. Fix the host or
number a divergence when a conformance difference appears; do not change SDK
semantics to hide it.

## Oversized results and panics

The host catches extension-call panics at the extension boundary and returns a
structured internal error when a response is possible. Results above the
protocol frame limit are replaced with a small `result_too_large` error so the
host does not write a frame no compliant extension can read.

## Piglet component runtime

A Piglet build/release may populate neutral runtime inputs:

| Input | Runtime job |
|---|---|
| effective Piglet | provide immutable agent composition |
| component closure | expose exact prebuilt subprocess runners/assets to ordinary resolution |
| fused registrations | register build-time-linked compatible Go factories |

Stock Pig has no Piglet-specific registrations or closure. Runtime registration
remains generic and does not own Piglet schema or product transport.

## Operational boundaries

| Pig owns | Product/platform owns |
|---|---|
| placement/reload facts | org policy and quotas |
| cell build/cache/start | build-environment scheduling |
| registration/cancellation/errors | vulnerability/signature policy |
| quarantine/fission | publication approval |
| local reports | external telemetry collection/storage |

## Verification

- reload success swaps all registries together;
- a failed build/start/register drops only that extension and reports it;
- packed crash quarantines the exact composition;
- next plan fissions quarantined members;
- report getters return copies and are race-safe;
- oversized results return a small structured error;
- SDK conformance compares isolated, packed, and fused paths where applicable;
- stress tests cover reload/provider lifecycle and cancellation.

Primary sources/tests:

- `coding/extension/host/subprocess/host.go`
- `reload_cells.go`, `packed_quarantine.go`, `reload_report.go`
- `coding/extension/host/runtimecell/`
- `tests/extension-conformance/`
