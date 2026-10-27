package extensionconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// ordered is a JSON object whose members are written in the order given, duplicates included, which a Go map cannot do.
type ordered [][2]any

func (o ordered) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, member := range o {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(member[0])
		value, err := json.Marshal(member[1])
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// TestConformance_SerializeConversationAgreesAcrossSDKs: Pi exports serializeConversation (core/compaction/utils.ts:120-155, index.ts), which omits thinking
// (Pi 1.2.0) and prints each tool call's arguments with JSON.stringify in property order (array-index keys first, ascending). The Node SDK runs Pi's own
// code, so its text is the reference: every conversation below runs in each SDK, with the raw JSON in the order written, and must equal it. A Go map would
// lose the order, so the Go SDK reads raw JSON.
func TestConformance_SerializeConversationAgreesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	cases := serializeConversationCases()
	run := func(t *testing.T, h *harness, c ordered) map[string]any {
		t.Helper()
		args, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), "serialize_probe", string(args)) {
			t.Fatal("fixture has no serialize_probe command")
		}
		var reported string
		pollUntilConformance(t, "serialize_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "serialize:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got map[string]any
		if err := json.Unmarshal([]byte(markLoneSurrogates(reported)), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got
	}

	var reference []map[string]any
	for _, tc := range sdkHarnessCases() {
		if tc.name != "subprocess-node" {
			continue
		}
		h := tc.make(t)
		for _, c := range cases {
			reference = append(reference, run(t, h, c.input))
		}
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	}
	if len(reference) != len(cases) {
		t.Fatalf("the Node reference ran %d of %d cases", len(reference), len(cases))
	}
	// Values Pi's code implies, so a mistake shared by every SDK cannot pass.
	for i, c := range cases {
		if c.want != nil && !reflect.DeepEqual(reference[i], c.want) {
			t.Fatalf("reference for %s = %v, want %v", c.name, reference[i], c.want)
		}
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if tc.name == "subprocess-node" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for i, c := range cases {
				if got := run(t, h, c.input); !reflect.DeepEqual(got, reference[i]) {
					t.Errorf("%s: %.300v, Node (Pi) reports %.300v", c.name, got, reference[i])
				}
			}
		})
	}
}

type serializeCase struct {
	name  string
	input ordered
	want  map[string]any
	// lone is set when the input or the output holds an unpaired surrogate, which only a JavaScript string can hold.
	lone bool
}

// markLoneSurrogates rewrites each unpaired \uD800-\uDFFF escape of a JSON text to a visible marker, because encoding/json turns every one into U+FFFD and the
// report would then hide the difference between a kept surrogate and a replaced one. A high surrogate followed by a low one is a pair and stays.
func markLoneSurrogates(text string) string {
	hex4 := func(at int) (rune, bool) {
		if at+6 > len(text) || text[at] != '\\' || text[at+1] != 'u' {
			return 0, false
		}
		n, err := strconv.ParseUint(text[at+2:at+6], 16, 32)
		return rune(n), err == nil
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\\' {
			out.WriteByte(text[i])
			i++
			continue
		}
		unit, ok := hex4(i)
		if !ok {
			out.WriteString(text[i:min(i+2, len(text))])
			i += 2
			continue
		}
		if next, paired := hex4(i + 6); unit >= 0xD800 && unit < 0xDC00 && paired && next >= 0xDC00 && next < 0xE000 {
			out.WriteString(text[i : i+12])
			i += 12
			continue
		}
		if unit >= 0xD800 && unit < 0xE000 {
			fmt.Fprintf(&out, "<lone %04x>", unit)
		} else {
			out.WriteString(text[i : i+6])
		}
		i += 6
	}
	return out.String()
}

