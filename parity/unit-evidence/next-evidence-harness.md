# next-evidence-harness report

Status: **SLICE BLOCKED next-evidence-harness: three text-rendering regressions require production fixes outside this test-only lane.** The other six rows have complete evidence or an explicit structural disposition.

Base: `fa8024b01666fb6737c9d863e054bf59aec01485` (`staging/main` when the lane started). The test/evidence commit is `b44407f05`. The final report commit message records the complete commit list and final code HEAD. The source oracle is `.upstream/current`, pinned by `coding/pigversion/pigversion.go`. The installed tools used here are Go 1.27.1 and Node 24.19.0 on Linux/amd64. Nothing is installed, no production code change is retained, and no service endpoint or default is changed.

## Row dispositions

| Upstream source | Disposition | Evidence or reason |
|---|---|---|
| `packages/agent/src/harness/tools/image.ts` | **PASS** | `TestHarnessImageMagicBoundaries` and `TestHarnessImageBMPPlanesAndBitDepth`; two compiling discriminator mutations fail. This credits MIME detection only, not base64 encoding. |
| `packages/agent/src/harness/session/jsonl/types.ts` | **PASS** | `TestHarnessJSONLHeaderRepositoryRoundTrip` and `TestHarnessJSONLInvalidHeaderRepositoryOpen`; emitted schema and consumer validation mutations fail. The runtime obligation is the persisted header, not the whole interface file. |
| `packages/agent/src/harness/session/testing/types.ts` | **TYPE-ONLY** | Only `StorageFixture` and `ConformanceCase` interfaces exist upstream. No manifest record or fabricated behavioral test is added. |
| `packages/agent/src/harness/pico3/index.ts` | **TYPE-ONLY** (barrel/structural) | Only re-exports exist upstream. Public package reachability does not prove the behavior of every exported implementation. No manifest record or facade smoke test is added. |
| `packages/agent/src/harness/pico3/jsonl.ts` | **PASS** | `TestHarnessPicoJSONLPublicationRecovery` and `TestHarnessPicoJSONLRetirementAndHighWater`; publication-confirmation and persisted-ID mutations fail. |
| `packages/agent/src/search/index.ts` | **TYPE-ONLY** | Only query/hit/service interfaces exist upstream. No search backend, ranking, or indexing algorithm is defined. No manifest record is added. |
| `packages/ai/src/utils/text.ts` | **BLOCKED** | Three compiling red regressions demonstrate missing separators, empty-section framing, and escaped section names. The mapped token estimator is not an implementation of the three upstream text helpers. |

The manifest is [`next-evidence-harness.json`](next-evidence-harness.json). All proof artifacts are under [`../testdata/next-evidence-harness/`](../testdata/next-evidence-harness/README.md). Initial green, mutation red, restored green, commands, assertion output, and elapsed times are portable repository files. Each named positive test fails under its own production mutation and passes after restoration. No skips, retries, comparator weakening, new divergences, or lint suppressions are used.

## Source and consumer binding

### Image detection

The mapped implementation is `internal/imageprocessing/images.go:DetectSupportedImageMimeType`, reached through the read tool's `internal/codingagent/tools/image_detect.go:SupportedImageMime`. The cases exercise JPEG-LS exclusion, short JPEG signatures, PNG IHDR requirements, APNG control chunks before and after IDAT, chunk truncation and maximum lengths, both GIF signatures, RIFF/WebP discrimination, BMP header boundaries, pixel offsets, planes, and all accepted bit depths. The BMP denominator is the explicit upstream allowlist, not a count observed from Go output. Base64 tail padding remains unclaimed because no real read-tool output assertion is added for it.

### JSONL schema

Upstream exports runtime format/storage constants and uses `JsonlStorageHeader` for the repository's persisted header. The test creates a real temporary filesystem repository, closes it, reads the exact emitted header as an independent JSON object, discovers it through a new repository, and reopens it without rewriting bytes. Invalid and future headers pass through `JsonlSessionRepo.Open` and `openV4JsonlStorage`; rejection must precede torn-tail repair. The schema literals 4 and 1 come from upstream's persisted format, not a hard-coded Pi release identifier or the Go constants under test.

### Pico3 JSONL

The tests call the mapped public storage implementation in `agent/harness/pico3/jsonl.go`. A commit writes task and sticky sidecars under one main marker. Removing the marker discards both halves and permits safe sequence reuse. Removing a referenced sidecar rejects reopen. Unterminated bytes in main, task, and sticky logs are removed without losing committed state. Terminal outcomes survive retirement and stale-sidecar cleanup. Committed ID allocation survives even when the allocated ID creates no table row. These are storage obligations; the tests do not credit the barrel, Chord facade, or scheduler from a storage-only assertion.

