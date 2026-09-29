# Editor upstream case audit

Source: `.upstream/v0.87.1/packages/tui/test/editor.test.ts`. Each row names one original `it` site; the negative autocomplete case at2170 includes all21 table inputs. `audited` means the existing Go case was checked against every original input and assertion. It does not claim a new production fix.

All192 original sites are accounted on the converged clean source. The90 KEEP cases are adopted from the planner-split-2 helpers; the23 completion cases use their owner's clean pre-edit acceptance integration. Full TUI race and both owning scenario paths pass. This closes this original test file, not every terminal/SDK contract: subprocess lone-surrogate preservation remains a separately red-proven qualification blocker.

| Pi line | Case | Status | Go evidence |
|---|---|---|---|
| 47 | does nothing on Up arrow when history is empty | audited | `tui/editor_history_test.go` |
| 55 | shows most recent history entry on Up arrow when editor is empty | audited | `tui/editor_history_test.go` |
| 66 | cycles through history entries on repeated Up arrow | audited | `tui/editor_history_test.go` |
| 86 | jumps to start before entering history from a non-empty draft | audited | `tui/editor_history_test.go` |
| 106 | navigates forward through history with Down arrow | audited | `tui/editor_history_test.go` |
| 131 | exits history mode when typing a character | audited | `tui/editor_history_test.go` |
| 142 | exits history mode on setText | audited | `tui/editor_history_test.go` |
| 156 | does not add empty strings to history | audited | `tui/editor_history_test.go` |
| 171 | does not add consecutive duplicates to history | audited | `tui/editor_history_test.go` |
| 185 | allows non-consecutive duplicates in history | audited | `tui/editor_history_test.go` |
| 202 | uses cursor movement instead of history when editor has content | audited | `tui/editor_history_test.go` |
| 218 | limits history to 100 entries | audited | `tui/editor_history_test.go` |
| 239 | places cursor at start after browsing history upward | audited | `tui/editor_history_test.go` |
| 254 | places cursor at end after browsing history downward | audited | `tui/editor_history_test.go` |
| 273 | allows opposite-direction cursor movement within multi-line history entry | audited | `tui/editor_history_test.go` |
| 292 | returns cursor position | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorStateAccessors` |
| 307 | returns lines as a defensive copy | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorStateAccessors` |
| 320 | inserts backslash immediately (no buffering) | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 329 | converts standalone backslash to newline on Enter | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 338 | inserts backslash normally when followed by other characters | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 347 | does not trigger newline when backslash is not immediately before cursor | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 363 | only removes one backslash when multiple are present | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 378 | ignores printable CSI-u sequences with unsupported modifiers | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 386 | inserts shifted CSI-u letters as text | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 394 | inserts shifted xterm modifyOtherKeys letters as text | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 404 | inserts mixed ASCII, umlauts, and emojis as literal text | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 423 | deletes single-code-unit unicode characters (umlauts) with Backspace | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 437 | deletes multi-code-unit emojis with single Backspace | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 450 | inserts characters at the correct position after cursor movement over umlauts | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 468 | moves cursor across multi-code-unit emojis with single arrow key | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 488 | preserves umlauts across line breaks | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 503 | replaces the entire document with unicode text via setText (paste simulation) | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 513 | moves cursor to document start on Ctrl+A and inserts at the beginning | ported | `tui/editor_literal_input_upstream_test.go#TestUpstreamEditorLiteralInput` |
| 525 | deletes words correctly with Ctrl+W and Alt+Backspace | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorWordNavigation` |
| 575 | navigates words correctly with Ctrl+Left/Right | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorWordNavigation` |
| 629 | stops at fullwidth Chinese punctuation (issue #4972) | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorWordNavigation` |
| 661 | handles mixed CJK and ASCII word movement | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorWordNavigation` |
| 707 | centers scroll indicators on wide borders | ported | `tui/editor_wrapping_upstream_test.go` |
| 720 | keeps truncated scroll indicators within width and preserves their color (issue #6962) | ported | `tui/editor_wrapping_upstream_test.go` |
| 745 | wraps lines correctly when text contains wide emojis | ported | `tui/editor_wrapping_upstream_test.go` |
| 760 | wraps long text with emojis at correct positions | ported | `tui/editor_wrapping_upstream_test.go` |
| 777 | renders isolated Thai and Lao AM clusters without width drift | ported | `tui/editor_wrapping_upstream_test.go` |
| 789 | wraps CJK characters correctly (each is 2 columns wide) | ported | `tui/editor_wrapping_upstream_test.go` |
| 809 | handles mixed ASCII and wide characters in wrapping | ported | `tui/editor_wrapping_upstream_test.go` |
| 825 | renders cursor correctly on wide characters | ported | `tui/editor_wrapping_upstream_test.go` |
| 841 | does not exceed terminal width with emoji at wrap boundary | ported | `tui/editor_wrapping_upstream_test.go` |
| 856 | shows cursor at end of line before wrap, wraps on next char | ported | `tui/editor_wrapping_upstream_test.go` |
| 878 | wraps at word boundaries instead of mid-word | ported | `tui/editor_wrapping_upstream_test.go` |
| 900 | does not start lines with leading whitespace after word wrap | ported | `tui/editor_wrapping_upstream_test.go` |
| 921 | breaks long words (URLs) at character level | ported | `tui/editor_wrapping_upstream_test.go` |
| 935 | preserves multiple spaces within words on same line | ported | `tui/editor_wrapping_upstream_test.go` |
| 947 | handles empty string | ported | `tui/editor_wrapping_upstream_test.go` |
| 958 | handles single word that fits exactly | ported | `tui/editor_wrapping_upstream_test.go` |
| 971 | wraps word to next line when it ends exactly at terminal width | ported | `tui/editor_wrapping_upstream_test.go` |
| 981 | keeps whitespace at terminal width boundary on same line | ported | `tui/editor_wrapping_upstream_test.go` |
| 991 | handles unbreakable word filling width exactly followed by space | ported | `tui/editor_wrapping_upstream_test.go` |
| 999 | wraps word to next line when it fits width but not remaining space | ported | `tui/editor_wrapping_upstream_test.go` |
| 1007 | keeps word with multi-space and following word together when they fit | ported | `tui/editor_wrapping_upstream_test.go` |
| 1015 | keeps word with multi-space and following word when they fill width exactly | ported | `tui/editor_wrapping_upstream_test.go` |
| 1023 | splits when word plus multi-space plus word exceeds width | ported | `tui/editor_wrapping_upstream_test.go` |
| 1032 | breaks long whitespace at line boundary | ported | `tui/editor_wrapping_upstream_test.go` |
| 1041 | breaks long whitespace at line boundary 2 | ported | `tui/editor_wrapping_upstream_test.go` |
| 1050 | breaks whitespace spanning full lines | ported | `tui/editor_wrapping_upstream_test.go` |
| 1059 | force-breaks when wide char after word boundary wrap still overflows | ported | `tui/editor_wrapping_upstream_test.go` |
| 1077 | splits oversized atomic segment across multiple chunks | ported | `tui/editor_wrapping_upstream_test.go` |
| 1102 | splits oversized atomic segment at start of line | ported | `tui/editor_wrapping_upstream_test.go` |
| 1122 | splits oversized atomic segment at end of line | ported | `tui/editor_wrapping_upstream_test.go` |
| 1141 | splits consecutive oversized atomic segments | ported | `tui/editor_wrapping_upstream_test.go` |
| 1163 | wraps normally after oversized atomic segment | ported | `tui/editor_wrapping_upstream_test.go` |
| 1201 | Ctrl+W saves deleted text to kill ring and Ctrl+Y yanks it | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1214 | Ctrl+U saves deleted text to kill ring | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1234 | Ctrl+K saves deleted text to kill ring | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1247 | Ctrl+Y does nothing when kill ring is empty | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1255 | Alt+Y cycles through kill ring after Ctrl+Y | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1282 | Alt+Y does nothing if not preceded by yank | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1298 | Alt+Y does nothing if kill ring has ≤1 entry | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1311 | consecutive Ctrl+W accumulates into one kill ring entry | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1326 | Ctrl+U accumulates multiline deletes including newlines | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1358 | backward deletions prepend, forward deletions append during accumulation | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1374 | non-delete actions break kill accumulation | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1397 | non-yank actions break Alt+Y chain | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1416 | kill ring rotation persists after cycling | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1444 | consecutive deletions across lines coalesce into one entry | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1469 | Ctrl+K at line end deletes newline and coalesces | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1496 | handles yank in middle of text | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1511 | handles yank-pop in middle of text | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1536 | multiline yank and yank-pop in middle of text | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1564 | Alt+D deletes word forward and saves to kill ring | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1581 | Alt+D at end of line deletes newline | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1598 | does nothing when undo stack is empty | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1605 | coalesces consecutive word characters into one undo unit | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1630 | undoes spaces one at a time | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1652 | undoes newlines and signals next word to capture state | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1678 | undoes backspace | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1693 | undoes forward delete | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1710 | undoes Ctrl+W (delete word backward) | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1733 | undoes Ctrl+K (delete to line end) | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1760 | undoes Ctrl+U (delete to line start) | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1784 | undoes yank | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1801 | undoes single-line paste atomically | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1820 | does not trigger autocomplete during single-line paste | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1840 | decodes CSI-u Ctrl+letter sequences inside bracketed paste (tmux popup) | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1850 | undoes multi-line paste atomically | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1869 | undoes insertTextAtCursor atomically | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1888 | insertTextAtCursor handles multiline text | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1909 | insertTextAtCursor normalizes CRLF and CR line endings | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1926 | undoes setText to empty string | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1949 | clears undo stack on submit | ported | `tui/editor_kill_undo_upstream_test.go` |
| 1971 | exits history browsing mode on undo | audited | `tui/editor_history_test.go#TestEditorUndoExitsHistoryBrowsing` |
| 2003 | undo restores to pre-history state even after multiple history navigations | audited | `tui/editor_history_test.go#TestEditorUndoRestoresPreHistoryStateAfterSeveralNavigations` |
| 2042 | cursor movement starts new undo unit | ported | `tui/editor_kill_undo_upstream_test.go` |
| 2075 | no-op delete operations do not push undo snapshots | ported | `tui/editor_kill_undo_upstream_test.go` |
| 2096 | undoes autocomplete | ported | `tui/editor_completion_upstream_test.go` |
| 2135 | triggers and debounces symbol completion after CJK punctuation | ported | `tui/editor_completion_upstream_test.go` |
| 2170 | does not auto-trigger after CJK letters or for unprefixed paths | ported | `tui/editor_autocomplete_boundary_upstream_test.go` |
| 2212 | requests path completion after CJK punctuation only on Tab | ported | `tui/editor_completion_upstream_test.go` |
| 2233 | completes Chinese path prefixes after whitespace or CJK punctuation with Tab | ported | `tui/editor_completion_upstream_test.go` |
| 2255 | ends unquoted trigger and debounce contexts at whitespace or CJK punctuation | ported | `tui/editor_completion_upstream_test.go` |
| 2288 | re-triggers CJK path completion after accepting directories and deleting | ported | `tui/editor_completion_upstream_test.go` |
| 2338 | auto-applies single force-file suggestion without showing menu | ported | `tui/editor_completion_upstream_test.go` |
| 2379 | shows menu when force-file has multiple suggestions | ported | `tui/editor_completion_upstream_test.go` |
| 2423 | keeps suggestions open when typing in force mode (Tab-triggered) | ported | `tui/editor_completion_upstream_test.go` |
| 2475 | debounces @ autocomplete while typing | ported | `tui/editor_completion_upstream_test.go` |
| 2508 | re-queries the autocomplete picker when the cursor moves back into the command name | ported | `tui/editor_completion_upstream_test.go` |
| 2567 | debounces # autocomplete while typing | ported | `tui/editor_completion_upstream_test.go` |
| 2600 | debounces custom triggerCharacters autocomplete while typing | ported | `tui/editor_completion_upstream_test.go` |
| 2626 | resets custom triggerCharacters when provider changes | ported | `tui/editor_autocomplete_boundary_upstream_test.go` |
| 2652 | aborts active @ autocomplete when typing continues | ported | `tui/editor_completion_upstream_test.go` |
| 2689 | hides autocomplete when backspacing slash command to empty | ported | `tui/editor_completion_upstream_test.go` |
| 2729 | applies exact typed slash-argument value on Enter even when first item is highlighted | ported | `tui/editor_completion_upstream_test.go` |
| 2785 | selects first prefix match on Enter when typed arg is not exact match | ported | `tui/editor_completion_upstream_test.go` |
| 2836 | highlights unique prefix match as user types (before full exact match) | ported | `tui/editor_completion_upstream_test.go` |
| 2885 | selects first prefix match when multiple items match | ported | `tui/editor_completion_upstream_test.go` |
| 2931 | works for built-in-style command argument completion path (model-like) | ported | `tui/editor_completion_upstream_test.go` |
| 2994 | awaits async slash command argument completions | ported | `tui/editor_completion_boundary_external_test.go` |
| 3019 | ignores invalid slash command argument completion results | ported | `tui/editor_completion_boundary_external_test.go` |
| 3042 | does not show argument completions when command has no argument completer | ported | `tui/editor_completion_upstream_test.go` |
| 3070 | jumps forward to first occurrence of character on same line | ported | `tui/editor_character_jump_upstream_test.go` |
| 3083 | jumps forward to next occurrence after cursor | ported | `tui/editor_character_jump_upstream_test.go` |
| 3098 | jumps forward across multiple lines | ported | `tui/editor_character_jump_upstream_test.go` |
| 3114 | jumps backward to first occurrence before cursor on same line | ported | `tui/editor_character_jump_upstream_test.go` |
| 3127 | jumps backward across multiple lines | ported | `tui/editor_character_jump_upstream_test.go` |
| 3140 | does nothing when character is not found (forward) | ported | `tui/editor_character_jump_upstream_test.go` |
| 3153 | does nothing when character is not found (backward) | ported | `tui/editor_character_jump_upstream_test.go` |
| 3166 | is case-sensitive | ported | `tui/editor_character_jump_upstream_test.go` |
| 3186 | cancels jump mode when Ctrl+] is pressed again | ported | `tui/editor_character_jump_upstream_test.go` |
| 3201 | cancels jump mode on Escape and processes the Escape | ported | `tui/editor_character_jump_upstream_test.go` |
| 3219 | cancels backward jump mode when Ctrl+Alt+] is pressed again | ported | `tui/editor_character_jump_upstream_test.go` |
| 3234 | searches for special characters | ported | `tui/editor_character_jump_upstream_test.go` |
| 3254 | handles empty text gracefully | ported | `tui/editor_character_jump_upstream_test.go` |
| 3266 | resets lastAction when jumping | ported | `tui/editor_character_jump_upstream_test.go` |
| 3302 | preserves target column when moving up through a shorter line | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3325 | preserves target column when moving down through a shorter line | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3346 | resets sticky column on horizontal movement (left arrow) | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3371 | resets sticky column on horizontal movement (right arrow) | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3398 | resets sticky column on typing | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3422 | resets sticky column on backspace | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3446 | resets sticky column on Ctrl+A (move to line start) | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3467 | resets sticky column on Ctrl+E (move to line end) | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3491 | resets sticky column on word movement (Ctrl+Left) | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3514 | resets sticky column on word movement (Ctrl+Right) | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3540 | resets sticky column on undo | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3578 | handles multiple consecutive up/down movements | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3603 | moves correctly through wrapped visual lines without getting stuck | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3627 | handles setText resetting sticky column | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3647 | sets preferredVisualCol when pressing right at end of prompt (last line) | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3676 | handles editor resizes when preferredVisualCol is on the same line | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3701 | handles editor resizes when preferredVisualCol is on a different line | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3739 | rewrapped lines: target fits current visual column | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3765 | rewrapped lines: target shorter than current visual column | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3806 | creates a paste marker for large pastes | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorAtomicPasteSegments` |
| 3812 | treats paste marker as single unit for right arrow | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorAtomicPasteSegments` |
| 3837 | treats paste marker as single unit for left arrow | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorAtomicPasteSegments` |
| 3859 | treats paste marker as single unit for backspace | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorAtomicPasteSegments` |
| 3881 | treats paste marker as single unit for forward delete | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorAtomicPasteSegments` |
| 3897 | treats paste marker as single unit for word movement | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorAtomicPasteSegments` |
| 3921 | undo restores marker after backspace deletion | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3943 | undo after paste marker deletion restores the paste registry | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3958 | undo after deleting the first of two paste markers restores both registry entries | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3977 | renumbers the paste registry in ascending id order when markers are out of order in text | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 3998 | undo after setText restores paste markers and registry | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4013 | handles multiple paste markers in same line | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorAtomicPasteSegments` |
| 4042 | does not treat manually typed marker-like text as atomic (no valid paste ID) | ported | `tui/editor_word_segments_upstream_test.go#TestUpstreamEditorAtomicPasteSegments` |
| 4057 | does not crash when paste marker is wider than terminal width | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4080 | does not crash when text + paste marker exceeds terminal width with cursor on marker | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4115 | wordWrapLine re-checks overflow after backtracking to wrap opportunity | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4144 | expands large pasted content literally in getExpandedText | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4166 | snaps to the paste marker start when navigating down into it | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4201 | preserves sticky column when navigating through paste marker line | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4243 | does not get stuck moving down from a multi-visual-line paste marker | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4304 | skips marker continuation VLs when preferred col falls in marker tail | ported | `tui/editor_sticky_paste_upstream_test.go` |
| 4345 | submits large pasted content literally | ported | `tui/editor_sticky_paste_upstream_test.go` |
