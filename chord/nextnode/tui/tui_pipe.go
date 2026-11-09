package tui

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/node/tui/pipe.ts. The launcher's end of the sandbox's pipes (slice 11 §4; fix round 2: the sandbox
// a child process): messages as plain length-prefixed binary frames, not Node's IPC. A frame is a 4-byte big-endian length,
// then the payload: a 4-byte big-endian header length, the header (the message as JSON, its body field left out), and the
// body (the raw bytes of `bytes`, or the UTF-8 text of `data`). The sandbox's bootstrap has the same codec (Pi's, vendored).
// What the sandbox sends is untrusted: each frame is bounded, and each message checked here before the launcher acts on it.

// MaxPipeFrame is the largest frame either side takes: 17 MiB (an attached file of 16 MiB and its header).
const MaxPipeFrame = 17 * 1024 * 1024

// maxHeader is the largest header of a frame from the sandbox: 1 MiB (a request's text or URL; frames carry their text as body).
const maxHeader = 1024 * 1024

// ErrMalformedFrame is a frame the sandbox sent that is no frame.
var ErrMalformedFrame = errors.New("terminal: the sandbox sent a malformed frame")

// ErrMalformedMessage is a message the sandbox sent that is no known message.
var ErrMalformedMessage = errors.New("terminal: the sandbox sent a malformed message")

// LauncherFrame is message as one frame for the sandbox.
func LauncherFrame(message LauncherMessage) ([]byte, error) {
	switch message.Kind {
	case "input":
		return frame(delta.JsonObjectOf("kind", "input"), []byte(message.Data))
	case "socket_message":
		return frame(delta.JsonObjectOf("kind", string(message.Kind), "socket", float64(message.Socket)), message.Bytes)
	case "attached":
		return frame(delta.JsonObjectOf("kind", string(message.Kind), "id", float64(message.ID), "name", message.Name), message.Bytes)
	case "resize":
		return frame(delta.JsonObjectOf("kind", string(message.Kind), "columns", float64(message.Columns), "rows", float64(message.Rows)), nil)
	case "socket_opened", "socket_closed":
		return frame(delta.JsonObjectOf("kind", string(message.Kind), "socket", float64(message.Socket)), nil)
	case "reply":
		return frame(delta.JsonObjectOf("kind", string(message.Kind), "id", float64(message.ID), "value", message.Value), nil)
	case "refused":
		return frame(delta.JsonObjectOf("kind", string(message.Kind), "id", float64(message.ID), "message", message.Message), nil)
	}
	return nil, errors.New("terminal: no such launcher message " + string(message.Kind))
}

// StartFrame is the first frame: the bridge's start and the base bundle's text as body.
func StartFrame(start BridgeStart, source string) ([]byte, error) {
	locales := make([]any, len(start.Locales))
	for i, locale := range start.Locales {
		locales[i] = locale
	}
	header := delta.JsonObjectOf("kind", "start", "start", delta.JsonObjectOf(
		"base", start.Base, "combined", start.Combined, "columns", float64(start.Columns), "rows", float64(start.Rows),
		"colors", start.Colors, "mode", start.Mode, "locales", locales,
	))
	return frame(header, []byte(source))
}

// frame is a frame of header and body.
func frame(header *delta.JsonObject, body []byte) ([]byte, error) {
	text, err := jsstring.MarshalJSON(header)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 8, 8+len(text)+len(body))
	binary.BigEndian.PutUint32(out[0:], uint32(4+len(text)+len(body)))
	binary.BigEndian.PutUint32(out[4:], uint32(len(text)))
	out = append(out, text...)
	return append(out, body...), nil
}

// FrameReader cuts the sandbox's output into messages as chunks arrive; it fails (the sandbox is ended) on a frame over
// MaxPipeFrame, a header over 1 MiB, or a message of no known shape. Each byte is copied in once, however small the chunks
// the sandbox writes.
type FrameReader struct {
	buffer []byte
	start  int
	end    int
}

// NewFrameReader is a reader with nothing read.
func NewFrameReader() *FrameReader { return &FrameReader{buffer: make([]byte, 64*1024)} }

// Push is the messages complete with chunk. A malformed frame in it fails the whole chunk with no message, as Pi's `push` throws
// before it returns any: the valid frames read before the bad one are dropped.
func (r *FrameReader) Push(chunk []byte) ([]SandboxMessage, error) {
	r.append(chunk)
	var messages []SandboxMessage
	for r.end-r.start >= 4 {
		length := int(binary.BigEndian.Uint32(r.buffer[r.start:]))
		if length < 4 || length > MaxPipeFrame {
			return nil, ErrMalformedFrame
		}
		if r.end-r.start < 4+length {
			break
		}
		// The message copies what it keeps: the buffer is reused.
		message, err := sandboxMessage(r.buffer[r.start+4 : r.start+4+length])
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
		r.start += 4 + length
	}
	if r.start == r.end {
		r.start, r.end = 0, 0
	}
	return messages, nil
}

