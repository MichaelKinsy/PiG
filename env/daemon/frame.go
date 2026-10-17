package daemon

// Ports packages/env/daemon/src/frame.rs

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// Frame types (docs/protocol.md).
const (
	FrameRequest byte = 1
	FrameResult  byte = 2
	FrameError   byte = 3
	FrameEvent   byte = 4
	FrameCancel  byte = 5
	FramePing    byte = 6
)

const (
	// MaxFrame is the largest frame either side accepts.
	MaxFrame = 16 * 1024 * 1024
	// MaxPayload is the largest payload a response carries, leaving room for its JSON.
	MaxPayload = MaxFrame - 64*1024
)

// Object is a JSON object.
type Object = map[string]any

// Frame is one message: `u32 length | u8 type | u32 id | u32 jsonLength | json | payload`, integers big-endian.
type Frame struct {
	Kind byte
	ID   uint32
	// JSON is the decoded JSON of the frame. A frame whose JSON does not parse arrives with a nil JSON and gets an
	// error reply, because the framing itself is intact.
	JSON    any
	Payload []byte
}

var errFrame = errors.New("frame error")

func invalidFrame(message string) error { return fmt.Errorf("%w: %s", errFrame, message) }

// ReadFrame reads one frame; it returns nil at a clean end of input.
func ReadFrame(input io.Reader) (*Frame, error) {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(input, lengthBytes[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, nil //nolint:nilnil // a clean end of input is not an error
		}
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(lengthBytes[:]))
	if length < 9 || length > MaxFrame {
		return nil, invalidFrame("frame length out of range")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(input, body); err != nil {
		return nil, err
	}
	jsonLength := int(binary.BigEndian.Uint32(body[5:9]))
	if 9+jsonLength > length {
		return nil, invalidFrame("JSON length out of range")
	}
	frame := &Frame{Kind: body[0], ID: binary.BigEndian.Uint32(body[1:5]), Payload: body[9+jsonLength:]}
	if jsonLength == 0 {
		frame.JSON = Object{}
	} else if err := json.Unmarshal(body[9:9+jsonLength], &frame.JSON); err != nil {
		frame.JSON = nil
	}
	return frame, nil
}

// encodeJSON encodes like serde_json: no HTML escaping, no trailing newline.
func encodeJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, invalidFrame("unserializable JSON")
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// WriteFrame writes one frame.
func WriteFrame(output io.Writer, frame *Frame) error {
	encoded, err := encodeJSON(frame.JSON)
	if err != nil {
		return err
	}
	length := 9 + len(encoded) + len(frame.Payload)
	if length > MaxFrame {
		return invalidFrame("frame too large")
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:4], uint32(length))
	header[4] = frame.Kind
	binary.BigEndian.PutUint32(header[5:9], frame.ID)
	binary.BigEndian.PutUint32(header[9:13], uint32(len(encoded)))
	for _, part := range [][]byte{header, encoded, frame.Payload} {
		if _, err := output.Write(part); err != nil {
			return err
		}
	}
	return nil
}

// unsignedNumber is a JSON number that is a non-negative integer, as serde_json's as_u64.
func unsignedNumber(value any) (uint64, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 || number != math.Trunc(number) || number > 1<<63 {
		return 0, false
	}
	return uint64(number), true
}
