package link_test

import (
	"bytes"
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/link/message.test.ts: round trips of every kind through both codecs, the byte layout
// of the binary codec, the tuples of the text codec, and the envelope check.

const fingerprint = "0123456789abcdef"

func hold(id int, params link.Part) *link.HoldMessage {
	return &link.HoldMessage{Hold: id, Type: "app.notes", Version: 1, Fingerprint: fingerprint, Params: params, Address: server()}
}

func transientCause() link.WireCause {
	return link.WireCause{Kind: "transient", Error: chordError("unavailable", "m")}
}

// everyKind is one message of every kind, with the optional fields both present and absent; parts are JSON text.
func everyKind() []link.Message {
	released := chordError("released", "gone")
	withIncarnation := hold(1, link.Text(`{"session":"S1"}`))
	withIncarnation.Address = link.WireAddress{Kind: "session", Params: object("id", "S1")}
	withIncarnation.Principal = object("kind", "guest")
	withIncarnation.Incarnation = incarnation
	onlyIncarnation := hold(2, link.Text("{}"))
	onlyIncarnation.Incarnation = incarnation
	first := hold(300, link.Text(`{"id":1}`))
	first.Version = 2
	chained := hold(3, link.Text("{}"))
	chained.Chain = []string{incarnation, incarnation}
	chained.Limit = some(65_536)
	limited := hold(4, link.Text("{}"))
	limited.Type = "t"
	limited.Limit = some(0)
	origin := hold(0, link.Text("{}"))
	origin.Type = "t"
	origin.Origin = some(2)
	return []link.Message{
		&link.HelloMessage{Protocol: 1, Incarnation: incarnation, Calls: 256},
		first,
		withIncarnation,
		onlyIncarnation,
		&link.ReleaseMessage{Hold: 0},
		&link.CallMessage{Call: 3, Hold: 0, Method: 1, Args: link.Text(`{"text":"x"}`)},
		&link.CallMessage{Call: 4, Hold: 0, Method: 0},
		&link.AbortMessage{Call: 3},
		&link.BoundMessage{Holds: []link.BoundEntry{{Hold: 0, Variant: 1, Members: []link.BoundMember{{Member: 0, Value: link.Text(`[1,"a"]`)}}, Failed: []link.FailedMember{{Member: 1, Error: released}}}}},
		&link.BoundMessage{Holds: []link.BoundEntry{
			{Hold: 0, Variant: 0},
			{Hold: 5, Variant: 2, Members: []link.BoundMember{{Member: 1, Value: link.Text("null")}}},
		}},
		&link.StateMessage{Holds: []link.StateEntry{
			{Hold: 0, Members: []link.StateMember{{Member: 2, Ops: link.Text(`[["s",["n"],1]]`)}}},
			{Hold: 1, Failed: []link.FailedMember{{Member: 0, Error: released}}},
			// Retired members (decision "One retirement mechanism"), alone and with a failure.
			{Hold: 2, Members: []link.StateMember{{Member: 1, Ops: link.Text(`[["r",{}]]`)}}, Retired: []int{1, 3}},
			{Hold: 3, Failed: []link.FailedMember{{Member: 0, Error: released}}, Retired: []int{2}},
		}},
		&link.FailedMessage{Hold: 0, Cause: transientCause()},
		&link.FailedMessage{Hold: 0, Cause: link.WireCause{Kind: "broken", Error: chordError("unavailable", "m")}},
		&link.FailedMessage{Hold: 0, Cause: transientCause(), RetryIn: some(0)},
		// Not the object's fault (dependency model §2): no backoff window, an event wakes it.
		&link.FailedMessage{Hold: 0, Cause: link.WireCause{Kind: "waiting", Error: chordError("contract_mismatch", "m")}},
		// The wire error format in `failed` and `gone`: a plugin's error travels too.
		&link.FailedMessage{Hold: 0, Cause: link.WireCause{Kind: "broken", Error: pluginError("config", "m")}},
		&link.GoneMessage{Hold: 1, Error: pluginError("config", "m", object("at", 1.0))},
		&link.FailedMessage{Hold: 9, Cause: transientCause(), RetryIn: some(60_000)},
		&link.RetryMessage{Hold: 300},
		// The retry operation it belongs to: an object reached twice handles it once.
		&link.RetryMessage{Hold: 300, Operation: incarnation},
		// And the instance ids of the objects it passed through: a ring stops after one lap.
		&link.RetryMessage{Hold: 300, Operation: incarnation, Through: []string{incarnation, incarnation}},
		&link.LoadingMessage{Hold: 300},
		&link.ResultMessage{Call: 3, Value: link.Text(`{"n":1}`)},
		&link.ResultMessage{Call: 3},
		&link.ErrorMessage{Call: 3, Error: pluginError("c", "m", []any{1.0})},
		&link.GoneMessage{Hold: 0, Error: chordError("released", "m")},
		&link.AckMessage{},
		&link.AckMessage{Origin: some(1)},
		&link.ClosedMessage{Origin: 300},
		origin,
		&link.ReleaseMessage{Origin: some(2), Hold: 3},
		&link.CallMessage{Origin: some(2), Call: 1, Hold: 3, Method: 0, Args: link.Text("[1]")},
		&link.AbortMessage{Origin: some(2), Call: 1},
		&link.RetryMessage{Origin: some(2), Hold: 3},
		&link.BoundMessage{Origin: some(2), Holds: []link.BoundEntry{{Hold: 3, Variant: 0}}},
		&link.StateMessage{Origin: some(2)},
		&link.FailedMessage{Origin: some(2), Hold: 3, Cause: link.WireCause{Kind: "broken", Error: chordError("unavailable", "m")}},
		&link.LoadingMessage{Origin: some(2), Hold: 3},
		&link.ResultMessage{Origin: some(2), Call: 1},
		&link.ErrorMessage{Origin: some(2), Call: 1, Error: pluginError("c", "m")},
		&link.GoneMessage{Origin: some(2), Hold: 3, Error: chordError("released", "m")},
		chained,
		limited,
		&link.RevokeMessage{Revocation: object("grant", "g1")},
		&link.PingMessage{},
		&link.PongMessage{},
	}
}

func TestMessages(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		for _, message := range everyKind() {
			t.Run("round-trip "+link.DescribeMessage(message)+" and pass the message schema", func(t *testing.T) {
				decoded, err := link.CheckMessage(decode(t, codec, encode(t, codec, message)), background)
				requireNoError(t, err)
				requireCanon(t, decoded, message)
			})
		}

		t.Run("round-trip deflated parts in every part's place, still deflated (a relay forwards them so)", func(t *testing.T) {
			deflated := func() link.Part { return link.NewDeflatedPart([]byte{0, 1, 2, 255}) }
			params := hold(0, deflated())
			params.Type = "t"
			messages := []link.Message{
				params,
				&link.CallMessage{Call: 0, Hold: 0, Method: 0, Args: deflated()},
				&link.ResultMessage{Call: 0, Value: deflated(), Segments: [][]byte{{7}}},
				&link.BoundMessage{Holds: []link.BoundEntry{{Hold: 0, Variant: 0, Members: []link.BoundMember{{Member: 0, Value: deflated()}}}}},
				&link.StateMessage{Holds: []link.StateEntry{{Hold: 0, Members: []link.StateMember{{Member: 0, Ops: deflated()}}}}},
			}
			for _, message := range messages {
				decoded, err := link.CheckMessage(decode(t, codec, encode(t, codec, message)), background)
				requireNoError(t, err)
				requireCanon(t, decoded, message)
			}
			decoded := decode(t, codec, encode(t, codec, messages[1])).(*link.CallMessage)
			if part, ok := decoded.Args.(*link.DeflatedPart); !ok || !bytes.Equal(part.Bytes, []byte{0, 1, 2, 255}) {
				t.Fatalf("args = %#v", decoded.Args)
			}
		})

		t.Run("round-trip parts received as bytes (a relay forwards them as they came)", func(t *testing.T) {
			message := &link.ResultMessage{Call: 0, Value: link.Bytes(`{"n":1}`)}
			requireCanon(t, decode(t, codec, encode(t, codec, message)), &link.ResultMessage{Call: 0, Value: link.Text(`{"n":1}`)})
		})

		t.Run("carry byte segments beside a payload, as views of the message", func(t *testing.T) {
			payload := bytes.Repeat([]byte{7}, 1024)
			thumbs := []any{[]byte{1, 2}, []byte{3}}
			part, err := link.PayloadPart(object("name", "a", "data", payload, "thumbs", thumbs), true)
			requireNoError(t, err)
			message := &link.CallMessage{Call: 0, Hold: 0, Method: 0, Args: link.Text(part.Json), Segments: part.Segments}
			encoded := encode(t, codec, message)
			decoded := decode(t, codec, encoded).(*link.CallMessage)
			if len(decoded.Segments) != 3 {
				t.Fatalf("segments = %d", len(decoded.Segments))
			}
			// A view of the received bytes: the first segment's memory lies inside the message.
			if !sameBacking(encoded, decoded.Segments[0]) {
				t.Fatal("the first segment is not a view of the message")
			}
			args, err := link.ParsePayload(decoded.Args, decoded.Segments)
			requireNoError(t, err)
			root := args.(*delta.JsonObject)
			if got := root.Value("data").([]byte); !bytes.Equal(got, payload) || !sameBacking(encoded, got) {
				t.Fatal("data is not a view of the message")
			}
			nexttest.RequireJSONEqual(t, root.Value("thumbs").([]any)[0], []byte{1, 2})
			// The segments travel raw: the message is barely larger than the bytes it carries.
			if len(encoded) >= 1024+3+100 {
				t.Fatalf("message = %d bytes", len(encoded))
			}
		})

		t.Run("refuse an empty message, an unknown kind and trailing bytes", func(t *testing.T) {
			release := encode(t, codec, &link.ReleaseMessage{Hold: 1})
			trailing := append(append([]byte{}, release...), 0)
			unknown := []byte{99}
			if codec == link.TextCodec {
				trailing = append(append([]byte{}, release...), 0x20, 0x31)
				unknown = []byte(`["?"]`)
			}
			if _, err := codec.Decode(nil); err == nil {
				t.Fatal("an empty message decoded")
			}
			_, err := codec.Decode(unknown)
			requireErrorContains(t, err, "unknown kind")
			if _, err := codec.Decode(trailing); err == nil {
				t.Fatal("trailing bytes decoded")
			}
		})

		t.Run("refuse an origin on a kind without one, an origin 0, and a closed without one (the message schema)", func(t *testing.T) {
			// A ping that carries an origin (forged: the Go message type has none) is refused by the decode.
			forged := []byte{15 | 0x80, 1}
			if codec == link.TextCodec {
				forged = []byte(`["@",1,["p"]]`)
			}
			if _, err := codec.Decode(forged); err == nil {
				t.Fatal("an origin on a ping decoded")
			}
			ack := decode(t, codec, encode(t, codec, &link.AckMessage{Origin: some(0)}))
			_, err := link.CheckMessage(ack, background)
			requireErrorContains(t, err, "message schema")
			closed := []byte{14}
			if codec == link.TextCodec {
				closed = []byte(`["C"]`)
			}
			_, err = link.CheckMessage(decode(t, codec, closed), background)
			requireErrorContains(t, err, "message schema")
		})

		t.Run("keep a leading U+FEFF in strings and parts (it is data, not a byte order mark)", func(t *testing.T) {
			text, err := link.JsonPart(object("\uFEFFkey", "\uFEFFvalue"))
			requireNoError(t, err)
			message := hold(0, text)
			message.Type = "\uFEFFthing"
			decoded := decode(t, codec, encode(t, codec, message)).(*link.HoldMessage)
			requireCanon(t, decoded, message)
			parsed, err := link.ParsePart(decoded.Params)
			requireNoError(t, err)
			if parsed.(*delta.JsonObject).Value("\uFEFFkey") != "\uFEFFvalue" {
				t.Fatalf("parsed = %v", parsed)
			}
			result := &link.ResultMessage{Call: 0, Value: link.Text(`"` + "\uFEFF" + `"`)}
			requireCanon(t, decode(t, codec, encode(t, codec, result)), result)
		})

		t.Run("leave the envelope to the message schema: a misshapen error passes the codec and fails the schema", func(t *testing.T) {
			decoded := decode(t, codec, encode(t, codec, &link.ErrorMessage{Call: 0, Error: object("kind", "nope")}))
			_, err := link.CheckMessage(decoded, background)
			requireErrorContains(t, err, "message: fails the message schema")
			_, err = link.CheckMessage(&link.ReleaseMessage{Hold: -1}, background)
			requireErrorContains(t, err, "message: fails the message schema")
		})
	})
}

