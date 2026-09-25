# next-evidence-app-b evidence

`source.json` records the staging base, pinned Pi identity, source hashes, and toolchain. No production changes land in this slice. All paths below are relative to this directory unless stated otherwise.

## Dispositions

| Upstream responsibility | Disposition | Evidence |
|---|---|---|
| `packages/coding-agent/src/utils/clipboard-image.ts` | **BLOCKED**, owner: `clipboard-images` production follow-up | `TestClipboardImageUnsupportedFormats` fails on the unmodified base: BMP remains `image/bmp`, cannot decode as PNG, and corrupt BMP returns bytes instead of no image. `base-tests.jsonl` records both failures. The passing `TestClipboardImageBackendResults` proves MIME preference/parameters, no stale X11 fallback for empty Wayland data, and fallback after backend errors, but does not close conversion. No manifest record. |
| `packages/coding-agent/src/utils/image-convert.ts` | **PASS** | `TestImageConvertDecodedPixels` decodes the complete returned PNG and checks dimensions and every pixel for JPEG, WebP, GIF, BMP, PNG declared as JPEG, and the upstream EXIF-after-XMP fixture. It checks byte/base64 API agreement and retained input. Existing `TestConvertToPngReturnsOriginalDataForPNG` and `TestConvertToPngReturnsNilWhenConversionFails` cover passthrough and corrupt data. All three have compiling mutation proof. |
| `packages/coding-agent/src/modes/interactive/components/show-images-selector.ts` | **BLOCKED**, owner: `selectors` production follow-up | `TestShowImagesUpstreamCallbacks` fails for both initial values, up/down navigation, Enter, and Escape: neither supplied callback runs. `TestShowImagesUpstreamRender` fails at widths 20 and 80: Pig adds search/blank rows, concatenates labels/descriptions, and pads rows instead of using Pi's SelectList layout. `base-tests.jsonl` records the failures. No manifest record. |
| `packages/tui/src/components/alt-screen-flash.ts` | **PASS** | `TestFlashExactRender`, `TestFlashOrderedExpiryAndDispose`, and `TestFlashNonPositiveExpiryRequestsRender` prove exact bytes, ordered messages, out-of-order expiry, the 999/1000 ms default boundary, nonpositive expiry, exact render requests, disposal, and reuse. All clocks run inside `synctest`; no wall-clock sleeps or polling. |

The two blocked test files are retained as `.go.repro` artifacts, not installed as failing tests or counted as evidence. The lane is test/evidence-only. Production owners must fix the sources and install the red guards before promoting those rows. The existing accepted tests remain unchanged, including the weaker show-images tests.

Further clipboard source-audit obligations remain with `clipboard-images`: Pig has no native clipboard bridge to inject the upstream native-transfer-error contract. The author's base also deduplicated X11 candidates by MIME base. Current main fixes that issue in `b0c6aa02fc` by deduplicating exact raw strings; the review does not carry it forward as an open blocker. Native clipboard parity and unsupported-format conversion remain incomplete. The review corrects `PORT_MAP.md` to name the backend in `clipboard.go` and its paste caller, removes the selector's unsupported “faithful port” claim, and marks both blocked rows partial. Neither row receives a unit-evidence manifest record.

## Oracle

Run from the repository root:

```sh
node parity/testdata/next-evidence-app-b/oracle.mjs
```

The oracle transpiles the exact pinned TypeScript source in memory with the installed TypeScript compiler. It supplies fake command/native clipboard operations, deterministic theme colors, fake timers, and the already installed Photon implementation. It never invokes an OS clipboard, provider, or network service. `oracle.log` contains raw selector/flash bytes and conversion results. The theme stub isolates layout; it is not theme evidence. The Photon loader stub isolates decoding from loader/install behavior; it is not Photon-loader evidence.

## Mutation proof

All six patches change production statements and compile. Each red log contains named runtime assertion failures, not build failures. `green.jsonl` is the pre-mutation pass. `restored-green.jsonl` is the final pass after all production files were restored byte-for-byte.

