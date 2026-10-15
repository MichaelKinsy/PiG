# lw-proc-output: `!` output coalesced into frames, equivalence proof

Slice lw-proc-output (LW roadmap S7), finding TO-T2. Commit `e171027364` changes when the interactive mode draws a running `!` command, not what a frame shows. The other commits of the slice (`5c0980060d`, `4cbe86cc58`, `baf05fe514`) change no bytes; their evidence is listed at the end.

## The change

Before: `OnChunk` posted `block.AppendOutput(chunk); tui.Render()` to the owner loop and waited for it, so every chunk drew a full differential frame, synchronously, and the pipe reader stalled until it was drawn.

After: `OnChunk` posts `block.AppendOutput(chunk); tui.RequestRender()` and returns. `RequestRender` is the renderer's coalesced request, throttled to the 16 ms frame (upstream `requestRender`).

Pi (`interactive-mode.ts` handleBashCommand, v1.0.3 line 7014-7020) does exactly this: `this.bashComponent.appendOutput(chunk); this.ui.requestRender();`. pig's old per-chunk render was a divergence from Pi's frame points, not a mirror of them.

## Why a frame shows the same thing

| Property | Argument | Evidence |
|---|---|---|
| Chunk order | The owner loop runs posted tasks FIFO (`uiTaskCh`, `postToMain` blocks when the queue is full). Each chunk is still appended by its own `AppendOutput` call, in the order the executor produced it, so the block's state after the k-th task equals its state after the k-th chunk before. | `TestUserBashCoalescedFramesMatchPerChunkFrames` |
| Completion after the last chunk | `finishUserBash` (and the error path) still posts its completion through `awaitUserBashMain`, after `ExecuteBashWithOperations` returned, so it queues behind every chunk task. | same test: the last frame equals the completed block |
| Cancellation | A queued chunk task checks `task.owner.Err()` when it runs, as `awaitUserBashMain` did, so a mode that shut down applies no further output. | `TestInteractiveUserBashShutdownInvalidatesQueued*`, `TestInteractiveEscapeCancelsRunningUserBash` |
| What a frame draws | A frame renders the component tree as it stands. Because chunks apply in order, a frame drawn after k chunks equals the frame the per-chunk render drew after chunk k. Coalescing skips some of those frames; it never draws one the old path could not draw. | `TestUserBashCoalescedFramesMatchPerChunkFrames` records the block rows of every render pass the TUI makes (a probe component in the tree, renders dispatched to the owner loop as in the real mode) for 153 chunks with pauses of up to 3 ms. Every running frame equals a per-chunk frame of a reference block fed the same chunks, in chunk order, and the last frame equals the reference block after `SetCompleteWithOutput`. Run: 21 frames for 153 chunks. The loader row is left out of the comparison because its spinner animates on its own clock in both forms. |
| Terminal bytes | The differential renderer writes the difference between the previous drawn frame and the next. With fewer frames the intermediate byte stream differs, as it does in Pi, whose frames fall wherever its 16 ms throttle fires. The final screen is the completed block in both. | `TestBashExecutionComponentMatchesPi` (block rendering against Pi), the user-bash suites listed below |

## Tests run by name (all green, Pi 1.1.0 mirror)

internal/codingagent: `TestUserBashChunksCoalesceIntoFrames` (new; base 302 synchronous renders for 300 chunks, now 2), `TestUserBashCoalescedFramesMatchPerChunkFrames` (new; passes on the base too, where every frame is a per-chunk frame), every `Test*` in `interactive_user_bash*_test.go`, `bash_execution_oracle_test.go`, `interactive_pending_bash_test.go`, `interactive_resume_bash_test.go`, `user_bash_port_test.go`, `bash_executor_test.go` and `output_pad_blocks_test.go` (38 tests).

## The byte-neutral commits

- `5c0980060d` (truncate): the cut-once walks equal the split/join forms field by field (`TestTruncationCutMatchesSplitJoin` in internal/truncate and extensions/sdk, 4000 random contents with limits from -3 to 100 each), and the oracles `TestTruncateTailMatchesPi`, tools `TestTruncateMatchesPi`, durable `TestTruncateMatchesPi` (16000 ops, 0 differing), `TestTailWindowMatchesTruncateTailOnEveryChunkSequence`, `TestHeadJoinEqualsTruncateHeadOfTheJoinedText`, `TestGrepContextOutputMatchesPi` stay green. One edge moved from a panic to Pi's value: a negative byte limit on an oversized last line keeps nothing, as `Buffer.slice` past the end does (the SDK already did this).
- `4cbe86cc58` (text): `TestScanFirstStripAndSanitizeMatchTheCopyingForms` (20000 random mixes) and `FuzzScanFirstStripAndSanitize` (30 s) compare with the copying forms; `TestSanitizeBinaryOutputMatchesPi`, `TestAnsiMatchesPi` green.
- `baf05fe514` (pipe reads, accumulator, user-bash executor): `TestUTF8StreamDecoderChunkingMatchesWholeDecode` (5000 random byte streams in random chunks, including invalid, truncated and BOM-split input), `TestUTF8StreamDecoderMatchesNodeTextDecoder`, `TestOutputAccumulatorMatchesPi`, `TestOutputAccumulatorTailMatchesTheStringTail`, `TestExecuteBashWithOperationsMatchesPi`, `TestSplitIncompleteAnsiSuffixMatchesPi`, `TestTruncatedResultDetailsShareTheReturnedText`. A pipe chunk is now lent for the `OnData` call; every in-tree consumer copies what it keeps.

`TestBashToolMatchesPi` passed on the final tree (200 of 200 probes, load 97). Three earlier runs at load 85-112 each differed on one to three timeout probes (`echo start; sleep 5; echo never` with a 1 s timeout, once a 10 s probe), a different probe each run: Pi, starved, had not printed `start` before its timeout, or pig's kill landed after `sleep` died. The base worktree passed in the same window.
