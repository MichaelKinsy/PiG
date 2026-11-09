package durable

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// EntryRecord.AppendStringify writes in one pass what MarshalJSON writes and the JSON.stringify compaction rewrites. Two
// oracles check it: the encoder it replaces (MarshalJSON through jsstring.MarshalJSON), byte for byte, and Node, which
// builds the same record from a description and runs JSON.stringify on it.

// entryPieces are the building blocks of the random strings: every byte class a JSON string writer branches on, and the
// WTF-8 form of lone and split UTF-16 surrogates.
var entryPieces = []string{
	"a", "Z", " ", "<", ">", "&", "'", "/", "\x7f", "\"", "\\", "\b", "\f", "\n", "\r", "\t", "\x00", "\x1f",
	"é", "€", "😀", "\u2028", "\u2029", "\ufffd", "\\u003c", "-0", "plain ascii text run ", "0123456789",
	"\xed\xa0\xbd", "\xed\xb8\x80", "\xed\xa0\x80", "\xed\xbf\xbf",
}

func isSurrogatePiece(piece string) bool { return strings.HasPrefix(piece, "\xed") }

type entryGenerator struct {
	random     *rand.Rand
	surrogates bool
	// everywhere keeps lone surrogates in a Kind and in a Data that is not an object too (the comparison with MarshalJSON,
	// which does the same to them); otherwise they appear only where JSON.stringify keeps them, as it does.
	everywhere bool
	// noPairs keeps a low surrogate from following a high one: a *delta.JsonObject writes the two halves of a pair as two
	// escapes where JSON.stringify writes the character they make.
	noPairs bool
	// splitPairs lets a tree hold them anyway.
	splitPairs bool
}

// text is a random string; with surrogates off it holds none of the lone-surrogate pieces.
func (g entryGenerator) text() string {
	var out strings.Builder
	previousHigh := false
	for range g.random.IntN(8) {
		piece := entryPieces[g.random.IntN(len(entryPieces))]
		if !g.surrogates && isSurrogatePiece(piece) {
			continue
		}
		if g.noPairs && previousHigh && isSurrogatePiece(piece) && piece[1] >= 0xb0 {
			continue
		}
		previousHigh = isSurrogatePiece(piece) && piece[1] < 0xb0
		out.WriteString(piece)
	}
	return out.String()
}

// The description language Node's child understands: null and booleans as themselves, {"u": units} a string,
// {"b": hex} the double with those bits, {"i": decimal} Number(BigInt(decimal)), {"a": [...]} an array and
// {"o": [[key, value], ...]} an object built with its own properties in that order.
func descString(s string) any { return map[string]any{"u": jv.UTF16Units(s)} }
func descBits(f float64) any  { return map[string]any{"b": fmt.Sprintf("%016x", math.Float64bits(f))} }
func descInt(n int64) any     { return map[string]any{"i": fmt.Sprint(n)} }
func descArray(items []any) any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"a": items}
}

type member struct {
	key   string
	value any
}

func descObject(members ...member) any {
	pairs := make([]any, 0, len(members))
	for _, m := range members {
		pairs = append(pairs, []any{descString(m.key), m.value})
	}
	return map[string]any{"o": pairs}
}

var entryNumbers = []float64{0, 1, -1, 0.5, 1e21, 1e-7, 123456789012345680000, 1 << 53, 1<<53 + 2, 5e-324, math.MaxFloat64, 0.1,
	1.0 / 3, 100, 1e300, 4.35, 1e-6, 999999999999999900000, 1e20, -1e-7, 2.5e-5}

func (g entryGenerator) number() float64 {
	switch g.random.IntN(6) {
	case 0:
		return math.Copysign(0, -1)
	case 1:
		for {
			f := math.Float64frombits(g.random.Uint64())
			if !math.IsNaN(f) && !math.IsInf(f, 0) {
				return f
			}
		}
	case 2:
		return float64(g.random.Int64N(1 << 62))
	case 3:
		return g.random.NormFloat64() * math.Pow(10, float64(g.random.IntN(40)-20))
	}
	return entryNumbers[g.random.IntN(len(entryNumbers))]
}