// sameBacking reports whether part lies inside whole's memory (a view, not a copy).
func sameBacking(whole, part []byte) bool {
	if len(part) == 0 || len(whole) == 0 {
		return true
	}
	start := uintptr(unsafe.Pointer(&whole[0]))
	at := uintptr(unsafe.Pointer(&part[0]))
	return at >= start && at+uintptr(len(part)) <= start+uintptr(len(whole))
}

func sameSlice(a, b []int) bool { return unsafe.SliceData(a) == unsafe.SliceData(b) }

func TestTheBinaryCodec(t *testing.T) {
	binary := link.BinaryCodec
	enc := func(message link.Message) []byte { return encode(t, binary, message) }

	t.Run("writes ids as varints, the incarnation as 16 and the fingerprint as 8 raw bytes, no outer length", func(t *testing.T) {
		if got := len(enc(&link.HelloMessage{Protocol: 1, Incarnation: incarnation, Calls: 1})); got != 1+1+16+1 {
			t.Fatalf("hello = %d bytes", got)
		}
		if got := enc(&link.ReleaseMessage{Hold: 300}); !bytes.Equal(got, []byte{2, 0xac, 0x02}) {
			t.Fatalf("release = %v", got)
		}
		message := hold(0, link.Text("{}"))
		message.Type = "t"
		// kind, id, type, version, fingerprint, params, the address's kind and params, the optional fields' flags
		if got := len(enc(message)); got != 1+1+2+1+8+3+7+3+1 {
			t.Fatalf("hold = %d bytes", got)
		}
		if got := enc(&link.AckMessage{}); !bytes.Equal(got, []byte{13}) {
			t.Fatalf("ack = %v", got)
		}
		// An origin: the kind byte's high bit, then the origin as a varint.
		if got := enc(&link.AckMessage{Origin: some(300)}); !bytes.Equal(got, []byte{13 | 0x80, 0xac, 0x02}) {
			t.Fatalf("ack with origin = %v", got)
		}
		if got := enc(&link.ClosedMessage{Origin: 1}); !bytes.Equal(got, []byte{14 | 0x80, 1}) {
			t.Fatalf("closed = %v", got)
		}
	})

	t.Run("writes a failure's cause as one byte, 0 transient, 1 broken, 2 waiting (dependency model §2); refuses another", func(t *testing.T) {
		failed := func(kind string) link.Message {
			return &link.FailedMessage{Hold: 0, Cause: link.WireCause{Kind: kind, Error: chordError("unavailable", "m")}}
		}
		for want, kind := range []string{"transient", "broken", "waiting"} {
			if got := enc(failed(kind))[2]; int(got) != want {
				t.Fatalf("%s = %d", kind, got)
			}
		}
		forged := enc(failed("waiting"))
		forged[2] = 3
		_, err := binary.Decode(forged)
		requireErrorContains(t, err, "an unknown failure kind")
	})

	t.Run("costs a state entry without retirements no byte: the failure count is doubled, plus one when they follow", func(t *testing.T) {
		state := func(retired []int) link.Message {
			return &link.StateMessage{Holds: []link.StateEntry{{Hold: 0, Retired: retired}}}
		}
		// kind, entry count, hold, member count, failures (0 doubled)
		if got := enc(state(nil)); !bytes.Equal(got, []byte{6, 1, 0, 0, 0}) {
			t.Fatalf("no retirements = %v", got)
		}
		// the low bit set, then the retired count and member
		if got := enc(state([]int{3})); !bytes.Equal(got, []byte{6, 1, 0, 0, 1, 1, 3}) {
			t.Fatalf("a retirement = %v", got)
		}
		first := decode(t, binary, enc(state(nil))).(*link.StateMessage).Holds[0].Retired
		second := decode(t, binary, enc(state(nil))).(*link.StateMessage).Holds[0].Retired
		// No allocation per message: decoded entries without retirements share one empty list.
		if len(first) != 0 || len(second) != 0 || !sameSlice(first, link.NoRetired) || !sameSlice(second, link.NoRetired) {
			t.Fatal("entries without retirements do not share NoRetired")
		}
	})

	t.Run("prefixes a part with its length doubled, plus one when it is deflated", func(t *testing.T) {
		plain := enc(&link.ResultMessage{Call: 0, Value: link.Text("12")})
		deflated := enc(&link.ResultMessage{Call: 0, Value: link.NewDeflatedPart([]byte{7, 8})})
		// kind, call, payload flags, prefix, bytes
		if !bytes.Equal(plain, []byte{8, 0, 1, 4, 0x31, 0x32}) || !bytes.Equal(deflated, []byte{8, 0, 1, 5, 7, 8}) {
			t.Fatalf("plain = %v, deflated = %v", plain, deflated)
		}
	})

	t.Run("decodes parts as views of the message, never parsing them: a malformed part decodes", func(t *testing.T) {
		encoded := enc(&link.ResultMessage{Call: 0, Value: link.Bytes("{not json")})
		decoded := decode(t, binary, encoded).(*link.ResultMessage)
		value, ok := decoded.Value.(link.Bytes)
		if !ok || !sameBacking(encoded, value) {
			t.Fatalf("value = %#v", decoded.Value)
		}
		if _, err := link.ParsePart(value); err == nil {
			t.Fatal("a malformed part parsed")
		}
	})

	t.Run("refuses varints past a safe integer and lengths past the message", func(t *testing.T) {
		long := append(append([]byte{2}, bytes.Repeat([]byte{0xff}, 8)...), 0x7f)
		_, err := binary.Decode(long)
		requireErrorContains(t, err, "a varint does not fit")
		_, err = binary.Decode([]byte{1, 0, 9, 0x61})
		requireErrorContains(t, err, "the message ends early")
	})

	t.Run("refuses a varint of more than 8 bytes, which would read as no number", func(t *testing.T) {
		state := append(append([]byte{6}, bytes.Repeat([]byte{0x80}, 150)...), 0)
		_, err := binary.Decode(state)
		requireErrorContains(t, err, "a varint does not fit")
	})

	t.Run("refuses unknown hold flags", func(t *testing.T) {
		message := hold(0, link.Text("{}"))
		message.Type = "t"
		forged := append([]byte{}, enc(message)...)
		forged[len(forged)-1] = 32
		_, err := binary.Decode(forged)
		requireErrorContains(t, err, "unknown hold flags")
	})

	t.Run("returns a right-sized message: a large one keeps no larger backing store", func(t *testing.T) {
		value, _ := link.JsonPart(strings.Repeat("x", 500_000))
		// Small fields after the large value: they must not double the buffer the message keeps.
		members := []link.BoundMember{{Member: 0, Value: value}, {Member: 1, Value: link.Text("1")}}
		large := enc(&link.BoundMessage{Holds: []link.BoundEntry{{Hold: 0, Variant: 0, Members: members}}})
		if spare := cap(large) - len(large); spare > 4096 {
			t.Fatalf("spare = %d", spare)
		}
		small := enc(&link.ReleaseMessage{Hold: 1})
		if cap(small) != len(small) {
			t.Fatalf("small cap = %d, len = %d", cap(small), len(small))
		}
		// A message of about two thirds of the starting buffer keeps no spare room either.
		text, _ := link.JsonPart(strings.Repeat("x", 160))
		medium := enc(&link.ResultMessage{Call: 0, Value: text})
		if cap(medium) != len(medium) {
			t.Fatalf("medium cap = %d, len = %d", cap(medium), len(medium))
		}
	})

	t.Run("refuses invalid UTF-8 in strings", func(t *testing.T) {
		if _, err := binary.Decode([]byte{1, 0, 1, 0xff}); err == nil {
			t.Fatal("invalid UTF-8 decoded")
		}
	})

	t.Run("writes retry as its kind, hold, flags, operation (16 raw bytes) and objects passed through, loading as its kind and hold, and failed's retryIn as one more than it (0: absent)", func(t *testing.T) {
		if got := enc(&link.RetryMessage{Hold: 300}); !bytes.Equal(got, []byte{11, 0xac, 0x02, 0}) {
			t.Fatalf("retry = %v", got)
		}
		if got := len(enc(&link.RetryMessage{Hold: 300, Operation: incarnation})); got != 4+16 {
			t.Fatalf("retry with operation = %d", got)
		}
		through := []string{incarnation, incarnation}
		if got := len(enc(&link.RetryMessage{Hold: 300, Operation: incarnation, Through: through})); got != 4+16+1+32 {
			t.Fatalf("retry with through = %d", got)
		}
		flagged := append([]byte{}, enc(&link.RetryMessage{Hold: 300})...)
		flagged[3] = 4
		_, err := binary.Decode(flagged)
		requireErrorContains(t, err, "an unknown retry flag")
		if got := enc(&link.LoadingMessage{Hold: 1}); !bytes.Equal(got, []byte{12, 1}) {
			t.Fatalf("loading = %v", got)
		}
		cause := link.WireCause{Kind: "broken", Error: chordError("released", "")}
		absent := enc(&link.FailedMessage{Hold: 0, Cause: cause})
		present := enc(&link.FailedMessage{Hold: 0, Cause: cause, RetryIn: some(0)})
		if absent[len(absent)-1] != 0 || present[len(present)-1] != 1 {
			t.Fatalf("retryIn bytes: %d, %d", absent[len(absent)-1], present[len(present)-1])
		}
	})
}

