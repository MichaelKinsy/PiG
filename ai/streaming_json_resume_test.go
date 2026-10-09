package ai

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// resumeCorpus holds argument texts that exercise every branch of the partial parser: escapes, surrogates, invalid UTF-8, control bytes, repeated and unsorted keys, malformed members, and texts that are not objects.
var resumeCorpus = []string{
	``, `   `, `{`, `{}`, ` {"a":1}`, `[1,2`, `"str`, `null`, `nul`, `{"a":`, `{"a":1`, `{"a":1,`, `{"a":1,"b"`, `{"a":1,"b":`,
	`{"path":"src/a.go","content":"line 1\nline 2\n\ttab \"quoted\" \\ back \/ slash","mode":"w"}`,
	`{"content":"emoji \ud83d\ude00 pair, lone \ud83d end, lone low \ude00, mixed \ud83d\n x, \u00e9\u4e2d"}`,
	"{\"content\":\"é中文😀 raw unicode \xff\xfe invalid bytes \xe2\x82 truncated\"}",
	`{"a":"raw` + "\n" + `newline","b":2}`, "{\"a\":\"tab\there\",\"b\":\"ctl\x01x\"}",
	`{"a":"bad \x escape","b":3}`, `{"a":"bad \u12G4 hex","b":3}`, `{"a":"short \u12`, `{"a":"ends in backslash \`, `{"a":"ends in \\`, `{"a":"ends high \ud83d`, `{"a":"ends high \ud83d\u`, `{"a":"ends high \ud83d\ud`,
	`{"b":1,"a":2,"10":3,"2":4,"z":{"y":1,"x":[{"q":1,"p":2}]}}`, `{"a":1,"a":2,"b":{"c":1,"d":2},"b":{"d":1,"c":2}}`, `{"a":{"x":1},"a":3,"a":{"y":[1,2]}}`,
	`{"n":[1,2.5,-3e2,1E+5,-,1e,0x1,tru,true,false,null,nul],"o":{"k":[{"deep":["x","y"]}]}}`,
	`{"a":1 2,"b":3}`, `{"a" 1,"b":2}`, `{a:1,"b":2}`, `{"a":,"b":2}`, `{"a":1,,"b":2}`, `{"a":1,}`, `{"a":1}}`, `{"a":1} trailing`, `{"a":[1,2}`, `{"a":{]`, `{"a":1]`, `{]`,
	`{"edits":[{"oldText":"old one\n","newText":"new one\n"},{"oldText":"old two","newText":"new tw`,
	`{"a":"` + strings.Repeat("abcdefghij", 50) + `\n` + strings.Repeat("é", 40) + `\ud83d\ude00` + strings.Repeat("z", 30) + `","b":"tail"}`,
	// Inputs on which the partial-json 0.1.7 algorithm and the parser differ or may differ (batch-ctor's json-parse.ts oracle): an inner failure the outer object reads past, 'E' against 'e' exponents, non-finite numbers, and a __proto__ member.
	`{"a":1E`, `{"a":1E-5,`, `{"a":1e`, `{"a":1e-`, `{"a":NaN`, `{"a":Infinity,"b":1}`, `{"a":-Infinity`, `{"a":1e400,"b":`, `{"__proto__":1,"a":`, `{"__proto__":{"x":1},"a":"b`,
	`{"a":{"b":tru, "c":2},"d":3}`, `{"a":{"b" 1,"c":2},"d":"x`, `{"a":[1,{"b":`, `{"a":[1,x,2],"b":"y`, `{"a":'x',"b":"y`, `{a:1,"b":2`, `{"a":"x"}{"b":2}`, "{\"a\":1,\"b\":\"\u2028\ufeff\x7f",
	// A malformed number reads the whole text: lastIndexOf("e") moves when later text holds another 'e', so the member before the ',' is not final.
	`{"a":1e,"b":2}`, `{"a":1e,"b":2,"name":"x"}`, `{"a":[1,x,2],"b":"y","e":1,"s":"tr`, `{"a":1e5e,"b":{"c":2},"d":"nested e`, "{\"a\":1-e5,\"b\":\"x\"}",
	// parseStr takes the first byte for the opening quote; a key or value that does not start with one is not a string to resume.
	`{"a":1 "b":2, 3x "c":"d`, `{x"ab", "c":"def`, `{"a":1,b"cd":2,"e":"f`, `{"a":"x"} {"b":"y`, "{\"a\":\"\u00a0 \u2003",
	// A closed object whose strict parse succeeds only after repair, behind an escaped quote: the depth scan must follow escapes or it keeps the closed text on the partial path.
	`{"q":"say \"hi\"","b":"x\qy"}`, "{\"q\":\"\\\"\",\"b\":\"raw\x01\"}",
	// A repeated key after the last committed member whose value parses on one prefix and fails on the next: the member order that the repeat dropped must come back, so the committed order cannot share the parse's working order.
	`{"y":0,"x":{"b":1,"a":2},"z":1,"x":tx`, `{"y":0,"x":[{"b":1,"a":2}],"z":1,"x":nx}`,
	`{"k":"v"}` + strings.Repeat(" ", 3), "\n\t {\"k\" : \"v\" , \"k2\" : [ 1 , 2 ] }",
}

func sameParse(t *testing.T, label string, parser *streamingArgumentsParser, text string) {
	t.Helper()
	gotArgs, gotOrder := parser.parse(text)
	wantArgs, wantOrder := parseStreamingJsonArguments(text)
	if !reflect.DeepEqual(gotArgs, wantArgs) || !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("%s: text %q\nresumable = %#v order %#v\nwhole     = %#v order %#v", label, text, gotArgs, gotOrder, wantArgs, wantOrder)
	}
}

// The resumable parser returns what the whole-text parser returns for every prefix of every text, at every byte boundary and for several delta sizes.
func TestStreamingArgumentsParserMatchesWholeTextParser(t *testing.T) {
	for _, text := range resumeCorpus {
		for _, step := range []int{1, 2, 3, 5, 8, 13, 64} {
			var parser streamingArgumentsParser
			for end := 0; ; {
				end = min(len(text), end+step)
				sameParse(t, "step", &parser, text[:end])
				if end == len(text) {
					break
				}
			}
		}
	}
}

// Random token soup, fed in random chunks, never makes the two parsers differ.
func TestStreamingArgumentsParserMatchesWholeTextParserOnRandomText(t *testing.T) {
	tokens := []string{`{`, `}`, `[`, `]`, `,`, `:`, `"`, `"a"`, `"b"`, `"key"`, `\`, `\n`, `\"`, `\\`, `\u00e9`, `\ud83d`, `\ude00`, `\u12`, `\x`, ` `, "\n", `1`, `-2.5e3`, `tru`, `true`, `null`, `é`, `😀`, "\xff", "\xe2\x82", "\x01", `{"a":`, `"x":"y",`, `[1,`, `"é\n"`}
	random := rand.New(rand.NewPCG(1, 2))
	for range 4000 {
		var text strings.Builder
		if random.IntN(4) != 0 {
			text.WriteByte('{')
		}
		for range random.IntN(40) {
			text.WriteString(tokens[random.IntN(len(tokens))])
		}
		whole := text.String()
		var parser streamingArgumentsParser
		for end := 0; ; {
			end = min(len(whole), end+1+random.IntN(7))
			sameParse(t, "random", &parser, whole[:end])
			if end == len(whole) {
				break
			}
		}
	}
}

// A text that does not extend the previous one restarts the parser.
func TestStreamingArgumentsParserRestartsOnUnrelatedText(t *testing.T) {
	var parser streamingArgumentsParser
	sameParse(t, "first", &parser, `{"a":"one","b":"tw`)
	sameParse(t, "unrelated", &parser, `{"c":"three","d`)
	sameParse(t, "shorter", &parser, `{"c"`)
	sameParse(t, "closed", &parser, `{"c":1}`)
	sameParse(t, "after close", &parser, `{"c":1} `)
}

// A delta that commits a member and then reaches a repeated key must not leak the repeat's dropped member order into the committed state: the next delta makes the repeated value fail, and the whole-text parser then keeps the first value's order.
func TestStreamingArgumentsParserKeepsCommittedOrderApartFromLaterMembers(t *testing.T) {
	for _, text := range []string{`{"y":0,"x":{"b":1,"a":2},"z":1,"x":tx`, `{"y":0,"x":[{"b":1,"a":2}],"z":1,"x":nx`} {
		comma := strings.LastIndex(text, ",")
		value := strings.LastIndex(text, ":") + 1
		var parser streamingArgumentsParser
		for _, end := range []int{comma, value + 1, len(text)} {
			sameParse(t, "cut", &parser, text[:end])
		}
	}
}

// Committed members and the decoded prefix of an open string are retained, so the bytes read per call are the new ones.
func TestStreamingArgumentsParserReadsOnlyNewBytes(t *testing.T) {
	var parser streamingArgumentsParser
	text := `{"path":"a","content":"` + strings.Repeat("x", 1000)
	parser.parse(text)
	if parser.resumeAt == 0 || parser.object["path"] != "a" {
		t.Fatalf("committed = %d %#v", parser.resumeAt, parser.object)
	}
	if parser.strings.safeEnd < len(text)-1 || parser.strings.decoded.Len() != 1000 {
		t.Fatalf("string safeEnd = %d, decoded = %d bytes", parser.strings.safeEnd, parser.strings.decoded.Len())
	}
}

func BenchmarkStreamingArgumentsParser(b *testing.B) {
	fragments := toolArgumentFragments(0, 400, 400)
	b.Run("whole-text", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var text strings.Builder
			for _, fragment := range fragments {
				text.WriteString(fragment)
				parseStreamingJsonArguments(text.String())
			}
		}
	})
	b.Run("resumable", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var text strings.Builder
			var parser streamingArgumentsParser
			for _, fragment := range fragments {
				text.WriteString(fragment)
				parser.parse(text.String())
			}
		}
	})
}

// Short texts over the structural characters reach the malformed shapes a token soup rarely builds: unmatched closers, members without colons or commas, keys that are not strings.
func TestStreamingArgumentsParserMatchesWholeTextParserOnStructuralText(t *testing.T) {
	alphabet := []string{"{", "}", "[", "]", ",", ",", `"a"`, ":", " ", " ", "1", "x", `"`, "e", `\`}
	random := rand.New(rand.NewPCG(5, 6))
	for range 20000 {
		text := "{"
		for range 4 + random.IntN(12) {
			text += alphabet[random.IntN(len(alphabet))]
		}
		var parser streamingArgumentsParser
		for end := 1; end <= len(text); end++ {
			sameParse(t, "structural", &parser, text[:end])
		}
	}
}

// Any text, cut at any delta sizes, parses as the whole-text parser parses each prefix.
func FuzzStreamingArgumentsParser(f *testing.F) {
	for index, text := range resumeCorpus {
		f.Add(text, uint64(index))
	}
	f.Fuzz(func(t *testing.T, text string, cuts uint64) {
		random := rand.New(rand.NewPCG(cuts, 7))
		var parser streamingArgumentsParser
		for end := 0; ; {
			end = min(len(text), end+1+random.IntN(9))
			sameParse(t, "fuzz", &parser, text[:end])
			if end == len(text) {
				return
			}
		}
	})
}