var entryKeys = []string{"a", "b", "c", "0", "1", "2", "10", "9", "01", "-1", "4294967294", "4294967295", "é", "__proto__", "constructor", "", "a b", "\u2028", "😀"}

// scalar is a leaf value: the Go value a durable decode holds and its description.
func (g entryGenerator) scalar() (any, any) {
	switch g.random.IntN(4) {
	case 0:
		return nil, nil
	case 1:
		b := g.random.IntN(2) == 0
		return b, b
	case 2:
		f := g.number()
		return f, descBits(f)
	}
	s := g.text()
	return s, descString(s)
}

// tree is a Data value: the Go value a durable decode holds (*delta.JsonObject, []any, float64, string) and its description.
func (g entryGenerator) tree(depth int) (any, any) {
	g.noPairs = !g.splitPairs
	if depth >= 3 || g.random.IntN(3) == 0 {
		return g.scalar()
	}
	if g.random.IntN(2) == 0 {
		items := make([]any, 0, 4)
		descs := make([]any, 0, 4)
		for range g.random.IntN(4) {
			item, desc := g.tree(depth + 1)
			items, descs = append(items, item), append(descs, desc)
		}
		return items, descArray(descs)
	}
	object := new(delta.JsonObject)
	var members []member
	used := map[string]bool{}
	for range g.random.IntN(5) {
		key := entryKeys[g.random.IntN(len(entryKeys))]
		if g.random.IntN(4) == 0 {
			key = g.text()
		}
		if used[key] {
			continue
		}
		used[key] = true
		value, desc := g.tree(depth + 1)
		object.Set(key, value)
		members = append(members, member{key, desc})
	}
	return object, descObject(members...)
}

// argumentMap is a tool call's arguments or a result's details: a Go map writes its keys sorted, so the keys come out of the
// sorted letters and the values are leaves and lists of leaves.
func (g entryGenerator) argumentMap() (ai.JsonObject, any) {
	arguments := ai.JsonObject{}
	var members []member
	for _, key := range []string{"a", "b", "c", "d"}[:g.random.IntN(5)] {
		var value, desc any
		if g.random.IntN(3) == 0 {
			var items, descs []any
			for range g.random.IntN(3) {
				item, itemDesc := g.scalar()
				items, descs = append(items, item), append(descs, itemDesc)
			}
			if items == nil {
				items = []any{}
			}
			value, desc = items, descArray(descs)
		} else {
			value, desc = g.scalar()
		}
		arguments[key] = value
		members = append(members, member{key, desc})
	}
	return arguments, descObject(members...)
}

func (g entryGenerator) timestamp() (int64, any) {
	n := g.random.Int64N(1 << 50)
	return n, descInt(n)
}

