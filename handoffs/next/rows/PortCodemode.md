# PortCodemode rows (branch fix/pi-codemode-nan, base 8b72390d2)

Pi 33cd7e507 "fix(codemode): print NaN and Infinity in console output and thrown values (#11945, issue #11901)".

| upstream file | disposition | Go target | covering tests | evidence |
|---|---|---|---|---|
| packages/codemode/src/runtime/prelude-source.ts | ported | codemode/assets/prelude.js (`format`: a number prints with `String()`, before the JSON path; D145's `errorReplacer` stays). Upstream prelude hash 57c8dc8c...5281 -> e64ad1a6...c119; PiG's patched file e6a68df9...ae34e (PROVENANCE.md and TestEmbeddedSourcesMatchTheirRecordedHashes updated) | codemode/sandbox_test.go TestPrintsNonFiniteNumbersInConsoleOutputAndThrownValuesLikeText (in behaviorTests: runs on the worker, inline and remote backends, as upstream's describe.each) | red 36579e489 (console shows null, `throw NaN` message null), green 62339c464 |
| packages/codemode/test/sandbox.test.ts "prints non-finite numbers in console output and thrown values like text()" | ported with Pi's inputs and expectations; extra legs: -0 prints 0, NaN/Infinity nested in an array or object stay `null` (JSON.stringify and text() do, as upstream says), `throw -Infinity` | same test | same |
| packages/codemode/CHANGELOG.md | ported | changelog.d/codemodenan-62339c4642.md | n/a | n/a |

Not touched (PinBump / vendored): coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-codemode/runtime/prelude-source.js is the vendored Pi dist copy (Node-side extension host); it gets the fix with the pin bump's re-vendor.

Oracle: codemode/script_oracle_test.go TestScriptOutcomesMatchPi runs the PINNED Pi build. Its `console.log(NaN, ...)` script, which already existed, and the added `console.log(Infinity, ...)`, `throw NaN`, `throw -Infinity`, `throw -0` scripts print NaN/Infinity in Pi only from 33cd7e507 on. They are red against the current pin (c23b9ec1, Pi prints null) and green once PinBump's pin includes 33cd7e507 (T=77959d66 does).
