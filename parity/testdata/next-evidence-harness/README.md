# Harness evidence artifacts

These artifacts bind tests to the source tree at base `fa8024b01666fb6737c9d863e054bf59aec01485`. The upstream pin comes from `coding/pigversion/pigversion.go`. The probe imports the exact `.upstream/current` source, not an installed comparator or a copied implementation. No production change is retained.

## Repeat the source probe

```sh
node parity/testdata/next-evidence-harness/upstream-probe.mjs
```

`upstream-probe.log` retains its output. It verifies the JPEG/PNG boundaries, the upstream-owned JSONL schema constants, mixed-block text extraction, and the three text expectations that fail in Go.

## Mutation evidence

Every patch changes production code and compiles. Every red log contains a named assertion failure, not a compiler error. Apply only one patch at a time. Run the command at the start of its red log. Reverse that patch before applying another.

| Patch | Production decision | Assertion failure |
|---|---|---|
| `image-mime.patch` | Invert JPEG-LS exclusion in `internal/imageprocessing/images.go` | Ordinary JPEG becomes unsupported; JPEG-LS becomes JPEG through both the detector and read-tool wrapper. |
| `image-bmp.patch` | Invert BMP planes validation in `internal/imageprocessing/images.go` | One-plane BMP becomes unsupported; invalid planes become BMP. |
| `jsonl-header.patch` | Serialize the header format under `format` instead of `v` in `agent/harness/session/jsonl_types.go` | The actual repository-created JSONL header differs from the upstream schema. |
| `jsonl-validation.patch` | Accept future storage versions in `agent/harness/session/jsonl_storage.go` | `JsonlSessionRepo.Open` accepts a future header instead of rejecting it before tail repair. |
| `pico-publication.patch` | Bypass sidecar publication confirmation in `agent/harness/pico3/jsonl.go` | Reopen exposes an unpublished running task instead of its committed pending state. |
| `pico-highwater.patch` | Persist zero instead of the committed ID horizon in `agent/harness/pico3/jsonl.go` | Reopen allocates ID 3 instead of ID 4 after an allocation is committed without creating a row. |

For example:

```sh
git apply parity/testdata/next-evidence-harness/pico-publication.patch
go test ./agent/harness/pico3 -run '^TestHarnessPicoJSONLPublicationRecovery$' -count=1 -json
git apply -R parity/testdata/next-evidence-harness/pico-publication.patch
```

`green.log` records the initial passing tests. `*-red.log` records each failure. `restored-green.log` records all six passing tests after every production mutation is removed. The logs preserve Go's `Output` events verbatim and retain top-level JSON completion events with elapsed time. They contain no skips. All six tests report 0–10 ms, with Go's displayed precision. These are correctness tests, not performance benchmarks.

The image evidence covers magic-byte detection only. It does not claim `encodeBase64` tail padding. The JSONL schema evidence covers emitted headers, metadata recovered by a fresh repository, rejection by the repository's real storage consumer, and preservation of invalid files. It does not claim behavior for every interface declaration. The Pico3 evidence covers the storage API directly, including both sidecars, missing referenced records, all three torn tails, sequence reuse, task retirement, and committed ID recovery. It does not claim Chord publication or scheduler semantics from those storage tests.

## Unresolved text regressions

The text row is not accepted evidence. The tests in `text-regression.go.txt` compile and fail on the unchanged base production source. They remain a portable reproducer rather than a failing test in the default suite because this lane prohibits production edits. No test is skipped or weakened, and no behavioral manifest record credits this row.

```sh
cp parity/testdata/next-evidence-harness/text-regression.go.txt agent/team_harness_text_test.go
go test ./agent -run '^TestHarnessText' -count=1 -json
rm agent/team_harness_text_test.go
```

Run this command only when `agent/team_harness_text_test.go` does not already exist. `text-red.log` contains the exact failures. The AI transcript/text owner must fix:

- `ai/transcript.go:330`, `systemContentText`: two text blocks produce `firstsecond` instead of `first\nsecond`.
- `ai/transcript.go:158`, `GetCurrentSystemPrompt`: an empty section adds an extra blank paragraph instead of being omitted.
- `ai/transcript.go:175`, `RenderSystemMessageUpdate`: `%q` escapes section names, while Pi interpolates them literally inside quotes.

The provider caller paths include `ai/openai.go:626,1265` and `ai/openai_responses.go:520,769,791`. The regressions currently stop at the pure rendering boundary. The production-fix owner must add serialized request coverage for both paths.

## Validation

`base-tests.log` and `step-tests.log` retain full package runs with supplemental statement coverage. `step-race.log` retains the focused race run. `step-contracts.log` retains every `check-contracts-fast` target except `closure-check`, plus coverage, port-map, and documentation drift checks. All commands exit zero. `interface-deps` is marked old with `make -o interface-deps` so the supplied compiler dependencies are used without running the installer. The interface tests execute normally. `step-lint.log` records the full repository lint result. Build, vet, and lint configuration verification also exit zero and produce no output.
