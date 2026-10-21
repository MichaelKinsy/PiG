### Changed

- `make divergence-quality` now fails a `docs/extension-api-parity.md` row that calls an extension capability Node-only (`Node only`, `Node runtime only`, `Go/Rust/Python have no ...`); the row says which SDK cells are `gap:<lane>` in `docs/extension-sdk-exports.md` instead. `test/parity/sdk-vectors/` and `automation/gen/generate-sdk-vectors.mjs` fix one format for the Pi-generated vectors the SDK helper ports are tested against, and `go run ./test/parity/cmd/sdksurface -check` validates the files (pin, case source, ledger export).

### Fixed

- The SDK surface scan no longer loses every Rust declaration after a raw string literal such as `r"\\?\"`; `with_file_mutation_queue` was reported missing from the Rust SDK. <!-- fix: e2d80347 -->
