### Fixed

- Hidden thinking with an empty label (`ctx.ui.setHiddenThinkingLabel("")`) renders no rows: thinking before a tool call no longer draws blank rows, and thinking before text draws one spacer. <!-- fix: 620abb0ff5 -->
