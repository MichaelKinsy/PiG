# Native Linux clipboard test port

Reference: Pi 0.87.1 `packages/tui/test/native-clipboard-linux.test.ts` and `packages/tui/native/linux/src/{linux-platform-x11.c,clipboard-worker.h}`.

## Contract

The helper loads without connecting to a display. It returns unavailable separately from an empty selection. A transfer failure is an error. One private operation survives a timed-out waiter and discards its late result before another operation starts. The caller waits at most three seconds. Discovery and incremental transfers share a two-second deadline. Process shutdown requests cancellation without waiting for uninterruptible private setup, matching Pi's detached thread and natural process-exit contract.

The Go backend uses X11 directly and builds with `CGO_ENABLED=0`. `tui.GetNativeClipboard` exposes the same helper object as the platform accessor where supported. The Windows and macOS implementations in the transferred backend compile on their targets, but desktop execution is not qualified by this Linux test file. Keep native-platform.test.ts partial until its owner qualifies those original platform guards.

## Original cases

Ten case sites expand to sixteen cases:

| Pi test site | Go evidence |
|---|---|
| 113, incremental property metadata | `TestUpstreamX11IncrementalMetadata` |
| 118, concurrent/process-exit/worker-exit | `TestUpstreamClipboardPrivateWorker` and `TestClipboardWorkerReleasesPrivateResults` |
| 129, stalled setup for text/image | `TestUpstreamStalledX11ConnectionSetup` |
| 159, Latin-1 STRING | `TestUpstreamNativeX11Transfers/decodes_X11_STRING_as_Latin-1` |
| 172, refused TARGETS | `TestUpstreamNativeX11Transfers/reads_UTF8_STRING_when_X11_TARGETS_is_refused` |
| 180, empty/text-only selections | `TestUpstreamNativeX11Transfers/returns_null_for_empty_X11_clipboards_and_text-only_selections` |
| 193, Unicode and 4 MiB text/image transfers | `TestUpstreamNativeX11Transfers/preserves_X11_Unicode_and_incremental_text/image_transfers` |
| 214, disconnect/timeout | both `reports_X11_transfer_*` subtests |
| 230, partial text/image cleanup | both `frees_partially_received_X11_*` subtests and `TestNativeX11CleanupFixture` |
| 237, partial text/image stalls | both `times_out_stalled_X11_*` subtests |

The actual original test also runs against the shipped Node helper in `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux`. Only module and fixture locations change. The C fixtures include the vendored production sources. The driver requires all sixteen cases to pass with zero skips; missing Linux dependencies fail qualification instead of silently passing. The explicit TAP reporter makes the execution-count assertion independent of Node's default reporter.

Go allocation evidence uses weak references and allocation profiles for the corresponding receive and accumulation buffers. The Node/C test retains the original allocation counters. These are implementation-specific instruments for the same retained-resource contract.

## Verification

Use the existing host tools `cc`, `pkg-config` with `xcb`, `Xvfb`, and `xclip`.

Targeted Go Linux cases pass with `-count=1 -race`. The original Node/C suite passes all sixteen cases. A compiling mutation that disables STRING-to-Latin-1 conversion fails the real-display assertion. Windows amd64 and macOS arm64 test binaries compile with CGO disabled; neither result is desktop runtime evidence.

The representative 4 MiB incremental-image benchmark records approximately 20.3 ms/op, 24.5 MB allocated/op, and 131 allocations/op in a three-operation qualification sample on this host. CPU samples primarily show allocation/GC and copying. This is a resource observation, not an optimization claim. The worker retains at most one private operation; cleanup tests prove release after invalid metadata and late completion. Shutdown cancels interruptible socket I/O but does not join arbitrary private work, so a blocked authority FIFO cannot hold process exit.

## Review regressions

Review exposed two real differences in the transferred implementation. `TestX11DisplayTransportPrefixes` and `TestNativeX11ExplicitTCPTransport` now cover explicit `tcp/` display prefixes; protocol parsing occurs before IPv6 bracket removal. The real Xvfb text read matches the review's Pi probe.

The process-exit fixture now includes the production `ShutdownClipboard` defer. Its uncooperative private read failed before the fix by hanging past the process deadline. `TestNativeClipboardProcessExitWithBlockedAuthority` also drives a real FIFO authority path. Shutdown no longer joins that uninterruptible setup operation; the process exits naturally after the original three-second waiter bound. Cancellable socket setup still closes and releases its worker slot.

Targeted review guards pass with `-count=1 -race` in `review-native-green.log`; compiling red evidence is in `review-native-red.log`, `review-shutdown-red.log`, and the independent `pair-review-6/clipboard-shutdown-probe.log`.

Detailed command logs, mutation output, and CPU/allocation profiles are retained by the maintainer (`native-linux-first.log`, `native-clipboard-vendored.log`, `native-latin1-mutation.log`, `native-benchmark.log`, and `native-port/*.pprof`).