func serializeConversationCases() []serializeCase {
	message := func(role string, content any, more ...[2]any) ordered {
		m := ordered{{"role", role}}
		if content != nil {
			m = append(m, [2]any{"content", content})
		}
		return append(m, more...)
	}
	text := func(s string) ordered { return ordered{{"type", "text"}, {"text", s}} }
	call := func(name string, arguments any) ordered {
		return ordered{{"type", "toolCall"}, {"id", "c1"}, {"name", name}, {"arguments", arguments}}
	}
	conv := func(name string, want map[string]any, messages ...any) serializeCase {
		if messages == nil {
			messages = []any{}
		}
		return serializeCase{name: name, input: ordered{{"messages", messages}}, want: want}
	}
	result := func(s string) map[string]any { return map[string]any{"result": s} }
	failure := map[string]any{"error": true}
	lone := func(name string, messages ...any) serializeCase {
		c := conv(name, nil, messages...)
		c.lone = true
		return c
	}

	cases := []serializeCase{
		conv("pinned conversation", result("[User]: hi\n\n[Assistant]: ok\n\n[Assistant tool calls]: read(1=\"x\\n\", 2=[1.5,null], b=1, a={\"9\":3,\"10\":2,\"z\":1})"),
			message("user", "hi"),
			message("assistant", []any{ordered{{"type", "thinking"}, {"thinking", "t"}}, text("ok"), call("read", ordered{{"b", 1}, {"2", []any{1.5, nil}}, {"1", "x\n"}, {"a", ordered{{"z", 1}, {"10", 2}, {"9", 3}}}})})),
		conv("duplicate keys keep the first position and the last value", result("[Assistant tool calls]: t(a=3, b=2)"),
			message("assistant", []any{call("t", ordered{{"a", 1}, {"b", 2}, {"a", 3}})})),
		conv("truncation", result("[Tool result]: "+strings.Repeat("r", 2000)+"\n\n[... 100 more characters truncated]"), message("toolResult", strings.Repeat("r", 2100))),
		conv("exactly the limit", result("[Tool result]: "+strings.Repeat("r", 2000)), message("toolResult", strings.Repeat("r", 2000))),
		conv("no messages", result("")),
		conv("a null message", failure, message("user", "x"), nil),
		conv("user content absent", failure, message("user", nil)),
		conv("user content a number", failure, message("user", 5)),
		conv("assistant content a string", failure, message("assistant", "text")),
		conv("a null block", failure, message("assistant", []any{nil})),
		conv("empty user and tool result content", result("[Assistant]: "), message("user", ""), message("toolResult", []any{}), message("assistant", []any{text("")})),
		conv("blocks that are not objects", result("[User]: a"), message("user", []any{5, "x", true, text("a"), []any{}})),
		conv("an unknown role and a role that is not a string", result("[User]: kept"), message("custom", "x"), ordered{{"role", 5}}, 7, "user", message("user", "kept")),
	}
	numbers := []any{0, 1, -1, 1.5, 0.1, 100, 1e21, 1.5e21, 1e-6, 1e-7, 123456789012345680000.0, 9007199254740993, 4294967294, 4294967295, 5e-324}
	strs := []string{"", "plain", "quote\"back\\slash", "\b\f\n\r\t", "\u0001\u001f\u007f", "\u2028\u2029", "é😀", "<&>"}
	for _, n := range numbers {
		cases = append(cases, conv("number "+strconv.FormatFloat(toFloat(n), 'g', -1, 64), nil, message("assistant", []any{call("t", ordered{{"n", n}, {"l", []any{n, n}}})})))
	}
	for _, s := range strs {
		cases = append(cases, conv("string "+strconv.Quote(s), nil,
			message("assistant", []any{call("t", ordered{{"s", s}, {s, 1}}), ordered{{"type", "thinking"}, {"thinking", s}}, text(s)}),
			message("user", s), message("toolResult", []any{text(s), ordered{{"type", "image"}, {"data", "x"}}, text(s)})))
	}
	for _, arguments := range []any{nil, "abc", "é😀", 5, true, false, []any{1, "a", nil}, ordered{}, ordered{{"10", 1}, {"9", 2}, {"x", 3}, {"01", 4}, {"-1", 5}, {"1.5", 6}, {"4294967294", 7}, {"4294967295", 8}, {"", 9}}} {
		cases = append(cases, conv("arguments "+strconv.Quote(string(serializeJSON(arguments))), nil, message("assistant", []any{call("t", arguments)})))
	}
	cases = append(cases,
		conv("tool call with no arguments member", nil, message("assistant", []any{ordered{{"type", "toolCall"}, {"name", "t"}}})),
		conv("tool call with no name", nil, message("assistant", []any{ordered{{"type", "toolCall"}, {"arguments", ordered{{"a", 1}}}}})),
		conv("tool call name that is not a string", nil, message("assistant", []any{ordered{{"type", "toolCall"}, {"name", 5}, {"arguments", ordered{}}}, ordered{{"type", "toolCall"}, {"name", nil}, {"arguments", ordered{}}}, ordered{{"type", "toolCall"}, {"name", []any{1, nil, 2}}, {"arguments", ordered{}}}})),
		conv("thinking that is not a string", nil, message("assistant", []any{ordered{{"type", "thinking"}}, ordered{{"type", "thinking"}, {"thinking", nil}}, ordered{{"type", "thinking"}, {"thinking", 5}}})),
		conv("text that is not a string", nil, message("assistant", []any{ordered{{"type", "text"}}, ordered{{"type", "text"}, {"text", nil}}, ordered{{"type", "text"}, {"text", 5}}, ordered{{"type", "text"}, {"text", true}}})),
		conv("tool result with only an image", result(""), message("toolResult", []any{ordered{{"type", "image"}}})),
		lone("an unpaired surrogate in a user message", message("user", json.RawMessage(`"a\ud800b\udc00c"`))),
		lone("a reversed pair is two unpaired units", message("user", json.RawMessage(`"\ude00\ud83d"`))),
		lone("an unpaired surrogate in a tool call name, a string argument and a string member", message("assistant", []any{call("n", json.RawMessage(`"x\ud83d"`)), ordered{{"type", "toolCall"}, {"name", json.RawMessage(`"n\udc00"`)}, {"arguments", ordered{{"s", json.RawMessage(`"\ud800"`)}}}}})),
		lone("the cut of a pair leaves its high surrogate", message("toolResult", strings.Repeat("a", 1999)+"😀😀")),
		lone("the cut of a pair after an unpaired unit", message("toolResult", json.RawMessage(`"\udc00`+strings.Repeat("b", 1998)+`😀😀"`))),
		conv("multi-unit truncation counts UTF-16 units", nil, message("toolResult", strings.Repeat("é", 2500)), message("toolResult", strings.Repeat("a", 1990)+"😀😀😀")),
	)
	random := rand.New(rand.NewPCG(3, 5))
	pickArgs := func() any {
		o := ordered{}
		for range random.IntN(5) {
			key := []string{"a", "b", "c", "1", "2", "10", "x y", "é", "0", "z"}[random.IntN(10)]
			var value any
			switch random.IntN(5) {
			case 0:
				value = numbers[random.IntN(len(numbers))]
			case 1:
				value = strs[random.IntN(len(strs))]
			case 2:
				value = []any{random.IntN(9), strs[random.IntN(len(strs))], nil}
			case 3:
				value = ordered{{"k", true}, {"2", false}, {"1", nil}}
			default:
				value = nil
			}
			o = append(o, [2]any{key, value})
		}
		return o
	}
	for i := range 300 {
		var messages []any
		for range 1 + random.IntN(4) {
			switch random.IntN(4) {
			case 0:
				messages = append(messages, message("user", strs[random.IntN(len(strs))]))
			case 1:
				messages = append(messages, message("toolResult", []any{text(strs[random.IntN(len(strs))])}))
			default:
				blocks := []any{}
				for range 1 + random.IntN(4) {
					switch random.IntN(3) {
					case 0:
						blocks = append(blocks, text(strs[random.IntN(len(strs))]))
					case 1:
						blocks = append(blocks, ordered{{"type", "thinking"}, {"thinking", strs[random.IntN(len(strs))]}})
					default:
						blocks = append(blocks, call([]string{"read", "write", "edit", "bash"}[random.IntN(4)], pickArgs()))
					}
				}
				messages = append(messages, message("assistant", blocks))
			}
		}
		cases = append(cases, conv("random "+strconv.Itoa(i), nil, messages...))
	}
	return cases
}

func toFloat(n any) float64 {
	switch n := n.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	return 0
}

func serializeJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
