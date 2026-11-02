### Fixed

- Tool rows and streamed answers no longer highlight the same code again on every redraw. A running codemode row highlighted its whole script on every progress update and timer tick, and a complete `write` row its whole file on every update and expand toggle. Rows now keep their last highlighting until the code, the theme or the loaded grammars change, a Markdown component keeps the highlighting of the code blocks it rendered until `Invalidate`, and `Text.SetText` with unchanged text keeps its wrapped lines (Pi #10971). <!-- fix: edf18625cb3180d511ef55ce83d4c48925b94d0f -->