func (g entryGenerator) message() (ai.Message, any) {
	timestamp, timestampDesc := g.timestamp()
	switch g.random.IntN(4) {
	case 0:
		text := g.text()
		return ai.UserMessage{Content: ai.UserText(text), Timestamp: timestamp},
			descObject(member{"role", descString("user")}, member{"content", descString(text)}, member{"timestamp", timestampDesc})
	case 1:
		text := g.text()
		return ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: text}, ai.ImageContent{Data: "AAA", MimeType: "image/png"}}, Timestamp: timestamp},
			descObject(member{"role", descString("user")}, member{"content", descArray([]any{
				descObject(member{"type", descString("text")}, member{"text", descString(text)}),
				descObject(member{"type", descString("image")}, member{"data", descString("AAA")}, member{"mimeType", descString("image/png")}),
			})}, member{"timestamp", timestampDesc})
	case 2:
		text := g.text()
		arguments, argumentsDesc := g.argumentMap()
		input, output := g.random.IntN(1000), g.random.IntN(100)
		cost := [5]float64{g.number(), g.number(), 0, 0, g.number()}
		usage := ai.Usage{Input: input, Output: output, CacheRead: 1, CacheWrite: 2, TotalTokens: input + output + 3,
			Cost: ai.UsageCost{Input: cost[0], Output: cost[1], CacheRead: cost[2], CacheWrite: cost[3], Total: cost[4]}}
		message := ai.AssistantMessage{
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}, ai.ToolCall{ID: "call-1", Name: "lookup", Arguments: arguments}},
			API:     "faux", Provider: "p", Model: "m", Usage: usage, StopReason: ai.StopReasonToolUse, Timestamp: timestamp,
		}
		members := []member{
			{"role", descString("assistant")},
			{"content", descArray([]any{
				descObject(member{"type", descString("text")}, member{"text", descString(text)}),
				descObject(member{"type", descString("toolCall")}, member{"id", descString("call-1")}, member{"name", descString("lookup")}, member{"arguments", argumentsDesc}),
			})},
			{"api", descString("faux")}, {"provider", descString("p")}, {"model", descString("m")},
			{"usage", descObject(member{"input", descInt(int64(input))}, member{"output", descInt(int64(output))},
				member{"cacheRead", descInt(1)}, member{"cacheWrite", descInt(2)}, member{"totalTokens", descInt(int64(input + output + 3))},
				member{"cost", descObject(member{"input", descBits(cost[0])}, member{"output", descBits(cost[1])}, member{"cacheRead", descBits(cost[2])},
					member{"cacheWrite", descBits(cost[3])}, member{"total", descBits(cost[4])})})},
			{"stopReason", descString("toolUse")}, {"timestamp", timestampDesc},
		}
		if text := g.text(); g.random.IntN(3) == 0 && text != "" {
			message.ErrorMessage = text
			members = append(members, member{"errorMessage", descString(text)})
		}
		return message, descObject(members...)
	}
	text := g.text()
	members := []member{{"role", descString("toolResult")}, {"toolCallId", descString("call-1")}, {"toolName", descString("lookup")},
		{"content", descArray([]any{descObject(member{"type", descString("text")}, member{"text", descString(text)})})}}
	result := ai.ToolResultMessage{ToolCallID: "call-1", ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		IsError: g.random.IntN(2) == 0, Timestamp: timestamp}
	switch g.random.IntN(3) {
	case 0:
		details, desc := g.argumentMap()
		result.Details = map[string]any(details)
		members = append(members, member{"details", desc})
	case 1:
		result.DetailsNull = true
		members = append(members, member{"details", nil})
	}
	members = append(members, member{"isError", result.IsError}, member{"timestamp", timestampDesc})
	return result, descObject(members...)
}

// entry is a random record and its description.
func (g entryGenerator) entry() (EntryRecord, any) {
	id, conversationId := g.random.Int64N(1<<53), g.random.Int64N(1<<20)
	kind := "pi." + g.outside().text()
	record := EntryRecord{Id: EntryId(id), ConversationId: ConversationId(conversationId), Kind: kind}
	members := []member{{"id", descInt(id)}, {"conversationId", descInt(conversationId)}, {"kind", descString(kind)}}
	if g.random.IntN(4) != 0 {
		record.Model = []ai.Message{}
		var descs []any
		for range g.random.IntN(4) {
			message, desc := g.message()
			record.Model, descs = append(record.Model, message), append(descs, desc)
		}
		members = append(members, member{"model", descArray(descs)})
	}
	if g.random.IntN(3) != 0 {
		var desc any
		for {
			// A top-level null is not a Go Data (nil is absent). A string outside a *delta.JsonObject holds no lone surrogate:
			// MarshalJSON turns it into U+FFFD, which JSON.stringify would not.
			data, dataDesc := g.tree(0)
			if _, isObject := data.(*delta.JsonObject); !isObject {
				data, dataDesc = g.outside().tree(0)
			}
			if data != nil {
				record.Data, desc = data, dataDesc
				break
			}
		}
		members = append(members, member{"data", desc})
	}
	if g.random.IntN(4) == 0 {
		head := g.random.Int64N(1000)
		record.Head = new(EntryId(head))
		members = append(members, member{"head", descInt(head)})
	}
	if g.random.IntN(5) == 0 {
		record.Edits = []ContextEdit{}
		var descs []any
		for range g.random.IntN(3) {
			target := g.random.Int64N(100)
			edit := ContextEdit{Target: EntryId(target), Action: EditOmit}
			desc := []member{{"target", descInt(target)}, {"action", descString("omit")}}
			if g.random.IntN(2) == 0 {
				message, messageDesc := g.message()
				edit.Action, edit.Messages = EditReplace, []ai.Message{message}
				desc = []member{{"target", descInt(target)}, {"action", descString("replace")}, {"messages", descArray([]any{messageDesc})}}
			}
			record.Edits, descs = append(record.Edits, edit), append(descs, descObject(desc...))
		}
		members = append(members, member{"edits", descArray(descs)})
	}
	if g.random.IntN(3) == 0 {
		task := g.random.Int64N(1000)
		record.ByTaskId = new(TaskId(task))
		members = append(members, member{"byTaskId", descInt(task)})
	}
	return record, descObject(members...)
}