### Structural correspondence for reviewer disposition

| Upstream declaration/export | Go correspondence | Existing consumer/navigation |
|---|---|---|
| `StorageFixture.storage`, async disposal | `sessiontesting.StorageFixture.Storage`, `Close` in `agent/harness/session/testing/types.go` | Backend fixtures in `agent/harness/session/jsonl_test.go` and `memory_conformance_test.go` |
| `ConformanceCase.group`, `name`, `run` | `ConformanceCase.Group`, `Name`, `Run` in the same Go file | Go-added `RunConformance` registers these cases; that runner is not TypeScript interface behavior. |
| Pico3 `bashTool`, `Bounded` exports | `BashTool`, `Bounded`/`NewBounded` | `agent/harness/pico3/bash.go`, `bounded.go` and their implementation tests |
| Pico3 `attachChordView`, service exports | `AttachChordView`, `CreatePicoConversationService`, `PicoHarnessService`, `PicoConversationService` | `chord.go`, `chord_service.go`, `chord_test.go`, `chord_lifecycle_test.go`; behavior belongs to `chord.ts`. |
| Pico3 `Harness`, storage exports | `Harness`/`OpenHarness`, `JsonlStorage`/`OpenJsonlStorage`, `MemoryStorage`/`NewMemoryStorage` | `helpers_test.go` consumes the implementations; the new tests directly consume JSONL storage. |
| Pico3 system/type exports | `DefineSystemSection`, `SystemSections`, and the package's exported data/protocol types | `system.go`, `types.go`, `harness.go`; the compiler-derived interface inventory owns exhaustive signature reachability. |
| `SearchQuery`, `SessionSearchHit`, `EntrySearchHit` | Same exported names in `agent/search/search.go`; the inline `top` shape becomes `SessionSearchTop` | `agent/search/search_test.go` contains signature checks, not backend behavior. |
| `SessionSearchService`, optional `searchEntries` | `SessionSearchService` plus optional `EntrySearchService` | Async results become blocking result/error returns; the upstream file defines no concrete service. |

The reviewer owns structural ledger corrections. All three structural rows remain untested in generated behavioral coverage. No new `team_harness_fixture_contract_test.go`, `team_harness_facade_test.go`, or `team_harness_search_contract_test.go` is added, because such tests would only prove signatures or misattribute implementation behavior.

### Blocked text row

The portable source probe imports the real upstream `contentText`, `getSystemMessageText`, and `renderSystemMessageUpdate` helpers. It verifies default/custom separators, string passthrough, empty content, omitted thinking/image/tool-call blocks, null section removal, empty section omission, and literal update framing. The Go regressions are preserved as `text-regression.go.txt`, not enabled or credited tests.

| Red test (all 0.00 s) | Actual Go output versus Pi | Owning source |
|---|---|---|
| `TestHarnessTextSystemBlockSeparator` | `firstsecond` versus `first\nsecond` | `ai/transcript.go:330`, `systemContentText` |
| `TestHarnessTextEmptySystemSection` | `base\n\n\n\nlast` versus `base\n\nlast` | `ai/transcript.go:158`, `GetCurrentSystemPrompt` |
| `TestHarnessTextSystemUpdateLiteralNames` | Go-escaped quotes/newlines in section names versus literal interpolation | `ai/transcript.go:175`, `RenderSystemMessageUpdate` |

The AI transcript/text owner must fix these sources and add serialized request coverage through both OpenAI Completions and Responses. Their production call sites are `ai/openai.go:626,1265` and `ai/openai_responses.go:520,769,791`. The correct `systemMessageText` helper already exists in `ai/estimate.go`, but the provider-facing helpers use a separate implementation. This is a source-level consistency gap, not a request to normalize output.

`PORT_MAP.md` currently maps this row to `agent/context_tokens.go` and `internal/codingagent/export/tool_renderer.go`. The former estimates character/token totals; it neither returns joined text nor implements system sections or update framing. The latter flattens tool-result text/images for export; it does not supply a general configurable-separator helper or either system-message helper. No public `ContentText` equivalent is found in `ai` or `agent`. The reviewer must correct the mapping before accepting broad evidence. A token-count assertion cannot honestly close this row. No production fix is attempted because the lane explicitly prohibits production edits.

There are no STOP-AND-ASK endpoint/default questions.

## Coverage before and after

