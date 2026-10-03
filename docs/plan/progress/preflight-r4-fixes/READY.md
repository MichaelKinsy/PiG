# READY: preflight-r4-fixes

Branch: team/smc1/preflight-r4-fixes (base agg-100).

Commands run: `go test -race ./chord/delta/`, `go tool golangci-lint run ./chord/delta/...` (0 issues), `go run ./test/parity/cmd/sourcehygiene -full` (clean), `make source-hygiene`, `make interface-go-drift`, `make interface-inventory-drift`, `go run ./test/parity/cmd/testinventorycheck` (OK).

## Not green, by cause

- `test-porting-release` stays red on the `packages/chord/test/delta-tracker/tracker.test.ts` policy row until the owner approves the four remaining designed-out cases. The rationale says `SCRUTINIZED:proposed`; no approval was written.
- `make divergence-guard` reports magic-literal hits in `agent/harness/utils` and `internal/codingagent/tools`, none in files this lane changed.
- Flaky on this base, with or without this change: `durable/harness` `TestTaskGraphView` (task status `running` vs `pending` under package load; passes alone) and `internal/chord` `TestPublicDelivery/mutable_state_reports_rejected_callbacks...` (fails only under parallel load).

## C16 source hygiene

`make source-hygiene` scans only added lines, so it was clean. `go run ./test/parity/cmd/sourcehygiene -full` found four SH003 hits: `coding/extension/host/subprocess/host.go:4332` ("stub" to "shim"), `internal/toolchain/python.go:9` ("stub" to "shim"), `internal/chord/chordctx/chordctx.go:1` ("TODO" to "the context package's to-do root"), `internal/evals/report.go:183` ("todo" to "to-do"). The full scan is now clean.

## C15 delta-tracker/tracker.test.ts

Sixteen cases were designed out. The internal diff-based tracker cannot express draft behavior, but `chord/delta` is the overlay port with Proxy-like handles, so twelve cases are ported there in `chord/delta/tracker_upstream_test.go` (tests written first; the file did not compile until `Fill` and `CopyWithin` existed, and the rest went red against the old key order, sort and emitter). Code changes to reach green:

- `Object.Keys`: JavaScript own-key order (integer-like keys ascending, then base keys sorted, then keys added in write order; a key deleted and re-added counts as added). The write-order list no longer scans on every first write (`orderAt`).
- `Array.Fill`, `Array.CopyWithin`: by-value placements as upstream `fill` and `copyWithin`.
- `Array.Sort`: the comparator receives draft handles and runs outside the draft lock, so it can edit the draft. After the sort, slot overrides snapshot before the sort are restored, the sorted order replaces the first min(start length, end length) slots, and an element left in two slots becomes an independent clone.
- Dense-region folding (`denseRegions`): 256 or more edited elements with at most one gap fold into one splice, as `buildDenseRegions`.

Mutations that each turn a test red: region folding off, gap widened, `readded` ignored, integer-key ordering off, sort restore removed, duplicate normalization removed, `copyWithin` splice emptied. One survived by construction: `count*2 >= length` in the region test is always true when gaps are at most one (upstream has the same redundant check; kept for fidelity).

Not representable in Go, so those assertions are reduced to the draft view: the key order of a committed revision (a revision is a `map`), `Array.isArray`, property descriptors, null prototype.

QUESTION for the lead: four cases remain designed out and need `SCRUTINIZED:approved` from the owner. Each needs a JavaScript mechanism with no Go counterpart.

1. `forwards borrowed mutators to ordinary generic array receivers`: `Reflect.apply(push, receiver, ...)` rebinds `this` to a plain array. Go methods bind `*Array`.
2. `matches native structural coercion ordering for splice, fill, and copyWithin`: asserts when `valueOf` runs during argument coercion. Go evaluates arguments before the call and has no coercion hook.
3. `does not report inherited methods as own array properties`: asserts a prototype-chain property descriptor. Go has no prototype chain.
4. `defines own properties without invoking inherited setters`: installs a setter on `Object.prototype`. Go has no prototype chain.

Do you approve these four, or do you want a different Go analog for any of them?
