package durable

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// decodeEntryFast must return what the general codec returns whenever it accepts a text. The general codec is the oracle.

func generalDecode(data []byte) (EntryRecord, error) {
	var record EntryRecord
	err := record.unmarshalGeneral(data)
	return record, err
}

const (
	sampleUser      = `{"model":[{"role":"user","content":"turn h0 tools=1","timestamp":1791311247477}],"kind":"pi.user","id":7,"conversationId":1}`
	sampleAssistant = `{"model":[{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"lookup","arguments":{"n":1}}],"api":"faux:1791311247461:bsqh44pv62k","provider":"faux","model":"scripted-1","usage":{"input":63,"output":4,"cacheRead":0,"cacheWrite":63,"totalTokens":130,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1791311247488}],"kind":"pi.assistant","id":11,"conversationId":1,"byTaskId":9}`
	sampleAnswer    = `{"model":[{"role":"assistant","content":[{"type":"text","text":"done after 1 lookups"}],"api":"faux:1","provider":"faux","model":"scripted-1","usage":{"input":76,"output":5,"cacheRead":63,"cacheWrite":76,"totalTokens":220,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1791311247506}],"kind":"pi.assistant","id":15,"conversationId":1,"byTaskId":14}`
	sampleResult    = `{"model":[{"role":"toolResult","toolCallId":"call-1","toolName":"lookup","content":[{"type":"text","text":"record 1: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}],"isError":false,"timestamp":1791311247499}],"data":{"diagnostics":[]},"kind":"pi.tool-result","id":13,"conversationId":1,"byTaskId":12}`
	sampleSystem    = `{"model":[{"role":"system","content":"","sections":{"preamble":"You are a benchmark agent."},"toolsAdded":[{"name":"lookup","description":"Look up","parameters":{"type":"object","required":["n"],"properties":{"n":{"type":"number"}}}}],"timestamp":1791311247483}],"kind":"pi.system","id":10,"conversationId":1,"byTaskId":9}`
)

var samples = []string{sampleUser, sampleAssistant, sampleAnswer, sampleResult, sampleSystem}

