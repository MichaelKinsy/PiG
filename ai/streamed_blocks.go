package ai

// streamedBlocks keeps, for the open blocks of one streamed message, the buffers that make a delta append in amortized constant time and the parsers that read only the bytes a delta adds. A block ends with release.
type streamedBlocks struct {
	accumulated map[accumulatorKey]*accumulatedString
	parsers     map[int]*streamingArgumentsParser
}

// accumulatorField is the string of a block that grows.
type accumulatorField uint8

const (
	contentField accumulatorField = iota
	signatureField
	argumentsField
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

// appendArguments is appendContent for the argument text of the tool call at contentIndex.
func (blocks *streamedBlocks) appendArguments(contentIndex int, current, delta string) string {
	return blocks.appendString(accumulatorKey{contentIndex: contentIndex, field: argumentsField}, current, delta)
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

// release drops the growing buffers and the argument parser of a block that ended.
func (blocks *streamedBlocks) release(contentIndex int) {
	delete(blocks.accumulated, accumulatorKey{contentIndex: contentIndex})
	delete(blocks.accumulated, accumulatorKey{contentIndex: contentIndex, field: signatureField})
	delete(blocks.accumulated, accumulatorKey{contentIndex: contentIndex, field: argumentsField})
	delete(blocks.parsers, contentIndex)
}

// streamingArguments is ParseStreamingJson of text, the arguments the tool call at contentIndex has received so far, reading only what text adds to the previous call's text.
func (blocks *streamedBlocks) streamingArguments(contentIndex int, text string) (JsonObject, schemaObjectOrder) {
	parser := blocks.parsers[contentIndex]
	if parser == nil {
		if blocks.parsers == nil {
			blocks.parsers = map[int]*streamingArgumentsParser{}
		}
		parser = &streamingArgumentsParser{}
		blocks.parsers[contentIndex] = parser
	}
	return parser.parse(text)
}
