package protocol

import (
	"errors"
	"strings"
	"testing"
)

const codecServerID = "12345678-1234-4234-8234-123456789abc"

func requireProtocolMessage(t *testing.T, err error, want string) {
	t.Helper()
	validation, ok := errors.AsType[*ProtocolValidationError](err)
	if !ok || validation.Message != want {
		t.Fatalf("error=%T %v, want ProtocolValidationError %q", err, err, want)
	}
}

func codecRequest(call any) RequestEnvelope {
	return RequestEnvelope{Id: "1", Target: ServerTarget{ServerId: codecServerID}, Call: call}
}

// Pi: packages/protocol/src/codec.ts:41-57 encodeProtocolMessage. A call nested past the default CBOR depth is a valid JSON value (isJsonValue has no depth limit,
// packages/chord/src/json.ts:78-120) and fails in encodeCbor with the wrapped message, whatever the depth. Pi 1.0.4 gives the same message at depths 64, 65, 512, 513, 600 and 5000.
func TestEncodeMessageDeepCallFailsInCborLikePi(t *testing.T) {
	t.Parallel()
	for _, depth := range []int{63, 64, 65, 512, 513, 600, 5000} {
		_, err := EncodeClientMessage(codecRequest(nestedArrays(depth)), FrameDecoderOptions{})
		if depth == 63 {
			if err != nil {
				t.Fatalf("depth %d: %v", depth, err)
			}
			continue
		}
		requireProtocolMessage(t, err, "Unable to encode client protocol message: CBOR nesting depth exceeds configured limit of 64")
	}
}

// Pi: codec.ts:14-27 isJsonValue rejects cyclic values (chord json.ts:85,108); the check runs before encodeCbor so the message is the validation error.
func TestEncodeMessageRejectsCyclicCalls(t *testing.T) {
	t.Parallel()
	cyclic := make([]any, 1)
	cyclic[0] = cyclic
	_, err := EncodeClientMessage(codecRequest(cyclic), FrameDecoderOptions{})
	requireProtocolMessage(t, err, "Invalid client protocol message")
	object := Object{{"self", nil}}
	object[0].Value = object
	_, err = EncodeClientMessage(codecRequest(object), FrameDecoderOptions{})
	requireProtocolMessage(t, err, "Invalid client protocol message")
	// A shared, non-cyclic child is valid JSON.
	shared := []any{float64(1)}
	if _, err := EncodeClientMessage(codecRequest([]any{shared, shared}), FrameDecoderOptions{}); err != nil {
		t.Fatal(err)
	}
}

// Pi: codec.ts:41-57. The frame limit is also the CBOR byte limit, so a message larger than the default 16 MiB encodes and decodes under a larger limit.
func TestMessageCodecFrameLimitIsTheCborByteLimit(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 17*1024*1024)
	_, err := EncodeClientMessage(codecRequest(big), FrameDecoderOptions{})
	requireProtocolMessage(t, err, "Unable to encode client protocol message: CBOR text string length exceeds configured limit of 16777216")
	limit := new(float64(20 * 1024 * 1024))
	options := FrameDecoderOptions{MaxFrameLength: limit}
	wire, err := EncodeClientMessage(codecRequest(big), options)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewClientMessageDecoder(options)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := decoder.Push(wire)
	if err != nil || len(messages) != 1 || messages[0].(RequestEnvelope).Call != big {
		t.Fatalf("decode under the larger limit: %d messages, %v", len(messages), err)
	}
	// The default frame limit rejects the same frame before parsing.
	decoder, err = NewClientMessageDecoder(FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = decoder.Push(wire)
	requireProtocolMessage(t, err, "Invalid client protocol frame: Frame length 17825876 exceeds configured limit of 16777216")
	// A frame within the frame limit but over a smaller CBOR byte limit cannot arise: both come from one option.
	small := FrameDecoderOptions{MaxFrameLength: new(10.0)}
	_, err = EncodeServerMessage(ServerHello{Version: 8, ServerId: codecServerID}, small)
	requireProtocolMessage(t, err, "Unable to encode server protocol message: CBOR byte length exceeds configured limit of 10")
	_, err = EncodeClientMessage(ClientHello{Version: 8}, FrameDecoderOptions{MaxFrameLength: new(-1.0)})
	requireProtocolMessage(t, err, "Unable to encode client protocol message: maxByteLength must be an integer between 0 and 4294967295")
	_, err = NewClientMessageDecoder(FrameDecoderOptions{MaxFrameLength: new(-1.0)})
	if _, ok := errors.AsType[*RangeError](err); !ok {
		t.Fatalf("invalid limit error=%T %v", err, err)
	}
}

