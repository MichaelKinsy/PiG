# CF host dependency review: Pi 1.0.4 to Pi 1.3.0 (2026-10-18)

Scope: `durable/core/host/cf/package.json` and its lockfile move `@earendil-works/chord`, `pi-ai` and `pi-durable` from 1.0.4 to 1.3.0 (exact pins, as before). That adds `@earendil-works/pi-codemode`, `@silvia-odwyer/photon` and `quickjs-wasi` to the lock and moves `pi-telemetry`. Reviewed with `npm view`, `npm pack` (tarball unpacked and inspected), the registry attestation endpoint and a lock diff against `HEAD`. npm 11.17.0 on the review host; the lock file is byte-identical to the candidate built with npm 12.1.0 (`~/cf-recycle/lock13`), and `npm install --package-lock-only` leaves it unchanged.

Verdict: nothing blocks the bump. One residual risk, photon (below), is accepted by the pin and is the same package Pi's own `pi-durable` 1.3.0 pins and `durable/interop/package-lock.json` already locks at the same integrity.

## Lock diff

Changed: chord, pi-ai, pi-durable, pi-telemetry (1.0.4 to 1.3.0). Added: pi-codemode 1.3.0, photon 0.3.3, quickjs-wasi 3.6.2. Removed: none. Every other package keeps its version and integrity. `esbuild` and its 26 `@esbuild/*` platform packages become `dev` only (chord 1.0.4 depended on `esbuild`; chord 1.3.0 depends on `typebox` alone). The dependency specifiers `pi-ai -> pi-telemetry` and `pi-durable -> chord/pi-ai` change from `^1.0.4` to exact `1.3.0`.

All 195 locked entries have an `integrity` (sha512) and resolve to `registry.npmjs.org`. The install-script set is unchanged: `@google/genai`, `esbuild`, `protobufjs` and `workerd` already had install scripts before this change; none of the new or moved packages has one.

## Per package

| Package | Version | License | Publisher | Provenance (SLSA, npm attestation) | Install scripts | Unpacked | Native / wasm |
|---|---|---|---|---|---|---|---|
| `@earendil-works/chord` | 1.3.0 | MIT | GitHub Actions OIDC (`npm-oidc-no-reply`); maintainers mitsuhiko, badlogic, rwachtler | yes | none (only `prepublishOnly`) | 1.87 MB, 131 files | none |
| `@earendil-works/pi-ai` | 1.3.0 | MIT | same | yes | none | 4.17 MB, 837 files | none |
| `@earendil-works/pi-durable` | 1.3.0 | MIT | same | yes | none | 3.20 MB, 327 files | none |
| `@earendil-works/pi-telemetry` | 1.3.0 | MIT | same | yes | none | 123 KB, 26 files | none |
| `@earendil-works/pi-codemode` | 1.3.0 | MIT | same | yes: repo `earendil-works/pi`, ref `refs/tags/v1.3.0`, workflow `build-binaries.yml`, commit `71023aec1546d6d677a65669c5665c0927ce0dd0` | none | 376 KB, 66 files | none (JS only) |
| `quickjs-wasi` | 3.6.2 | MIT | GitHub Actions OIDC; maintainers tootallnate, adamdong (vercel-labs) | yes: repo `vercel-labs/quickjs-wasi`, ref `refs/heads/main`, workflow `release.yml`, commit `5a7a0eeda87c99542f8cf3095b6d61ecfa755977` | none (scripts are `build`, `test`, `bench*`, `ci:*`) | 1.52 MB, 25 files | `quickjs.wasm` (637 KB) and five `extensions/*/*.so` that are WebAssembly side modules (magic `\0asm`), no ELF/Mach-O/`.node` |
| `@silvia-odwyer/photon` | 0.3.3 | Apache-2.0 | silvia-odwyer (individual), sole maintainer | **no** (registry signature only) | none (no scripts at all) | 2.15 MB, 6 files | `photon_rs_bg.wasm` (1.88 MB, WebAssembly MVP) |

Registry ECDSA signatures (key `SHA256:DhQ8wR5A...`) are present on all seven. The three `@earendil-works` runtime packages published together (2026-10-15) from the same repository.