// nodeStringifier starts one Node child that builds the value each request line describes and answers its JSON.stringify.
func nodeStringifier(t *testing.T) func(description any) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "node", "-e", `
const lines = require('node:readline').createInterface({ input: process.stdin });
const view = new DataView(new ArrayBuffer(8));
const text = (units) => { let s = ''; for (let i = 0; i < units.length; i += 8192) s += String.fromCharCode(...units.slice(i, i + 8192)); return s; };
const build = (d) => {
  if (d === null || typeof d === 'boolean') return d;
  if ('u' in d) return text(d.u);
  if ('b' in d) { view.setBigUint64(0, BigInt('0x' + d.b)); return view.getFloat64(0); }
  if ('i' in d) return Number(BigInt(d.i));
  if ('a' in d) return d.a.map(build);
  const object = {};
  for (const [key, value] of d.o) Object.defineProperty(object, build(key), { value: build(value), enumerable: true, writable: true, configurable: true });
  return object;
};
lines.on('line', (line) => process.stdout.write(JSON.stringify(build(JSON.parse(line))) + '\n'));`)
	in, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("node is required for the JSON.stringify differential: %v", err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = command.Wait()
	})
	reader := bufio.NewReaderSize(out, 1<<16)
	return func(description any) string {
		t.Helper()
		line, err := json.Marshal(description)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(in, string(line)+"\n"); err != nil {
			t.Fatal(err)
		}
		answer, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("node: %v", err)
		}
		return strings.TrimSuffix(answer, "\n")
	}
}

// A record written by AppendStringify is the text Node's JSON.stringify writes for the same record: member order (array-index
// keys first in an object), string escapes (no HTML escaping, U+2028 literal, a lone surrogate kept where MarshalJSON keeps it:
// in a message and in a *delta.JsonObject), numbers (-0 is 0, 1e21 is 1e+21, large doubles, shortest round trip) and ids.
// MarshalJSON through the general encoder gives the same text.
func TestEntryRecordAppendStringifyMatchesNode(t *testing.T) {
	stringify := nodeStringifier(t)
	generator := entryGenerator{random: rand.New(rand.NewPCG(7, 1)), surrogates: true}
	for n := range 3000 {
		record, description := generator.entry()
		want := stringify(description)
		got, err := record.AppendStringify([]byte("prefix"))
		if err != nil {
			t.Fatalf("record %d: %v", n, err)
		}
		if string(got) != "prefix"+want {
			t.Fatalf("record %d\n got: %s\nwant: %s", n, got[len("prefix"):], want)
		}
		general, err := jsstring.MarshalJSON(record)
		if err != nil || string(general) != want {
			t.Fatalf("record %d: jsstring.MarshalJSON\n got: %s (%v)\nwant: %s", n, general, err, want)
		}
	}
}

