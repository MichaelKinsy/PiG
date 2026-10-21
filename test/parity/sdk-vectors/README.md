# Pi vectors for the SDK helper ports

A pure helper that Pi exports (`truncateToWidth`, `matchesKey`, `fuzzyFilter`, `calculateCost`, ...) is ported once per SDK (Go, Rust, Python). Each port is tested against vectors produced by Pi's own implementation at the pin, so a port that returns a constant, or a plausible but different answer, fails. Node serves Pi's own code, so it needs no vectors.

The vectors are the evidence `test/parity/sdk-exports.toml` counts as reaching a `native:` cell: an SDK test file that contains the path `test/parity/sdk-vectors/` and calls the symbol.

## Layout

```
test/parity/sdk-vectors/
  cases/<module-dir>/<export>.mjs   inputs (hand-written)
  <module-dir>/<export>.json        outputs (generated; never edit)
```

`<module-dir>` is the module with `/` as `-`: `pi-coding-agent`, `pi-tui`, `pi-ai`, `pi-ai-compat`, `pi-agent-core`. `<export>` is the export name as in the ledger (`truncateToWidth`); a class member is `Class.member`.

## Producing vectors

`cases/<module-dir>/<export>.mjs`:

```js
export const cases = [
  { name: "wide char at the edge", args: ["a\u4e2db", 2, "…"] },
];
// optional: reach a class member, or build an input an argument list cannot hold
export const call = (fn, ...args) => fn(...args);
```

`node automation/gen/generate-sdk-vectors.mjs` runs Pi's code from the pinned oracle (`python3 automation/pin/pin.py install-oracle` puts it in `extensions/sdk-ts/node_modules`) and writes the JSON. `--check` fails when a checked-in file differs, and `go run ./test/parity/cmd/sdksurface -check` fails a vector file whose pin is not the current upstream pin, that lacks a case source, that names no ledger export, or that is malformed. Refresh the files per upstream leap by re-running the generator.

## File format

```json
{
 "pin": "1.3.0+1402973a",
 "package": "pi-tui@1.3.0",
 "module": "pi-tui",
 "export": "truncateToWidth",
 "cases": [
  { "name": "...", "args": [ ... ], "result": ... },
  { "name": "...", "args": [ ... ], "throws": "message" }
 ]
}
```

Every case has a unique `name`, an `args` array, and exactly one of `result` (Pi's return value, awaited) or `throws` (the error message Pi threw). Values are plain JSON except these tagged objects, which every consumer decodes: `{"$":"undefined"}`, `{"$":"NaN"}`, `{"$":"Infinity"}`, `{"$":"-Infinity"}`, `{"$":"bigint","value":"123"}`, `{"$":"symbol","value":"description"}`, `{"$":"map","value":[[k,v],...]}`, `{"$":"set","value":[...]}`. Inputs must be JSON-encodable (no functions); use `call` to construct anything else from them.

## Consuming vectors in an SDK

Read the file by its repository path (`test/parity/sdk-vectors/pi-tui/truncateToWidth.json`; this exact prefix is what the ledger looks for), decode the tags, call the SDK symbol for every case and compare the result with deep equality, comparing numbers as JSON numbers and strings byte for byte. A case with `throws` expects the SDK to return its error type with the same message. Do not skip a case, and do not regenerate expected values from the port.

- Go: a `_test.go` in `extensions/sdk` (or a subpackage).
- Rust: a `*tests*.rs` under `extensions/sdk-rs`.
- Python: a `test_*.py` under `extensions/sdk-py/tests`.
