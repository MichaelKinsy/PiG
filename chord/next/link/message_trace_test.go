package link_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports the trace-carrier cases of packages/chord/test/link/message.test.ts (R5, slice 9): a carrier on a hold and a
// call, with and without an origin, arguments and other fields; its bounds; the kind byte's flag 0x40; the text
// codec's positions.

// traceKinds are the carrier samples of upstream's `MESSAGES`.
func traceKinds() []link.Message {
	withTrace := hold(5, link.Text("{}"))
	withTrace.Trace = map[string]string{"pi-memory-span": "1", "pi-memory-trace": "s:1"}
	withOrigin := hold(6, link.Text("{}"))
	withOrigin.Origin = some(3)
	withOrigin.Limit = some(10)
	withOrigin.Trace = map[string]string{"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}
	return []link.Message{
		withTrace,
		withOrigin,
		&link.CallMessage{Call: 7, Hold: 0, Method: 1, Args: link.Text(`{"text":"x"}`), Trace: map[string]string{"a": "b"}},
		&link.CallMessage{Origin: some(2), Call: 8, Hold: 3, Method: 0, Trace: map[string]string{}},
		&link.CallMessage{Call: 9, Hold: 0, Method: 1, Args: link.Text(`{"text":"x"}`), Old: true},
		&link.CallMessage{Origin: some(2), Call: 10, Hold: 3, Method: 0, Trace: map[string]string{"a": "b"}, Old: true},
	}
}

// largestCarrier is a carrier at the bounds: 8 keys, keys and values of 256 characters.
func largestCarrier() map[string]string {
	carrier := map[string]string{}
	for index := range 8 {
		key := string(rune('0' + index))
		carrier[key+strings.Repeat("k", 255)] = strings.Repeat("v", 256)
	}
	return carrier
}

func TestMessagesCarryATraceCarrier(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		for _, message := range traceKinds() {
			t.Run("round-trip "+link.DescribeMessage(message)+" and pass the message schema", func(t *testing.T) {
				decoded, err := link.CheckMessage(decode(t, codec, encode(t, codec, message)), background)
				requireNoError(t, err)
				requireCanon(t, decoded, message)
				if !reflect.DeepEqual(traceOf(decoded), traceOf(message)) {
					t.Fatalf("trace = %#v, want %#v", traceOf(decoded), traceOf(message))
				}
			})
		}

		t.Run("carry a trace carrier up to its bounds; past them, of another shape or on another kind, the schema refuses it", func(t *testing.T) {
			check := func(message *link.CallMessage) (link.Message, error) {
				decoded, err := codec.Decode(encode(t, codec, message))
				if err != nil {
					return nil, err
				}
				return link.CheckMessage(decoded, background)
			}
			call := func(carrier map[string]string) *link.CallMessage {
				return &link.CallMessage{Call: 0, Hold: 0, Method: 0, Trace: carrier}
			}
			ok := func(carrier map[string]string) {
				t.Helper()
				decoded, err := check(call(carrier))
				requireNoError(t, err)
				if !reflect.DeepEqual(traceOf(decoded), carrier) {
					t.Fatalf("trace = %#v, want %#v", traceOf(decoded), carrier)
				}
			}
			refused := func(carrier map[string]string) {
				t.Helper()
				_, err := check(call(carrier))
				requireErrorContains(t, err, "message schema")
			}
			ok(largestCarrier())
			bigger := largestCarrier()
			bigger["nine"] = "x"
			refused(bigger)
			refused(map[string]string{strings.Repeat("k", 257): "v"})
			refused(map[string]string{"k": strings.Repeat("v", 257)})
			// Printable ASCII only (review M2): no control character, no space in a key; a value may hold one (W3C's
			// `tracestate`).
			refused(map[string]string{"k": "a\u001b[2J"})
			refused(map[string]string{"k\u0007": "v"})
			refused(map[string]string{"a b": "v"})
			refused(map[string]string{"k": "é"})
			ok(map[string]string{"k": "a=1, b=2"})
		})

		t.Run("refuse a carrier of another shape on the wire: not an object of strings", func(t *testing.T) {
			for _, forged := range []string{`{"k":1}`, `["k"]`} {
				var bytes []byte
				if codec == link.TextCodec {
					bytes = []byte(`["c",0,0,0,null,null,` + forged + `]`)
				} else {
					bytes = append([]byte{3 | 0x40, byte(len(forged))}, forged...)
					bytes = append(bytes, 0, 0, 0, 0)
				}
				_, err := codec.Decode(bytes)
				requireErrorContains(t, err, "message schema")
			}
		})

		t.Run("carry a trace carrier beside a call's byte segments", func(t *testing.T) {
			part, err := link.PayloadPart(object("data", []byte{1, 2, 3}), true)
			requireNoError(t, err)
			message := &link.CallMessage{Call: 0, Hold: 0, Method: 0, Args: link.Text(part.Json), Segments: part.Segments, Trace: map[string]string{"a": "b"}}
			decoded := decode(t, codec, encode(t, codec, message)).(*link.CallMessage)
			if !reflect.DeepEqual(decoded.Trace, map[string]string{"a": "b"}) {
				t.Fatalf("trace = %#v", decoded.Trace)
			}
			if !reflect.DeepEqual(decoded.Segments, part.Segments) {
				t.Fatalf("segments = %v, want %v", decoded.Segments, part.Segments)
			}
		})

		t.Run("refuse a type or host kind past 256 characters (review M1: a relay records them unparsed)", func(t *testing.T) {
			message := hold(0, link.Text("{}"))
			message.Type = strings.Repeat("t", 256)
			message.Address = link.WireAddress{Kind: strings.Repeat("k", 256), Params: object()}
			_, err := link.CheckMessage(decode(t, codec, encode(t, codec, message)), background)
			requireNoError(t, err)
			message.Type = strings.Repeat("t", 257)
			_, err = link.CheckMessage(decode(t, codec, encode(t, codec, message)), background)
			requireErrorContains(t, err, "message schema")
			message.Type = "t"
			message.Address = link.WireAddress{Kind: strings.Repeat("k", 257), Params: object()}
			_, err = link.CheckMessage(decode(t, codec, encode(t, codec, message)), background)
			requireErrorContains(t, err, "message schema")
		})
	})
}

