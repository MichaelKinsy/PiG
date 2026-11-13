### Fixed

- A Node extension that draws Pi's `ToolExecutionComponent` for a tool that renders itself (`renderShell: "self"`) no longer fails with `RangeError: Maximum call stack size exceeded` when the tool's result draws about 125,000 rows or more: the bundled Node runtime carries Pi's fix (Pi #12145). PiG's own interactive screen draws tool cards in Go and never had this limit. <!-- fix: 37c6264b53 -->
