### Upgrading

- Go API `ai`: `anthToolResultContent` is internal, but the Anthropic and Bedrock converters now send the images of an error tool result after the tool results of the turn. A caller that read the old note `[image omitted: error tool results cannot contain images]` in an error result sees `(image attached after the tool results)` there and the images in the same user message, after a text block `Images from the error result of tool call <id> (<tool>):`.

### Added

- `ai.SplitErrorToolResultImages` and `ai.SplitErrorToolResult`: split an error tool result into a text-only result and the images to send after the tool results, for APIs that reject images in error results (Pi 5ab7fe74).

### Fixed

- Anthropic and Claude on Bedrock no longer lose the images of an error tool result: Pi sends them after the tool results of the turn, under a caption naming the tool call, instead of replacing them with a note ([#10794](https://github.com/earendil-works/pi/issues/10794)). Other Bedrock models keep the images in the result.

### Removed

- Divergence D171 (an errored Anthropic tool result carries no images) is retired: PiG follows Pi's converter.
