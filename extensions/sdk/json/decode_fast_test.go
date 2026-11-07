package json

import (
	"bytes"
	"errors"
	"math"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// randomDocument is a valid JSON value of random shape: nested objects and arrays, empty containers, every literal, numbers in every
// form, white space between tokens, and strings with every escape the encoder writes and ill-formed bytes it replaces.
func randomDocument(rng *rand.Rand, depth int, out *strings.Builder) {
	space := func() {
		if rng.IntN(4) == 0 {
			out.WriteString([]string{" ", "\n", "\t", "\r\n  "}[rng.IntN(4)])
		}
	}
	text := func() {
		var b strings.Builder
		for range rng.IntN(10) {
			b.WriteString(swarPieces[rng.IntN(len(swarPieces))])
		}
		encoded, _ := MarshalStringify(b.String())
		out.Write(encoded)
	}
	switch kind := rng.IntN(10); {
	case kind < 3 && depth < 6:
		out.WriteByte('{')
		for i := range rng.IntN(5) {
			if i > 0 {
				out.WriteByte(',')
			}
			space()
			text()
			space()
			out.WriteByte(':')
			space()
			randomDocument(rng, depth+1, out)
			space()
		}
		out.WriteByte('}')
	case kind < 5 && depth < 6:
		out.WriteByte('[')
		for i := range rng.IntN(5) {
			if i > 0 {
				out.WriteByte(',')
			}
			space()
			randomDocument(rng, depth+1, out)
			space()
		}
		out.WriteByte(']')
	case kind < 7:
		text()
	case kind == 7:
		out.WriteString([]string{"0", "-0", "1", "-12", "3.5", "1e3", "-2.5E-7", "123456789012345678901234567890", "0.000001"}[rng.IntN(9)])
	default:
		out.WriteString([]string{"true", "false", "null"}[rng.IntN(3)])
	}
}

// The decoder crosses a value it does not store (a RawMessage member, an unknown struct field) without stepping the scanner. Each member of
// a random object, kept raw, must be exactly its value (it decodes alone to what the member decodes to in the whole), and the members after
// a crossed value must still decode: the scanner is left where stepping the closing byte would leave it.
func TestDecodeCrossesValuesWithoutLosingItsPlace(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for i := range 3000 {
		var doc strings.Builder
		doc.WriteByte('{')
		keys := 1 + rng.IntN(6)
		for k := range keys {
			if k > 0 {
				doc.WriteByte(',')
			}
			doc.WriteString(`"k` + string(rune('a'+k)) + `":`)
			randomDocument(rng, 1, &doc)
		}
		doc.WriteString(`,"last":7}`)
		data := []byte(doc.String())

		var whole map[string]any
		if err := Unmarshal(data, &whole); err != nil {
			t.Fatalf("%d: %s: %v", i, data, err)
		}
		for name, decode := range map[string]func([]byte, any) error{"Unmarshal": Unmarshal, "UnmarshalFrame": UnmarshalFrame, "UnmarshalTrusted": UnmarshalTrusted} {
			var raw map[string]RawMessage
			if err := decode(data, &raw); err != nil {
				t.Fatalf("%d: %s into raw members with %s: %v", i, data, name, err)
			}
			if len(raw) != len(whole) {
				t.Fatalf("%d: %s: %s kept %d members, the value has %d", i, data, name, len(raw), len(whole))
			}
			for key, member := range raw {
				var alone any
				if err := Unmarshal(member, &alone); err != nil {
					t.Fatalf("%d: %s: member %q of %s is not one JSON value: %q: %v", i, data, key, name, member, err)
				}
				if !reflect.DeepEqual(alone, whole[key]) {
					t.Fatalf("%d: %s: member %q of %s is %q, decodes to %#v, not %#v", i, data, key, name, member, alone, whole[key])
				}
			}
			var tail struct{ Last int }
			if err := decode(data, &tail); err != nil || tail.Last != 7 {
				t.Fatalf("%d: %s: the member after every crossed value with %s: Last = %d, err %v", i, data, name, tail.Last, err)
			}
		}
	}
}

// A frame decode hands a RawMessage the frame's own bytes, with no spare capacity, so an append by the receiver copies and never writes into
// the frame; Unmarshal copies, as the standard library does.
func TestUnmarshalFrameAliasesRawMessages(t *testing.T) {
	frame := []byte(`{"type":"request","request":{"method":"event","args":{"a":[1,2,{"b":"c"}]}},"after":"x"}`)
	type request struct {
		Method string
		Args   RawMessage `json:"args"`
	}
	type envelope struct {
		Type    string
		Request request
		After   string
	}
	var env envelope
	if err := UnmarshalFrame(frame, &env); err != nil {
		t.Fatal(err)
	}
	const want = `{"a":[1,2,{"b":"c"}]}`
	if string(env.Request.Args) != want || env.After != "x" {
		t.Fatalf("decoded %q after %q", env.Request.Args, env.After)
	}
	at := bytes.Index(frame, []byte(want))
	if &env.Request.Args[0] != &frame[at] {
		t.Error("Args is a copy of the frame's bytes")
	}
	if len(env.Request.Args) != cap(env.Request.Args) {
		t.Errorf("Args has spare capacity: len %d cap %d", len(env.Request.Args), cap(env.Request.Args))
	}
	before := string(frame)
	_ = append(env.Request.Args, "overwritten"...)
	if string(frame) != before {
		t.Error("an append to Args wrote into the frame")
	}

	var copied envelope
	if err := Unmarshal(frame, &copied); err != nil {
		t.Fatal(err)
	}
	if &copied.Request.Args[0] == &frame[at] {
		t.Error("Unmarshal aliased the input")
	}

	// A null member and a string member are aliased too, and read as before.
	var nulls struct{ A, B RawMessage }
	if err := UnmarshalFrame([]byte(`{"A":null,"B":"s"}`), &nulls); err != nil || string(nulls.A) != "null" || string(nulls.B) != `"s"` {
		t.Fatalf("A %q B %q err %v", nulls.A, nulls.B, err)
	}
}

// A truncated, corrupted or ill-formed frame gets from UnmarshalFrame the error the scanner gives it (message and offset, as a
// *SyntaxError), a frame nested past the frame limit the "exceeded max depth" error the reader recognises, and a frame with ill-formed
// UTF-8 inside a string decodes, the bad bytes read as U+FFFD, as before.
func TestUnmarshalFrameErrorsAreTheScannersErrors(t *testing.T) {
	frame := `{"type":"request","id":"r1","request":{"method":"event","args":{"messages":[{"role":"user","text":"a\nb\u00e9 é"}],"n":-1.5e3,"ok":true,"x":null}}}`
	type envelope struct {
		Type    string
		ID      string
		Request *struct {
			Method string
			Args   RawMessage
		}
	}
	for n := range len(frame) {
		checkFrameError(t, []byte(frame[:n]))
	}
	for i := range len(frame) {
		for _, b := range []byte{0, 0x1f, '"', '\\', '{', '}', '[', ']', ',', ':', 'x'} {
			checkFrameError(t, []byte(frame[:i]+string([]byte{b})+frame[i+1:]))
		}
	}
	deep := strings.Repeat("[", frameNestingDepth+1) + strings.Repeat("]", frameNestingDepth+1)
	var env envelope
	err := UnmarshalFrame([]byte(`{"type":"request","request":{"args":`+deep+`}}`), &env)
	var syntax *SyntaxError
	if !errors.As(err, &syntax) || !strings.Contains(err.Error(), "exceeded max depth") {
		t.Fatalf("a frame past the depth limit: %v", err)
	}

	for _, bad := range []string{"\"a\xffb\"", "\"\xc3(\"", "\"\xed\xa0\x80\"", "\"\xf4\x90\x80\x80\""} {
		var got struct{ Type string }
		if err := UnmarshalFrame([]byte(`{"type":`+bad+`}`), &got); err != nil {
			t.Fatalf("ill-formed UTF-8 %q: %v", bad, err)
		}
		want, ok := unquoteBytesReference([]byte(bad))
		if !ok || got.Type != string(want) {
			t.Fatalf("ill-formed UTF-8 %q decoded to %q, want %q", bad, got.Type, want)
		}
	}
}

func checkFrameError(t *testing.T, frame []byte) {
	t.Helper()
	var scan scanner
	scan.maxDepth = frameNestingDepth
	want := checkValid(frame, &scan)
	var env struct{ Type string }
	got := UnmarshalFrame(frame, &env)
	if want == nil {
		var syntax *SyntaxError
		if errors.As(got, &syntax) {
			t.Fatalf("%q: UnmarshalFrame refused a document the scanner accepts: %v", frame, got)
		}
		return
	}
	var wantSyntax, gotSyntax *SyntaxError
	if !errors.As(want, &wantSyntax) || !errors.As(got, &gotSyntax) || wantSyntax.Error() != gotSyntax.Error() || wantSyntax.Offset != gotSyntax.Offset {
		t.Fatalf("%q: scanner error %v, UnmarshalFrame error %v", frame, want, got)
	}
}

func FuzzDecodeCrossesValues(f *testing.F) {
	f.Add([]byte(`{"a":[1,{"b":"c\"]}"}],"last":7}`))
	f.Add([]byte(`{"a":{},"b":[],"c":"[","last":7}`))
	f.Add([]byte(`{"a":"\\","b":"}","last":7}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if !Valid(data) {
			return
		}
		var whole map[string]any
		if Unmarshal(data, &whole) != nil {
			return
		}
		var raw map[string]RawMessage
		if err := UnmarshalFrame(data, &raw); err != nil {
			t.Fatalf("%q: %v", data, err)
		}
		for key, member := range raw {
			var alone any
			if err := Unmarshal(member, &alone); err != nil || !reflect.DeepEqual(alone, whole[key]) {
				t.Fatalf("%q: member %q is %q: %v", data, key, member, err)
			}
		}
		var tail struct{ Last int }
		if err := UnmarshalTrusted(data, &tail); err != nil && !strings.Contains(err.Error(), "cannot unmarshal") {
			t.Fatalf("%q: %v", data, err)
		}
	})
}

// Numbers of an interface value decode to the float64 strconv reads, whether they come from the table of small integers or from a parse in
// place, and a number outside the float64 range is JavaScript's Infinity.
func TestInterfaceNumbers(t *testing.T) {
	for _, literal := range []string{"0", "1", "9", "10", "99", "100", "255", "256", "999", "1000", "-0", "-1", "-255", "0.5", "1.5", "1e2", "1E2", "1e-2", "100e0", "2.5e+3", "123456789", "9007199254740993", "0.1", "-1.25e-7", "1e308", "1e309", "-1e309", "5e-324", "1e-400"} {
		want, err := strconv.ParseFloat(literal, 64)
		if err != nil && !isInfinityOfRange(want, err) && !errors.Is(err, strconv.ErrRange) {
			t.Fatal(literal, err)
		}
		for _, document := range []string{literal, "[" + literal + "]", `{"n":` + literal + `}`} {
			var got any
			if err := Unmarshal([]byte(document), &got); err != nil {
				t.Fatalf("%s: %v", document, err)
			}
			var number any
			switch v := got.(type) {
			case []any:
				number = v[0]
			case map[string]any:
				number = v["n"]
			default:
				number = v
			}
			f, ok := number.(float64)
			if !ok || math.Float64bits(f) != math.Float64bits(want) {
				t.Fatalf("%s decoded to %#v, want float64 %v", document, number, want)
			}
		}
	}
}

// Short strings of an interface value are shared through the decode state's table; every value decodes to its own text whatever the table
// holds from earlier decodes, in map keys and in values, for strings with escapes, non-ASCII text, 32 bytes and 33, from concurrent decodes.
func TestInterfaceStringsAreTheirOwnText(t *testing.T) {
	texts := []string{"", "a", "role", "assistant", "tool_call_id", strings.Repeat("k", 32), strings.Repeat("k", 33), "tab\there", "quote\"here", "back\\slash", "é", "日本語", "a\u00e9b", "a b", "~", "\x7f", "ill\xffformed"}
	decode := func() {
		for round := range 3 {
			for _, text := range texts {
				quoted, err := MarshalStringify(text)
				if err != nil {
					t.Error(err)
					return
				}
				document := `{` + string(quoted) + `:` + string(quoted) + `,"list":[` + string(quoted) + `,` + string(quoted) + `],"round":` + strconv.Itoa(round) + `}`
				var got map[string]any
				if err := Unmarshal([]byte(document), &got); err != nil {
					t.Error(err)
					return
				}
				want, _ := unquoteBytesReference(quoted)
				list, _ := got["list"].([]any)
				if got[string(want)] != string(want) || len(list) != 2 || list[0] != string(want) || list[1] != string(want) || got["round"] != float64(round) {
					t.Errorf("%q decoded to %#v", text, got)
					return
				}
			}
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decode()
		}()
	}
	wg.Wait()
}