// AppendStringify writes byte for byte what the encoder it replaces writes: MarshalJSON, then the JSON.stringify compaction.
// The records hold lone surrogates in every string, a Kind and a Go value's too, where MarshalJSON makes U+FFFD of them: such
// a record goes through MarshalJSON. Without lone surrogates, a decode of the text followed by another write gives the same text.
func TestEntryRecordAppendStringifyMatchesTheCompactedMarshalJSON(t *testing.T) {
	generator := entryGenerator{random: rand.New(rand.NewPCG(11, 1)), surrogates: true, everywhere: true}
	for n := range 20000 {
		record, _ := generator.entry()
		want, wantErr := jsstring.MarshalJSON(entryMarshaler(record))
		got, err := record.AppendStringify(nil)
		if err != nil || wantErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("record %d\n got: %s (%v)\nwant: %s (%v)", n, got, err, want, wantErr)
		}
		if strings.Contains(string(got), `\ud`) {
			continue // a lone surrogate reads back as U+FFFD in a message of a decoded record
		}
		var decoded EntryRecord
		if err := decoded.UnmarshalJSON(got); err != nil {
			t.Fatalf("record %d: decoding %s: %v", n, got, err)
		}
		if again, err := decoded.AppendStringify(nil); err != nil || !bytes.Equal(again, got) {
			t.Fatalf("record %d: decode then write\n got: %s (%v)\nwant: %s", n, again, err, got)
		}
	}
}

type namedNumber int32

func TestEntryRecordAppendStringifyMatchesTheCompactedMarshalJSONOnOtherData(t *testing.T) {
	nested := func(depth int) any {
		var value any = "leaf"
		for range depth {
			value = []any{value}
		}
		return value
	}
	var nilObject *delta.JsonObject
	cyclicSlice := []any{nil}
	cyclicSlice[0] = cyclicSlice
	cyclicMap := map[string]any{}
	cyclicMap["self"] = cyclicMap
	object := new(delta.JsonObject)
	object.Set("b", math.Copysign(0, -1))
	object.Set("a", []any(nil))
	object.Set("2", nilObject)
	cases := map[string]any{
		"empty string":  "",
		"false":         false,
		"negative zero": math.Copysign(0, -1),
		"nil object":    nilObject,
		"nil slice":     []any(nil),
		"empty slice":   []any{},
		"empty object":  new(delta.JsonObject),
		"object":        object,
		"go map":        map[string]any{"z": 1.5, "a": []any{"<&>\u2028"}, "m": nil},
		"typed ints":    []any{1, int64(-2), uint8(3), namedNumber(4), float32(0.1)},
		"raw message":   json.RawMessage(` {"a" : [1, 2e3 ,"\u003c"] } `),
		"struct": struct {
			A int    `json:"a"`
			B string `json:"b,omitempty"`
		}{A: 2},
		"map in tree":      []any{map[string]any{"k": "v"}, new(delta.JsonObject)},
		"deep":             nested(900),
		"deeper":           nested(1100),
		"cyclic slice":     cyclicSlice,
		"cyclic map":       cyclicMap,
		"not a number":     math.NaN(),
		"infinite":         []any{1.0, math.Inf(-1)},
		"nan in object":    func() any { o := new(delta.JsonObject); o.Set("n", math.NaN()); return o }(),
		"unsupported kind": []any{make(chan int)},
		"unsupported func": func() {},
	}
	for name, data := range cases {
		for _, record := range []EntryRecord{
			{Id: 1, ConversationId: 2, Kind: "k", Data: data},
			{Id: 1, ConversationId: 2, Kind: "k", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("x"), Timestamp: 1}}, Data: data, Edits: []ContextEdit{}},
		} {
			want, wantErr := jsstring.MarshalJSON(entryMarshaler(record))
			got, err := record.AppendStringify(nil)
			if (err == nil) != (wantErr == nil) || err != nil && err.Error() != wantErr.Error() || !bytes.Equal(got, want) {
				t.Errorf("%s\n got: %.200s (%v)\nwant: %.200s (%v)", name, got, err, want, wantErr)
			}
		}
	}
}