| Patch | Package | Exact `-run` | Red failure |
|---|---|---|---|
| `image-passthrough.patch` | `./internal/codingagent` | `^TestConvertToPngReturnsOriginalDataForPNG$` | Declared PNG returns nil instead of the original bytes. |
| `image-failure.patch` | `./internal/codingagent` | `^TestConvertToPngReturnsNilWhenConversionFails$` | Corrupt input returns a fabricated successful conversion. |
| `image-orientation.patch` | `./internal/codingagent` | `^TestImageConvertDecodedPixels$` | EXIF fixture remains 2x1 instead of 1x2. The mutation is in `decodeAutoOriented`, called directly by mapped `internal/imageprocessing/image_convert.go`. |
| `flash-removal.patch` | `./tui` | `^TestFlash(OrderedExpiryAndDispose\|NonPositiveExpiryRequestsRender)$` | Expired entries remain visible. |
| `flash-truncation.patch` | `./tui` | `^TestFlashExactRender$` | Width+1 produces different reverse-video bytes. |
| `clipboard-fallback.patch` | `./internal/codingagent` | `^TestClipboardImageBackendResults$` | Empty Wayland reads expose stale X11 data and unexpected backend calls. This is partial evidence only; the row remains blocked. |

Each patch has a matching `<stem>-red.jsonl` file. Replay one patch at a time in a clean disposable worktree:

```sh
git apply --unidiff-zero parity/testdata/next-evidence-app-b/image-orientation.patch
go test ./internal/codingagent -run '^TestImageConvertDecodedPixels$' -count=1 -json
# Expect exit 1 and TestImageConvertDecodedPixels/EXIF_after_XMP FAIL.
git apply --unidiff-zero -R parity/testdata/next-evidence-app-b/image-orientation.patch
go test ./internal/codingagent -run '^TestImageConvertDecodedPixels$' -count=1 -json
# Expect exit 0.
```

Replay the blocked guards without a production mutation:

```sh
cp parity/testdata/next-evidence-app-b/team_app_clipboard_blocked_test.go.repro internal/codingagent/team_app_clipboard_blocked_test.go
cp parity/testdata/next-evidence-app-b/team_app_show_images_test.go.repro tui/team_app_show_images_test.go
go test ./internal/codingagent ./tui -run '^(TestClipboardImageUnsupportedFormats|TestShowImagesUpstream.*)$' -count=1 -json
# Expect exit 1. Remove only these two copied test files after the probe.
```

## Timing and coverage

The named non-race restored run reports 0.01 seconds for `TestImageConvertDecodedPixels`; all other named tests report 0.00 seconds at Go JSON timing precision. No named test exceeds 50 ms. Full-package timing is not individual test timing.

Supplemental full-package coverage before → after:

- `./internal/codingagent`: 65.7% → 65.7%.
- `./tui`: 88.3% → 88.3%.

`coverage.diff` records the author's generated before/after report. Image conversion moves from weak-only to three behavioral unit assertions. Flash moves from untested to three behavioral unit assertions. Clipboard and show-images receive no behavioral evidence. On the author's base, the roll-up moves from 414 behavioral / 7 weak-only / 24 untested to 416 / 6 / 23. These are evidence mappings, not fresh paired scenario runs or new ported source files. The review regenerates coverage against current main after marking the blocked rows partial; the historical delta is not the current roll-up.

The generated coverage commit includes `README.md` and `.github/badges/parity-coverage.svg` alongside `parity/coverage.md` and the `AGENTS.md` block because `coverage-drift` checks all those owning generator outputs. It contains no hand-edited coverage claims.

## Gates

`gates.txt` records baseline/final command results. Both the base and final lane gates pass. Full package race tests pass. The focused race suite also passes ten repetitions. No new suppressions, skips, baselines, production APIs, dependencies, or provider/service contacts are added.

No runtime performance claim is made. The clipboard seam performs only local fake operations; conversion holds only tiny fixture images; every flash test disposes its timers inside a fake-time bubble. No background work survives a test.