// Pi: codec.ts:60-103 ValidatedMessageDecoder. Every failure latches; end reports its own wrapped framing message; a failed decoder keeps reporting "decoder has failed".
func TestMessageDecoderErrorsLatchLikePi(t *testing.T) {
	t.Parallel()
	hello := encodeClient(t, ClientHello{Version: 8})
	t.Run("invalid CBOR", func(t *testing.T) {
		t.Parallel()
		decoder, _ := NewClientMessageDecoder(FrameDecoderOptions{})
		_, err := decoder.Push([]byte{0, 0, 0, 1, 0x1c})
		requireProtocolMessage(t, err, "Invalid client protocol frame: Malformed CBOR additional information")
		_, err = decoder.Push(hello)
		requireProtocolMessage(t, err, "client message decoder has failed")
		requireProtocolMessage(t, decoder.End(), "client message decoder has failed")
	})
	t.Run("invalid message keeps the validation error", func(t *testing.T) {
		t.Parallel()
		decoder, _ := NewServerMessageDecoder(FrameDecoderOptions{})
		wire, err := EncodeFrame([]byte{0xf6})
		if err != nil {
			t.Fatal(err)
		}
		_, err = decoder.Push(wire)
		requireProtocolMessage(t, err, "Invalid server protocol message")
		_, err = decoder.Push(nil)
		requireProtocolMessage(t, err, "server message decoder has failed")
	})
	t.Run("messages before a bad frame are dropped", func(t *testing.T) {
		t.Parallel()
		decoder, _ := NewClientMessageDecoder(FrameDecoderOptions{})
		messages, err := decoder.Push(append(append([]byte{}, hello...), 0, 0, 0, 1, 0xff))
		if messages != nil {
			t.Fatalf("messages=%v", messages)
		}
		requireProtocolMessage(t, err, "Invalid client protocol frame: CBOR break marker is not supported")
	})
	t.Run("oversized frame header", func(t *testing.T) {
		t.Parallel()
		decoder, _ := NewClientMessageDecoder(FrameDecoderOptions{MaxFrameLength: new(5.0)})
		_, err := decoder.Push([]byte{0, 0, 0, 6})
		requireProtocolMessage(t, err, "Invalid client protocol frame: Frame length 6 exceeds configured limit of 5")
	})
	t.Run("truncated stream", func(t *testing.T) {
		t.Parallel()
		decoder, _ := NewServerMessageDecoder(FrameDecoderOptions{})
		if _, err := decoder.Push([]byte{0, 0}); err != nil {
			t.Fatal(err)
		}
		requireProtocolMessage(t, decoder.End(), "Invalid server protocol framing: Truncated frame at end of stream")
		_, err := decoder.Push(nil)
		requireProtocolMessage(t, err, "server message decoder has failed")
		requireProtocolMessage(t, decoder.End(), "server message decoder has failed")
	})
	t.Run("clean end then end again", func(t *testing.T) {
		t.Parallel()
		decoder, _ := NewClientMessageDecoder(FrameDecoderOptions{})
		if messages, err := decoder.Push(hello); err != nil || len(messages) != 1 {
			t.Fatalf("push: %v %v", messages, err)
		}
		if err := decoder.End(); err != nil {
			t.Fatal(err)
		}
		requireProtocolMessage(t, decoder.End(), "Invalid client protocol framing: Frame decoder has ended")
	})
}

// Pi: codec.ts:36-38 boundedErrorMessage truncates a message over 500 characters to 497 plus an ellipsis.
func TestBoundedCodecErrorTruncatesAtFiveHundred(t *testing.T) {
	t.Parallel()
	if got := boundedCodecError(errors.New(strings.Repeat("a", 500))); got != strings.Repeat("a", 500) {
		t.Fatalf("500 characters changed: %d", len(got))
	}
	if got := boundedCodecError(errors.New(strings.Repeat("a", 501))); got != strings.Repeat("a", 497)+"..." {
		t.Fatalf("501 characters = %d %q", len(got), got[len(got)-5:])
	}
}