// A record whose model or edit cannot be encoded fails with the error MarshalJSON gives, and appends nothing.
func TestEntryRecordAppendStringifyReportsTheMarshalJSONError(t *testing.T) {
	bad := ai.AssistantMessage{Usage: ai.Usage{Cost: ai.UsageCost{Total: math.NaN()}}}
	for _, record := range []EntryRecord{
		{Id: 1, Model: []ai.Message{bad}},
		{Id: 1, Edits: []ContextEdit{{Target: 1, Action: EditReplace, Messages: []ai.Message{bad}}}},
	} {
		want, wantErr := jsstring.MarshalJSON(entryMarshaler(record))
		got, err := record.AppendStringify([]byte("keep"))
		if wantErr == nil || err == nil || err.Error() != wantErr.Error() || string(got) != "keep" || want != nil {
			t.Errorf("got %q (%v), want %q and %v", got, err, "keep", wantErr)
		}
	}
}

func BenchmarkEntryRecordEncode(b *testing.B) {
	generator := entryGenerator{random: rand.New(rand.NewPCG(3, 1))}
	var records []EntryRecord
	for len(records) < 64 {
		record, _ := generator.entry()
		if record.Model != nil {
			records = append(records, record)
		}
	}
	b.Run("general", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			if _, err := jsstring.MarshalJSON(entryMarshaler(records[i%len(records)])); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("append", func(b *testing.B) {
		b.ReportAllocs()
		var buffer []byte
		for i := range b.N {
			var err error
			if buffer, err = records[i%len(records)].AppendStringify(buffer[:0]); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// A Go map in Data is written as JSON.stringify writes an object holding the same properties when none of its keys is an
// array index: a Go map has no insertion order, so the keys come out sorted (MarshalJSON sorts every key as a string, so an
// index key comes where its digits sort; the comparison with MarshalJSON covers that).
func TestEntryRecordAppendStringifyWritesAGoMapAsNodeWritesAnObject(t *testing.T) {
	stringify := nodeStringifier(t)
	generator := entryGenerator{random: rand.New(rand.NewPCG(13, 1)), surrogates: false}
	for n := range 1500 {
		data := map[string]any{}
		for range generator.random.IntN(6) {
			value, _ := generator.scalar()
			if generator.random.IntN(4) == 0 {
				value = generator.random.IntN(1 << 30)
			} else if generator.random.IntN(5) == 0 {
				value = map[string]any{"b": 1.5, "a": "x", "c": []any{true}}
			}
			if key := entryKeys[generator.random.IntN(len(entryKeys))]; !jsparity.IsIndexKey(key) {
				data[key] = value
			}
		}
		keys := make([]string, 0, len(data))
		for key := range data {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		members := make([]member, 0, len(keys))
		for _, key := range keys {
			var desc any
			switch value := data[key].(type) {
			case nil, bool:
				desc = value
			case float64:
				desc = descBits(value)
			case map[string]any:
				desc = descObject(member{"a", descString("x")}, member{"b", descBits(1.5)}, member{"c", descArray([]any{true})})
			case int:
				desc = descInt(int64(value))
			case string:
				desc = descString(value)
			}
			members = append(members, member{key, desc})
		}
		record := EntryRecord{Id: 1, ConversationId: 2, Kind: "k", Data: data}
		want := stringify(descObject(member{"id", descInt(1)}, member{"conversationId", descInt(2)}, member{"kind", descString("k")}, member{"data", descObject(members...)}))
		got, err := record.AppendStringify(nil)
		if err != nil || string(got) != want {
			t.Fatalf("map %d\n got: %s (%v)\nwant: %s", n, got, err, want)
		}
	}
}

// outside is g for a string MarshalJSON turns a lone surrogate in into U+FFFD: it has none unless g.everywhere.
func (g entryGenerator) outside() entryGenerator {
	g.surrogates = g.surrogates && g.everywhere
	return g
}
