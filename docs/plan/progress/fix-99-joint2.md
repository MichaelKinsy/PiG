# Lane fix-99-joint2: joint-run regressions no lane owns

Base `porter/pi-0.99.1` (4a03576fe). Scope: the family 2 RPC oracle regressions, the Darwin pasteboard file-path test, and the `internal/codingagent` clipboard tests that died under memory pressure.

## 1. Family 2 RPC (`cmd/pig`)

Both reds are deterministic on the base tree (`TestRPCPiMessagesMatchesPi` tool/buffered and tool/pending; `TestRPCBedrockConverseStreamObservation` buffered text, thinking, tool and mixed: 100 of 100 runs on the base, not 1 in 10). Cherry-picking `909b8ae54`, `2ae76304e`, `588ac54b4` and `2973753a5` from `rev-fix-rpc-buffered` (as `-x` commits, same content as the accepted branch) fixes neither: they address the lazy-stream forwarder registration, the Responses wire test and the fixture server, which is a different defect. They are kept because they are the reviewed fix for the 0.3.1 buffered race and are not on this line yet.

Method: a copy of upstream's real 0.99.1 `pi --mode rpc` was run under `node --import` with hooks on `AssistantMessageEventStream.prototype.push`, `JSON.parse`, `JSON.stringify` and an `async_hooks` job counter (scratch under `/tmp`, not committed); the same events were traced from the Go binary with a temporary counter on the executor's microtask hook (removed before committing).

### pi-messages: earlier shallow copies keep the old usage object

Pi: `pi-messages.ts:195-217` `Object.assign(partial, { usage: event.usage })` assigns a new usage object (and `appendAssistantMessageDiagnostic`, `diagnostics.ts:42-47`, a new diagnostics array). `agent-loop.ts:408-416` emits `{ ...partialMessage }` at `start`; that copy keeps the usage the partial had, while its `content` array stays shared. The real trace has `rpc:message_start usage=0` after `push:s0:done`, with the tool call in `content`. PiG published the terminal usage in place, so a shallow copy saw 10/3/13.

Fix: `AssistantMessageEventStream.push(event, replacements)` (unexported) carries the nested objects a producer assigned; `Push` passes none. The pi-messages converter records `Usage` (and `Diagnostics` for a rewrite) on `done` and `error`. `ai/event_stream.go`, `ai/assistant_publication.go`, `ai/pi_messages.go`, `ai/pi_messages_events.go`.

Tests: `ai.TestPiMessagesDoneReplacesUsageRetainedByEarlierShallowCopy` (pure layer, red before: copy observed usage 10/3/13), `TestRPCPiMessagesMatchesPi` (caller boundary, 30 cases). Mutation: `replaced.Usage = false` turns both red.

### Bedrock: the Session listener ran one reaction early

Pi: `agent-session.ts:1098` awaits `_emitExtensionEvent`, which awaits `runner.emit` (`:1267`): public listeners run two reactions after the Agent emits `message_start`. PiG modeled one (`emitSynchronousSessionEvent`). Provider production timing matched Pi exactly (`s0:start` to `s0:text_start` = 26 jobs in Node and in Go; layer spacing equal); the RPC serialization was at +25 in Go and +27 in Pi, so it fell before the second Bedrock item. 0.99.1's extra per-event `await onProviderStreamEvent` slowed the provider by a round and exposed the latent shortfall; the 0.87.1 oracle windows were wide enough to hide it. The earlier hypothesis (PiG passes no observer) is not the cause: `await undefined` and `await asyncFn()` both cost two jobs in Node 24.19.0, measured.

Fix: `coding/session_boundaries.go` `handleAgentEvent` yields once after the extension hook, before `emitSynchronousSessionEvent`, for events that carry a stream observation.

Tests: `TestSessionListenerSeesMessageStartAfterTwoExtensionEventRounds` (`coding`): oracle is a real 0.99.1 `createAgentSession` with a producer pushing `start` and then `await null` per round, which logs `push start | tick1 .. tick5 | LISTENER message_start | tick6`; red before (listener after tick4). Caller boundary: `TestRPCBedrockConverseStreamObservation` (12 cases x 200 runs) and `TestRPCPiMessagesMatchesPi`. Mutation: removing the yield turns the new test and the Bedrock replay red.

### Regression check

`go test ./cmd/pig`, `./coding`, `./agent`, `./ai`, `./internal/codingagent` failure sets equal the base's, except the two fixed tests. With `UpstreamVersion` set to 0.99.1 locally, the `TestRPC33*` matrix passes before and after. Load: `GOMAXPROCS=4`, 12 CPU burners pinned to the same 8 CPUs: `go test -race -count=24` on the `ai` pi-messages tests and the new coding test pass; `go test ./cmd/pig -run "TestRPCPiMessagesMatchesPi|TestRPCBedrockConverseStreamObservation" -count=3` passes (1317 s under load).

## 2. Darwin pasteboard file paths

Not reproducible on Linux (macOS-only native module). darwin-platform.m CLIPBOARD_FILES returns each URL's `fileSystemRepresentation`, which the port mirrors exactly, so the port is faithful and the test's expectation was too strict: the platform may return decomposed Unicode ("é" as "e" plus U+0301) and may drop `/private` from `/private/tmp` or `/private/var`. The test now creates the files and requires each returned path to be `os.SameFile` as the one written. **Mark for macOS verification**: `go test ./internal/nativeplatform -run TestDarwinPasteboardFilePaths` on a Mac; `GOOS=darwin go vet` (arm64 and amd64) passes.

## 3. `internal/codingagent` clipboard tests

Not a leak: `TestClipboardImageCommandEnforcesDefaultBufferLimit` peak RSS is flat across repeats (259840 KiB at count 1, 258048 KiB at count 30, no lingering children). It reads 50 MiB twice in one process and the peak was the sum of both large cases. The test now returns each case's garbage to the OS (`debug.FreeOSMemory`), peak 260 MB to 158 MB. A production change to chunk-and-join the output as `clipboard-command.ts` does cut total allocation (2.67x to 2x) but raised the resident peak (319 MB), so it was reverted. `TestRunClipboardCommandEncodesJavaScriptUTF8` ("signal: killed") is the 5 s deadline killing a helper that re-executes the whole test binary while the host was swapping; it passes in 0.3 s and 32 MB here. The deadline is unchanged and no code cause exists to fix.
