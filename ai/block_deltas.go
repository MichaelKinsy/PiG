package ai

// BlockDeltas is the state a consumer of a streamed message keeps for its open blocks so that a delta costs the delta and not the block: the bytes of
// earlier deltas stay in one growing buffer, and the argument text of a tool call is parsed from where the previous delta ended. It is the machinery the
// provider stream builders use, for a consumer outside this package that applies `text += delta` and `parseStreamingJson(partialJson)` per event
// (packages/agent/src/proxy.ts processProxyEvent). The zero value is ready to use. A block ends with Release.
type BlockDeltas struct{ blocks streamedBlocks }

// AppendContent is current+delta for the text or thinking of the block at contentIndex.
func (deltas *BlockDeltas) AppendContent(contentIndex int, current, delta string) string {
	return deltas.blocks.appendContent(contentIndex, current, delta)
}

// AppendArguments is current+delta for the argument text of the tool call at contentIndex.
func (deltas *BlockDeltas) AppendArguments(contentIndex int, current, delta string) string {
	return deltas.blocks.appendArguments(contentIndex, current, delta)
}

// SetStreamingArguments is call.SetStreamingArguments(text) for the tool call at contentIndex, whose text is the previous call's text with the delta of
// AppendArguments added.
func (deltas *BlockDeltas) SetStreamingArguments(call *ToolCall, contentIndex int, text string) {
	call.setParsedArguments(deltas.blocks.streamingArguments(contentIndex, text))
}

// Release drops the buffers and parser of the block at contentIndex.
func (deltas *BlockDeltas) Release(contentIndex int) {
	deltas.blocks.release(contentIndex)
}
