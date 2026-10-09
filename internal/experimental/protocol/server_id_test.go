package protocol

import (
	"reflect"
	"testing"
)

// packages/protocol/src/protocol.ts:12-15 (ServerIdSchema, type ServerId): a ServerId is a string matching the
// lowercase canonical UUIDv4 pattern; IsServerId is its guard.
func TestServerIdIsTheCanonicalUUIDv4String(t *testing.T) {
	var id ServerId = codecServerID
	if !IsServerId(id) {
		t.Fatalf("IsServerId(%q) = false", id)
	}
	for _, bad := range []ServerId{"", "12345678-1234-1234-8234-123456789abc", "12345678-1234-4234-c234-123456789abc", "12345678-1234-4234-8234-123456789ABC"} {
		if IsServerId(bad) {
			t.Errorf("IsServerId(%q) = true", bad)
		}
	}
}

// upstream: packages/protocol/src/protocol.ts:66-70 (ServerHelloSchema serverId: ServerIdSchema) and codec.ts:39-63 (encodeServerMessage parses before it encodes): the hello carries a lowercase canonical UUIDv4 and the encoder rejects any other.
func TestServerIdIsCarriedByTheServerHelloAndValidated(t *testing.T) {
	var id ServerId = codecServerID
	frame, err := EncodeServerMessage(ServerHello{Version: ProtocolVersion, ServerId: id}, FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewServerMessageDecoder(FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := decoder.Push(frame)
	if err != nil || len(messages) != 1 {
		t.Fatalf("decoded %v, err %v", messages, err)
	}
	if want := (ServerHello{Version: ProtocolVersion, ServerId: id}); !reflect.DeepEqual(messages[0], want) {
		t.Fatalf("hello = %#v, want %#v", messages[0], want)
	}
	var upper ServerId = "12345678-1234-4234-8234-123456789ABC"
	if _, err := EncodeServerMessage(ServerHello{Version: ProtocolVersion, ServerId: upper}, FrameDecoderOptions{}); err == nil {
		t.Fatal("a non-lowercase server id was encoded")
	}
}
