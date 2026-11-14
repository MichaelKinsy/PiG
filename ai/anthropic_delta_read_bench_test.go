package ai

import "testing"

// BenchmarkAnthropicDeltaRead reads one content_block_delta of the Anthropic stream: a plain text delta (the fast decode), a delta with an
// escape (the decode by reflection), a delta whose text a server wrote as the escapes of a surrogate pair, and one that ends with the first
// half of such a pair (the decode that keeps the half for the next delta to complete).
func BenchmarkAnthropicDeltaRead(b *testing.B) {
	for _, row := range []struct{ name, text string }{
		{"plain", `Hello, world`}, {"escape", `line\nnext \"quoted\"`}, {"escaped pair", `smile \ud83d\ude00`}, {"first half", `smile \ud83d`},
	} {
		var sse anthropicStreamEvent
		sse.Type = "content_block_delta"
		sse.JSON = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + row.text + `"}}`
		b.Run(row.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var ev anthEventContentBlockDelta
				if err := decodeAnthropicDeltaEvent(sse, &ev); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
