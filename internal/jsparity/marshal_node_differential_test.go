package jsparity_test

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// stringPieces is the adversarial alphabet the fuzz strings are built from: every byte class the escapers branch on (C0 controls, the quote,
// the backslash, HTML characters, U+007F, multi-byte characters, U+2028/U+2029, a literal U+FFFD and WTF-8 lone and split surrogates), and
// ASCII runs long enough to cross the 8-byte scan blocks.
var stringPieces = []string{
	"a", "Z", " ", "<", ">", "&", "'", "/", "\x7f", "\"", "\\", "\b", "\f", "\n", "\r", "\t", "\x00", "\x01", "\x1f",
	"é", "€", "😀", "\u2028", "\u2029", "\ufffd", "\\u003c", "\\u2028", "-0",
	"\xed\xa0\x80", "\xed\xb0\x80", "\xed\xa0\xbd", "\xed\xb8\x80", "\xed\xaf\xbf\xed\xbf\xbf",
	"plain ascii text run ", "0123456", "01234567", "012345678",
}

func randomPieces(rng *rand.Rand) string {
	var b strings.Builder
	for range rng.IntN(48) {
		b.WriteString(stringPieces[rng.IntN(len(stringPieces))])
	}
	return b.String()
}

// The encoders PiG writes JSON.stringify text with agree with Node on every string a JavaScript string can be: jsstring.MarshalJSON (the
// session-line encoder), jsstring.AppendString and jsparity.AppendString all print what JSON.stringify prints for the same UTF-16 units,
// including C0 controls, lone and split surrogates, U+2028/U+2029 and HTML characters.
func TestMarshalJSONMatchesNode(t *testing.T) {
	stringify := jsparity.NodeStringify(t)
	check := func(units []uint16, s string) {
		t.Helper()
		want := stringify(map[string][]uint16{"units": units})
		marshaled, err := jsstring.MarshalJSON(s)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(marshaled); got != want {
			t.Fatalf("MarshalJSON(%q)\n got: %s\nwant: %s", s, got, want)
		}
		if got := string(jsparity.AppendString(nil, s)); got != want {
			t.Fatalf("AppendString(%q)\n got: %s\nwant: %s", s, got, want)
		}
		if got := string(jsstring.AppendString([]byte("prefix"), s)); got != "prefix"+want {
			t.Fatalf("jsstring.AppendString(%q)\n got: %s\nwant: prefix%s", s, got, want)
		}
	}
	rng := rand.New(rand.NewPCG(5, 6))
	for range 3000 {
		units := jsparity.RandomUnits(rng)
		var cuts []int
		for i := 1; i < len(units); i++ {
			if rng.IntN(3) == 0 {
				cuts = append(cuts, i)
			}
		}
		check(units, jsparity.WTF8(units, cuts...))
	}
	for range 3000 {
		s := randomPieces(rng)
		check(jv.UTF16Units(s), s)
	}
	long := strings.Repeat("export const f = (a, b) => a + b;\n", 6000) + "\u2028\xed\xa0\x80<&>"
	check(jv.UTF16Units(long), long)
}

// LW-JSON-1: bytes that are not WTF-8 hold one U+FFFD per WHATWG maximal subpart once Node decodes them, and JSON.stringify writes that
// character raw. MarshalJSON (the session-line encoder), jsparity.AppendString and the session line of an assistant message whose text and
// tool-call arguments hold those bytes all write what Node writes: an ai.AssistantMessage, whose types encode through the codec's HTML mode
// and whose \ufffd escapes the JSON.stringify compaction writes raw, and a session message entry, whose members append themselves.
func TestMarshalJSONIllFormedUTF8MatchesNode(t *testing.T) {
	stringify := jsparity.NodeStringify(t)
	rng := rand.New(rand.NewPCG(7, 8))
	pool := []byte{'a', '<', '"', 0x80, 0x8F, 0x90, 0x9F, 0xA0, 0xBF, 0xC0, 0xC2, 0xDF, 0xE0, 0xE1, 0xE2, 0x82, 0xED, 0xEF, 0xF0, 0xF1, 0xF4, 0xF5, 0xFF}
	checked := 0
	for range 3000 {
		raw := make([]byte, 1+rng.IntN(10))
		for i := range raw {
			raw[i] = pool[rng.IntN(len(pool))]
		}
		if jsparity.WTF8Surrogate(raw) || utf8.Valid(raw) {
			continue
		}
		checked++
		ints := make([]int, len(raw))
		for i, b := range raw {
			ints[i] = int(b)
		}
		node := stringify(map[string][]int{"bytes": ints})
		if got := string(jsparity.AppendString(nil, string(raw))); got != node {
			t.Fatalf("AppendString(%q)\n got: %s\nwant (Node): %s", raw, got, node)
		}
		marshaled, err := jsstring.MarshalJSON(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		if string(marshaled) != node {
			t.Fatalf("MarshalJSON(%q)\n got: %s\nwant (Node): %s", raw, marshaled, node)
		}
		content := []ai.AssistantContentBlock{ai.TextContent{Text: "x" + string(raw)}, ai.ToolCall{ID: "c", Name: "write", Arguments: ai.JsonObject{"content": string(raw)}}}
		message := ai.AssistantMessage{API: ai.APIOpenAICompletions, Provider: "p", Model: "m", StopReason: ai.StopReasonToolUse, Timestamp: 1, Content: content}
		line, err := jsstring.MarshalJSON(message)
		if err != nil {
			t.Fatal(err)
		}
		// The session's message entry appends its message's members into the line (json.StringifyAppender) rather than compacting them.
		entry := sessionentry.MessageEntry{SessionEntryBase: sessionentry.SessionEntryBase{Type: "message", ID: "e", Timestamp: "t"},
			Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: ai.APIOpenAICompletions, Provider: "p",
				ModelID: "m", StopReason: ai.StopReasonToolUse, Timestamp: 1, Content: content}}}
		entryLine, err := jsstring.AppendMarshalJSON(nil, entry)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range [][]byte{line, entryLine} {
			if !strings.Contains(string(line), `"text":"x`+node[1:]) || !strings.Contains(string(line), `"content":`+node) || strings.Contains(string(line), `\ufffd`) {
				t.Fatalf("session line of an assistant message holding %q:\n%s\nwant its text and argument as Node writes them: %s", raw, line, node)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no ill-formed input")
	}
}
