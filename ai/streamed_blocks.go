package ai

// streamedBlocks keeps, for the open blocks of one streamed message, the buffers that make a delta append in amortized constant time. A block ends with release.
type streamedBlocks struct {
	accumulated map[accumulatorKey]*accumulatedString
}

// accumulatorField is the string of a block that grows.
type accumulatorField uint8

const (
	contentField accumulatorField = iota
	signatureField
)

// accumulatorKey names one growing string of a block.
type accumulatorKey struct {
	contentIndex int
	field        accumulatorField
}

// appendContent is current+delta for the content of the block at contentIndex, with the bytes of earlier deltas kept in one growing buffer instead of copied per delta.
func (blocks *streamedBlocks) appendContent(contentIndex int, current, delta string) string {
	return blocks.appendString(accumulatorKey{contentIndex: contentIndex}, current, delta)
}

// appendSignature is appendContent for the signature of the block.
func (blocks *streamedBlocks) appendSignature(contentIndex int, current, delta string) string {
	return blocks.appendString(accumulatorKey{contentIndex: contentIndex, field: signatureField}, current, delta)
}

func (blocks *streamedBlocks) appendString(key accumulatorKey, current, delta string) string {
	accumulated := blocks.accumulated[key]
	if accumulated == nil {
		if blocks.accumulated == nil {
			blocks.accumulated = map[accumulatorKey]*accumulatedString{}
		}
		accumulated = &accumulatedString{}
		blocks.accumulated[key] = accumulated
	}
	return accumulated.append(current, delta)
}

// release drops the growing buffers of a block that ended.
func (blocks *streamedBlocks) release(contentIndex int) {
	delete(blocks.accumulated, accumulatorKey{contentIndex: contentIndex})
	delete(blocks.accumulated, accumulatorKey{contentIndex: contentIndex, field: signatureField})
}