## Integrity

`npm pack <name>@<version>` of pi-codemode, photon, quickjs-wasi and pi-durable reports the same sha512 as the lockfile (and as `dist.integrity`):

- chord 1.3.0 `sha512-qqYhjALZ193atpBzmmS+M5pHQoiI4Aynetc1jXpv+5O2crXH3Y8wbFEy33orU2nmFrlw1U36rsdgUjaW4b3m/w==`
- pi-ai 1.3.0 `sha512-KaBI8x7q7sPoFUXICNGD9efHzDG+oRNwVvkA9jdIsSQWH1RjFssOlEGsbLtewUPPgCrvmz0l2KTgSLXTyj3acw==`
- pi-durable 1.3.0 `sha512-2aD0xead1LH0BBwUBxex28in/PpUJEwyaEiW/nLoTFUcZ/lDYEl6LHX6mGZKJG8tUHzERwJyysDgy0ruidt84w==`
- pi-telemetry 1.3.0 `sha512-ERGZr+aXjNJzoUbB7ZzHVH3g5otzMHjHLs2Fyizm12By5X+gBEhod9cJXFOk13S7M8a29Am+Mq21zm73mRJFLg==`
- pi-codemode 1.3.0 `sha512-2G9tmbsPhgzwfz7cmzJpbLSLqOx3eu22Gbzm+Wc3HqeZMjLQS4F3KfAabozVhv7uEjq3k2O1dZTHmhhWr2xbfQ==`
- photon 0.3.3 `sha512-8BhUjEch4slwRe8uXnaA4vcA5uiiOTT90UMsxulOj2gN98X1p0q9Z4Ysk4DkD05uNgbR9XoSqtZ37w+33w4QKQ==`
- quickjs-wasi 3.6.2 `sha512-FCqGtGOrMgzUiIrMNMA2YnsOxCNwo31dzqXvclXUC6xeT35NJLKXQJsvbeCTjvoFAwZgEAPg8U6+KAPDGXn8Mg==`

`durable/interop/package-lock.json` (already on Pi 1.3.0) records the same integrity for pi-codemode, photon and quickjs-wasi, so two lockfiles in the repo agree on the bytes.

## Findings

- No lifecycle script (`preinstall`, `install`, `postinstall`, `prepare`) in any new or moved package. Nothing runs at install; only `prepublishOnly` exists in the Pi packages.
- Code scan of quickjs-wasi `dist/index.js` and pi-codemode `dist/`: no `child_process`, `node:net`, `node:http` or `process.env` use. The only hits are `eval`/`fetch` inside comments, error text and the wasm export `qjs_eval` (the VM's own eval entry point).
- photon's JS glue (`photon_rs.js`) has no `import` statements and no Node built-ins; it is wasm-bindgen output (image functions over a `__wbg_ptr`).
- quickjs-wasi and pi-codemode execute guest JavaScript inside a WebAssembly QuickJS VM; this is the sandbox the CF host's codemode already ports (`src/codemode/`), so the new package is the upstream copy of code the repository already carries a port of.
- **Residual risk, photon:** no npm provenance; one individual maintainer; the 0.3.3 tarball dates from 2025-05-10 and the package has been quiet since (not a compromise indicator, but a takeover target). 1.88 MB of opaque wasm that cannot be rebuilt from the tarball alone. Mitigations: exact pin plus integrity in the lock (a republished tarball fails `npm ci`); no install script; the wasm runs in the engine sandbox and the glue imports no module; Pi's own pi-durable 1.3.0 pins this exact version, and the interop lock already holds it. The CF host's own `src/` does not import photon; pi-durable reaches it from its `dist/images/` (`node.js`, `node-worker.js`, `cloudflare.js`, `index.js`).
- chord 1.3.0 drops `esbuild` from its dependencies, which shrinks the runtime install surface (esbuild stays as a dev dependency of this package).
- `npm audit signatures` could not run in this environment (it audits only trees installed without `--ignore-scripts`/platform overrides); signatures and attestations were checked from the registry metadata above instead.
