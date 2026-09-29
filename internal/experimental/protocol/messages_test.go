package protocol

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func protocolFields(object Object, fields ...Property) Object {
	result := slices.Clone(object)
	for _, field := range fields {
		found := false
		for i := range result {
			if result[i].Key == field.Key {
				result[i] = field
				found = true
				break
			}
		}
		if !found {
			result = append(result, field)
		}
	}
	return result
}
func assertProtocolError(t *testing.T, err error) {
	t.Helper()
	if _, ok := errors.AsType[*ProtocolValidationError](err); !ok {
		t.Fatalf("error=%v, want ProtocolValidationError", err)
	}
}
func assertClientParse(t *testing.T, value Object) {
	t.Helper()
	message, err := ParseClientMessage(value)
	if err != nil {
		t.Fatal(err)
	}
	if got := message.clientObject(); !reflect.DeepEqual(got, value) {
		t.Fatalf("message=%#v, want %#v", got, value)
	}
}
func assertServerParse(t *testing.T, value Object) {
	t.Helper()
	message, err := ParseServerMessage(value)
	if err != nil {
		t.Fatal(err)
	}
	if got := message.serverObject(); !reflect.DeepEqual(got, value) {
		t.Fatalf("message=%#v, want %#v", got, value)
	}
}
func encodeClient(t *testing.T, message ClientMessage) []byte {
	t.Helper()
	data, err := EncodeClientMessage(message, FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func encodeServer(t *testing.T, message ServerMessage) []byte {
	t.Helper()
	data, err := EncodeServerMessage(message, FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProtocolValidation(t *testing.T) {
	t.Parallel()
	serverId := "00000000-0000-4000-8000-000000000001"
	clientHello := ClientHello{Version: ProtocolVersion}
	serverHello := ServerHello{Version: ProtocolVersion, ServerId: serverId}
	// upstream: packages/protocol/test/protocol.test.ts:30.
	t.Run("negotiates protocol version 8", func(t *testing.T) {
		t.Parallel()
		if ProtocolVersion != 8 {
			t.Fatalf("version=%d, want 8", ProtocolVersion)
		}
		for _, test := range []struct {
			version float64
			want    bool
		}{{8, true}, {7, false}, {8.5, false}} {
			if got := IsSupportedProtocolVersion(test.version); got != test.want {
				t.Fatalf("supported(%v)=%v, want %v", test.version, got, test.want)
			}
		}
	})
	// upstream: packages/protocol/test/protocol.test.ts:37.
	for _, version := range []float64{0, ProtocolVersion, ProtocolVersion + 1} {
		t.Run(fmt.Sprintf("accepts integer client hello version %v for negotiation", version), func(t *testing.T) {
			t.Parallel()
			assertClientParse(t, protocolFields(clientHello.clientObject(), Property{"version", version}))
		})
	}
	// upstream: packages/protocol/test/protocol.test.ts:42.
	for i, message := range []Object{protocolFields(clientHello.clientObject(), Property{"version", fmt.Sprint(ProtocolVersion)}), protocolFields(clientHello.clientObject(), Property{"version", ProtocolVersion + 0.5}), protocolFields(clientHello.clientObject(), Property{"extra", true})} {
		t.Run(fmt.Sprintf("rejects an invalid client hello/%d", i), func(t *testing.T) { t.Parallel(); _, err := ParseClientMessage(message); assertProtocolError(t, err) })
	}
	// upstream: packages/protocol/test/protocol.test.ts:50.
	for _, id := range []string{"", "server-1", "00000000-0000-7000-8000-000000000001", "00000000-0000-4000-7000-000000000001", "00000000-0000-4000-8000-00000000000A"} {
		t.Run(fmt.Sprintf("rejects non-canonical UUIDv4 server ID %q", id), func(t *testing.T) {
			t.Parallel()
			_, err := ParseClientMessage(RequestEnvelope{Id: "request-1", Target: ServerTarget{id}, Call: Object{{"serviceId", "pi.models"}, {"member", "list"}, {"args", []any{}}}}.clientObject())
			assertProtocolError(t, err)
		})
	}
	// upstream: packages/protocol/test/protocol.test.ts:67.
	t.Run("keeps routed request and event payloads opaque", func(t *testing.T) {
		t.Parallel()
		request := RequestEnvelope{Id: "request-1", Target: SessionTarget{serverId, "session-1", "attachment-1"}, Call: Object{{"serviceId", "application.custom"}, {"instance", Object{{"key", "instance-1"}, {"generation", 2}}}, {"member", "invoke"}, {"args", []any{Object{{"arbitrary", true}}, []any{"opaque"}}}}}
		assertClientParse(t, request.clientObject())
		assertClientParse(t, protocolFields(request.clientObject(), Property{"call", Object{{"arbitrary", "strict JSON whose service meaning belongs to Chord"}}}))
		assertServerParse(t, ServiceEventEnvelope{SubscriptionId: "subscription-1", Update: Object{{"applicationDefined", true}}}.serverObject())
	})
	// upstream: packages/protocol/test/protocol.test.ts:99.
	t.Run("rejects non-JSON opaque payloads", func(t *testing.T) {
		t.Parallel()
		call := Object{{"serviceId", "application.custom"}, {"member", "invoke"}, {"args", []any{}}}
		request := RequestEnvelope{Id: "request-1", Target: ServerTarget{serverId}, Call: call}
		cyclic := make(Object, 1)
		cyclic[0] = Property{"self", cyclic}
		for _, test := range []struct {
			name  string
			value any
		}{{"byte array", []byte{1}}, {"non-finite number", math.NaN()}, {"undefined property", Object{{"value", Undefined{}}}}, {"cycle", cyclic}} {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				_, err := ParseClientMessage(protocolFields(request.clientObject(), Property{"call", protocolFields(call, Property{"args", []any{test.value}})}))
				assertProtocolError(t, err)
				_, err = ParseServerMessage(ResponseEnvelope{Id: "request-1", Ok: true, HasResult: true, Result: test.value}.serverObject())
				assertProtocolError(t, err)
			})
		}
	})
	// upstream: packages/protocol/test/protocol.test.ts:124.
	t.Run("validates request cancellation envelopes", func(t *testing.T) {
		t.Parallel()
		cancel := CancelEnvelope{Id: "request-1", Target: ServerTarget{serverId}}.clientObject()
		assertClientParse(t, cancel)
		_, err := ParseClientMessage(protocolFields(cancel, Property{"id", ""}))
		assertProtocolError(t, err)
		_, err = ParseClientMessage(protocolFields(cancel, Property{"extra", true}))
		assertProtocolError(t, err)
	})
	// upstream: packages/protocol/test/protocol.test.ts:135.
	t.Run("validates attachment route updates", func(t *testing.T) {
		t.Parallel()
		attached := AttachmentEnvelope{Attachment: &SessionTarget{serverId, "session-1", "attachment-1"}}.serverObject()
		detached := AttachmentEnvelope{}.serverObject()
		assertServerParse(t, attached)
		assertServerParse(t, detached)
		_, err := ParseServerMessage(protocolFields(attached, Property{"attachment", Object{{"sessionId", "session-1"}}}))
		assertProtocolError(t, err)
	})
	// upstream: packages/protocol/test/protocol.test.ts:152.
	request := RequestEnvelope{Id: "request-1", Target: ServerTarget{serverId}, Call: Object{{"serviceId", "pi.models"}, {"member", "list"}, {"args", []any{}}}}.clientObject()
	for _, test := range []struct {
		name   string
		object Object
	}{{"empty request id", protocolFields(request, Property{"id", ""})}, {"extra envelope field", protocolFields(request, Property{"extra", true})}} {
		t.Run("rejects malformed request boundaries: "+test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseClientMessage(test.object)
			assertProtocolError(t, err)
		})
	}
	// upstream: packages/protocol/test/protocol.test.ts:176.
	t.Run("accepts a successful void response without a result field", func(t *testing.T) {
		t.Parallel()
		assertServerParse(t, ResponseEnvelope{Id: "request-1", Ok: true}.serverObject())
	})
	// upstream: packages/protocol/test/protocol.test.ts:184.
	for _, test := range []struct {
		name   string
		object Object
	}{{"invalid server id", protocolFields(serverHello.serverObject(), Property{"serverId", "server-1"})}, {"extra response field", protocolFields(ResponseEnvelope{Id: "request-1", Ok: true, HasResult: true, Result: []any{}}.serverObject(), Property{"extra", true})}, {"empty error code", ResponseEnvelope{Id: "request-1", Error: &ProtocolError{Code: "", Message: "bad"}}.serverObject()}} {
		t.Run("rejects malformed server boundaries: "+test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseServerMessage(test.object)
			assertProtocolError(t, err)
		})
	}
	// upstream: packages/protocol/test/protocol.test.ts:192.
	for _, code := range []string{"wrong_server", "cancelled", "service_not_found", "application_error"} {
		t.Run("accepts the opaque "+code+" error code", func(t *testing.T) {
			t.Parallel()
			assertServerParse(t, ResponseEnvelope{Id: "request-1", Error: &ProtocolError{Code: code, Message: "safe"}}.serverObject())
		})
	}
	// upstream: packages/protocol/test/protocol.test.ts:205.
	t.Run("rejects unknown messages and fields", func(t *testing.T) {
		t.Parallel()
		_, err := ParseServerMessage(protocolFields(serverHello.serverObject(), Property{"snapshot", Object{}}))
		assertProtocolError(t, err)
		_, err = ParseServerMessage(Object{{"type", "unknown"}, {"event", Object{}}})
		assertProtocolError(t, err)
	})
	// upstream: packages/protocol/test/protocol.test.ts:210.
	t.Run("does not parse JSON strings as messages", func(t *testing.T) {
		t.Parallel()
		_, err := ParseClientMessage(`{"type":"hello","version":8}`)
		assertProtocolError(t, err)
		_, err = ParseServerMessage(`{"type":"hello","version":8,"serverId":"00000000-0000-4000-8000-000000000001"}`)
		assertProtocolError(t, err)
	})
}

