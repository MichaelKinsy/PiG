# pi-d217: the pinned Pi's modules plus the Pi fixes 0.6.0 carries (D217)

PiG 0.6.0 carries Pi bug fixes that landed after the pin (1.4.0+b3a29f11). The Go code behaves as the fixed Pi does, but the Pi oracles
load the pinned Pi's modules, which do not. A Node module hook swaps the modules listed in `manifest.json` for the generated ones of this
directory; the comparators stay exact and no probe is skipped. Three load paths use it:

- The parity runner starts the real pi as `node --import <repo>/tui/testdata/pi-d217/register.mjs <pin>/dist/cli.js <args>` instead of the pin's bundled
  `dist/bundle/cli.js` (which inlines pi-tui, so the hook cannot reach it), for every scenario that runs the resolved pi, while `manifest.json` is
  not empty (`d217Launch` in `test/parity/runner/d217.go`; the base args and the scenario's `pi_args` and `-e` flags follow as for the bundle). A scenario
  that names its own `pi_bin` (a script such as `node ./test/parity/testdata/x-pi.mjs`) is not launched through it; see the next item. With `manifest.json` set to
  `[]` the bundle runs and nothing is imported.
- An oracle script that loads the built pi-tui (the 15 `tui/testdata/*.mjs`, and the scripts of a `pi_bin` scenario or of a Go test that render text through it)
  imports `register.mjs` first (`import "../../tui/testdata/pi-d217/register.mjs"`, relative to the script). The import goes in the script, not in the
  environment: `NODE_OPTIONS` would reach every Node process the script starts, and an oracle that reads a child's output through a pipe
  (`test/parity/testdata/codemode-declarations-pi.mjs`) then loses its tail.
- `internal/pioracle.Run` passes `--import register.mjs` while this directory exists; the vendored `shims/pi-dist` it loads has pi-tui's
  `utils.js` (external to the pi-tui bundle) and `image-resize-core.js` as separate modules.

| file | replaces (URL suffix) | carried |
|---|---|---|
| `utils.js` | `/@earendil-works/pi-tui/dist/utils.js` (the built oracle, the real pi's and the scripts') | `2ce9d527` (earendil-works/pi#10925) |
| `utils.vendored.js` | `/pi-dist/pi-tui/utils.js` (vendored), the same file with vendor-pi-dist.sh's one import rewrite | `2ce9d527` |
| `image-resize-core.js` | `/pi-dist/pi-coding-agent/utils/image-resize-core.js` (vendored) | `e7a0df27` (earendil-works/pi#11551) |

- `*.patch`: `git diff <sha>^ <sha> -- <file>` of Pi, unchanged.
- `gen.sh`: applies the patches to the pin's sources (mirror `.upstream/current`) and compiles with the pin build's TypeScript and
  `tsconfig.build.json`. Before writing anything it proves, for each module, that the UNPATCHED pin source compiles to the built oracle's
  file byte for byte, and that the vendored copy equals what vendor-pi-dist.sh makes of the built file.
- `manifest.json`: per module the URL suffix, the SHA-256 of the file it replaces, and the replacement. `hook.mjs` throws if the module it
  replaces has another hash, so a different build is never patched silently.

A later carry that changes another module adds its patch and file to `gen.sh`; it does not add another mechanism.

Remove when the pin is at or past `c2760d32`: delete this directory, `test/parity/runner/d217.go` (and its call in `TestParity`, `Launch` and `forScenario` in `binaries.go`), `d217Register` in
`internal/pioracle/pioracle.go`, and the `import "./pi-d217/register.mjs"` line of each script.