func TestTheBinaryCodecFlagsATrace(t *testing.T) {
	t.Run("flags a trace carrier in the kind byte (0x40) and writes it as JSON after the origin", func(t *testing.T) {
		call := encode(t, link.BinaryCodec, &link.CallMessage{Origin: some(2), Call: 0, Hold: 0, Method: 0, Trace: map[string]string{"a": "b"}})
		json := []byte(`{"a":"b"}`)
		want := append([]byte{3 | 0x80 | 0x40, 2, byte(len(json))}, json...)
		if !reflect.DeepEqual(call[:len(want)], want) {
			t.Fatalf("call = %v, want it to start with %v", call, want)
		}
		plain := encode(t, link.BinaryCodec, &link.CallMessage{Call: 0, Hold: 0, Method: 0})
		if plain[0] != 3 {
			t.Fatalf("plain kind byte = %d", plain[0])
		}
	})
}

func TestTheTextCodecWritesATrace(t *testing.T) {
	text := func(message link.Message) string { return string(encode(t, link.TextCodec, message)) }

	t.Run("leaves out a hold's absent optional fields at the end, null in the middle (trace)", func(t *testing.T) {
		message := hold(0, link.Text("{}"))
		message.Type = "t"
		message.Trace = map[string]string{"a": "b"}
		head := `["H",0,"t",1,"0123456789abcdef",[0,{}],["server",{}]`
		if got := text(message); got != head+`,null,null,null,null,{"a":"b"}]` {
			t.Fatalf("trace = %s", got)
		}
	})

	t.Run("writes a call's trace carrier last, its payload's fields positional then (null when absent)", func(t *testing.T) {
		call := &link.CallMessage{Call: 0, Hold: 1, Method: 2}
		call.Trace = map[string]string{"a": "b"}
		if got := text(call); got != `["c",0,1,2,null,null,{"a":"b"}]` {
			t.Fatalf("no payload = %s", got)
		}
		call.Args = link.Text("[1]")
		if got := text(call); got != `["c",0,1,2,[0,[1]],null,{"a":"b"}]` {
			t.Fatalf("payload = %s", got)
		}
		call.Trace = nil
		if got := text(call); got != `["c",0,1,2,[0,[1]]]` {
			t.Fatalf("no trace = %s", got)
		}
	})

	t.Run("writes a call's old-work flag after its carrier (null when absent), its payload's fields positional then", func(t *testing.T) {
		call := &link.CallMessage{Call: 0, Hold: 1, Method: 2, Old: true}
		if got := text(call); got != `["c",0,1,2,null,null,null,true]` {
			t.Fatalf("old = %s", got)
		}
		call.Args = link.Text("[1]")
		call.Trace = map[string]string{"a": "b"}
		if got := text(call); got != `["c",0,1,2,[0,[1]],null,{"a":"b"},true]` {
			t.Fatalf("old with trace = %s", got)
		}
	})

	t.Run("leaves the old-work flag's value to the message schema: only true passes", func(t *testing.T) {
		_, err := link.TextCodec.Decode([]byte(`["c",0,1,2,null,null,null,1]`))
		requireErrorContains(t, err, "message schema")
	})
}

func traceOf(message link.Message) map[string]string {
	switch typed := message.(type) {
	case *link.HoldMessage:
		return typed.Trace
	case *link.CallMessage:
		return typed.Trace
	}
	return nil
}

// Pi writes a carrier's keys in insertion order, so a carrier that arrived as `{z, a}` is written on as `{z, a}`; a carrier
// made here, a Go map, is written with sorted keys.
func TestACarrierKeepsTheOrderOfItsKeysAsItArrivedAndIsWrittenSortedWhenMade(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		arrived := &link.CallMessage{Call: 1, Hold: 0, Method: 1, Trace: map[string]string{"z": "1", "a": "2", "m": "3"}, TraceOrder: []string{"z", "a", "m"}}
		decoded := decode(t, codec, encode(t, codec, arrived)).(*link.CallMessage)
		if !reflect.DeepEqual(decoded.TraceOrder, []string{"z", "a", "m"}) {
			t.Fatalf("decoded key order = %v, want z, a, m", decoded.TraceOrder)
		}
		again := decode(t, codec, encode(t, codec, decoded)).(*link.CallMessage)
		if !reflect.DeepEqual(again.TraceOrder, []string{"z", "a", "m"}) {
			t.Fatalf("relayed key order = %v, want z, a, m", again.TraceOrder)
		}
		made := &link.CallMessage{Call: 1, Hold: 0, Method: 1, Trace: map[string]string{"z": "1", "a": "2", "m": "3"}}
		if got := decode(t, codec, encode(t, codec, made)).(*link.CallMessage).TraceOrder; !reflect.DeepEqual(got, []string{"a", "m", "z"}) {
			t.Fatalf("made carrier key order = %v, want sorted", got)
		}
	})
}
