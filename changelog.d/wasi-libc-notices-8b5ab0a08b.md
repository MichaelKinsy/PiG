### Fixed

- Added the complete `wasi-libc` (wasi-sdk 30) license texts, component notices (`dlmalloc`, `emmalloc`, `cloudlibc`, `musl`, `musl-fts`), and SPDX REUSE annotations for the embedded `quickjs.wasm` and `quickjs-wasi` WebAssembly binaries in `THIRD_PARTY_NOTICES.md`, `LICENSES/`, `REUSE.toml`, and `codemode/assets/PROVENANCE.md`. <!-- fix: 8b5ab0a08b --> <!-- cause: THIRD_PARTY_NOTICES.md named wasi-libc without reproducing its license terms and both REUSE.toml annotations marked quickjs.wasm as MIT only -->
