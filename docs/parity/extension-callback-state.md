# Extension callback state regressions

## Expanded editor state at terminal callbacks

Pi 0.87.1 `packages/coding-agent/src/modes/interactive/interactive-mode.ts:2560` returns `editor.getExpandedText()` when available. A compact paste marker is editor presentation, not the value of `ctx.ui.getEditorText()`.

The Mode's remote-listener admission path copied `editor.Text()` into `TerminalInputArgs`, while native SDK host queries used the expanded owner snapshot. Node therefore received `[paste #1 +11 lines]` where Pi and the native query returned the full eleven-line text. The admission path now reads the same `ExtUIContext.GetEditorText()` as the host query. On a bound editor this is the existing immutable owner-published string; no new cache, worker or per-key paste expansion is introduced.

`TestTerminalInputSnapshotKeepsExpandedPasteContents` drives the production input loop, listener pass and subprocess bridge. It preserves empty, ordinary, 1,000-character boundary, 1,001-character compact, multiline compact, UTF-16 compact, reset and replacement inputs. All three compact cases were red while the native getter was correct. A compiling mutation restoring the raw editor read fails the same cases. Restored source passes ten race-enabled runs and the full internal coding-agent race suite.

Scenario `49-terminal-input-expanded-editor` loads the same Node extension into actual Pi and PiG. It retains the callback Context after command completion, sends bracketed paste through the real terminal/editor path, and compares the complete JSON editor value between fixed capture markers. Before the fix PiG reports the compact marker; Pi reports the full paste. Restored source passes three `output_equal` pairs without normalization or a crop change.

`BenchmarkTerminalInputEditorSnapshotRead` covers empty, 1,000-character, 64 KiB and 1 MiB text. Three Go 1.27.1 linux/amd64 samples measure 2.4–2.8 ns per cached read, zero bytes and zero allocations. Profiles are retained. This isolates the shared snapshot getter, not IPC, initial paste expansion, rendering or complete keystroke latency. The existing editor owns the retained string and releases it on replacement.

## Python provider cancellation after request arming

The generic Context lifetime change moved request cancellation creation into `_arm_request`. The `provider_stream_simple` branch still referred to its former local `cancel` variable. Both real isolated and packed Python producer tests failed with `NameError: name 'cancel' is not defined` before the first successful stream.

The dispatcher now aliases the existing `ctx._cancelled` signal. The callback, active-stream admission, event forwarding and terminal check share the same signal. The read under `_state_lock` uses that signal directly; it does not call a Context getter that would reacquire the lock. No new signal, Provider lease, parent promotion, timeout or socket-pump policy is added.

The existing `TestProviderProducersAcrossSDKs/python-isolated` and `/python-packed` are source-red and pass three race-enabled runs after the fix, including Stream, StreamSimple, rejected credentials and cancellation. The complete nine-realization producer matrix also passes. `test_provider_dispatch_uses_armed_context_cancellation` checks normal completion and cancellation before admission, during iteration and after iterator exhaustion. A syntactically valid mutation substituting a fresh `ProviderSignal()` fails all three cancellation rows. Active-stream and request records are empty after dispatch.

## Qualification

User documentation is updated in `docs/site/docs/extensions.md`. Go native/Windows vet, parity-tagged touched lint, the Python SDK suite, the full internal coding-agent race suite and targeted conformance pass. Scenarios49,48,35c,41 and providers-registry22 each pass all three declared Pi pairs. `ci-drift` reaches pending D78 scrutiny and `ci-contracts` reaches the existing 218 release findings; neither is waived. The broad changed-file lint command against public/main includes nested module paths that the root module cannot load; direct touched-package lint and changed-file lint against this patch's own base pass. These fixes do not close the remaining runner/provider registration cases, factory-failure/cache cases, Markdown ordering, native ResourceLoader work, or separately reported export ordering. Merge-train work remains paused by the lead; this source is qualified on the lane's existing base.