func TestTheTextCodec(t *testing.T) {
	text := func(message link.Message) string { return string(encode(t, link.TextCodec, message)) }
	decodeText := func(tuple string) (link.Message, error) { return link.TextCodec.Decode([]byte(tuple)) }

	t.Run("writes one JSON tuple per message with short tags and positional fields, parts spliced in", func(t *testing.T) {
		check := func(message link.Message, want string) {
			t.Helper()
			if got := text(message); got != want {
				t.Fatalf("%T = %s, want %s", message, got, want)
			}
		}
		check(&link.ReleaseMessage{Hold: 3}, `["R",3]`)
		check(&link.CallMessage{Call: 1, Hold: 0, Method: 2}, `["c",1,0,2]`)
		check(&link.BoundMessage{Holds: []link.BoundEntry{{Hold: 0, Variant: 1, Members: []link.BoundMember{{Member: 0, Value: link.Text("5")}}}}}, `["b",[[0,1,[[0,[0,5]]]]]]`)
		check(&link.RetryMessage{Hold: 2}, `["T",2]`)
		check(&link.LoadingMessage{Hold: 2}, `["l",2]`)
		wire := `{"kind":"chord","error":{"code":"unavailable","message":"m"}}`
		check(&link.FailedMessage{Hold: 2, Cause: transientCause()}, `["f",2,"t",`+wire+`]`)
		check(&link.FailedMessage{Hold: 2, Cause: transientCause(), RetryIn: some(750)}, `["f",2,"t",`+wire+`,750]`)
		check(&link.FailedMessage{Hold: 2, Cause: link.WireCause{Kind: "waiting", Error: chordError("unavailable", "m")}}, `["f",2,"w",`+wire+`]`)
		_, err := decodeText(`["f",2,"x",{}]`)
		requireErrorContains(t, err, "an unknown failure kind")
		check(&link.AckMessage{}, `["a"]`)
		// An origin wraps the message's own tuple.
		check(&link.ReleaseMessage{Origin: some(4), Hold: 3}, `["@",4,["R",3]]`)
		check(&link.ClosedMessage{Origin: 4}, `["@",4,["C"]]`)
	})

	t.Run("tags every part with its codec, [0, json] or [1, base64] for a deflated one, so no value is reserved", func(t *testing.T) {
		message := &link.ResultMessage{Call: 1, Value: link.NewDeflatedPart([]byte{1, 2, 3})}
		if got := text(message); got != `["r",1,[1,"AQID"]]` {
			t.Fatalf("deflated = %s", got)
		}
		requireCanon(t, decode(t, link.TextCodec, encode(t, link.TextCodec, message)), message)
		// A value of any shape is data, the one-key `$deflated` object the first builder reserved included.
		shaped := &link.ResultMessage{Call: 1, Value: link.Text(`{"$deflated":"AQID"}`)}
		if got := text(shaped); got != `["r",1,[0,{"$deflated":"AQID"}]]` {
			t.Fatalf("shaped = %s", got)
		}
		requireCanon(t, decode(t, link.TextCodec, encode(t, link.TextCodec, shaped)), shaped)
		if got := text(&link.RevokeMessage{Revocation: object("grant", "g")}); got != `["V",{"grant":"g"}]` {
			t.Fatalf("revoke = %s", got)
		}
	})

	t.Run("refuses a part that is no [codec, payload] pair, or of an unknown codec", func(t *testing.T) {
		_, err := decodeText(`["r",1,{"n":1}]`)
		requireErrorContains(t, err, "no [codec, payload] pair")
		_, err = decodeText(`["r",1,[2,"AQID"]]`)
		requireErrorContains(t, err, "unknown codec")
		_, err = decodeText(`["r",1,[1,5]]`)
		requireErrorContains(t, err, "unknown codec")
	})

	t.Run("carries a binary link's deflated part over a text hop and back unchanged (a relay between codecs)", func(t *testing.T) {
		part := link.NewDeflatedPart([]byte{9, 8, 7})
		message := &link.StateMessage{Holds: []link.StateEntry{{Hold: 0, Members: []link.StateMember{{Member: 0, Ops: part}}}}}
		overText := decode(t, link.TextCodec, encode(t, link.TextCodec, decode(t, link.BinaryCodec, encode(t, link.BinaryCodec, message))))
		requireCanon(t, decode(t, link.BinaryCodec, encode(t, link.BinaryCodec, overText)), message)
	})

	t.Run("leaves out a hold's absent optional fields at the end, null in the middle", func(t *testing.T) {
		message := hold(0, link.Text("{}"))
		message.Type = "t"
		head := `["H",0,"t",1,"0123456789abcdef",[0,{}],["server",{}]`
		if got := text(message); got != head+"]" {
			t.Fatalf("bare = %s", got)
		}
		message.Incarnation = incarnation
		if got := text(message); got != head+`,null,"`+incarnation+`"]` {
			t.Fatalf("incarnation = %s", got)
		}
		message.Incarnation = ""
		message.Limit = some(5)
		if got := text(message); got != head+",null,null,null,5]" {
			t.Fatalf("limit = %s", got)
		}
	})

	t.Run("puts byte segments after the tuple and a newline, their lengths the tuple's last field", func(t *testing.T) {
		got := encode(t, link.TextCodec, &link.ResultMessage{Call: 0, Value: link.Text(`{"$bytes":0}`), Segments: [][]byte{{1, 2}}})
		if want := append([]byte("[\"r\",0,[0,{\"$bytes\":0}],[2]]\n"), 1, 2); !bytes.Equal(got, want) {
			t.Fatalf("encoded = %q, want %q", got, want)
		}
	})

	t.Run("refuses a tuple of the wrong arity and segments without a payload carrying them", func(t *testing.T) {
		for tuple, want := range map[string]string{
			`["R",1,2]`:              `a "R" message with 2 fields`,
			`["s",[[0,[],[],[],9]]]`: "a state entry with 5 fields",
			`["s",[[0]]]`:            "a state entry with 1 fields",
			`["@",1,["@",2,["a"]]]`:  "wraps one message",
			`["@",1]`:                "wraps one message",
		} {
			_, err := decodeText(tuple)
			requireErrorContains(t, err, want)
		}
		_, err := link.TextCodec.Decode(append([]byte("[\"R\",1]\n"), 1))
		requireErrorContains(t, err, "byte segments without a payload")
	})

	t.Run("refuses a hold without its address: every hold names one", func(t *testing.T) {
		_, err := decodeText(`["H",0,"t",1,"0123456789abcdef",[0,{}]]`)
		requireErrorContains(t, err, `a "H" message with 5 fields`)
		_, err = decodeText(`["H",0,"t",1,"0123456789abcdef",[0,{}],null]`)
		requireErrorContains(t, err, "expected a list")
		_, err = decodeText(`["H",0,"t",1,"0123456789abcdef",[0,{}],["server"]]`)
		requireErrorContains(t, err, "an address is a pair")
		// A hold without its params or address (the Go message type cannot omit the address; the params can be absent).
		noParams := &link.HoldMessage{Type: "t", Version: 1, Fingerprint: fingerprint, Address: server()}
		_, err = link.CheckMessage(noParams, background)
		requireErrorContains(t, err, "message schema")
	})
}

