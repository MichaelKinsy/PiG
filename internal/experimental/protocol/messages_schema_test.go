package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

type protocolCase struct {
	name  string
	json  string
	valid bool
}

// Each case below was run through Pi's own parseClientMessage and parseServerMessage (pinned 1.0.4 sources, typebox Check plus
// isJsonValue) and the accept/reject result recorded here is what Pi returned. packages/protocol/src/protocol.ts:1-110 and codec.ts:14-27.
var clientProtocolCases = []protocolCase{
	{"hello", `{"type":"hello","version":8}`, true},
	{"hello version zero", `{"type":"hello","version":0}`, true},
	{"hello huge version", `{"type":"hello","version":9007199254740991}`, true},
	{"hello negative version", `{"type":"hello","version":-1}`, false},
	{"hello fractional version", `{"type":"hello","version":1.5}`, false},
	{"hello string version", `{"type":"hello","version":"8"}`, false},
	{"hello missing version", `{"type":"hello"}`, false},
	{"hello extra key", `{"type":"hello","version":8,"extra":1}`, false},
	{"not an object", `[]`, false},
	{"null", `null`, false},
	{"string", `"hello"`, false},
	{"empty type", `{"type":""}`, false},
	{"unknown type", `{"type":"goodbye"}`, false},
	{"missing type", `{"version":8}`, false},
	{"server hello type", `{"type":"hello_error","version":8}`, false},
	{"request server target", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":{"a":1}}`, true},
	{"request session target", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":"s","attachmentId":"a"},"call":[1,"x",null]}`, true},
	{"request null call", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":null}`, true},
	{"request missing call", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc"}}`, false},
	{"request empty id", `{"type":"request","id":"","target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":1}`, false},
	{"request numeric id", `{"type":"request","id":1,"target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":1}`, false},
	{"request missing id", `{"type":"request","target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":1}`, false},
	{"request missing target", `{"type":"request","id":"1","call":1}`, false},
	{"request extra key", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":1,"extra":true}`, false},
	{"request null target", `{"type":"request","id":"1","target":null,"call":1}`, false},
	{"request array target", `{"type":"request","id":"1","target":[],"call":1}`, false},
	{"request target without server id", `{"type":"request","id":"1","target":{},"call":1}`, false},
	{"request target uppercase server id", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789ABC"},"call":1}`, false},
	{"request target version 1 uuid", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-1234-8234-123456789abc"},"call":1}`, false},
	{"request target bad variant", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-c234-123456789abc"},"call":1}`, false},
	{"request target variant 9", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-9234-123456789abc"},"call":1}`, true},
	{"request target variant b", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-b234-123456789abc"},"call":1}`, true},
	{"request target uppercase first group", `{"type":"request","id":"1","target":{"serverId":"ABCDEF12-1234-4234-8234-123456789abc"},"call":1}`, false},
	{"request target uppercase last group", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789ABC"},"call":1}`, false},
	{"request target leading text", `{"type":"request","id":"1","target":{"serverId":"x12345678-1234-4234-8234-123456789abc"},"call":1}`, false},
	{"request target trailing text", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc0"},"call":1}`, false},
	{"request target trailing newline", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc\n"},"call":1}`, false},
	{"request target short group", `{"type":"request","id":"1","target":{"serverId":"1234567-1234-4234-8234-123456789abc"},"call":1}`, false},
	{"request target extra key", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","extra":1},"call":1}`, false},
	{"request target session only", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":"s"},"call":1}`, false},
	{"request target attachment only", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","attachmentId":"a"},"call":1}`, false},
	{"request target empty session", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":"","attachmentId":"a"},"call":1}`, false},
	{"request target empty attachment", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":"s","attachmentId":""},"call":1}`, false},
	{"request target numeric session", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":1,"attachmentId":"a"},"call":1}`, false},
	{"request session target extra key", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":"s","attachmentId":"a","x":1},"call":1}`, false},
	{"cancel server target", `{"type":"cancel","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc"}}`, true},
	{"cancel session target", `{"type":"cancel","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":"s","attachmentId":"a"}}`, true},
	{"cancel with call", `{"type":"cancel","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":1}`, false},
	{"cancel empty id", `{"type":"cancel","id":"","target":{"serverId":"12345678-1234-4234-8234-123456789abc"}}`, false},
	{"cancel missing target", `{"type":"cancel","id":"1"}`, false},
	{"opaque nested call", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":{"a":[{"b":[1,2,{"c":null}]}],"d":"😀"}}`, true},
}

var serverProtocolCases = []protocolCase{
	{"hello", `{"type":"hello","version":8,"serverId":"12345678-1234-4234-8234-123456789abc"}`, true},
	{"hello version 7", `{"type":"hello","version":7,"serverId":"12345678-1234-4234-8234-123456789abc"}`, false},
	{"hello version 9", `{"type":"hello","version":9,"serverId":"12345678-1234-4234-8234-123456789abc"}`, false},
	{"hello fractional version", `{"type":"hello","version":8.5,"serverId":"12345678-1234-4234-8234-123456789abc"}`, false},
	{"hello string version", `{"type":"hello","version":"8","serverId":"12345678-1234-4234-8234-123456789abc"}`, false},
	{"hello missing server id", `{"type":"hello","version":8}`, false},
	{"hello bad server id", `{"type":"hello","version":8,"serverId":"nope"}`, false},
	{"hello empty server id", `{"type":"hello","version":8,"serverId":""}`, false},
	{"hello extra key", `{"type":"hello","version":8,"serverId":"12345678-1234-4234-8234-123456789abc","x":1}`, false},
	{"hello_error", `{"type":"hello_error","error":{"code":"c","message":"m"}}`, true},
	{"hello_error empty message", `{"type":"hello_error","error":{"code":"c","message":""}}`, true},
	{"hello_error empty code", `{"type":"hello_error","error":{"code":"","message":"m"}}`, false},
	{"hello_error missing message", `{"type":"hello_error","error":{"code":"c"}}`, false},
	{"hello_error numeric message", `{"type":"hello_error","error":{"code":"c","message":1}}`, false},
	{"hello_error extra error key", `{"type":"hello_error","error":{"code":"c","message":"m","x":1}}`, false},
	{"hello_error missing error", `{"type":"hello_error"}`, false},
	{"hello_error null error", `{"type":"hello_error","error":null}`, false},
	{"hello_error extra key", `{"type":"hello_error","error":{"code":"c","message":"m"},"x":1}`, false},
	{"response ok with result", `{"type":"response","id":"1","ok":true,"result":{"a":1}}`, true},
	{"response ok without result", `{"type":"response","id":"1","ok":true}`, true},
	{"response ok null result", `{"type":"response","id":"1","ok":true,"result":null}`, true},
	{"response ok with error", `{"type":"response","id":"1","ok":true,"error":{"code":"c","message":"m"}}`, false},
	{"response ok extra key", `{"type":"response","id":"1","ok":true,"x":1}`, false},
	{"response error", `{"type":"response","id":"1","ok":false,"error":{"code":"c","message":"m"}}`, true},
	{"response error without error", `{"type":"response","id":"1","ok":false}`, false},
	{"response error with result", `{"type":"response","id":"1","ok":false,"error":{"code":"c","message":"m"},"result":1}`, false},
	{"response error empty code", `{"type":"response","id":"1","ok":false,"error":{"code":"","message":"m"}}`, false},
	{"response error extra key", `{"type":"response","id":"1","ok":false,"error":{"code":"c","message":"m","x":1}}`, false},
	{"response empty id", `{"type":"response","id":"","ok":true}`, false},
	{"response numeric id", `{"type":"response","id":1,"ok":true}`, false},
	{"response string ok", `{"type":"response","id":"1","ok":"true"}`, false},
	{"response missing ok", `{"type":"response","id":"1"}`, false},
	{"response null ok", `{"type":"response","id":"1","ok":null}`, false},
	{"service_update", `{"type":"service_update","subscriptionId":"s","update":{"a":[1]}}`, true},
	{"service_update null update", `{"type":"service_update","subscriptionId":"s","update":null}`, true},
	{"service_update missing update", `{"type":"service_update","subscriptionId":"s"}`, false},
	{"service_update empty subscription", `{"type":"service_update","subscriptionId":"","update":1}`, false},
	{"service_update numeric subscription", `{"type":"service_update","subscriptionId":1,"update":1}`, false},
	{"service_update extra key", `{"type":"service_update","subscriptionId":"s","update":1,"x":1}`, false},
	{"attachment session", `{"type":"attachment","attachment":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":"s","attachmentId":"a"}}`, true},
	{"attachment null", `{"type":"attachment","attachment":null}`, true},
	{"attachment missing", `{"type":"attachment"}`, false},
	{"attachment server target", `{"type":"attachment","attachment":{"serverId":"12345678-1234-4234-8234-123456789abc"}}`, false},
	{"attachment empty object", `{"type":"attachment","attachment":{}}`, false},
	{"attachment bad server id", `{"type":"attachment","attachment":{"serverId":"x","sessionId":"s","attachmentId":"a"}}`, false},
	{"attachment extra key", `{"type":"attachment","attachment":{"serverId":"12345678-1234-4234-8234-123456789abc","sessionId":"s","attachmentId":"a"},"x":1}`, false},
	{"attachment string", `{"type":"attachment","attachment":"s"}`, false},
	{"request type", `{"type":"request","id":"1","target":{"serverId":"12345678-1234-4234-8234-123456789abc"},"call":1}`, false},
	{"unknown type", `{"type":"nope"}`, false},
	{"array", `[]`, false},
	{"number", `1`, false},
}

func TestParseClientMessageMatchesPiSchema(t *testing.T) {
	t.Parallel()
	for _, test := range clientProtocolCases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := FromJSON(json.RawMessage(test.json))
			if err != nil {
				t.Fatal(err)
			}
			message, err := ParseClientMessage(value)
			if !test.valid {
				if validation, ok := errors.AsType[*ProtocolValidationError](err); !ok || validation.Message != "Invalid client protocol message" || message != nil {
					t.Fatalf("accepted or wrong error: %v %v", message, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := message.clientObject(); !reflect.DeepEqual(got, value) {
				t.Fatalf("message=%#v, want %#v", got, value)
			}
			decoder, err := NewClientMessageDecoder(FrameDecoderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := decoder.Push(encodeClient(t, message))
			if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], message) {
				t.Fatalf("framed round trip = %#v, %v", got, err)
			}
		})
	}
}

func TestParseServerMessageMatchesPiSchema(t *testing.T) {
	t.Parallel()
	for _, test := range serverProtocolCases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := FromJSON(json.RawMessage(test.json))
			if err != nil {
				t.Fatal(err)
			}
			message, err := ParseServerMessage(value)
			if !test.valid {
				if validation, ok := errors.AsType[*ProtocolValidationError](err); !ok || validation.Message != "Invalid server protocol message" || message != nil {
					t.Fatalf("accepted or wrong error: %v %v", message, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := message.serverObject(); !reflect.DeepEqual(got, value) {
				t.Fatalf("message=%#v, want %#v", got, value)
			}
			decoder, err := NewServerMessageDecoder(FrameDecoderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := decoder.Push(encodeServer(t, message))
			if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], message) {
				t.Fatalf("framed round trip = %#v, %v", got, err)
			}
		})
	}
}
