# Editor edit-state oracle

`upstream-tests.mjs` executes the original Pi 0.87.1 kill-ring, undo, character-jump, sticky-column and paste-marker sections, excluding completion case2096. It erases TypeScript syntax only. The sections contain the90 KEEP cases and8 already-ported marker cases. The shared runtime loader checks the installed Pi version before loading its Editor.

`25-editor-edit-state.toml` compares native production Editor input, programmatic insertion, history, expansion, submission, cursor and every complete rendered row. The fixtures use identical action sequences from `cases.json`. Hex retains text and terminal ANSI bytes without trimming or normalization. The rendering uses an identity border-color callback on both sides and the original24-row terminal's seven-line editor cap.

Pi `components/editor.ts:1112-1164` normalizes programmatic changes, resets the paste registry on replacement, and snapshots the old state. Lines1200-1217 coalesce consecutive word characters but snapshot before each whitespace input. Lines1251-1314 snapshot before registering a paste. Lines1995-2129 snapshot yanks and restore text, cursor and paste registry on undo. Lines1540-1589 reset or preserve the preferred column according to the vertical movement decision table. No asynchronous provider work occurs in this fixture.

## Regression evidence

The pre-fix Go engine fails original cases for kill accumulation across SetText, per-character undo, undo cursor restoration, CRLF insertion, paste registry restoration after replacement, and Ctrl+A sticky reset. The fixed engine passes these exact original inputs. Canonical25 also differs from Pi when built with the pre-fix engine through a disposable Go overlay.

The following source mutations each compile, fail the named original Go group, and change canonical25's complete output relative to Pi:

| Mutation | Rejecting Go group |
|---|---|
| Ignore valid paste IDs in the wrapping cache | `TestEditorSetTextSameMarkerInvalidatesAtomicWrap` |
| Snapshot every typed character instead of word coalescing | `TestUpstreamEditorUndo` |
| Save a zero cursor in each snapshot | `TestUpstreamEditorUndo` |
| Omit the paste registry from snapshots | `TestUpstreamEditorPasteMarkerAtomicBehavior` |
| Retain lastAction across SetText | `TestUpstreamEditorKillRing` |
| Leave the previous yank in place during yank-pop | `TestUpstreamEditorKillRing` |
| Assign cursor column directly on Home, retaining sticky state | `TestUpstreamEditorStickyColumn` |
| Bypass normalization in programmatic insertion | `TestUpstreamEditorUndo` |

The wrapping cache also depends on the valid paste-ID set. Replacing a document with the same visible marker text clears its registry; the next render must wrap that marker as ordinary text. `TestEditorSetTextSameMarkerInvalidatesAtomicWrap` red-proves this cache invalidation boundary, and canonical25 compares the complete narrow rendered rows against Pi.

`BenchmarkEditorEditUndo` measures real typing, deletion, paste, undo and rendering for empty, ordinary and1024-character inputs. It is a baseline, not a speedup claim. Each iteration owns its Editor. Snapshot text/registry references live until undo, submit or Editor release; popped slots are cleared. Consecutive word input does not allocate one full-document snapshot per character. This work introduces no goroutines, timers or external resources.

## Qualification boundary

This fixture contains valid UTF-8 text. It does not qualify lone-surrogate JSON transport, subprocess terminal listeners, or autocomplete acceptance. The native UTF-16 tests and separate subprocess qualification own those boundaries. Passing native Editor tests does not establish cross-SDK surrogate preservation.
