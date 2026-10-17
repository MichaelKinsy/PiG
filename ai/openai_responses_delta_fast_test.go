package ai

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsjson"
)

// responsesDeltaSeeds are delta events as OpenAI, Azure and Codex send them, and near misses the fast decoder must leave to json.Unmarshal.
var responsesDeltaSeeds = []string{
	`{"type":"response.output_text.delta","sequence_number":5,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"Hi","logprobs":[],"obfuscation":"x2"}`,
	`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"summary_index":0,"delta":"**Plan** \"q\" \u00e9\n"}`,
	`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"path\":"}`,
	`{"type":"response.custom_tool_call_input.delta","output_index":3,"delta":"x"}`,
	`{"type":"response.refusal.delta","output_index":0,"delta":"no","logprobs":[{"token":"no","logprob":-0.1,"bytes":[110,111],"top_logprobs":[]}]}`,
	` { "delta" : "a" , "type" : "response.reasoning_text.delta" } `,
	`{"type":"response.output_text.delta","output_index":1,"delta":""}`,
	`{"type":"response.output_text.delta","output_index":1,"delta":null}`,
	`{"type":"response.output_text.delta","output_index":1.0,"delta":"a"}`,
	`{"type":"response.output_text.delta","output_index":"1","delta":"a"}`,
	`{"type":"response.output_text.delta","output_index":-1,"delta":"a"}`,
	`{"type":"response.output_text.delta","Delta":"a","delta":"b"}`,
	`{"type":"response.output_text.delta","delta":"a","delta":"b"}`,
	`{"type":"response.output_text.delta","delta":"a","item":{}}`,
	`{"type":"response.output_item.added","delta":"a"}`,
	`{"type":"response.output_text.delta","delta":"\ud800"}`,
	`{"type":"response.output_text.delta","delta":"a","x":01}`,
	`{"type":"response.output_text.delta","delta":"a"} x`,
	`{"t\u0079pe":"response.output_text.delta","delta":"a"}`,
	`{"type":"response.output_text.delta","delta":"a","x":{"y":[1,{"z":null}],"w":true}}`,
}

// checkResponsesDeltaFast fails when the fast decoder accepts data and json.Unmarshal, as processResponsesStream calls it, decodes another
// event or refuses it.
func checkResponsesDeltaFast(t *testing.T, data string) bool {
	t.Helper()
	fast, ok := parseResponsesDeltaFast(data)
	if !ok {
		return false
	}
	var want respSSEEvent
	if err := json.Unmarshal(jsjson.NormalizeIntegers([]byte(data)), &want); err != nil {
		t.Fatalf("fast decode accepted %q, which json.Unmarshal refuses: %v", data, err)
	}
	if !reflect.DeepEqual(fast, want) {
		t.Fatalf("%q: fast decode %+v, json.Unmarshal %+v", data, fast, want)
	}
	return true
}

// The fast decoder takes the delta events providers send and agrees with json.Unmarshal on every input it accepts: the seeds, and random
// edits of them that splice in bytes, member names and values.
func TestResponsesDeltaFastMatchesUnmarshal(t *testing.T) {
	for i, seed := range responsesDeltaSeeds {
		accepted := checkResponsesDeltaFast(t, seed)
		if wantAccepted := i < 6 || i == len(responsesDeltaSeeds)-2 || i == len(responsesDeltaSeeds)-1; accepted != wantAccepted {
			t.Errorf("seed %d %q: fast decode accepted=%v, want %v", i, seed, accepted, wantAccepted)
		}
	}
	pieces := []string{`"`, `\`, `{`, `}`, `[`, `]`, `:`, `,`, ` `, `0`, `-`, `1.5`, `null`, `true`, `"type"`, `"delta"`, `"Delta"`, `"output_index"`,
		`"item"`, `"x"`, `"response.output_text.delta"`, `\u0041`, `\ud83d\ude00`, "é", "\xff", "\x01"}
	random := rand.New(rand.NewPCG(15, 7))
	accepted := 0
	for range 200000 {
		data := []byte(responsesDeltaSeeds[random.IntN(len(responsesDeltaSeeds))])
		for range 1 + random.IntN(3) {
			at := random.IntN(len(data) + 1)
			switch random.IntN(3) {
			case 0:
				data = append(data[:at:at], append([]byte(pieces[random.IntN(len(pieces))]), data[at:]...)...)
			case 1:
				if at < len(data) {
					data = append(data[:at:at], data[at+1:]...)
				}
			default:
				if at < len(data) {
					data[at] = pieces[random.IntN(len(pieces))][0]
				}
			}
		}
		if checkResponsesDeltaFast(t, string(data)) {
			accepted++
		}
	}
	t.Logf("fast decode accepted %d of 200000 edited events", accepted)
}

func FuzzResponsesDeltaFast(f *testing.F) {
	for _, seed := range responsesDeltaSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		checkResponsesDeltaFast(t, data)
	})
}

// BenchmarkResponsesDeltaDecode decodes an OpenAI text delta event with the fast decoder and with json.Unmarshal of its normalized copy.
func BenchmarkResponsesDeltaDecode(b *testing.B) {
	data := responsesDeltaSeeds[0]
	b.Run("fast", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := parseResponsesDeltaFast(data); !ok {
				b.Fatal("not decoded")
			}
		}
	})
	b.Run("unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var event respSSEEvent
			if err := json.Unmarshal(jsjson.NormalizeIntegers([]byte(data)), &event); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// foldsToResponsesEventField answers as namesAlike does for each field respSSEEvent reads.
func TestResponsesEventFieldFoldMatchesNamesAlike(t *testing.T) {
	fields := []string{"type", "output_index", "response", "item", "delta", "part", "arguments", "input", "error", "code", "message"}
	alphabet := []string{"t", "y", "p", "e", "o", "u", "_", "-", "i", "n", "d", "x", "T", "E", "D", "L", "A", "\u212a", "é", "ſ"}
	random := rand.New(rand.NewPCG(15, 8))
	keys := append(append([]string(nil), fields...), "sequence_number", "item_id", "content_index", "logprobs", "obfuscation", "Output-Index", "TYPE", "__delta__", "")
	for range 100000 {
		var key string
		for range random.IntN(14) {
			key += alphabet[random.IntN(len(alphabet))]
		}
		keys = append(keys, key)
	}
	for _, key := range keys {
		want := false
		for _, field := range fields {
			want = want || namesAlike(key, field)
		}
		if got := foldsToResponsesEventField(key); got != want {
			t.Fatalf("foldsToResponsesEventField(%q) = %v, namesAlike says %v", key, got, want)
		}
	}
}
