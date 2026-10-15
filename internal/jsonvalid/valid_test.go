package jsonvalid

import (
	"math/rand"
	"strings"
	"testing"

	stdjson "encoding/json"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Every verdict equals the scanner's: the session loader skipped exactly the lines sdkjson.Valid rejected.
func agree(t *testing.T, text string) {
	t.Helper()
	if got, want := Valid([]byte(text)), sdkjson.Valid([]byte(text)); got != want {
		t.Fatalf("Valid(%q) = %v, scanner %v", text, got, want)
	}
}

func TestValidAgreesWithTheScannerOnEveryShortText(t *testing.T) {
	alphabet := []string{"{", "}", "[", "]", ",", ":", `"`, `\`, "a", "0", "1", "-", "+", ".", "e", "E", " ", "\t", "\n", "\x00", "t", "u", "n", "l", "\x7f", "\xff"}
	var walk func(prefix string, depth int)
	walk = func(prefix string, depth int) {
		agree(t, prefix)
		if depth == 0 {
			return
		}
		for _, piece := range alphabet {
			walk(prefix+piece, depth-1)
		}
	}
	walk("", 5)
}

func TestValidAgreesWithTheScannerOnRandomTokenTexts(t *testing.T) {
	tokens := []string{"{", "}", "[", "]", ",", ":", `"a"`, `"`, `\"`, `\\`, `\u00e9`, `\ud800`, `\uZZZZ`, `\u12`, `\x`, "\\/", "0", "00", "-0", "-", "1", "12", "1.5", "1.", ".5", "1e5", "1E+5", "1e", "1e-", "true", "false", "null", "tru", "nul", " ", "\t", "\r", "\n", "\f", "\v", "\u00a0", "\ufeff", "\xff", "é", "😀", "\x01", "\x1f", "\x7f", "NaN", "Infinity", "undefined", "//", "/*"}
	rng := rand.New(rand.NewSource(7))
	for range 400000 {
		var b strings.Builder
		for range rng.Intn(14) {
			b.WriteString(tokens[rng.Intn(len(tokens))])
		}
		agree(t, b.String())
	}
}

// A valid text with one byte changed, inserted or deleted is the cheapest way to land on the edge of every rule.
func TestValidAgreesWithTheScannerOnMutatedValidTexts(t *testing.T) {
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
					agree(t, seed[:pos]+string([]byte{b})+seed[pos+1:])
					agree(t, seed[:pos]+seed[pos+1:])
				}
				agree(t, seed[:pos]+string([]byte{b})+seed[pos:])
			}
			agree(t, seed[:pos])
		}
		for range 20000 {
			raw := []byte(seed)
			for range 1 + rng.Intn(4) {
				raw[rng.Intn(len(raw))] = byte(rng.Intn(256))
			}
			agree(t, string(raw))
		}
	}
}

func TestValidAgreesWithTheScannerAtTheNestingLimit(t *testing.T) {
	for _, depth := range []int{1, 63, 64, 65, 511, 512, 513, 9999, 10000, 10001, 10002, 20000} {
		agree(t, strings.Repeat("[", depth)+strings.Repeat("]", depth))
		agree(t, strings.Repeat("[", depth)+strings.Repeat("]", depth-1))
		agree(t, strings.Repeat(`{"a":`, depth)+"1"+strings.Repeat("}", depth))
		agree(t, strings.Repeat(`[{"a":`, depth/2)+"1"+strings.Repeat("}]", depth/2))
		agree(t, strings.Repeat(`{"a":[`, depth/2)+"1"+strings.Repeat("]}", depth/2))
	}
}

// The standard library agrees with the scanner too, so the verdict is the textbook one.
func TestValidAgreesWithEncodingJSON(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	tokens := []string{"{", "}", "[", "]", ",", ":", `"a"`, "1", "-1.5e3", "true", "null", " ", `"\u00e9"`, `\`, `"`}
	for range 100000 {
		var b strings.Builder
		for range rng.Intn(12) {
			b.WriteString(tokens[rng.Intn(len(tokens))])
		}
		if got, want := Valid([]byte(b.String())), stdjson.Valid([]byte(b.String())); got != want {
			t.Fatalf("Valid(%q) = %v, encoding/json %v", b.String(), got, want)
		}
	}
}