// Pi: codec.ts:26-34,133-136 and protocol.ts:17-20: nil messages are not valid, isSupportedProtocolVersion is integer equality with 8.
func TestProtocolPredicatesAndNilMessages(t *testing.T) {
	t.Parallel()
	for version, want := range map[float64]bool{8: true, 7: false, 9: false, 0: false, 8.5: false, -8: false} {
		if IsSupportedProtocolVersion(version) != want {
			t.Fatalf("IsSupportedProtocolVersion(%v) != %v", version, want)
		}
	}
	if ProtocolVersion != 8 {
		t.Fatalf("ProtocolVersion=%d", ProtocolVersion)
	}
	_, err := EncodeClientMessage(nil, FrameDecoderOptions{})
	requireProtocolMessage(t, err, "Invalid client protocol message")
	_, err = EncodeServerMessage(nil, FrameDecoderOptions{})
	requireProtocolMessage(t, err, "Invalid server protocol message")
	for value, want := range map[ServerId]bool{
		codecServerID: true, strings.ToUpper(codecServerID): false, "": false, codecServerID + "\n": false, " " + codecServerID: false,
		"12345678-1234-1234-8234-123456789abc": false, "12345678-1234-4234-c234-123456789abc": false, "12345678123442348234123456789abc": false,
	} {
		// packages/protocol/src/protocol.ts:12-19 ServerId is a lowercase canonical UUIDv4 string (ServerIdSchema pattern, isServerId).
		// Pi: packages/protocol/src/protocol.ts:15 (ServerId)
		id := ServerId(value)
		if IsServerId(id) != want {
			t.Fatalf("IsServerId(%q) != %v", value, want)
		}
	}
	// protocol.ts:17 isServerId(value: unknown): Check(ServerIdSchema, value) is false for anything that is not a string.
	var typedNil *string
	for _, value := range []any{nil, 42, true, []byte(codecServerID), []string{codecServerID}, map[string]any{}, typedNil} {
		if IsServerId(value) {
			t.Fatalf("IsServerId(%#v) = true, want false for a value that is not a string", value)
		}
	}
}

// Pi: codec.ts:59-72 encodeClientMessage/encodeServerMessage parse before encoding, so a typed message Pi's schema rejects is never framed.
func TestEncodeMessageValidatesBeforeFraming(t *testing.T) {
	t.Parallel()
	for _, message := range []ClientMessage{
		ClientHello{Version: -1}, ClientHello{Version: 1.5}, RequestEnvelope{Id: "", Target: ServerTarget{ServerId: codecServerID}},
		RequestEnvelope{Id: "1", Target: ServerTarget{ServerId: "nope"}}, RequestEnvelope{Id: "1"}, CancelEnvelope{Id: "1", Target: SessionTarget{ServerId: codecServerID, SessionId: "s"}},
	} {
		_, err := EncodeClientMessage(message, FrameDecoderOptions{})
		requireProtocolMessage(t, err, "Invalid client protocol message")
	}
	failure := ProtocolError{Code: "c", Message: "m"}
	for _, message := range []ServerMessage{
		ServerHello{Version: 9, ServerId: codecServerID}, ServerHello{Version: 8, ServerId: "nope"}, ServerHelloError{Error: ProtocolError{Message: "m"}},
		ResponseEnvelope{Id: "", Ok: true}, ResponseEnvelope{Id: "1", Ok: true, Error: &failure}, ResponseEnvelope{Id: "1", Ok: false}, ResponseEnvelope{Id: "1", Ok: false, HasResult: true, Error: &failure},
		ServiceEventEnvelope{SubscriptionId: "", Update: nil}, AttachmentEnvelope{Attachment: &SessionTarget{ServerId: codecServerID, SessionId: "s"}},
	} {
		_, err := EncodeServerMessage(message, FrameDecoderOptions{})
		requireProtocolMessage(t, err, "Invalid server protocol message")
	}
}

// upstream: codec.ts ProtocolValidationError(message) carries the message as its Error text, and the codec raises it for an invalid client message.
func TestNewProtocolValidationErrorCarriesTheMessage(t *testing.T) {
	err := NewProtocolValidationError("Invalid client protocol message")
	if err.Message != "Invalid client protocol message" || err.Error() != err.Message {
		t.Fatalf("NewProtocolValidationError = %+v", err)
	}
	if _, parseErr := ParseClientMessage(map[string]any{"type": "nonsense"}); parseErr == nil || parseErr.Error() != err.Error() {
		t.Fatalf("ParseClientMessage error = %v, want %v", parseErr, err)
	}
}
