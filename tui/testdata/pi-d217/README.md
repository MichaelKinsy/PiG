# pi-d217: the built pinned pi-tui plus the Pi fixes 0.6.0 carries (D217)

PiG 0.6.0 carries Pi bug fixes that landed after the pin (1.4.0+b3a29f11). The Go code behaves as the fixed Pi does, but the oracle scripts
in `tui/testdata/*.mjs` load the pinned pi-tui build, which does not. For the scripts that import `./pi-d217/register.mjs` first, a Node
module hook replaces one module of that build, `@earendil-works/pi-tui/dist/utils.js`, with `utils.js` of this directory. Their
comparators stay exact; nothing else of the build changes.

- `utils.ts-<sha>.patch`: `git diff <sha>^ <sha> -- packages/tui/src/utils.ts` of Pi, unchanged. Carried now: `2ce9d527` (earendil-works/pi#10925).
- `gen.sh`: applies the patches to the pin's `utils.ts` (mirror `.upstream/current`) and compiles with the pin build's TypeScript and
  `tsconfig.build.json`. It first compiles the UNPATCHED `utils.ts` and fails unless that equals the built oracle's `dist/utils.js`
  byte for byte, which proves the compile setup is the build's.
- `utils.js`: the generated module. `pinned-utils.sha256`: SHA-256 of the pinned build's `dist/utils.js`
  (`42e0d12fe4f0fbd04fd997a044a305ac15d30c739b1a351c45c28aedb9cedd5a`).
- `hook.mjs`: loads the module it replaces, throws unless its SHA-256 is `pinned-utils.sha256`, then returns `utils.js`. `register.mjs`
  registers it.

A later carry that changes another built pi-tui module adds its patch and file to `gen.sh` and `hook.mjs`; it does not add another mechanism.

Remove when the pin is at or past `c2760d32`: delete this directory and the `import "./pi-d217/register.mjs"` line of each script.
