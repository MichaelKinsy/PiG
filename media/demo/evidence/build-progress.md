# Piglet build progress evidence

## Contract

D18 owns these product-neutral diagnostics. The builder reports work at the call that performs it. It does not add fetches, split a compiler invocation into invented phases, or change extension placement, compiler inputs, cache identity, signing, or record verification. The reporter uses the pinned Pi loader's ten Braille frames and 80 ms interval. It renders dim step text beneath the phase and elapsed time.

`--verbose` streams compiler output with member labels. A packed cell labels its member set. The final Go invocation labels the Piglet because it compiles fused members together. Non-terminal stderr receives plain phase lines. JSON and source-script stdout keep their existing framing. Failures name the current phase and show a diagnostic tail and hint. Stock PiG's optional macOS ad-hoc signing remains best-effort but reports a warning instead of discarding the failure.

## Regression evidence

The following tests fail against the base Go implementations, overlaid from `staging/mirror/main`, and pass with progress reporting:

- `TestBuildReportsResolvingPhaseBeforeManifestFailure`: base prints only the manifest error, without the phase or hint.
- `TestBuildAcceptsVerbose` and `TestBuildVerboseStreamsCompilerFailure`: base rejects `--verbose`.
- `TestBuildJSONKeepsProgressOffStdout`: base has no phase-bearing error or live progress.
- `TestNativePigletBinaryBuildLeavesSourceTreeUntouched`: a successful base build omits the ordered phase trace. The test still checks that the build leaves source untouched and that the resulting binary starts.
- `TestContainerBuilderBuildProducesHostArtifactAndContainerIdentity`: base emits no phase events and does not forward verbosity. Its artifact and provenance assertions remain intact.
- `TestBuildCommandReportsCompilerFailureAndPhase`: base shows the compiler error but omits the phase and hint.

`test_build_failure_streams_diagnostics_and_keeps_log` fails before the demo launcher uses `tee`: the compiler diagnostic exists only in the log. It passes after the launcher streams that log and preserves pipeline failure status.

The real mixed-language demo initially failed with Rust E0631. Its three event callbacks took `Value`, while the current SDK accepts `&mut Value`. `test_rust_demo_builds_against_current_sdk` reproduces those errors against a disposable crate and passes after the signatures match the SDK.

The new `TestBuildSigningFailureIsVisibleAndBestEffort` and `TestToolLinesPreservesUTF8AtChunkBoundary` were added after implementation. Both have compiling mutation evidence: discarding the signing result removes the warning, diagnostic, and hint; flushing a full byte buffer without retaining an incomplete UTF-8 rune splits the diagnostic text.

`TestReporterGolden` compares exact escaped output against `internal/buildprogress/testdata/tty-{true,false}.golden`. `TestReporterDiagnosticPreservesStatus` proves that backend warnings clear and redraw the status rows instead of appending to the dim subtext; a compiling direct-write mutation fails the exact output assertion. Other reporter tests cover narrow terminals, terminal-control sanitization, bounded output tails, long and fragmented lines, cancellation, concurrent callbacks, timer joining, and no writes after close. `TestPhaseEventsAndLiveToolOutput` waits for a compiler line before releasing the child process, so completion-buffered output cannot pass.

## Review regression evidence

`TestReporterPlainDiagnosticsStripTerminalControls` and `TestReporterPlainFailureStripsTerminalControls` prove that backend diagnostics and phase failures remove terminal controls while preserving line breaks, tabs, and the structured error cause. `TestBuildCommandPlainOutputPathFailure` drives the source-build caller with an invalid output path containing ANSI controls. All three fail with the original reporter from `974d82e404` and pass after sanitization. The existing exact TTY and non-TTY goldens remain unchanged.

## Demo builds

The production demo sources were staged into a disposable HOME using the same `pig-go` and `pig-demo` compositions as `media/demo/stage.py`. The first selects the Go `pigrunner` factory. The second also selects Rust `chain-rs`. The native toolchains and existing dependency caches were used offline. `media/demo/build-piglets.sh` completed both builds and printed their checksums.

Representative redirected trace for `pig-demo`:

```text
Resolving manifest (0s) — pig-demo.yaml
Resolving extensions (0s) — pig-demo
Packing resources (0s) — Inlining prompts and skills
Selecting builder (0s) — auto
Locking build inputs (0s) — Hashing source, extensions, and toolchain identities
Preparing fused Go members (2s) — pigrunner
Building rust members (2s) — chain-rs
Compiling Rust members (2s) — chain-rs (dependency resolution, compile, link)
Checking fused Go members (35s) — Process-global safety checks
Packing build inputs (35s) — Embedding cells, resources, and component records
Compiling and linking Go binary (35s) — pig-demo → <output>/pig-demo
Checksumming embedded cells (39s) — pig-demo
Checksumming and verifying binary (39s) — <output>/pig-demo
Writing binary and records (39s) — Publishing verified artifact to the managed store
Built <output>/pig-demo (36057351 bytes) in 39s
```

The Go-only demo completed in 12 seconds and produced 34681095 bytes. These are observations, not performance thresholds or reproducibility claims.

A separate PTY build of the mixed-language demo used `--verbose --json`. It produced its first stderr output in 0.036 seconds, showed multiple spinner frames and dim step text, streamed `[chain-rs]` Cargo lines, and completed in 38.9 seconds. Stdout parsed as one successful JSON result. The captured PTY trace contained 60505 bytes. The verification checks ran before the final summary.

## Resource disposition

A terminal reporter owns one ticker goroutine. `Close`, success, and failure join it. Pipes start no ticker. Tool output callbacks execute synchronously under the reporter's mutex, so slow output applies backpressure rather than growing an event queue. The line assembly buffer holds at most 4096 bytes and preserves UTF-8 boundaries. The reporter reuses a 16 KiB diagnostic-tail buffer. The existing compiler-output capture remains intact because shared extension builders use it for build-failure classification; the reporting path adds no second unbounded capture.

`BenchmarkBuildProgressOutput` measures repeated member-prefixed compiler lines with allocations. Three local samples with CPU and allocation profiles found that concatenating the diagnostic tail allocated about 18.7 KiB per event. Reusing the fixed-capacity tail reduced the samples to 257 B and eight allocations per event, with approximately 1.03–1.08 microseconds per event on this host. These microbenchmarks do not claim a change in compiler or total build latency.
