package ai

// BlockDeltas is the state a consumer of a streamed message keeps for its open blocks so that a delta costs the delta and not the block: the bytes of
// earlier deltas stay in one growing buffer. It is the machinery the provider stream builders use, for a consumer outside this package that applies
// `text += delta` per event (packages/agent/src/proxy.ts processProxyEvent). The zero value is ready to use. A block ends with Release.
type BlockDeltas struct{ blocks streamedBlocks }

// AppendContent is current+delta for the text or thinking of the block at contentIndex.
func (deltas *BlockDeltas) AppendContent(contentIndex int, current, delta string) string {
	return deltas.blocks.appendContent(contentIndex, current, delta)
}

// Release drops the buffers of the block at contentIndex.
func (deltas *BlockDeltas) Release(contentIndex int) {
	deltas.blocks.release(contentIndex)
}
