package host

import (
	"bytes"
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// frameSchema is the JSON Schema TypeBox builds for wire.ts FrameSchema: Pi b45e0ebc gives
// hash64(canonicalJson(FrameSchema)) = 678648a861625402 over 7361 characters (typebox 1.3.27).
func TestFrameSchemaIsPiFrameSchema(t *testing.T) {
	text := CanonicalJSON(frameSchema)
	if got := Hash64(text); got != "678648a861625402" || len(text) != 7361 {
		t.Fatalf("frame schema hash %s length %d, Pi 678648a861625402 7361", got, len(text))
	}
}

// wire.ts (Pi 90f17f59) declares each frame once, as its schema: error codes are the ChordErrorCode union and a hold's
// token is a contract Description (scope, object schemas, member descriptions). Expectations are Pi isFrame's.
func TestIsFrameChecksErrorCodesAndContractTokens(t *testing.T) {
	token := func(fields string) string {
		base := `"name":"c","scope":"remote","version":1,"input":{"type":"object"},"variants":{"main":{"m":{"kind":"method","args":{},"result":{}}}}`
		if fields != "" {
			base = `"name":"c","version":1,` + fields
		}
		return `{"t":"hold","id":1,"at":"h","input":{},"token":{` + base + `}}`
	}
	variants := func(variants string) string {
		return token(`"scope":"remote","input":{"type":"object"},"variants":` + variants)
	}
	for _, test := range []struct {
		frame string
		valid bool
	}{
		{`{"t":"gone","id":1,"error":{"code":"teapot","message":"m"}}`, false},
		{`{"t":"result","id":1,"error":{"code":"nope","message":"m"}}`, false},
		{`{"t":"state","id":1,"error":{"code":"x","message":"m"}}`, false},
		{`{"t":"vote","reload":1,"error":{"code":"x","message":"m"}}`, false},
		{`{"t":"result","id":1,"error":{"code":"upgrade_required","message":"m"}}`, true},
		{token(""), true},
		{token(`"scope":"global","input":{},"variants":{}`), false},
		{token(`"scope":"local","input":"string","variants":{}`), false},
		{token(`"scope":"local","input":[],"variants":{}`), false},
		{variants(`{"main":{"m":{"kind":"event"}}}`), false},
		{variants(`{"main":{"m":{"kind":"method","args":{}}}}`), true},
		{variants(`{"main":{"m":{"kind":"method"}}}`), true},
		{variants(`{"main":{"m":{"kind":"method","result":1}}}`), false},
		{variants(`{"main":{"s":{"kind":"state","schema":true}}}`), false},
		{variants(`{"main":{"o":{"kind":"opaque","id":1}}}`), false},
		{variants(`{"main":{"o":{"kind":"opaque","id":"x"},"s":{"kind":"state","schema":{}}}}`), true},
		{variants(`{"main":[]}`), false},
		{variants(`{"main":1}`), false},
	} {
		value, err := chordjson.Decode([]byte(test.frame))
		if err != nil {
			t.Fatal(err)
		}
		if got := IsFrame(value); got != test.valid {
			t.Errorf("IsFrame(%s) = %v, Pi %v", test.frame, got, test.valid)
		}
	}
}

func TestFramesRoundTrip(t *testing.T) {
	kind, reason := "viewer", "done"
	deadline := 2.5
	frames := []Frame{
		HelloFrame{Name: "h", Kind: "host", CodeVersion: 0},
		HelloFrame{Protocol: ProtocolVersion, Name: "h", Kind: "host", CodeVersion: 0, Acks: true},
		HoldFrame{ID: 1, At: "a", Token: chatV1.Description, Input: chordjson.ObjectOf("session", "s"), Principal: nil, HasPrincipal: true, Origin: "i:1", NoState: true, Again: true},
		HoldFrame{ID: 2, At: "a", Token: chatV1.Description, Input: nil},
		ReleaseFrame{ID: 1},
		ShapeFrame{ID: 1, Variant: &kind, Members: []string{"view"}, Contract: "f", State: []string{"view"}},
		ShapeFrame{ID: 1, Members: []string{}, Contract: "f", State: []string{}},
		GoneFrame{ID: 1, Error: WireError{Code: CodeGone, Message: "gone: x", Reason: "r"}},
		CallFrame{ID: 1, Hold: 2, Member: "send", Args: chordjson.ObjectOf("text", "x"), HasArgs: true, Root: &RootRef{RootID: "r1", CodeVersion: 1}},
		CallFrame{ID: 1, Hold: 2, Member: "ping"},
		ResultFrame{ID: 1},
		ResultFrame{ID: 1, Value: nil, HasValue: true},
		ResultFrame{ID: 1, Error: &WireError{Code: CodeInvalid, Message: "invalid: x"}},
		CancelFrame{ID: 1},
		SubFrame{Hold: 1},
		UnsubFrame{Hold: 1},
		ResyncFrame{Hold: 1, M: "view"},
		StateFrame{Hold: 1, M: "view", As: "snap", Value: chordjson.ObjectOf("text", "a"), HasValue: true},
		StateFrame{Hold: 1, M: "view", As: "reset", Value: nil, HasValue: true},
		StateFrame{Hold: 1, M: "view", As: "ops", Ops: []delta.WireOp{{"r", "x"}}},
		StateFrame{Hold: 1, M: "view", As: "error", Error: &WireError{Code: CodeGone, Message: "gone: x"}},
		PingFrame{N: 3},
		PongFrame{N: 3},
		ByeFrame{Reason: &reason},
		ByeFrame{},
		RootEndFrame{RootID: "r1"},
		InvalidateFrame{Pattern: chordjson.ObjectOf("id", "a")},
		PrepareFrame{Reload: 1, CodeVersion: 2, Plugins: []string{"p"}, Options: WireReloadOptions{Remove: []string{"q"}, GateDeadline: &deadline}},
		VoteFrame{Reload: 1, Report: &WireReloadReport{CodeVersion: 2, Previous: 1, Changed: []string{"a"}, Removed: []string{}, Prepared: []string{}, Gated: []string{}}},
		CommitFrame{Reload: 1},
		RetireFrame{CodeVersion: 1},
		AbortFrame{Reload: 1},
	}
	for _, frame := range frames {
		data, err := EncodeFrame(frame)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeFrame(data)
		if err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		again, _ := EncodeFrame(decoded)
		if !bytes.Equal(again, data) {
			t.Fatalf("round trip %s -> %s", data, again)
		}
	}
	data, _ := EncodeFrame(HoldFrame{ID: 1, At: "a", Token: chatV1.Description, Input: nil})
	decoded, _ := DecodeFrame(data)
	if got := decoded.(HoldFrame).Token; CanonicalJSON(got) != CanonicalJSON(chatV1.Description) {
		t.Fatal("the token changed on the wire")
	}
	if token, _ := contractFromDescription(decoded.(HoldFrame).Token); token.Fingerprint != chatV1.Fingerprint {
		t.Fatal("the decoded token has another fingerprint")
	}
}

func TestIsFrameRejectsMalformedFrames(t *testing.T) {
	for _, text := range []string{
		`{"t":"hello","name":"","kind":"host","codeVersion":0}`,
		`{"t":"hello","name":"h","kind":"peer","codeVersion":0}`,
		`{"t":"hello","protocol":0,"name":"h","kind":"host","codeVersion":0}`,
		`{"t":"release","id":0}`,
		`{"t":"call","id":1,"hold":1}`,
		`{"t":"result","id":1.5}`,
		`{"t":"gone","id":1,"error":{"code":"gone"}}`,
		`{"t":"nope"}`,
		`[]`,
	} {
		if _, err := DecodeFrame([]byte(text)); err == nil {
			t.Fatalf("accepted %s", text)
		}
	}
	// Unknown extra fields are allowed: TypeBox objects are open.
	if _, err := DecodeFrame([]byte(`{"t":"ping","n":1,"x":2}`)); err != nil {
		t.Fatal(err)
	}
}

func TestWireErrorsKeepCodeReasonAndMessage(t *testing.T) {
	original := Invalid("args", "bad", nil)
	wire := ToWireError(original)
	if wire != (WireError{Code: CodeInvalid, Message: "invalid/args: bad", Reason: "args"}) {
		t.Fatalf("wire %+v", wire)
	}
	back := FromWireError(wire)
	if back.Message != original.Message || back.Code != original.Code || back.Reason != original.Reason {
		t.Fatalf("back %+v", back)
	}
	if got := ToWireError(io.ErrUnexpectedEOF); got.Code != CodeUnavailable || got.Message != io.ErrUnexpectedEOF.Error() {
		t.Fatalf("plain error %+v", got)
	}
}
