package json

import (
	stdjson "encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// validFast never accepts text the scanner rejects, and (so that Unmarshal's fast pass pays off) accepts what it accepts.
func agreeFastValid(t *testing.T, text string) {
	t.Helper()
	scan := newScanner()
	defer freeScanner(scan)
	want := checkValid([]byte(text), scan) == nil
	if got := validFast([]byte(text)); got != want {
		t.Fatalf("validFast(%q) = %v, scanner %v", text, got, want)
	}
}

func TestValidFastAgreesWithTheScannerOnEveryShortText(t *testing.T) {
	alphabet := []string{"{", "}", "[", "]", ",", ":", `"`, `\`, "a", "0", "1", "-", "+", ".", "e", "E", " ", "\t", "\n", "\x00", "t", "u", "n", "l", "\x7f", "\xff"}
	var walk func(prefix string, depth int)
	walk = func(prefix string, depth int) {
		agreeFastValid(t, prefix)
		if depth == 0 {
			return
		}
		for _, piece := range alphabet {
			walk(prefix+piece, depth-1)
		}
	}
	walk("", 5)
}

func TestValidFastAgreesWithTheScannerOnRandomTokenTexts(t *testing.T) {
	tokens := []string{"{", "}", "[", "]", ",", ":", `"a"`, `"`, `\"`, `\\`, `\u00e9`, `\ud800`, `\uZZZZ`, `\u12`, `\x`, "\\/", "0", "00", "-0", "-", "1", "12", "1.5", "1.", ".5", "1e5", "1E+5", "1e", "1e-", "true", "false", "null", "tru", "nul", " ", "\t", "\r", "\n", "\f", "\v", "\u00a0", "\ufeff", "\xff", "é", "😀", "\x01", "\x1f", "\x7f", "NaN", "Infinity", "undefined", "//", "/*"}
	rng := rand.New(rand.NewSource(7))
	for range 300000 {
		var b strings.Builder
		for range rng.Intn(14) {
			b.WriteString(tokens[rng.Intn(len(tokens))])
		}
		agreeFastValid(t, b.String())
	}
}

func TestValidFastAgreesWithTheScannerOnMutatedValidTexts(t *testing.T) {
	seeds := []string{
		`{"type":"message","id":"a1b2","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi \"there\" \u00e9\n"},{"type":"toolCall","id":"c","name":"f","arguments":{"a":[1,2.5e-3,-0,true,false,null]}}],"usage":{"input":1,"output":2}}}`,
		`[ 1 , [ ] , { } , "x" , -1.5E+10 , {"k" : [ {"k":{}} ]} ]`,
		`"a string with \\ and \/ and \b\f\n\r\t and \uD83D\uDE00"`,
		`-0.0e0`, `{"":""}`, `[[[[[[[[[[1]]]]]]]]]]`,
	}
	rng := rand.New(rand.NewSource(11))
	for _, seed := range seeds {
		for pos := 0; pos <= len(seed); pos++ {
			for _, b := range []byte{0, '"', '\\', '{', '}', '[', ']', ',', ':', ' ', '0', '-', 'e', '.', 'u', 'x', 0x1f, 0x7f, 0x80, 0xff} {
				if pos < len(seed) {
					agreeFastValid(t, seed[:pos]+string([]byte{b})+seed[pos+1:])
					agreeFastValid(t, seed[:pos]+seed[pos+1:])
				}
				agreeFastValid(t, seed[:pos]+string([]byte{b})+seed[pos:])
			}
			agreeFastValid(t, seed[:pos])
		}
		for range 10000 {
			raw := []byte(seed)
			for range 1 + rng.Intn(4) {
				raw[rng.Intn(len(raw))] = byte(rng.Intn(256))
			}
			agreeFastValid(t, string(raw))
		}
	}
}

func TestValidFastAgreesWithTheScannerAtTheNestingLimit(t *testing.T) {
	for _, depth := range []int{1, 63, 64, 65, 511, 512, 513, 9999, 10000, 10001, 10002, 20000} {
		agreeFastValid(t, strings.Repeat("[", depth)+strings.Repeat("]", depth))
		agreeFastValid(t, strings.Repeat("[", depth)+strings.Repeat("]", depth-1))
		agreeFastValid(t, strings.Repeat(`{"a":`, depth)+"1"+strings.Repeat("}", depth))
		agreeFastValid(t, strings.Repeat(`[{"a":`, depth/2)+"1"+strings.Repeat("}]", depth/2))
	}
}

// Unmarshal reports the scanner's error for text the fast pass rejects.
func TestUnmarshalReportsTheScannersErrorForInvalidText(t *testing.T) {
	for _, text := range []string{``, `{`, `{"a":}`, `[1,]`, `"a` + "\n" + `"`, `{"a":1}x`, strings.Repeat("[", 10001) + strings.Repeat("]", 10001)} {
		var v any
		got := Unmarshal([]byte(text), &v)
		scan := newScanner()
		want := checkValid([]byte(text), scan)
		freeScanner(scan)
		if got == nil || want == nil || got.Error() != want.Error() {
			t.Errorf("Unmarshal(%q) = %v, scanner %v", text, got, want)
		}
	}
}

// slowSkip is skip as it was written before skipContainer: every byte through the scanner.
func slowSkip(d *decodeState) {
	s, data, i := &d.scan, d.data, d.off
	depth := len(s.parseState)
	for {
		op := s.step(s, data[i])
		i++
		if len(s.parseState) < depth {
			d.off = i
			d.opcode = op
			return
		}
	}
}

// skip leaves the decoder, and the scanner's state after it, exactly as the per-byte steps do, whatever the container and whatever follows.
func TestSkipMatchesThePerByteScan(t *testing.T) {
	values := []string{
		`{}`, `[]`, `{"a":1}`, `[1,2,3]`, `{"a":{"b":[1,{"c":"x]}\"[{"}]}}`, `[{"a":"\\"},"\\\\\"]",[]]`, `{"k":"\u005d\u007d","e":"]\\"}`, `[[],[[]],{"a":[]}]`,
		`{ "a" : [ 1 , 2 ] , "b" : { } }`, "[\n\t{\"a\": \"b\"}\n]", `[` + strings.Repeat(`{"a":[`, 200) + strings.Repeat(`]}`, 200) + `]`,
		`[` + strings.Repeat(`"x[{ ]} \" y",`, 50) + `"z"]`,
	}
	ran := 0
	suffixes := []string{``, `,1`, ` ,"b":2}`, `]`, `}`, ` `, `,{"a":[1]}]`}
	for _, value := range values {
		for _, tail := range suffixes {
			for _, wrap := range []string{"%s", "[%s", `{"k":%s`, `[1,%s`, `{"k":1,"j":%s`} {
				prefix, _, _ := strings.Cut(wrap, "%s")
				text := prefix + value + tail
				run := func(skip func(*decodeState)) (*decodeState, bool) {
					d := &decodeState{}
					d.init([]byte(text))
					d.scan.reset()
					// Step to just after the value's opening bracket.
					for d.off < len(d.data) && d.off < len(prefix)+1 {
						d.scanNext()
					}
					if d.off != len(prefix)+1 || len(d.scan.parseState) == 0 {
						return d, false
					}
					skip(d)
					return d, true
				}
				slow, ok1 := run(slowSkip)
				fast, ok2 := run((*decodeState).skip)
				if ok1 != ok2 {
					t.Fatalf("%q: setup differs", text)
				}
				if !ok1 {
					continue
				}
				ran++
				if slow.off != fast.off || slow.opcode != fast.opcode || !reflect.DeepEqual(slow.scan.parseState, fast.scan.parseState) || slow.scan.endTop != fast.scan.endTop {
					t.Fatalf("%q: skip left off %d opcode %d state %v endTop %v, per-byte scan off %d opcode %d state %v endTop %v", text, fast.off, fast.opcode, fast.scan.parseState, fast.scan.endTop, slow.off, slow.opcode, slow.scan.parseState, slow.scan.endTop)
				}
				// The same next bytes give the same answers.
				for slow.off < len(slow.data) {
					a, b := slow.scan.step(&slow.scan, slow.data[slow.off]), fast.scan.step(&fast.scan, fast.data[fast.off])
					if a != b {
						t.Fatalf("%q: next step differs at %d: %d vs %d", text, slow.off, a, b)
					}
					slow.off++
					fast.off++
				}
			}
		}
	}
	if ran < 200 {
		t.Fatalf("only %d cases ran", ran)
	}
}

// A document read past unknown members and into raw members decodes as encoding/json decodes it: skip finds each value's end the way the scanner does.
func TestUnmarshalSkipsUnknownMembersAsEncodingJSONDoes(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	pieces := []string{`"x"`, `"]}\"["`, `"a b c d e f g h i j k l m n o p"`, `"\\"`, `"\u005d"`, `1`, `-2.5e3`, `true`, `null`, `[]`, `{}`}
	var value func(depth int) string
	value = func(depth int) string {
		switch k := rng.Intn(6); {
		case k < 3 || depth > 4:
			return pieces[rng.Intn(len(pieces))]
		case k < 5:
			var members []string
			for range rng.Intn(5) {
				members = append(members, `"`+[]string{"unknown", "raw", "known", "k]", `q\"`}[rng.Intn(5)]+`":`+value(depth+1))
			}
			return "{" + strings.Join(members, ",") + "}"
		default:
			var items []string
			for range rng.Intn(5) {
				items = append(items, value(depth+1))
			}
			return "[" + strings.Join(items, " , ") + "]"
		}
	}
	type target struct {
		Known any              `json:"known"`
		Raw   RawMessage       `json:"raw"`
		Ptr   *struct{ A int } `json:"ptr"`
	}
	for range 5000 {
		text := `{"unknown":` + value(0) + `,"raw":` + value(0) + `,"ptr":` + value(0) + `,"known":` + value(0) + `, "tail": ` + value(0) + `}`
		var got, want target
		gotErr := Unmarshal([]byte(text), &got)
		wantErr := stdjson.Unmarshal([]byte(text), &want)
		if (gotErr == nil) != (wantErr == nil) || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %+v (%v), encoding/json %+v (%v)", text, got, gotErr, want, wantErr)
		}
	}
}
