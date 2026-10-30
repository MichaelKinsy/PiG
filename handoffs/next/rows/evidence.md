# Rows of lane `evidence` (unit evidence for B1 ai/catalog and D1-D3 chord/src/next) for the integrator

Branch `fix/follow-evidence` on `fix/follow-pi-next` `ba33bfe21a`. Delete after transcribing. `evidence.json` carries the (c) test rows (see below).

## (a) upstream files

No upstream file changes disposition. The 126 ported PORT_MAP rows of `packages/ai/catalog/**` (55) and `packages/chord/src/next/**`, `next-node/compression.ts` (71) gain evidence `unit:` entries from `test/parity/unit-evidence/follow-b1.json` and `follow-d123.json`; every record names a mutation that was run and turned the named test red. `make coverage`: untested 130 -> 4 (the 4 at the 0.6.0 base: oauth/trusted-url.ts, utils/streaming-json.ts, chord delta/pending.ts, durable harness/partial-changes.ts).

## (b) PORT_MAP rows
None.

## (c) tests (see evidence.json)
- `store/columns.test.ts` 'row types > types rows with optional opt(...) fields': designed out under the type-level class; Go mechanism: compile-time assertions + runtime check in `TestEvidenceStoreAColumnsOptionalRowType`. OWNER DECISION NEEDED: the Go API has no pointer/option column type (Row is `map[string]any`, an optional column is a `ColumnSchema` made by `Opt` with `Optional` set), so the assertions pin that shape, not a pointer type.
- `store/table-migration.test.ts` 'the chunk yield' (2 cases): ported as deterministic tests through an unexported yield seam (`prepareTables(..., yield)`; `PrepareTables` passes `runtime.Gosched`). Red with the yield removed.

## (d) async contracts
Chunk yield between migration chunks: contract unchanged (one yield between two chunks, none after the last), now evidenced by the two tests above.

## (e) interfaces
None (the seam is unexported).
