# Editor wrapping and scroll-border oracle

This fixture uses Pi 0.87.1 as the oracle for `packages/tui/test/editor.test.ts:707–1197`. `upstream-tests.mjs` reads the 32 assigned original test bodies and erases TypeScript syntax without changing their inputs or assertions. `pi-runtime.mjs` verifies the installed Pi package version before importing its implementation.

The Go port is `tui/editor_wrapping_upstream_test.go`. It preserves the original 24-row terminal's seven-line editor viewport, both padding rows, the Thai and Lao table entries, and all five explicit oversized-segment arrays with their UTF-16 source indices. The marker's actual length comes from the string, not the inaccurate upstream `21 chars` comment.

Run the original bodies with `node test/parity/testdata/editor-wrap-helper/upstream-tests.mjs`. Run the Go cases with `go test ./tui -run '^TestUpstreamEditor(ScrollIndicators|GraphemeWrapping|WordWrapping)$' -count=1`. Use an isolated temporary HOME and unset `PIG_CODING_AGENT_DIR` and `PI_CODING_AGENT_DIR` for every command.

## Paired caller contract

`24-editor-wrap-scroll.toml` compares the complete outputs of `pi.mjs` and `pig/main.go`. Both fixtures drive the production Editor through SetText, actual input handling, and Render. They retain every rendered row, including borders, cursor ANSI, and trailing spaces, as hexadecimal bytes. They compare logical text and UTF-16 cursor coordinates at every input transition. Hex encoding is lossless; neither fixture strips ANSI nor trims whitespace.

The original direct word-wrapper inputs use editor width plus one because the unpadded editor reserves one cursor column. Atomic segment cases use real bracketed-paste input to populate the production paste registry. Widths 2 through 42 exercise narrow ellipses, the fallback label, and both centering thresholds. Width 1 is outside this fixture because D66 owns its end-cursor overflow distinction.

Pi `components/editor.ts:276–295` lays out the complete uncolored scroll border before `renderTopBorder` and `renderBottomBorder` at lines 508–516 apply the color callback. The fixture supplies the same magenta callback to both Editors. It does not substitute a theme-level prefix for the callback.

## Translation contract

- `TextChunk` start and end indices count UTF-16 units. Display width counts terminal cells. Grapheme segmentation controls visual split boundaries.
- The wrapper preserves spaces and reconstructs all source units, including oversized registered markers that remain logically atomic during editing.
- The callback colors the complete border after truncation. The scroll labels report hidden visual rows, not logical lines.
- No asynchronous operations or ownership transfers occur in these test paths. Existing Editor input and rendering own the state synchronously.

## Mutation guards

Each mutation compiles before its behavioral failure counts. Apply mutations through temporary Go overlays rather than editing a shared source file.

| Mutation in `tui/editor.go` | Required rejecting test |
|---|---|
| Shift the centered label to three left rules | `TestUpstreamEditorScrollIndicators` |
| Remove the truncation ellipsis | `TestUpstreamEditorScrollIndicators` |
| Ignore the explicit border-color callback | `TestUpstreamEditorScrollIndicators` |
| Disable `wrapOppIndex >= 0` backtracking | `TestUpstreamEditorWordWrapping` |
| Remove `currentWidth-wrapOppWidth+gWidth <= maxWidth` viability check | `TestEditorWrappingOverflowAndUTF16Offsets` |
| Remove `charIndex +` from both emitted atomic subchunk offsets | `TestUpstreamEditorWordWrapping` |
| Remove the reserved cursor column from `layoutWidth` | `TestUpstreamEditorGraphemeWrapping` |

The complete narrow-border bytes strengthen the original prefix-only assertion at test line 720. All seven mutations also fail canonical scenario 24 against real Pi. The initial atomic-offset mutation survived the caller fixture when it captured only the cursor at the end; Home and marker-arrow transitions now exercise the rendered chunk indices, and reject that mutation without changing a comparator.

The original CJK overflow case at test line 1059 still passes when the backtracking viability check is removed. Pi now records a CJK wrap opportunity immediately before `你` at source lines 203–209, so that exact body does not reach its named force-break branch. The unchanged original remains in the port. `TestEditorWrappingOverflowAndUTF16Offsets` adds the same prefix followed by the non-CJK wide grapheme `✅` and rejects the viability mutation. Direct Pi probes confirm both this fallback and the complete chunk indices for `A😀B😀C` at width 3.
