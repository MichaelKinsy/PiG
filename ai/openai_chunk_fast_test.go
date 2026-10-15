package ai

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsjson"
)

func chunkSeeds() []string {
	return []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"id": "fake", "object": "chat.completion.chunk", "model": "fake-model", "choices": [{"index": 0, "delta": {"content": "line\nbreak \"quoted\" \\ \u00e9 \ud83d\ude00 \u2028"}, "finish_reason": null}]}`,
		`{"id":"x","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null,"logprobs":null}],"usage":null,"system_fingerprint":"fp_1","service_tier":"default","obfuscation":"abc"}`,
		`{"id":"x","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"x","model":"m","choices":[]}`,
		`{"id":"x","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think","reasoning":null,"reasoning_text":"t"},"finish_reason":null}]}`,
		` {"choices":[{"delta":{"content":"a"}}, {"index":1,"delta":{"content":"b"}}] , "id":"x" } `,
		`{"ID":"upper","Model":"m","Choices":[{"Delta":{"Content":"folded"}}]}`,
		`{"id":"x","usage":{"prompt_tokens":1},"choices":[]}`,
		`{"id":"x","error":{"message":"boom"}}`,
		`{"id":"x","choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"n","arguments":"{}"}}]}}]}`,
		`{"id":"x","id":"y","choices":[]}`,
		`{"id":"x","choices":[{"delta":{"content":"a","content":"b"}}]}`,
		`{"id":"x","choices":[{"delta":{"content":"\ud800"}}]}`,
		`{"id":"x","choices":[{"delta":{"content":"\ud83d"}}]}`,
		`{"id":"x","choices":[{"delta":{"content":"\ude00"}}]}`,
		`{"id":"x","choices":[{"delta":{"content":"tab	raw"}}]}`,
		"{\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"bad \xff utf8\"}}]}",
		`{"id":"x","choices":[{"index":1.0,"delta":{}}]}`,
		`{"id":"x","choices":[{"index":01,"delta":{}}]}`,
		`{"id":"x","choices":[{"index":-0,"delta":{}}]}`,
		`{"id":"x","created":1e400,"choices":[]}`,
		`{"id":"x","created":-,"choices":[]}`,
		`{"id":"x","choices":[],}`,
		`{"id":"x","choices":[{"delta":{"content":"a"},}]}`,
		`{"id":"x"} trailing`,
		`{"id":7,"choices":[]}`,
		`{"id":null,"model":null,"choices":[]}`,
		`{"model":"a\"b\\c","choices":[]}`,
		`{"reasoning-content":"x","choices":[{"delta":{"reasoning-content":"y","Reasoning_Text":"z"}}]}`,
		`{"choices":[{"delta":{"c\u006fntent":"escaped key"}}]}`,
		`{"\u0069d":"escaped id"}`,
		`{"choices":null}`,
		`{"choices":[null]}`,
		`{"choices":[{"delta":null}]}`,
		`{"ID":"folded","MODEL":"m"}`,
		`{"Model":"m","choices":[]}`,
		`{"id":"x","choices":[{"delta":{"content":"\ude00\ude00"}}]}`,
		`{"id":"x","choices":[{"delta":{"role":"assistant"},"delta":{"content":"x"}}]}`,
		`{"id":"x","choices":[{"finish_reason":"a","finish_reason":"b","delta":{}}]}`,
		`{"id":"x","choices":[{"index":1,"index":2,"delta":{}}]}`,
		`{"id":"x","choices":[{"delta":{},"usage":null}]}`,
		`{"id":"x","error":null}`,
		`{"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\": \"a\\nb\"}"}}]},"finish_reason":null}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"x"}},{"index":1,"id":"d","function":{"name":"n","arguments":"y"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":null}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":null}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"custom":{"name":"g","input":"x"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"a","arguments":"b"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"n"},"function":{"arguments":"b"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"INDEX":2,"ID":"x","Function":{"NAME":"n","Arguments":"b"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":1.0,"function":{"arguments":"a"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":5}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[null]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"a"},"extra":1}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"a"}}],"tool_calls":[]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"NAME":"n","Arguments":"b"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"ID":"x","function":{"name":"n"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"Type":"x","function":{"name":"n"}}]}}]}`,
		`[]`, `{}`, ``, `{`, `null`,
	}
}

