package subprocess

import (
	"bytes"
	"encoding/json/jsontext"
	"reflect"
	"strings"
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func envelopeFrames() []string {
	return []string{
		`{"type":"notify","notify":{"method":"state_update","args":{"isIdle":true,"thinkingLevel":"high"}}}`,
		`{"type":"notify","notify":{"method":"x","args":null}}`,
		`{"type":"notify","notify":{"method":"x","args":[]}}`,
		`{"type":"response","id":"7","response":{"result":{"content":[{"type":"text","text":"héllo <b> \ud83d\ude00"}]}}}`,
		`{"type":"response","id":"7","response":{"result":null}}`,
		`{"type":"response","id":"7","response":{"error":{"code":"E","message":"bad","stack":"at x"}}}`,
		`{"type":"call","id":"c1","call":{"method":"ui.notify","args":{"message":"hi"},"parent_request_id":"r","timer":true,"tick":false}}`,
		`{"type":"call_result","id":"c1","call_result":{"result":1.5e3}}`,
		`{"type":"request","id":"r","request":{"method":"tool_call","tool":"read","handler_id":3,"args":{"path":"a"},"tool_call_index":0,"signal_timeout_ms":2.5}}`,
		`{"type":"register","register":{"name":"ext","version":"1","tools":[{"name":"t","description":"d","parameters":{"type":"object"}}],"markdown_transformer":true}}`,
		`{"type":"request_state","request_state":{"request_id":"r","state":"blocked","reason":"host_call"}}`,
		`{"type":"ping","ping":{}}`, `{"type":"shutdown","shutdown":{}}`, `{"type":"cancel","cancel":{"request_id":"r"}}`,
		`{"type":"widget_push","widget_push":{"key":"k","lines":["a","\u001b[1mb\u001b[0m"],"width":80}}`,
		`{"Type":"notify","NOTIFY":{"Method":"m"}}`,
		`{"type":"notify","type":"ping","notify":{"method":"a"},"notify":{"args":{"x":1}}}`,
		`{"type":"response","response":{"result":"\ud800"}}`, `{"type":"response","response":{"result":"\ud83d"}}`,
		`{"type":"response","response":{"result":"\udc00\ud83d\ude00"}}`, `{"type":"response","response":{"result":"\\ud800"}}`,
		"{\"type\":\"response\",\"response\":{\"result\":\"a\xffb\xed\xa0\x80\"}}",
		`{"type":"response","id":5}`, `{"type":5}`, `{"type":"x","ready":"no"}`, `{"type":"call","call":{"method":"m","timer":"yes"}}`,
		`{"type":"request","request":{"method":"m","handler_id":1.5}}`, `{"type":"request","request":{"handler_id":99999999999999999999}}`,
		`{}`, `null`, `[]`, `5`, ``, `{"type":"x"`, `{"type":"x"}}`, `{"type":"x",}`, `{"id":"` + "\x01" + `"}`,
		// An escaped backslash before "ud800" is text, and a real lone surrogate escape after it is not: each member the decoder fills as a string
		// keeps the unit under the sdk decoder.
		`{"type":"\\ud800\udc00"}`, `{"type":"notify","notify":{"method":"\\ud83d\udc00"}}`, `{"type":"x","id":"\\\\ud800\udc00"}`,
		`{"type":"x","id":"a\\ud800\udfff"}`, `{"type":"x","id":"\\ud800"}`, `{"type":"ready","ready":{"cwd":"\\ud800\udc00","width":1}}`,
		`{"type":"ready","ready":{"cwd":"/","width":1,"state":{"model":"\\udbff\udfff\udc00"}}}`,
		`{"type":"ready","ready":{"cwd":"/","width":1,"state":{"model":["\\ud800\udc00"]}}}`,
		`{"type":"widget_push","widget_push":{"key":"k","lines":["\\ud800\udc00","b"],"width":80}}`,
		// The three-byte form of a surrogate, and other invalid UTF-8, in a string the decoder fills.
		"{\"type\":\"notify\",\"notify\":{\"method\":\"\xed\xa0\x80\"}}", "{\"type\":\"x\",\"id\":\"a\xed\xbf\xbfb\"}",
		"{\"type\":\"a\xffb\"}", "{\"type\":\"x\",\"id\":\"\xc0\xaf\xf8\x88\x80\x80\x80\"}",
		"{\"type\":\"ready\",\"ready\":{\"cwd\":\"\xed\xa0\x80\",\"width\":1}}",
		"{\"type\":\"ready\",\"ready\":{\"cwd\":\"/\",\"width\":1,\"state\":{\"model\":\"\xed\xb0\x80\"}}}",
		// Integer forms, floats, -0 and big numbers, and duplicate members mixing null and values.
		`{"type":"request","request":{"method":"m","handler_id":1e2}}`, `{"type":"request","request":{"method":"m","handler_id":1.0}}`,
		`{"type":"request","request":{"method":"m","handler_id":-0}}`, `{"type":"request","request":{"method":"m","handler_id":-0.0}}`,
		`{"type":"request","request":{"method":"m","handler_id":9223372036854775807}}`, `{"type":"request","request":{"method":"m","handler_id":9223372036854775808}}`,
		`{"type":"request","request":{"method":"m","handler_id":-9223372036854775808}}`, `{"type":"request","request":{"method":"m","handler_id":"1"}}`,
		`{"type":"ready","ready":{"cwd":"/","width":1,"state":{"model":-0}}}`, `{"type":"ready","ready":{"cwd":"/","width":1,"state":{"model":12345678901234567890123}}}`,
		`{"type":"ready","ready":{"cwd":"/","width":1,"state":{"model":1e400}}}`, `{"type":"ready","ready":{"cwd":"/","width":1,"state":{"model":{"a":1,"a":{"b":2}}}}}`,
		`{"type":"ready","ready":{"cwd":"/","width":1,"state":{"model":"\u2028\u2029<&>"}}}`,
		`{"type":"notify","type":null}`, `{"type":"notify","notify":{"method":"a"},"notify":null}`, `{"type":"notify","notify":null,"notify":{"method":"a"}}`,
		`{"type":"notify","notify":{"method":"a","args":[1]},"notify":{"args":null}}`,
		" \t\r\n{\"type\":\"x\"}\r\n ", "\ufeff{\"type\":\"x\"}", `{"type":"x"} {"type":"y"}`, `{"type":"x"}` + "\x00",
		`{"\u0074ype":"x"}`, `{"typ\u0065":"x","\u0069d":"7"}`,
	}
}

func nested(open, close string, n int) string {
	return strings.Repeat(open, n) + strings.Repeat(close, n)
}

// A frame nested around the depth limit of the decoders is accepted or rejected as the sdk decoder does.
func TestDecodeEnvelopeNestingMatchesTheSDKDecoder(t *testing.T) {
	for _, n := range []int{9990, 9996, 9997, 9998, 9999, 10000, 10001, 10060, 10070} {
		sameDecode(t, []byte(`{"type":"notify","notify":{"method":"m","args":`+nested("[", "]", n)+`}}`))
		sameDecode(t, []byte(`{"type":"ready","ready":{"cwd":"/","width":1,"state":{"model":`+nested("[", "]", n)+`}}}`))
	}
}

func sameDecode(t testing.TB, frame []byte) {
	t.Helper()
	var got, want Envelope
	gotErr := decodeEnvelope(frame, &got)
	wantErr := json.Unmarshal(frame, &want)
	if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() {
		t.Fatalf("%q: error %v, sdk decoder %v", frame, gotErr, wantErr)
	}
	if wantErr == nil && !reflect.DeepEqual(got, want) {
		// A frame that spells a member name in another case is the one difference: v2 matches names exactly.
		t.Fatalf("%q:\n got: %+v\nwant: %+v", frame, got, want)
	}
}

// Every frame decodes to the Envelope the sdk decoder gives and fails with its error, except where a member name differs from the protocol's spelling.
func TestDecodeEnvelopeMatchesTheSDKDecoder(t *testing.T) {
	for _, frame := range envelopeFrames() {
		if frame == `{"Type":"notify","NOTIFY":{"Method":"m"}}` {
			continue
		}
		sameDecode(t, []byte(frame))
	}
}

// A member name is matched exactly, as JSON.parse reads it (a JavaScript property read is case-sensitive). The sdk decoder, like encoding/json v1, also matched it in another case; a protocol frame always
// spells its names as the SDKs write them.
func TestDecodeEnvelopeMatchesNamesExactly(t *testing.T) {
	var env Envelope
	if err := decodeEnvelope([]byte(`{"Type":"notify","NOTIFY":{"Method":"m"}}`), &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "" || env.Notify != nil {
		t.Fatalf("decoded %+v", env)
	}
}

func FuzzDecodeEnvelope(f *testing.F) {
	for _, frame := range envelopeFrames() {
		f.Add([]byte(frame))
	}
	f.Fuzz(func(t *testing.T, frame []byte) {
		var got, want Envelope
		gotErr := decodeEnvelope(frame, &got)
		wantErr := json.Unmarshal(frame, &want)
		if spellsMemberInAnotherCase(frame) {
			t.Skip("a member name in another case is matched exactly by v2")
		}
		if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() {
			t.Fatalf("%q: error %v, sdk decoder %v", frame, gotErr, wantErr)
		}
		if wantErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("%q:\n got: %+v\nwant: %+v", frame, got, want)
		}
	})
}

// protocolNames are the member names any type reachable from Envelope declares.
func protocolNames() map[string]bool {
	names := map[string]bool{}
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		for field := range t.Fields() {
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name != "" && name != "-" {
				names[name] = true
			}
			walk(field.Type)
		}
	}
	walk(reflect.TypeFor[Envelope]())
	return names
}