// append copies chunk in after the unread bytes, moving them to the front or growing the buffer when it is full.
func (r *FrameReader) append(chunk []byte) {
	unread := r.end - r.start
	if r.end+len(chunk) > len(r.buffer) {
		needed := unread + len(chunk)
		target := r.buffer
		if needed > len(r.buffer) {
			target = make([]byte, max(needed, min(len(r.buffer)*2, MaxPipeFrame+4)))
		}
		copy(target, r.buffer[r.start:r.end])
		r.buffer = target
		r.start = 0
		r.end = unread
	}
	copy(r.buffer[r.end:], chunk)
	r.end += len(chunk)
}

// safeInteger is Number.isSafeInteger for a field of the header: false for anything but a JSON number (a missing field, null, a
// string, a boolean, an array, an object), and for a number that is not an integer or is beyond 2^53 - 1 (or beyond a float64).
func safeInteger(field json.RawMessage) (int, bool) {
	if len(field) == 0 || (field[0] != '-' && (field[0] < '0' || field[0] > '9')) {
		return 0, false
	}
	var number float64
	if err := json.Unmarshal(field, &number); err != nil || number != math.Trunc(number) || math.Abs(number) > 1<<53-1 {
		return 0, false
	}
	//portlint:allow numbers number is a JSON number already checked to be a finite integer within the frame limits above (the checks before this line), so the conversion cannot overflow
	return int(number), true
}

// stringField is the field's value when it is a JSON string; false for anything else (a missing field, null, a number, ...).
func stringField(field json.RawMessage) (string, bool) {
	if len(field) == 0 || field[0] != '"' {
		return "", false
	}
	var text string
	if err := json.Unmarshal(field, &text); err != nil {
		return "", false
	}
	return text, true
}

// sandboxMessage is the message in payload (a frame from the sandbox, without its length); it fails for anything else. Trust
// boundary: the sandbox's pipe. The header's fields stay raw until a message kind asks for one, so nothing of it is decoded into an
// interface value.
func sandboxMessage(payload []byte) (SandboxMessage, error) {
	headerLength := int(binary.BigEndian.Uint32(payload))
	if headerLength > maxHeader || 4+headerLength > len(payload) {
		return SandboxMessage{}, ErrMalformedMessage
	}
	var header map[string]json.RawMessage
	if err := json.Unmarshal(payload[4:4+headerLength], &header); err != nil || header == nil {
		return SandboxMessage{}, ErrMalformedMessage
	}
	body := payload[4+headerLength:]
	socket, socketOK := safeInteger(header["socket"])
	kind, _ := stringField(header["kind"])
	switch kind {
	case "write":
		return SandboxMessage{Kind: SandboxMessageKind(kind), Data: string(body)}, nil
	case "socket_send":
		if !socketOK {
			return SandboxMessage{}, ErrMalformedMessage
		}
		// A copy: the payload's memory is the reader's.
		return SandboxMessage{Kind: SandboxMessageKind(kind), Socket: socket, Bytes: append([]byte(nil), body...)}, nil
	case "socket_open", "socket_close":
		if !socketOK {
			return SandboxMessage{}, ErrMalformedMessage
		}
		return SandboxMessage{Kind: SandboxMessageKind(kind), Socket: socket}, nil
	case "request":
		id, ok := safeInteger(header["id"])
		request, valid := bridgeRequest(header["request"])
		if !ok || !valid {
			return SandboxMessage{}, ErrMalformedMessage
		}
		return SandboxMessage{Kind: SandboxMessageKind(kind), ID: id, Request: request}, nil
	case "outdated":
		return SandboxMessage{Kind: SandboxMessageKind(kind)}, nil
	case "failed":
		return SandboxMessage{Kind: SandboxMessageKind(kind), Message: jsString(header["message"])}, nil
	case "exit":
		code, ok := safeInteger(header["code"])
		if !ok {
			code = 1
		}
		return SandboxMessage{Kind: SandboxMessageKind(kind), Code: code}, nil
	}
	return SandboxMessage{}, ErrMalformedMessage
}

// jsString is String(value) for a field of the header: "undefined" for a missing field or null, the text of a string, else the
// value's JSON.
func jsString(field json.RawMessage) string {
	if len(field) == 0 || string(field) == "null" {
		return "undefined"
	}
	if text, ok := stringField(field); ok {
		return text
	}
	value, err := delta.DecodeJson(field)
	if err != nil {
		return "undefined"
	}
	text, _ := jsstring.MarshalJSON(value)
	return string(text)
}

// bridgeRequest is the request the field names; false for anything else.
func bridgeRequest(field json.RawMessage) (BridgeRequest, bool) {
	var request map[string]json.RawMessage
	if len(field) == 0 || field[0] != '{' || json.Unmarshal(field, &request) != nil {
		return BridgeRequest{}, false
	}
	kind, _ := stringField(request["kind"])
	switch kind {
	case "base":
		return BridgeRequest{Kind: BridgeRequestKind(kind)}, true
	case "fetch", "attach":
		if path, ok := stringField(request["path"]); ok {
			return BridgeRequest{Kind: BridgeRequestKind(kind), Path: path}, true
		}
	case "copy":
		if text, ok := stringField(request["text"]); ok {
			return BridgeRequest{Kind: BridgeRequestKind(kind), Text: text}, true
		}
	case "open":
		if url, ok := stringField(request["url"]); ok {
			return BridgeRequest{Kind: BridgeRequestKind(kind), URL: url}, true
		}
	}
	return BridgeRequest{}, false
}
