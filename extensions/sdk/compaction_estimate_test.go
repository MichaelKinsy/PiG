package sdk

import (
	"testing"

	jsjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// JavaScript's Number::toString: the cases ECMA-262 7.1.12.1 and V8 print.
func TestJSNumberStringMatchesNumberToString(t *testing.T) {
	for value, want := range map[float64]string{
		0: "0", 1: "1", -1: "-1", 1.5: "1.5", 0.1: "0.1", 100: "100", 123456789012345680000: "123456789012345680000", 1e21: "1e+21", 1.5e21: "1.5e+21",
		1e-6: "0.000001", 1e-7: "1e-7", 1.5e-7: "1.5e-7", 123e-20: "1.23e-18", 0.000001234: "0.000001234", 5e-324: "5e-324", 1.7976931348623157e308: "1.7976931348623157e+308",
		-0.5: "-0.5", 4294967296.5: "4294967296.5", 1e20: "100000000000000000000", 12345.678: "12345.678",
	} {
		if got := jsNumberString(value); got != want {
			t.Errorf("jsNumberString(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestEstimateTokensCountsUTF16UnitsAndJSONStringifyLength(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message map[string]any
		want    int
	}{
		{"user string", map[string]any{"role": "user", "content": "abcde"}, 2},
		{"user astral", map[string]any{"role": "user", "content": "😀😀"}, 1},
		{"user image and text", map[string]any{"role": "user", "content": []any{map[string]any{"type": "image"}, map[string]any{"type": "text", "text": "abcd"}}}, 1201},
		{"assistant tool call", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "name": "read", "arguments": map[string]any{"path": "a\"b"}}}}, 5},
		{"unknown role", map[string]any{"role": "x", "content": "abc"}, 0},
	} {
		if got := EstimateTokens(tc.message); got != tc.want {
			t.Errorf("%s: EstimateTokens = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// piEstimateCases are strings with the numbers Pi 1.5.0's own estimateTokens (core/compaction/compaction.ts, run by node) gives them: units is the string's
// JavaScript length (the user message of the text four times is that many tokens), tokens the user message of the text once, and callTokens an assistant
// tool call named "" with the arguments {"k": text}, which counts JSON.stringify's length (an unpaired surrogate is its six-character escape). The strings are
// UTF-16 units, so an unpaired surrogate is spelled the way the SDK holds it (WTF-8, extensions/sdk/json).
var piEstimateCases = []struct {
	name       string
	units      []uint16
	length     int
	tokens     int
	callTokens int
}{
	{"empty", []uint16{}, 0, 0, 2},
	{"ascii", []uint16{0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68}, 8, 2, 4},
	{"lone high surrogate", []uint16{0xd800}, 1, 1, 4},
	{"lone low surrogate", []uint16{0xdc00}, 1, 1, 4},
	{"eight lone high surrogates", []uint16{0xd800, 0xd800, 0xd800, 0xd800, 0xd800, 0xd800, 0xd800, 0xd800}, 8, 2, 14},
	{"low then high", []uint16{0xdc00, 0xd800}, 2, 1, 5},
	{"lone high between ASCII", []uint16{0x61, 0xd83d, 0x62}, 3, 1, 4},
	{"astral", []uint16{0xd83d, 0xde00}, 2, 1, 3},
	{"eight astral", []uint16{0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00}, 16, 4, 6},
	{"high half of a pair", []uint16{0xd83d}, 1, 1, 4},
	{"low half of a pair", []uint16{0xde00}, 1, 1, 4},
	{"combining acute", []uint16{0x65, 0x301}, 2, 1, 3},
	{"combining run", []uint16{0x61, 0x300, 0x301, 0x302, 0x303}, 5, 2, 4},
	{"zwj sequence", []uint16{0xd83d, 0xdc68, 0x200d, 0xd83d, 0xdc69, 0x200d, 0xd83d, 0xdc67}, 8, 2, 4},
	{"regional indicators", []uint16{0xd83c, 0xddfa, 0xd83c, 0xddf8}, 4, 1, 3},
	{"hangul jamo", []uint16{0x1100, 0x1161, 0x11a8}, 3, 1, 3},
	{"bmp edges", []uint16{0xd7ff, 0xe000, 0xffff}, 3, 1, 3},
	{"noncharacters", []uint16{0xfdd0, 0xfffe}, 2, 1, 3},
	{"bom", []uint16{0xfeff}, 1, 1, 3},
	{"line separators", []uint16{0x2028, 0x2029}, 2, 1, 3},
	{"nul", []uint16{0x0}, 1, 1, 4},
	{"ascii then lone high", []uint16{0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0xd800}, 9, 3, 6},
	{"two highs then a pair", []uint16{0xd83d, 0xd83d, 0xd83d, 0xde00}, 4, 1, 6},
	{"lone surrogates beside multibyte", []uint16{0xe9, 0xd800, 0x65e5, 0xdfff, 0xd83d, 0xde00}, 6, 2, 6},
	{"control characters", []uint16{0x1, 0x1f, 0x7f}, 3, 1, 6},
}

// An unpaired surrogate is one UTF-16 unit, as it is in the JavaScript string it came from; the SDK holds it as the three WTF-8 bytes of its code point, which
// utf8 decoding reads as three invalid bytes (the old count: eight lone surrogates sized 6 tokens where Pi gives 2). Astral characters are two units and a
// combining mark one, whatever the text around them.
func TestEstimateTokensMatchesPiOnLoneSurrogatesAstralAndCombiningMarks(t *testing.T) {
	for _, tc := range piEstimateCases {
		text := jsUnitsString(tc.units)
		if got := utf16Length(text); got != len(tc.units) {
			t.Errorf("%s: utf16Length = %d, want %d units", tc.name, got, len(tc.units))
		}
		repeated := ""
		for range 4 {
			repeated += text
		}
		if got := EstimateTokens(map[string]any{"role": "user", "content": repeated}); got != tc.length {
			t.Errorf("%s: four copies sized %d tokens, Pi gives %d", tc.name, got, tc.length)
		}
		if got := EstimateTokens(map[string]any{"role": "user", "content": text}); got != tc.tokens {
			t.Errorf("%s: user message sized %d tokens, Pi gives %d", tc.name, got, tc.tokens)
		}
		call := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c", "name": "", "arguments": map[string]any{"k": text}}}}
		if got := EstimateTokens(call); got != tc.callTokens {
			t.Errorf("%s: tool call sized %d tokens, Pi gives %d", tc.name, got, tc.callTokens)
		}
	}
}

// The path an extension takes: a message read from the wire text with the SDK's decoder, whose unpaired surrogate escapes become WTF-8.
func TestEstimateTokensOfADecodedMessageWithLoneSurrogates(t *testing.T) {
	var message map[string]any
	if err := jsjson.Unmarshal([]byte(`{"role":"user","content":"\ud800\ud800\ud800\ud800\ud800\ud800\ud800\ud800"}`), &message); err != nil {
		t.Fatal(err)
	}
	if got := EstimateTokens(message); got != 2 {
		t.Errorf("eight lone surrogates sized %d tokens, Pi gives 2", got)
	}
}
