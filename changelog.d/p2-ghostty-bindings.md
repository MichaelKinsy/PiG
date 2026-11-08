### Added

- `apps/p2/plugins/terminals/ghostty` binds libghostty-vt (the vendored `ghostty-vt.wasm`, byte-identical to Pi's) through wazero, the Go form of Pi's `ghostty.ts`: `Load`, `Terminal`, and `VtTerminal` with `Write`, `Resize`, `Snapshot`, `Frame`, `EncodeKey`, `EncodeMouse`, `EncodePaste`, scrolling and colors. The module is compiled once per process, writes allocate nothing, and a corpus recorded from Pi's bindings (`testdata/gen.mjs`) pins every answer byte for byte. <!-- feat: p2-ghostty-bindings -->
