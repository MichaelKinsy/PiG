package ai

import (
	"math/rand"
	"reflect"
	"testing"
)

func anthropicDeltaSeeds() []string {
	return []string{
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type": "content_block_delta", "index": 3, "delta": {"type": "text_delta", "text": "line\nbreak \"q\" \u00e9 \ud83d\ude00"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"hm"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":null}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":5}}`,
		`{"type":"content_block_delta","index":"0","delta":{"type":"text_delta","text":"a"}}`,
		`{"type":"content_block_delta","index":1.5,"delta":{"type":"text_delta","text":"a"}}`,
		`{"type":"content_block_delta","index":1e0,"delta":{"type":"text_delta","text":"a"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"a"}}`,
		`{"type":"content_block_delta","index":0,"delta":null}`,
		`{"type":"content_block_delta","index":0}`,
		`{"type":"content_block_delta","index":0,"delta":{}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":7,"text":"a"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"unknown_delta","text":"a"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a","thinking":"b"}}`,
		`{"type":"content_block_delta","index":0,"index":1,"delta":{"type":"text_delta","text":"a"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"},"delta":{"type":"thinking_delta","thinking":"b"}}`,
		`{"type":"content_block_delta","INDEX":2,"delta":{"type":"text_delta","text":"a"}}`,
		`{"type":"content_block_delta","index":0,"Delta":{"Type":"text_delta","TEXT":"a"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","partial-json":"a","text":"b"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\ud800"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"}} x`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a",}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"},"extra":null,"n":-1.5e3,"t":true}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"},"extra":{"a":1}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"b","THINKING":"x","Partial_JSON":"y"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a","text":"b"}}`,
		`{"type":"ping"}`, `[]`, `{}`, ``, `null`, `{"index":0,"delta":{"type":"text_delta","text":"a"}}`,
	}
}

func checkAnthropicDeltaFast(t testing.TB, data string) {
	t.Helper()
	got, ok := decodeAnthropicDeltaFast(data)
	if !ok {
		return
	}
	var want anthEventContentBlockDelta
	if err := decodeAnthropicDeltaEventReflect(anthropicStreamEvent{JSON: data}, &want); err != nil {
		t.Fatalf("the fast path accepts %q, the reflective decode fails: %v", data, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q\n fast %#v\n full %#v", data, got, want)
	}
}

// TestDecodeAnthropicDeltaFastEqualsTheFullDecode: wherever the fast decode answers, it is the reflective decode's result.
func TestDecodeAnthropicDeltaFastEqualsTheFullDecode(t *testing.T) {
	seeds := anthropicDeltaSeeds()
	for _, data := range seeds {
		checkAnthropicDeltaFast(t, data)
	}
	rng := rand.New(rand.NewSource(13))
	alphabet := []string{`"`, `\`, `{`, `}`, `,`, `:`, ` `, `n`, `0`, `-`, `.`, `e`, "\n", "\xff", `"index"`, `"text"`, `\ud83d`, `null`, `1`}
	for range 100000 {
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
		checkAnthropicDeltaFast(t, data)
	}
}

func FuzzDecodeAnthropicDeltaFast(f *testing.F) {
	for _, data := range anthropicDeltaSeeds() {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data string) { checkAnthropicDeltaFast(t, data) })
}

// TestDecodeAnthropicDeltaFastTakesTheCommonRecords pins what the fast path exists for.
func TestDecodeAnthropicDeltaFastTakesTheCommonRecords(t *testing.T) {
	seeds := anthropicDeltaSeeds()
	for _, data := range seeds[:5] {
		if _, ok := decodeAnthropicDeltaFast(data); !ok {
			t.Errorf("not decoded by the fast path: %q", data)
		}
	}
	for _, data := range seeds[5:8] {
		if _, ok := decodeAnthropicDeltaFast(data); ok {
			t.Errorf("an empty or absent member is decoded by the fast path, which skips the second look: %q", data)
		}
	}
}