var envelopeNames = protocolNames()

// spellsMemberInAnotherCase reports whether the frame has a member whose name is one the protocol declares in another case, the one spelling
// the sdk decoder matched and v2 does not.
func spellsMemberInAnotherCase(frame []byte) bool {
	decoder := jsontext.NewDecoder(bytes.NewReader(frame), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	for {
		token, err := decoder.ReadToken()
		if err != nil {
			return false
		}
		if token.Kind() == '"' && decoder.StackDepth() > 0 {
			if kind, length := decoder.StackIndex(decoder.StackDepth()); kind == '{' && length%2 == 1 {
				name := token.String()
				if !envelopeNames[name] {
					for declared := range envelopeNames {
						if strings.EqualFold(declared, name) {
							return true
						}
					}
				}
			}
		}
	}
}

// BenchmarkEnvelopeDecode reads the frames a host reads most: a tool update, a response with a tool result, and a call.
func BenchmarkEnvelopeDecode(b *testing.B) {
	frames := map[string][]byte{
		"notify":   []byte(`{"type":"notify","notify":{"method":"tool_update","args":{"toolCallId":"call-1","partial":{"content":[{"type":"text","text":"` + strings.Repeat("streamed output ", 24) + `"}],"details":{"lines":42}}}}}`),
		"response": []byte(`{"type":"response","id":"req-17","response":{"result":{"content":[{"type":"text","text":"` + strings.Repeat("file contents ", 64) + `"}],"details":null,"isError":false}}}`),
		"call":     []byte(`{"type":"call","id":"c-9","call":{"method":"ui.notify","args":{"message":"done","level":"info"},"parent_request_id":"req-17","timer":false}}`),
	}
	for name, frame := range frames {
		b.Run(name+"/sdk", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(frame)))
			for b.Loop() {
				var env Envelope
				if err := json.Unmarshal(frame, &env); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(name+"/v2", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(frame)))
			for b.Loop() {
				var env Envelope
				if err := decodeEnvelope(frame, &env); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
