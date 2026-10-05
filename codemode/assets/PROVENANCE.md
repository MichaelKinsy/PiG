# Embedded assets

| file | source | SHA-256 |
|---|---|---|
| `quickjs.wasm` | npm `quickjs-wasi@3.6.2` (`quickjs.wasm`, 637,405 bytes), the exact version the coding-agent package of upstream 1.0.2 pins; QuickJS-NG compiled with wasi-sdk 30 for `wasm32-wasip1` in reactor mode | `d4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9` |
| `prelude.js` | the string `PRELUDE_SOURCE` of `packages/codemode/src/runtime/prelude-source.ts` in upstream 1.0.2, written to a file byte for byte | `c8c292ac0bc913654384ae12ecd759d6de89862acab0ce912f2e733878749880` |

Licenses: `quickjs-wasi` is MIT, Copyright (c) 2026 Vercel, Inc. The module contains QuickJS-NG (MIT) and wasi-libc (permissive licenses). `prelude.js` is MIT, Copyright (c) 2025 Mario Zechner. `THIRD_PARTY_NOTICES.md` reproduces the notices.

Refresh on an upstream pin move, in one change:

1. Install the new `@earendil-works/pi-coding-agent` in a scratch directory and copy `node_modules/.../quickjs-wasi/quickjs.wasm` over `quickjs.wasm`.
2. Regenerate `prelude.js`: `node -e 'import("<upstream>/packages/codemode/src/runtime/prelude-source.ts").then(m => require("fs").writeFileSync("prelude.js", m.PRELUDE_SOURCE))'`.
3. Update the hashes here, in `codemode/sandbox_test.go` (`TestEmbeddedSourcesMatchTheirRecordedHashes`) and in `THIRD_PARTY_NOTICES.md`.
4. Re-run the ported sandbox tests: the prelude assumes the engine's behavior.
