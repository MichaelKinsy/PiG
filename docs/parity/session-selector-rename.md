# Session selector rename

## Reference and original cases

Pi 0.87.1 is the oracle. `packages/coding-agent/test/session-selector-rename.test.ts` has three cases. The Pi adapter transpiles those original bodies without changing their inputs or assertions and imports published production modules.

| Site | Original case | Go evidence |
|---|---|---|
| 41 | shows rename hint in interactive /resume picker configuration | `TestUpstreamSessionSelectorRename` |
| 60 | does not show rename hint in --resume picker configuration | `TestUpstreamSessionSelectorRename` |
| 79 | enters rename mode on Ctrl+R and submits with Enter | `TestUpstreamSessionSelectorRename` |

The last case supplies `Old`, Kitty Ctrl+R, `X`, and Enter. It requires the rename panel, excludes the resume panel, and requires exactly one callback with `/tmp/a.jsonl` and `XOld`. The path is an inert fixture identifier; no test writes to it.

`session-selector.ts:726,794-796,885-918` owns one persistent Input and keeps an empty submission open. `input.ts:60-63` replaces the value while retaining and clamping the UTF-16 cursor. It does not move the cursor to the end or round a surrogate-half position. Input insertion and deletion at `input.ts:248-285` slice in those same units. `TestSessionSelectorRenameRetainsInputAcrossVisits` and `TestSessionSelectorRenameEmptySubmissionKeepsFocus` cover repeat visits and empty submission. These are supplemental cases, not additions to the original denominator.

## Root causes and corrections

The native selector recreated a completion-latching input on each visit. Its `SetText` moved to the end. The selector now retains a callback-driven `NewInput`, uses clamped `SetText`, and keeps its submit/cancel callbacks. The existing owned modal filesystem worker remains unchanged.

The shared Input stored byte offsets. It now stores UTF-16 offsets and retains unpaired units in internal WTF-8 strings. Slicing, insertion, deletion, undo, kill/yank, word movement, rendering and mouse positioning use the same representation. Terminal output encodes unpaired units as Node does; it does not mutate the retained input value. Host confirmation inputs retain their existing end-prefill `SetText` contract.

One older render guard required the contiguous string `alpha` inside ANSI output. With the correct initial cursor, Pi renders `> ESC[7maESC[27mlpha`. The guard now asserts that exact cursor-bearing fragment instead of the incompatible substring.

## Authorized dependency transfer

The TUI owner authorized the Input implementation from `afaedb0b68e8fb83ed4f9237c4aa5ffed204cf04`. The immutable `edcb41210d5802826b9928ea140a3e684217d1e2` snapshot has the same Input, word-navigation and width files. The transfer includes the three required `internal/jsstring` files, word segmentation and reproducible ICU dictionary data, license notices, narrow width changes, and Input/word/utility tests. It does not import that branch's ancestry, Editor, stdin, key decoder, or generic focus changes. Only the now-caller-free `graphemeAt` helper is removed from the older grapheme file.

All 36 original Input cases and all 19 original word-navigation cases are present. `test/parity/testdata/pt-tui-input-terminal/port-input-tests.mjs` derives the straight-line Input cases from the pinned source. Render and submit cases retain their original handwritten assertions. The shared segmenter now includes ICU 78.3's Southeast Asian engines and rule tailoring; D27 is narrowed to ICU's process-wide engine-cache history.

## Differential and mutation evidence

`test/parity/scenarios/session/10-session-selector-rename.toml` uses `output_equal`, three pairs, and no normalization. It executes the three original selector bodies, all 36 original Input bodies and all 19 original word-navigation bodies. It compares exact numeric UTF-16 units for fresh, retained, clamped, empty, astral-prefix, split-pair, backward-delete-half and forward-delete-half inputs. A high-surrogate/X/low-surrogate result is `[55357,88,56832]`, not a rounded scalar or replacement character.

