### Added

- A Piglet frontend member receives a tool result's images on its tool card (`ToolCard.Images`), the ones Pi shows after the output: while `terminal.showImages` is on, each with its bytes, MIME type, SHA-256 and the width `terminal.imageWidthCells` gives it. Tern draws them as native images that open in its viewer on a click. Terminal output is unchanged.
- A Piglet frontend member also receives the images a user message carries, such as a pasted screenshot (`MarkdownText.Images`). Tern shows them as thumbnails above the message that open in its viewer on a click. The terminal still shows only the message text, as Pi does.
