### Added

- `apps/p2/plugins/terminals/ghostty` binds libghostty-vt (the vendored `ghostty-vt.wasm`, the same file as Pi's) through wazero, the Go form of Pi's `ghostty.ts`: `Load`, `Terminal`, and `VtTerminal` with `Write`, `Resize`, `Snapshot`, `Frame`, `EncodeKey`, `EncodeMouse`, `EncodePaste`, scrolling and colors. The module is compiled once per process, writes allocate nothing, and a corpus recorded from Pi's bindings (`testdata/gen.mjs`) pins every answer exactly. <!-- feat: p2-ghostty-bindings -->