The initial native original case failed with `OldX`. The independent Input guards failed on cursor retention and both surrogate-half deletions. Six compiling mutations fail their owning assertions:

- Start backward word movement from the wrong offset: original word-navigation and Input word-deletion assertions fail; the paired scenario fails.
- Invert hint visibility: both original hint cases fail; the paired scenario fails.
- Move the replacement cursor to the end: the original callback becomes `OldX`; the paired scenario fails.
- Round through Unicode scalars: surrogate-half guards fail; the paired scenario fails while Pi retains the units.
- Recreate the Input on entry: repeat-visit cursor assertions fail.
- Exit on blank submit: the empty-submission focus assertion fails.

The repeat-visit and empty-submission guards were added before their final correction but were not independently red-run until the compiling mutation campaign. The initial selector test invocation also contained a misspelled cleanup method; only the subsequent compiling `OldX` failure counts as original-case red evidence.

External logs, overlays, benchmark profiles and raw paired artifacts are retained in the session-selector-rename lane evidence directory. `mutations.py` uses subprocess-local overlays; no mutation remains installed.

## Resource lifetime and measurements

The selector retains one Input per picker. Its loader work is cancelled and joined through the existing `close` owner. The change adds no goroutine, IPC, filesystem operation or Session-history scan to input handling. Input transformations still scale with the edited string, not transcript history. The dictionary borrows immutable embedded data and does not construct a runtime dictionary map.

`BenchmarkSessionSelectorRename` measures construction, loader completion, rename, render, callback and joined cleanup without disk I/O. One recorded sample measured approximately 30.5 microseconds/10 KB for empty names, 32.9 microseconds/11 KB for 64-byte names, and 1.48 milliseconds/649 KB for 64 KiB names. `BenchmarkInputWordEditing` measured approximately 118 microseconds/56 KB for ASCII and 791 microseconds/143 KB for CJK. Allocation and CPU profiles identify string conversion/slicing, rendering and word segmentation costs. These are characterization measurements, not a speedup claim or a disk/IPC latency bound.

## Verification boundaries

Full `internal/jsstring`, `internal/wordsegmenter`, `tui/widthx`, `tui`, and `internal/codingagent` packages pass. Focused Input/word/rename race tests pass three repetitions. Native `go vet ./...`, Windows vet for touched packages, configured touched lint, `make lint-changed LINT_BASE=7481cf519`, and full `make lint` pass.

The exact rename scenario and selector search/path-delete cross-probes pass at declared durability. `make ci-contracts` passes inventory checks after local regeneration and rejects remaining pending/partial hot-path tests. `make ci-drift` rejects the existing D78 record without approval. The full hermetic Session run passes rename and startup selector cases but reports separate failures in `17-session-lax-message-content` (production UIBridge prompt getter signature drift, owned by the parent lane) and `10-terminal-disconnect-exit` (exit 1 instead of 129). These are not treated as successful family verification. The broad parity unit gates also report the existing unapproved D78 record and stale extension SDK surface inventory. Generated files are regenerated locally and restored; integration remains paused by the coordinator.

## Unclosed storage boundary

A downstream probe reaches actual `Session.AppendSessionInfo` with the retained high-surrogate/X/low-surrogate name. The current Go Session-entry JSON codec replaces the halves with U+FFFD. Pi `session-manager.ts:1302` preserves `"name":"\\ud83dX\\ude00"`. The external `TestRenameSurrogatePersistenceProbe` compiles and fails on the raw entry, while `persistence-pi.mjs` passes against published Pi. The coordinator assigned encode/decode and reopen closure to the Session persistence owner. Do not round the Input cursor or normalize callback values to conceal this separate boundary failure.

The assigned original three-case test file and its native Input assertions pass. Full-product rename remains blocked until the shared Session-entry codec preserves the same units through serialization and reopening. This record does not claim complete selector persistence/error/refresh semantics or subprocess JSON lone-surrogate preservation.