func TestFastEntryDecodeMatchesTheGeneralCodecOnStoredRecords(t *testing.T) {
	for index, text := range samples {
		want, err := generalDecode([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := decodeEntryFast([]byte(text))
		if index == len(samples)-1 {
			if ok {
				t.Fatal("system messages are the general codec's")
			}
			continue
		}
		if !ok {
			t.Fatalf("sample %d was refused", index)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("sample %d: fast %#v, general %#v", index, got, want)
		}
	}
}

func randomEntryText(random *rand.Rand) string {
	words := []string{"", "a", "héllo", "emoji \U0001F600", "tab\t\"q\" \\ /", "\u2028", "line\nbreak", "<tag>&"}
	word := func() string { return words[random.Intn(len(words))] }
	var messages []ai.Message
	for range random.Intn(4) {
		switch random.Intn(5) {
		case 0:
			messages = append(messages, ai.UserMessage{Content: ai.UserText(word()), Timestamp: random.Int63n(1 << 50)})
		case 1:
			messages = append(messages, ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: word()}, ai.ImageContent{Data: "AAA", MimeType: "image/png"}}, Timestamp: 5})
		case 2:
			blocks := []ai.AssistantContentBlock{ai.TextContent{Text: word(), TextSignature: []string{"", "sig"}[random.Intn(2)]}}
			if random.Intn(2) == 0 {
				blocks = append(blocks, ai.ToolCall{ID: "c1", Name: "lookup", Arguments: ai.JsonObject{"n": float64(random.Intn(9)), "s": word(), "nested": []any{1.5, nil, true}}})
			}
			if random.Intn(4) == 0 {
				blocks = append(blocks, ai.ThinkingContent{Thinking: word()})
			}
			messages = append(messages, ai.AssistantMessage{Content: blocks, API: "faux", Provider: "p", Model: "m", StopReason: []ai.StopReason{"stop", "toolUse", "error"}[random.Intn(3)],
				Usage: ai.Usage{Input: random.Intn(99), Output: random.Intn(9), CacheRead: 1, CacheWrite: 2, TotalTokens: 7, Cost: ai.UsageCost{Input: 0.5, Total: 1e-7}}, Timestamp: 9,
				ErrorMessage: []string{"", "boom"}[random.Intn(2)]})
		case 3:
			result := ai.ToolResultMessage{ToolCallID: "c1", ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: word()}}, IsError: random.Intn(2) == 0, Timestamp: 3}
			switch random.Intn(3) {
			case 0:
				result.Details = map[string]any{"k": []any{word()}}
			case 1:
				result.DetailsNull = true
			}
			messages = append(messages, result)
		default:
			messages = append(messages, ai.SystemMessage{Content: ai.SystemText(word()), Timestamp: 1})
		}
	}
	record := EntryRecord{Id: EntryId(random.Intn(1 << 30)), ConversationId: ConversationId(random.Intn(5)), Kind: "pi." + word()}
	if random.Intn(4) != 0 {
		record.Model = messages
		if messages == nil {
			record.Model = []ai.Message{}
		}
	}
	if random.Intn(2) == 0 {
		record.Data = map[string]any{"a": word(), "b": []any{float64(random.Intn(5)), map[string]any{}, []any{}}, "n": nil, "f": 1e21}
	}
	if random.Intn(5) == 0 {
		record.Head = new(EntryId(random.Intn(100)))
	}
	if random.Intn(3) == 0 {
		record.ByTaskId = new(TaskId(random.Intn(100)))
	}
	if random.Intn(8) == 0 {
		record.Edits = []ContextEdit{{Target: 3, Action: EditOmit}}
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func checkAgainstGeneral(t *testing.T, text string) (accepted bool) {
	t.Helper()
	got, ok := decodeEntryFast([]byte(text))
	if !ok {
		return false
	}
	want, err := generalDecode([]byte(text))
	if err != nil {
		t.Fatalf("the fast decoder accepted a text the general codec rejects (%v): %q", err, text)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("text %q:\n fast    %#v\n general %#v", text, got, want)
	}
	return true
}

func TestFastEntryDecodeMatchesTheGeneralCodecOnEncodedRecords(t *testing.T) {
	random := rand.New(rand.NewSource(21))
	accepted := 0
	const total = 6000
	for range total {
		if checkAgainstGeneral(t, randomEntryText(random)) {
			accepted++
		}
	}
	// Thinking blocks, system messages and edits are the general codec's; the rest must take the fast path.
	if accepted < total/2 {
		t.Fatalf("only %d of %d encoded records took the fast path", accepted, total)
	}
}

// TestFastEntryDecodeNeverAcceptsWhatTheGeneralCodecReadsDifferently mutates valid records byte by byte.
func TestFastEntryDecodeNeverAcceptsWhatTheGeneralCodecReadsDifferently(t *testing.T) {
	random := rand.New(rand.NewSource(33))
	snippets := []string{"\\", "\"", "\\u0041", "\\ud800", "\\udc00x", " ", "\n", "\x00", "\xff", "é", "\u2028", ",", ":", "{", "}", "[", "]", "1e999", "-0", "01", "1.", ".5", "null", "true", "\"role\":\"user\",", "\"id\":1,"}
	accepted, mutated := 0, 0
	for range 400 {
		text := randomEntryText(random)
		if random.Intn(3) == 0 {
			text = samples[random.Intn(len(samples)-1)]
		}
		for range 40 {
			bytes := []byte(text)
			position := random.Intn(len(bytes) + 1)
			switch random.Intn(4) {
			case 0:
				if position < len(bytes) {
					bytes = append(bytes[:position:position], bytes[position+1:]...)
				}
			case 1:
				bytes = append(bytes[:position:position], append([]byte(snippets[random.Intn(len(snippets))]), bytes[position:]...)...)
			case 2:
				if position < len(bytes) {
					bytes[position] = "{}[],:\"\\0aé "[random.Intn(13)]
				}
			default:
				if position < len(bytes) {
					bytes = bytes[:position]
				}
			}
			mutated++
			if checkAgainstGeneral(t, string(bytes)) {
				accepted++
			}
		}
	}
	t.Logf("%d mutations, %d accepted by the fast decoder", mutated, accepted)
}

func TestFastEntryDecodeRefusesWhatTheGeneralCodecReadsLeniently(t *testing.T) {
	for name, text := range map[string]string{
		"differently cased key":  `{"Id":7,"conversationId":1,"kind":"k"}`,
		"duplicate key":          `{"id":7,"id":8,"conversationId":1,"kind":"k"}`,
		"unknown key":            `{"id":7,"conversationId":1,"kind":"k","extra":1}`,
		"float id":               `{"id":7.0,"conversationId":1,"kind":"k"}`,
		"surrogate escape":       `{"id":7,"conversationId":1,"kind":"\ud800"}`,
		"invalid utf-8":          "{\"id\":7,\"conversationId\":1,\"kind\":\"\xff\"}",
		"trailing data":          `{"id":7,"conversationId":1,"kind":"k"} x`,
		"unknown message key":    `{"id":7,"conversationId":1,"kind":"k","model":[{"role":"user","content":"x","timestamp":1,"durationMs":5}]}`,
		"message without a role": `{"id":7,"conversationId":1,"kind":"k","model":[{"content":"x","timestamp":1}]}`,
	} {
		if _, ok := decodeEntryFast([]byte(text)); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestFastEntryDecodeHandWrittenVariants covers spellings an encoder does not produce.
func TestFastEntryDecodeHandWrittenVariants(t *testing.T) {
	accepted := map[string]string{
		"empty edits":         `{"id":7,"conversationId":1,"kind":"k","edits":[]}`,
		"null edits":          `{"id":7,"conversationId":1,"kind":"k","edits":null}`,
		"null head and task":  `{"id":7,"conversationId":1,"kind":"k","head":null,"byTaskId":null}`,
		"null data and model": `{"id":7,"conversationId":1,"kind":"k","data":null,"model":null}`,
		"empty model":         `{"id":7,"conversationId":1,"kind":"k","model":[]}`,
		"empty object":        `{}`,
		"spaces and newlines": " {\n \"id\" : 7 ,\n\t\"kind\" : \"k\" , \"model\" : [ { \"role\" : \"user\" , \"content\" : \"x\" , \"timestamp\" : 1 } ] }\n",
		"unicode escapes":     `{"id":7,"kind":"caf\u00e9 \u4e2d \/ \b\f"}`,
		"raw unicode":         `{"id":7,"kind":"café 中 😀"}`,
		"negative and big":    `{"id":-7,"conversationId":9007199254740993,"kind":"k"}`,
		"data numbers":        `{"id":7,"kind":"k","data":{"a":1,"b":-0,"c":1E5,"d":1.5e-7,"e":[0,10,true,false,null,"s"],"f":{}}}`,
		"user blocks":         `{"id":7,"kind":"k","model":[{"role":"user","content":[{"type":"text","text":"a"},{"type":"image","data":"AA","mimeType":"image/png"}],"timestamp":2}]}`,
		"details null":        `{"id":7,"kind":"k","model":[{"role":"toolResult","toolCallId":"c","toolName":"t","content":[],"details":null,"isError":true,"timestamp":2}]}`,
		"details value":       `{"id":7,"kind":"k","model":[{"role":"toolResult","toolCallId":"c","toolName":"t","content":[{"type":"text","text":"r"}],"details":{"z":1,"a":[1]},"isError":false,"timestamp":2}]}`,
		"key order":           `{"kind":"k","id":1,"model":[{"timestamp":3,"content":"x","role":"user"}]}`,
		"unsorted arguments":  `{"id":7,"kind":"k","model":[{"role":"assistant","content":[{"type":"toolCall","id":"c","name":"t","arguments":{"z":1,"a":{"y":1,"b":[{"q":1,"c":2},{"d":1,"a":2}]},"m":null}}],"api":"a","provider":"p","model":"m","usage":{},"stopReason":"toolUse","timestamp":1}]}`,
		"repeated argument":   `{"id":7,"kind":"k","model":[{"role":"assistant","content":[{"type":"toolCall","id":"c","name":"t","arguments":{"b":{"y":1,"a":2},"a":1,"b":{"a":1,"b":2}},"thoughtSignature":"s","namespace":"ns"}],"api":"a","provider":"p","model":"m","usage":{},"stopReason":"toolUse","timestamp":1}]}`,
		"null arguments":      `{"id":7,"kind":"k","model":[{"role":"assistant","content":[{"type":"toolCall","id":"c","name":"t","arguments":null}],"api":"a","provider":"p","model":"m","usage":{},"stopReason":"toolUse","timestamp":1}]}`,
		"thinking delegated":  `{"id":7,"kind":"k","model":[{"role":"assistant","content":[{"type":"thinking","thinking":"hm","thinkingSignature":""},{"type":"text","text":"x"}],"api":"a","provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0.1,"output":0.2,"cacheRead":0,"cacheWrite":0,"total":0.3}},"stopReason":"stop","timestamp":5}]}`,
	}
	for name, text := range accepted {
		if !checkAgainstGeneral(t, text) {
			t.Errorf("%s: refused", name)
		}
	}
	for name, text := range map[string]string{
		"usage extra key":     `{"id":7,"kind":"k","model":[{"role":"assistant","content":[],"api":"a","provider":"p","model":"m","usage":{"input":1,"cacheWrite1h":2},"stopReason":"stop","timestamp":1}]}`,
		"cost extra key":      `{"id":7,"kind":"k","model":[{"role":"assistant","content":[],"api":"a","provider":"p","model":"m","usage":{"cost":{"input":1,"other":2}},"stopReason":"stop","timestamp":1}]}`,
		"escaped key":         `{"i\u0064":7}`,
		"array arguments":     `{"id":7,"kind":"k","model":[{"role":"assistant","content":[{"type":"toolCall","id":"c","name":"t","arguments":[1]}],"api":"a","provider":"p","model":"m","usage":{},"stopReason":"toolUse","timestamp":1}]}`,
		"float timestamp":     `{"id":7,"kind":"k","model":[{"role":"user","content":"x","timestamp":1.5}]}`,
		"null content":        `{"id":7,"kind":"k","model":[{"role":"assistant","content":null,"api":"a","provider":"p","model":"m","stopReason":"stop","timestamp":1}]}`,
		"number out of range": `{"id":7,"kind":"k","data":1e999}`,
		// UnmarshalContentBlock rejects a block without a type, so a typeless block must not decode as text or a call.
		"untyped text block":      `{"id":7,"kind":"k","model":[{"role":"user","content":[{"text":"x"}],"timestamp":1}]}`,
		"untyped tool call block": `{"id":7,"kind":"k","model":[{"role":"assistant","content":[{"id":"c","name":"t","arguments":{}}],"api":"a","provider":"p","model":"m","usage":{},"stopReason":"toolUse","timestamp":1}]}`,
	} {
		checkAgainstGeneral(t, text)
		if _, ok := decodeEntryFast([]byte(text)); ok {
			t.Errorf("%s: accepted, but the general codec reads it differently or not at all", name)
		}
	}
}

func BenchmarkEntryDecode(b *testing.B) {
	for _, shape := range []struct{ name, text string }{{"user", sampleUser}, {"assistantToolCall", sampleAssistant}, {"assistantAnswer", sampleAnswer}, {"toolResult", sampleResult}, {"system", sampleSystem}} {
		b.Run(shape.name+"/fast", func(b *testing.B) {
			b.ReportAllocs()
			data := []byte(shape.text)
			for b.Loop() {
				var record EntryRecord
				if err := record.UnmarshalJSON(data); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(shape.name+"/general", func(b *testing.B) {
			b.ReportAllocs()
			data := []byte(shape.text)
			for b.Loop() {
				var record EntryRecord
				if err := record.unmarshalGeneral(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