`make coverage RESULTS=` succeeds. Each PASS row changes from `**0 (untested)** | **0**` to `0 | 2 (unit:...)`, with the exact two tests named in the disposition table. The fixture, barrel, search, and text rows remain `**0 (untested)** | **0**`. The paired-scenario count remains zero for every row. `not run` remains the scenario-result status; the generator does not pretend unit evidence is a fresh parity run.

```text
Before: 445 / 536 intended-portable; 414 behavioral (93.0%), 7 weak-only, 24 untested.
After:  445 / 536 intended-portable; 417 behavioral (93.7%), 7 weak-only, 21 untested.
```

Supplemental full-package statement coverage changes from 88.2% to 88.2% for tools, 84.9% to 85.1% for session, and 83.7% to 83.8% for Pico3. Mutation failures, not those percentages, establish the behavioral proof.

The task gives conflicting generated-file instructions. Its later acceptance requirement explicitly requests a final code commit containing `parity/coverage.md` and `AGENTS.md`, so those files are committed in that separate final code commit. The generator also changes the README summary and coverage badge; those companion outputs are retained with the manifest so `coverage-drift` remains green. The integrator can regenerate all four outputs together after accepting the manifests.

## Gates

Base build, vet, targeted package tests, and contract/drift checks pass. All required lane gates pass on the evidence worktree. Final logs are under `parity/testdata/next-evidence-harness/final-*.log`.

```sh
go build ./...
go vet ./...
go test -count=1 -cover ./internal/codingagent/tools ./agent/harness/session ./agent/harness/pico3 ./agent ./ai ./parity/cmd/coverage
go test -count=1 -json ./internal/codingagent/tools ./agent/harness/session ./agent/harness/pico3 -run '^TestHarness(Image|JSONL|Pico)'
go test -race -count=1 ./internal/codingagent/tools ./agent/harness/session ./agent/harness/pico3 -run '^TestHarness(Image|JSONL|Pico)'
make -k -o interface-deps correspondence-check porter-check interface-inventory interface-inventory-test interface-go-drift interface-recommendations-drift interface-mapping-quality interface-delta behavior-contracts test-inventory-drift test-inventory format-version-inventory custom-factory-ledger-drift coverage-drift port-map-drift docs-drift
go tool golangci-lint config verify
make lint-changed LINT_BASE=staging/main
make lint
make coverage RESULTS=
```

`-o interface-deps` prevents the installer from running against the supplied dependencies; all requested contract checks execute. `closure-check` is excluded as instructed. No exported Go API changes, so inventory regeneration is unnecessary; `interface-go-drift` passes. No full-repository race or live parity run is claimed. New tests take at most 10 ms in the retained initial/restored runs; none requires a >200 ms justification. All files and storage handles are temporary and closed. No goroutines, clocks, process-global environment changes, or production resource policy are added.

## Proposed delivery/tests.tsv rows

`delivery/tests.tsv` is absent on this base. No delivery file is edited. The following proposed TSV uses an explicit header for the integrator:

```tsv
slice	upstream	disposition	package	tests	proof
next-evidence-harness	packages/agent/src/harness/tools/image.ts	PASS	./internal/codingagent/tools	TestHarnessImageMagicBoundaries;TestHarnessImageBMPPlanesAndBitDepth	parity/testdata/next-evidence-harness/README.md
next-evidence-harness	packages/agent/src/harness/session/jsonl/types.ts	PASS	./agent/harness/session	TestHarnessJSONLHeaderRepositoryRoundTrip;TestHarnessJSONLInvalidHeaderRepositoryOpen	parity/testdata/next-evidence-harness/README.md
next-evidence-harness	packages/agent/src/harness/session/testing/types.ts	TYPE-ONLY	./agent/harness/session/testing	-	parity/unit-evidence/next-evidence-harness.md
next-evidence-harness	packages/agent/src/harness/pico3/index.ts	TYPE-ONLY	./agent/harness/pico3	-	parity/unit-evidence/next-evidence-harness.md
next-evidence-harness	packages/agent/src/harness/pico3/jsonl.ts	PASS	./agent/harness/pico3	TestHarnessPicoJSONLPublicationRecovery;TestHarnessPicoJSONLRetirementAndHighWater	parity/testdata/next-evidence-harness/README.md
next-evidence-harness	packages/agent/src/search/index.ts	TYPE-ONLY	./agent/search	-	parity/unit-evidence/next-evidence-harness.md
next-evidence-harness	packages/ai/src/utils/text.ts	BLOCKED	./agent	TestHarnessTextSystemBlockSeparator;TestHarnessTextEmptySystemSection;TestHarnessTextSystemUpdateLiteralNames (repro only)	parity/testdata/next-evidence-harness/text-red.log
```