func checkChunkFast(t testing.TB, data string) {
	t.Helper()
	got, ok := parseOpenAIChunkFast(data)
	if !ok {
		return
	}
	var want oaiChunk
	if err := json.Unmarshal(jsjson.NormalizeIntegers([]byte(data)), &want); err != nil {
		t.Fatalf("the fast path accepts %q, json.Unmarshal rejects it: %v", data, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q\n fast %#v\n json %#v", data, got, want)
	}
}

// TestParseOpenAIChunkFastEqualsJSON: wherever the fast chunk decode answers, it is json.Unmarshal's chunk (and
// json.Unmarshal succeeds); it answers false for everything it does not understand.
func TestParseOpenAIChunkFastEqualsJSON(t *testing.T) {
	seeds := chunkSeeds()
	for _, data := range seeds {
		checkChunkFast(t, data)
	}
	rng := rand.New(rand.NewSource(11))
	alphabet := []string{`"`, `\`, `{`, `}`, `[`, `]`, `,`, `:`, ` `, `n`, `u`, `0`, `-`, `.`, `e`, "\n", "\x00", "\xff", `"id"`, `"content"`, `\u00`, `\ud83d`, `null`, `true`, `1`}
	for range 200000 {
		data := seeds[rng.Intn(len(seeds))]
		for range 1 + rng.Intn(3) {
			at := rng.Intn(len(data) + 1)
			switch rng.Intn(3) {
			case 0:
				data = data[:at] + alphabet[rng.Intn(len(alphabet))] + data[at:]
			case 1:
				if at < len(data) {
					data = data[:at] + data[at+1:]
				}
			default:
				if at < len(data) {
					data = data[:at] + alphabet[rng.Intn(len(alphabet))] + data[at+1:]
				}
			}
		}
		checkChunkFast(t, data)
	}
}

func FuzzParseOpenAIChunkFast(f *testing.F) {
	for _, data := range chunkSeeds() {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data string) { checkChunkFast(t, data) })
}

// TestParseOpenAIChunkFastTakesTheCommonChunks pins the shapes the fast path exists for: a text delta, with and without
// the members providers add, an empty delta, the finish chunk, and a usage-free empty choices chunk.
func TestParseOpenAIChunkFastTakesTheCommonChunks(t *testing.T) {
	for _, data := range chunkSeeds()[:7:7] {
		if _, ok := parseOpenAIChunkFast(data); !ok {
			t.Errorf("not decoded by the fast path: %q", data)
		}
	}
	for _, data := range chunkSeeds()[7:] {
		if strings.Contains(data, `"ID"`) {
			if _, ok := parseOpenAIChunkFast(data); ok {
				t.Errorf("a case-folded member name is decoded by the fast path: %q", data)
			}
		}
	}
}

func BenchmarkOpenAIChunkDecode(b *testing.B) {
	data := chunkSeeds()[1]
	b.Run("fast", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			parseOpenAIChunkFast(data)
		}
	})
	b.Run("json", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			var c oaiChunk
			_ = json.Unmarshal([]byte(data), &c)
		}
	})
}

// TestParseOpenAIChunkFastTakesToolCallChunks: the chunks that carry a function call's name and arguments, the bulk of a
// streamed write, are decoded by the fast path.
func TestParseOpenAIChunkFastTakesToolCallChunks(t *testing.T) {
	taken := 0
	for _, data := range chunkSeeds() {
		if !strings.Contains(data, `"call_1"`) && !strings.Contains(data, `\\nb`) && !strings.Contains(data, `"id":"d"`) {
			continue
		}
		taken++
		chunk, ok := parseOpenAIChunkFast(data)
		if !ok {
			t.Errorf("a tool call chunk is not decoded by the fast path: %q", data)
			continue
		}
		if len(chunk.Choices) == 0 || len(chunk.Choices[0].Delta.ToolCalls) == 0 {
			t.Errorf("no tool call decoded from %q", data)
		}
	}
	if taken != 3 {
		t.Fatalf("%d tool call seeds were found, want 3", taken)
	}
}
