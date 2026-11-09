# Embedded assets

| file | source | SHA-256 |
|---|---|---|
| `quickjs.wasm` | npm `quickjs-wasi@3.6.2` (`quickjs.wasm`, 637,405 bytes), the exact version the coding-agent package of upstream 1.0.4 pins; QuickJS-NG compiled with wasi-sdk 30 for `wasm32-wasip1` in reactor mode | `d4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9` |
| `prelude.js` | the string `PRELUDE_SOURCE` of `packages/codemode/src/runtime/prelude-source.ts` in upstream 1.1.0+c661bc57 (the `settle` that gives a rejection its properties; image errors name an image block, not an MCP block; 33cd7e507, `format()` prints a number with `String()`, Pi #11945), written to a file, then patched by divergence D145 (`errorReplacer`, its three uses and the captured `Object.assign`; upstream hash `e64ad1a6afffbfb8f75a0d08434a9a3fec88d1e54206b03953bfc7b5820cc119`) | `e6a68df9fa044c3058a3b9d64f39f3014e5d0330677ae5f0e2c9c59bf96ae34e` |

Licenses: `quickjs-wasi` is MIT, Copyright (c) 2026 Vercel, Inc. The module contains QuickJS-NG (MIT) and wasi-libc (permissive licenses). `prelude.js` is MIT, Copyright (c) 2025 Mario Zechner. `THIRD_PARTY_NOTICES.md` reproduces the notices.

Refresh on an upstream pin move, in one change:

1. Install the new `@earendil-works/pi-coding-agent` in a scratch directory and copy `node_modules/.../quickjs-wasi/quickjs.wasm` over `quickjs.wasm`, then run `go generate ./codemode` to rebuild `quickjs.wasm.zst`, the zstd frame of it that the binary embeds (`TestEmbeddedQuickJSWasmFrameDecodesToTheAsset` fails when the two differ; the frame must come from the pinned zstd, see `internal/zstdblob`).
2. Regenerate `prelude.js` and reapply D145 unless upstream fixed Pi #10650: `node -e 'import("<upstream>/packages/codemode/src/runtime/prelude-source.ts").then(m => require("fs").writeFileSync("prelude.js", m.PRELUDE_SOURCE))'`.
3. Update the hashes here, in `codemode/sandbox_test.go` (`TestEmbeddedSourcesMatchTheirRecordedHashes`) and in `THIRD_PARTY_NOTICES.md`.
4. Re-run the ported sandbox tests: the prelude assumes the engine's behavior.
