# pi-d217: the pinned Pi's modules plus the Pi fixes 0.6.0 carries (D217)

PiG 0.6.0 carries Pi bug fixes that landed after the pin (1.4.0+b3a29f11). The Go code behaves as the fixed Pi does, but the Pi oracles
load the pinned Pi's modules, which do not. A Node module hook swaps the modules listed in `manifest.json` for the generated ones of this
directory; the comparators stay exact and no probe is skipped. Three load paths use it:

- `tui/testdata/*.mjs` (15 scripts) and the scenario Pi programs import `register.mjs` first (`--import {{REPO}}/tui/testdata/pi-d217/register.mjs`
  in a scenario's `pi_args` or `NODE_OPTIONS`); they load the built pi-tui.
- `internal/pioracle.Run` passes `--import register.mjs` while this directory exists; the vendored `shims/pi-dist` it loads has pi-tui's
  `utils.js` (external to the pi-tui bundle) and `image-resize-core.js` as separate modules.

| file | replaces (URL suffix) | carried |
|---|---|---|
| `utils.js` | `/@earendil-works/pi-tui/dist/utils.js` (built oracle) | `2ce9d527` (earendil-works/pi#10925) |
| `utils.js` | `/packages/tui/dist/utils.js` (the pin build's own workspace path, where the real `pi` binary of a scenario loads it) | `2ce9d527` |
| `utils.vendored.js` | `/pi-dist/pi-tui/utils.js` (vendored), the same file with vendor-pi-dist.sh's one import rewrite | `2ce9d527` |
| `image-resize-core.js` | `/pi-dist/pi-coding-agent/utils/image-resize-core.js` (vendored) | `e7a0df27` (earendil-works/pi#11551) |

- `*.patch`: `git diff <sha>^ <sha> -- <file>` of Pi, unchanged.
- `gen.sh`: applies the patches to the pin's sources (mirror `.upstream/current`) and compiles with the pin build's TypeScript and
  `tsconfig.build.json`. Before writing anything it proves, for each module, that the UNPATCHED pin source compiles to the built oracle's
  file byte for byte, and that the vendored copy equals what vendor-pi-dist.sh makes of the built file.
- `manifest.json`: per module the URL suffix, the SHA-256 of the file it replaces, and the replacement. `hook.mjs` throws if the module it
  replaces has another hash, so a different build is never patched silently.

A later carry that changes another module adds its patch and file to `gen.sh`; it does not add another mechanism.

Remove when the pin is at or past `c2760d32`: delete this directory, `d217Register` in `internal/pioracle/pioracle.go`, the `import
"./pi-d217/register.mjs"` line of each script, and the `--import`/`NODE_OPTIONS` of the scenarios.