func TestValidatedFramedProtocol(t *testing.T) {
	t.Parallel()
	clientHello := ClientHello{Version: ProtocolVersion}
	serverHello := ServerHello{Version: ProtocolVersion, ServerId: "00000000-0000-4000-8000-000000000001"}
	// upstream: packages/protocol/test/protocol.test.ts:217.
	t.Run("encodes complete client and server frames", func(t *testing.T) {
		t.Parallel()
		clientFrames := requireFrames(t, requireFrameDecoder(t, FrameDecoderOptions{}), encodeClient(t, clientHello))
		value, err := DecodeCbor(clientFrames[0], CborOptions{})
		if err != nil {
			t.Fatal(err)
		}
		client, err := ParseClientMessage(value)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(client, clientHello) {
			t.Fatalf("client=%#v, want %#v", client, clientHello)
		}
		serverFrames := requireFrames(t, requireFrameDecoder(t, FrameDecoderOptions{}), encodeServer(t, serverHello))
		value, err = DecodeCbor(serverFrames[0], CborOptions{})
		if err != nil {
			t.Fatal(err)
		}
		server, err := ParseServerMessage(value)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(server, serverHello) {
			t.Fatalf("server=%#v, want %#v", server, serverHello)
		}
	})
	// upstream: packages/protocol/test/protocol.test.ts:224.
	t.Run("enforces outbound frame limits", func(t *testing.T) {
		t.Parallel()
		_, err := EncodeClientMessage(clientHello, FrameDecoderOptions{MaxFrameLength: new(8.0)})
		assertProtocolError(t, err)
		_, err = EncodeServerMessage(serverHello, FrameDecoderOptions{MaxFrameLength: new(8.0)})
		assertProtocolError(t, err)
	})
	// upstream: packages/protocol/test/protocol.test.ts:229.
	t.Run("incrementally decodes fragmented and coalesced client messages", func(t *testing.T) {
		t.Parallel()
		request := RequestEnvelope{Id: "request-1", Target: ServerTarget{serverHello.ServerId}, Call: Object{{"serviceId", "pi.session-directory"}, {"member", "list"}, {"args", []any{}}}}
		wire := append(encodeClient(t, clientHello), encodeClient(t, request)...)
		for split := 0; split <= len(wire); split++ {
			t.Run(fmt.Sprint(split), func(t *testing.T) {
				t.Parallel()
				decoder, err := NewClientMessageDecoder(FrameDecoderOptions{})
				if err != nil {
					t.Fatal(err)
				}
				first, err := decoder.Push(wire[:split])
				if err != nil {
					t.Fatal(err)
				}
				second, err := decoder.Push(wire[split:])
				if err != nil {
					t.Fatal(err)
				}
				if err := decoder.End(); err != nil {
					t.Fatal(err)
				}
				if got, want := slices.Concat(first, second), []ClientMessage{clientHello, request}; !reflect.DeepEqual(got, want) {
					t.Fatalf("messages=%#v, want %#v", got, want)
				}
			})
		}
	})
	// upstream: packages/protocol/test/protocol.test.ts:250.
	t.Run("incrementally decodes fragmented and coalesced server messages", func(t *testing.T) {
		t.Parallel()
		response := ResponseEnvelope{Id: "request-1", Ok: true, HasResult: true, Result: []any{}}
		first, second := encodeServer(t, serverHello), encodeServer(t, response)
		wire := append(slices.Clone(first), second...)
		split := len(first) + len(second)/2
		decoder, err := NewServerMessageDecoder(FrameDecoderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := decoder.Push(wire[:split])
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []ServerMessage{serverHello}) {
			t.Fatalf("first messages=%#v", got)
		}
		got, err = decoder.Push(wire[split:])
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []ServerMessage{response}) {
			t.Fatalf("second messages=%#v", got)
		}
		if err := decoder.End(); err != nil {
			t.Fatal(err)
		}
	})
	// upstream: packages/protocol/test/protocol.test.ts:265.
	schemaInvalid, err := EncodeCbor(Object{{"type", "hello"}, {"version", 1}, {"extra", true}}, CborOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		payload []byte
	}{{"empty CBOR payload", []byte{}}, {"malformed CBOR", []byte{0xff}}, {"schema-invalid CBOR", schemaInvalid}} {
		t.Run("rejects invalid framed input: "+test.name, func(t *testing.T) {
			t.Parallel()
			decoder, err := NewClientMessageDecoder(FrameDecoderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = decoder.Push(requireFrame(t, test.payload))
			assertProtocolError(t, err)
			_, err = decoder.Push(encodeClient(t, clientHello))
			if err == nil || !strings.Contains(err.Error(), "failed") {
				t.Fatalf("decoder error=%v, want failed", err)
			}
		})
	}
	// upstream: packages/protocol/test/protocol.test.ts:275.
	t.Run("rejects truncated and oversized framing", func(t *testing.T) {
		t.Parallel()
		truncated, err := NewServerMessageDecoder(FrameDecoderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := truncated.Push([]byte{0, 0, 0, 2, 1})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []ServerMessage{}) {
			t.Fatalf("truncated messages=%#v", got)
		}
		assertProtocolError(t, truncated.End())
		oversized, err := NewClientMessageDecoder(FrameDecoderOptions{MaxFrameLength: new(3.0)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = oversized.Push([]byte{0, 0, 0, 4})
		assertProtocolError(t, err)
	})
}
