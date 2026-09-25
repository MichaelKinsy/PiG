package utils

import "unicode/utf8"

// utf8StreamDecoder decodes UTF-8 like a WHATWG TextDecoder: each maximal
// invalid subpart becomes one U+FFFD, and in streaming mode an incomplete
// trailing sequence waits for the next chunk.
type utf8StreamDecoder struct {
	pending []byte
}

// decode appends chunk to pending bytes and returns the decoded text. With
// stream false, an incomplete trailing sequence becomes one U+FFFD.
func (decoder *utf8StreamDecoder) decode(chunk []byte, stream bool) string {
	input := make([]byte, 0, len(decoder.pending)+len(chunk))
	input = append(append(input, decoder.pending...), chunk...)
	decoder.pending = nil
	output := make([]byte, 0, len(input))
	for index := 0; index < len(input); {
		if input[index] < utf8.RuneSelf {
			output = append(output, input[index])
			index++
			continue
		}
		length, complete := validSequenceLength(input[index:])
		switch {
		case complete:
			output = append(output, input[index:index+length]...)
			index += length
		case index+length == len(input) && length > 0 && stream:
			decoder.pending = append([]byte(nil), input[index:]...)
			return string(output)
		default:
			output = utf8.AppendRune(output, utf8.RuneError)
			index += max(length, 1)
		}
	}
	return string(output)
}

// validSequenceLength returns the length of the valid prefix of the sequence
// starting at input[0] and whether that prefix is a complete code point. A
// zero length means input[0] cannot start a sequence.
func validSequenceLength(input []byte) (int, bool) {
	need, low, high := sequenceShape(input[0])
	if need == 0 {
		return 0, false
	}
	for offset := 1; offset <= need; offset++ {
		if offset >= len(input) {
			return offset, false
		}
		lower, upper := byte(0x80), byte(0xbf)
		if offset == 1 {
			lower, upper = low, high
		}
		if input[offset] < lower || input[offset] > upper {
			return offset, false
		}
	}
	return need + 1, true
}

// leadShape describes the UTF-8 lead bytes first..last: the continuation
// count and the allowed range of the second byte.
type leadShape struct {
	first, last byte
	need        int
	low, high   byte
}

var leadShapes = []leadShape{
	{0xc2, 0xdf, 1, 0x80, 0xbf},
	{0xe0, 0xe0, 2, 0xa0, 0xbf},
	{0xe1, 0xec, 2, 0x80, 0xbf},
	{0xed, 0xed, 2, 0x80, 0x9f},
	{0xee, 0xef, 2, 0x80, 0xbf},
	{0xf0, 0xf0, 3, 0x90, 0xbf},
	{0xf1, 0xf3, 3, 0x80, 0xbf},
	{0xf4, 0xf4, 3, 0x80, 0x8f},
}

// sequenceShape returns the continuation count and the allowed range of the
// second byte for a lead byte; need is zero for a byte that cannot lead.
func sequenceShape(lead byte) (need int, low, high byte) {
	for _, shape := range leadShapes {
		if lead >= shape.first && lead <= shape.last {
			return shape.need, shape.low, shape.high
		}
	}
	return 0, 0, 0
}
