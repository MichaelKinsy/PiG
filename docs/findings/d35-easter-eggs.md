# Hidden command parity: D35

## Contract and scope

Pi 0.87.1 is the oracle. This change restores upstream behavior, not an additive PiG product feature. `interactive-mode.ts` handles exactly `/arminsayshi` and `/dementedelves` before compaction queues and model dispatch. Neither command belongs in the builtin command registry, autocomplete, or help. Arguments and case variants remain ordinary prompts. Only the editor submit handler recognizes the commands: they never enter editor history, and an initial message or compaction-queued replay of the same text reaches `session.prompt` as ordinary model input (`interactive-mode.ts:1164-1166`, `4616-4690`). The displayed content is transient and does not enter Session history.

`armin.ts` packs the 31×36 LSB-first XBM into eighteen half-block rows. The port preserves effect selection, random-call order, Fisher–Yates shuffling, all seven frame transitions, accent styling, clipping, padding, and the untruncated label. Node truncates the timer delay to 33 ms, or 16 ms for glitch. Pi's rain effect never reports completion because empty columns never settle. Its rendered image still converges to the final bitmap. The port retains that behavior while its component is mounted.

`earendil-announcement.ts` supplies both dynamic borders, padded title and blog text, spacers, and the shared terminal Image component. The image uses `maxWidthCells: 56` and filename `clankolas.png`. Unsupported terminals render Pi's image fallback. Kitty and iTerm2 use the existing image encoders. Component invalidation reaches the image so capability negotiation can replace a cached fallback. The PNG is embedded unchanged; `internal/codingagent/assets/README.md` and `REUSE.toml` record its MIT provenance and checksum.

There are no upstream test cases referencing either component or command in the pinned coding-agent test tree. The new differential tests execute the exact pinned component sources. The unchanged covering interactive-mode tests continue to run with the owning Go package.

## Regressions and proof

- `TestHiddenEasterEggsSubmitDuringCompaction` failed before implementation: neither label rendered, and both commands appeared in the compaction model-input queue.
- Review rev-d35 moved recognition from the shared `handleSubmitWithImages` entry into `setupEditorSubmitHandler`. `TestHiddenEasterEggsEditorSubmitSkipsHistoryAndPromptLoop` and `TestHiddenEasterEggsNonEditorSubmitPathsReachModel` were red before that move: idle submission recorded the command in editor history and deferred it through the prompt loop, and the initial-message/compaction-replay entry rendered the component instead of sending model input.
- `TestHiddenEasterEggsStreamingInputAndExactMatch` drives the editor submit callback while streaming. Only argument/case variants enter the model queue.
- `TestArminFramesMatchPinnedPi` compares every intermediate frame under an identical deterministic random stream. Completion occurs at frames 187, 19, 38, 19, 9, and 28 for typewriter, scanline, fade, CRT, glitch, and dissolve respectively. Rain is compared for 600 frames without declaring it complete. Initial and final render tests cover widths 0, 1, 12, 31, 32, and 80. A compiling mutation from the first XBM byte `0xff` to `0xfe` fails all seven effect comparisons.
- `TestEarendilAnnouncementMatchesPinnedPi` compares the complete ANSI-bearing render at widths 1, 12, 32, 56, 80, and 120. `TestEarendilEmbeddedAssetAndImageProtocols` checks source-byte identity and the production component's Kitty/iTerm2 configuration and image rows.
- `TestEarendilInvalidationRefreshesImageCapabilities` was red before propagating invalidation to the image: the announcement incorrectly retained its text fallback after Kitty capability negotiation.
- The existing `TestNoRetainedRendererBoundMethodValues` found a captured renderer callback in the first implementation. The callback now resolves the current renderer through `requestRender`. `TestArminAnimationFollowsRendererReplacement` exercises the queued frame after replacement.
- Timer tests cover frame publication, completion, repeated disposal, rejection of a queued frame after disposal, and clear-time draining. The worker owns a ticker and allows at most one queued frame. Only the UI owner mutates the bitmap. Clear, Session rebuild, compaction replacement, and mode shutdown cancel and join the worker.
- `slash-commands/12-earendil-announcement` and `13-armin-bitmap` pass three Pi/PiG pairs each with both `output_equal` and `escaped_output_equal`. They fail with the pre-fix submit handler restored through a compiling Go overlay. The bitmap scenario waits for every source-derived row rather than sleeping through a random animation.
- Cross-checks `slash-commands/03-new-session` and `09-hotkeys-styled-tables` pass their declared runs with unchanged assertions.

## Verification

The following pass:

- `go build ./...`
- `go vet ./...`
- `go test ./internal/codingagent ./tui ./test/upstream-parity -count=1`
- `go test -race ./internal/codingagent -run 'Test(Armin|HiddenEasterEggs|Earendil|NoRetainedRendererBoundMethodValues)' -count=1`
- `go tool golangci-lint config verify`
- `make lint-changed LINT_BASE=HEAD`
- `make divergence-quality docs-drift port-map-drift test-porting-release`
- `make divergence-consistency divergence-guard`
- `make normalization-inventory lint-scenarios` in the disposable generated checkout
- `go fix -diff ./internal/codingagent` (empty)

`make lint` reports an existing goimports error at `coding/extension/host/subprocess/owner_upgrade_test.go:8`. That file is unchanged. The default `make lint-changed` also selects an absent nested module from the release branch's diff against main; the explicit lane baseline checks all changed Go files successfully.

`go test ./...` is not green. Broader failures include missing TypeScript/esbuild/upstream-test dependencies, Cargo's unresolved mise shim inside isolated homes, missing XCB development headers, and the Node URL oracle's `file://xn--a.b/x` mismatch. These are outside D35. The active-divergence header mismatch introduced by removing D35 was fixed, and the complete upstream-parity package now passes. The complete coding-agent and TUI packages pass after supplying the already installed exact Pi 0.87.1 package through ignored local dependency links.

The installed Go version is 1.27.1. Node 26.7.0 and tmux 3.4 differ from the qualified development versions. No global toolchain settings are changed. The parity runner cleans its private tmux sessions and keeper; no owned tmux server remains. No command writes to the user's PiG home.

`make generate` passes in a disposable checkout containing this change. The lane leaves generated files untouched as requested. Integration must regenerate `AGENTS.md`, `test/parity/coverage.md`, `test/parity/interfaces/pig-go.json`, and `test/parity/interfaces/recommendations-v0.87.1.json`, then refresh the scenario normalization inventory. The generated port denominator is 422/557, with 406 behaviorally verified entries. No hand-edited inventory or coverage claim is included here.

## Resource measurement

On an Intel Xeon Platinum 8462Y+ with Go 1.27.1, `BenchmarkArminFrameRender` measures approximately 23.4 µs, 8,293 B, and 137 allocations per animated frame through the production Container. `BenchmarkEarendilAnnouncementRender` measures approximately 3.56 µs, 1,520 B, and 8 allocations for a cached fallback render. CPU and allocation profiles accompany the lane's test logs. The bitmap state is fixed-size; the PNG is embedded once, its base64 encoding is computed once, and the shared Image component caches protocol output. These measurements describe the component paths, not a claim of whole-Session rendering speed.
