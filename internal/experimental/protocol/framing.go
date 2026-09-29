// Package protocol implements the framed experimental Pi wire contract.
package protocol

// Ports packages/protocol/src/framing.ts.

import (
	"encoding/binary"
	"fmt"
	"math"
)

// DefaultMaxFrameLength bounds one framed CBOR payload.
const DefaultMaxFrameLength = 16 * 1024 * 1024

const payloadBlockSize = 64 * 1024

// FrameDecoderOptions preserves omitted limits separately from zero. Limits use JavaScript number semantics and are checked before conversion.
type FrameDecoderOptions struct {
	MaxFrameLength *float64
}

// FrameError reports a malformed frame or an invalid decoder transition.
type FrameError struct{ Message string }

func (err *FrameError) Error() string { return err.Message }

// RangeError reports a numeric framing option outside Pi's accepted range.
type RangeError struct{ Message string }

func (err *RangeError) Error() string { return err.Message }

// EncodeFrame copies payload after its four-byte unsigned big-endian length.
func EncodeFrame(payload []byte) ([]byte, error) {
	if uint64(len(payload)) > math.MaxUint32 || len(payload) > int(^uint(0)>>1)-4 {
		return nil, &RangeError{Message: "Frame payload exceeds the unsigned 32-bit length limit"}
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

// FrameDecoder assembles arbitrary chunks without allocating a whole declared payload before its bytes arrive. Push copies every input; completed frames do not alias input chunks or later frames.
type FrameDecoder struct {
	header                    [4]byte
	headerLength              int
	maxFrameLength            uint32
	payloadBlocks             [][]byte
	currentPayloadBlock       []byte
	currentPayloadBlockLength int
	expectedPayloadLength     uint32
	payloadLength             uint32
	state                     string
}

// NewFrameDecoder creates an open decoder. An omitted limit selects DefaultMaxFrameLength.
func NewFrameDecoder(options FrameDecoderOptions) (*FrameDecoder, error) {
	limit := float64(DefaultMaxFrameLength)
	if options.MaxFrameLength != nil {
		limit = *options.MaxFrameLength
	}
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 || limit > math.MaxUint32 || math.Trunc(limit) != limit {
		return nil, &RangeError{Message: "maxFrameLength must be an integer between 0 and 4294967295"}
	}
	return &FrameDecoder{maxFrameLength: uint32(limit), state: "open"}, nil
}

// Push emits complete frames in order. A framing error permanently fails the decoder and discards its retained partial frame.
func (decoder *FrameDecoder) Push(chunk []byte) ([][]byte, error) {
	if decoder.state == "ended" {
		return nil, &FrameError{Message: "Frame decoder has ended"}
	}
	if decoder.state == "failed" {
		return nil, &FrameError{Message: "Frame decoder has failed"}
	}
	frames := make([][]byte, 0)
	for len(chunk) > 0 {
		if decoder.expectedPayloadLength == 0 {
			n := min(len(decoder.header)-decoder.headerLength, len(chunk))
			copy(decoder.header[decoder.headerLength:], chunk[:n])
			decoder.headerLength += n
			chunk = chunk[n:]
			if decoder.headerLength < len(decoder.header) {
				continue
			}
			length := binary.BigEndian.Uint32(decoder.header[:])
			decoder.headerLength = 0
			if length > decoder.maxFrameLength {
				return nil, decoder.fail(fmt.Sprintf("Frame length %d exceeds configured limit of %d", length, decoder.maxFrameLength))
			}
			if length == 0 {
				frames = append(frames, []byte{})
				continue
			}
			decoder.expectedPayloadLength = length
			decoder.clearPayload()
		}
		for len(chunk) > 0 && decoder.payloadLength < decoder.expectedPayloadLength {
			if decoder.currentPayloadBlock == nil || decoder.currentPayloadBlockLength == len(decoder.currentPayloadBlock) {
				decoder.currentPayloadBlock = make([]byte, min(payloadBlockSize, decoder.expectedPayloadLength-decoder.payloadLength))
				decoder.payloadBlocks = append(decoder.payloadBlocks, decoder.currentPayloadBlock)
				decoder.currentPayloadBlockLength = 0
			}
			n := min(len(decoder.currentPayloadBlock)-decoder.currentPayloadBlockLength, len(chunk))
			copy(decoder.currentPayloadBlock[decoder.currentPayloadBlockLength:], chunk[:n])
			decoder.currentPayloadBlockLength += n
			decoder.payloadLength += uint32(n)
			chunk = chunk[n:]
		}
		if decoder.payloadLength == decoder.expectedPayloadLength {
			if len(decoder.payloadBlocks) == 1 {
				frames = append(frames, decoder.payloadBlocks[0])
			} else {
				payload := make([]byte, decoder.expectedPayloadLength)
				offset := 0
				for _, block := range decoder.payloadBlocks {
					offset += copy(payload[offset:], block)
				}
				frames = append(frames, payload)
			}
			decoder.clearPayload()
			decoder.expectedPayloadLength = 0
		}
	}
	return frames, nil
}

// End closes a clean stream or fails a truncated one. End is not idempotent.
func (decoder *FrameDecoder) End() error {
	if decoder.state == "ended" {
		return &FrameError{Message: "Frame decoder has ended"}
	}
	if decoder.state == "failed" {
		return &FrameError{Message: "Frame decoder has failed"}
	}
	if decoder.headerLength != 0 || decoder.expectedPayloadLength != 0 {
		return decoder.fail("Truncated frame at end of stream")
	}
	decoder.state = "ended"
	return nil
}

func (decoder *FrameDecoder) clearPayload() {
	decoder.payloadBlocks = nil
	decoder.currentPayloadBlock = nil
	decoder.currentPayloadBlockLength = 0
	decoder.payloadLength = 0
}

func (decoder *FrameDecoder) fail(message string) error {
	decoder.state = "failed"
	decoder.headerLength = 0
	decoder.expectedPayloadLength = 0
	decoder.clearPayload()
	return &FrameError{Message: message}
}