func TestDescribeMessage(t *testing.T) {
	if got := link.DescribeMessage(&link.AbortMessage{Call: 7}); got != `["x",7]` {
		t.Fatalf("abort = %s", got)
	}
	got := link.DescribeMessage(&link.ResultMessage{Call: 0, Value: link.Text(`{"$bytes":0}`), Segments: [][]byte{make([]byte, 3)}})
	if got != `["r",0,[0,{"$bytes":0}],[3]]` {
		t.Fatalf("result = %s", got)
	}
}

func TestACodecMismatch(t *testing.T) {
	hello := &link.HelloMessage{Protocol: 1, Incarnation: incarnation, Calls: 256}
	if _, err := link.BinaryCodec.Decode(encode(t, link.TextCodec, hello)); err == nil {
		t.Fatal("the binary codec decoded a text hello")
	}
	if _, err := link.TextCodec.Decode(encode(t, link.BinaryCodec, hello)); err == nil {
		t.Fatal("the text codec decoded a binary hello")
	}
}

func TestTheBinaryCodecsStrings(t *testing.T) {
	t.Run("survive the buffer growing around them, at every offset near the initial size", func(t *testing.T) {
		for length := range 400 {
			// A long value of three-byte characters, then a short one that lands at every offset around the first growth.
			value, _ := link.JsonPart(strings.Repeat("€", length))
			members := []link.BoundMember{{Member: 0, Value: value}, {Member: 1, Value: link.Text("1")}}
			message := &link.BoundMessage{Holds: []link.BoundEntry{{Hold: 1, Variant: 0, Members: members}}}
			requireCanon(t, decode(t, link.BinaryCodec, encode(t, link.BinaryCodec, message)), message)
		}
	})

	for name, content := range map[string]string{
		"ASCII":                            strings.Repeat("x", 10_000),
		"three-byte characters":            strings.Repeat("€", 10_000),
		"surrogate pairs":                  strings.Repeat("😀", 5_000),
		"mixed, at the in-place threshold": strings.Repeat("a", 4_093) + "😀€",
		"a leading U+FEFF":                 "\uFEFF" + strings.Repeat("y", 5_000),
	} {
		t.Run("round-trip "+name+", encoded in place behind a padded length", func(t *testing.T) {
			value, _ := link.JsonPart(content)
			message := &link.ResultMessage{Call: 0, Value: value}
			encoded := encode(t, link.BinaryCodec, message)
			requireCanon(t, decode(t, link.BinaryCodec, encoded), message)
			if spare := cap(encoded) - len(encoded); spare > 4096 {
				t.Fatalf("spare = %d", spare)
			}
		})
	}

	for _, length := range []int{0, 1, 41, 42, 43} {
		t.Run("round-trip short strings at three-byte characters, around the one-byte length", func(t *testing.T) {
			typ := strings.Repeat("€", length)
			params, _ := link.JsonPart(typ)
			message := hold(0, params)
			message.Type = typ
			requireCanon(t, decode(t, link.BinaryCodec, encode(t, link.BinaryCodec, message)), message)
		})
	}
}
