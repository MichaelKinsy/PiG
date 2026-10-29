package link_test

import (
	"math"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// Ports the link-level half of packages/chord/test/next/bench/links.ts and compression.ts as Go benchmarks, run by
// hand (`go test -run '^$' -bench . -benchmem ./chord/next/link`): per codec side by side, the wire bytes of a
// message, the encode and decode time and allocations per message, for payloads modelled on p2 (a notes list, a
// 500 KB conversation document, a reply streamed token by token, calls with and without bytes), and compression at
// the origin (the 500 KB snapshot and the 1 MiB result: deflate, synchronous inflate, the asynchronous stream, and
// the origin's choice). The end-to-end rows (commit to frame, hold to live, reconnects, parked calls, memory
// retained per hold, bytes received over a host) need createHost and the documents layer: pending on lanes D4-D6.

func benchEntry(id, length int) any {
	return delta.JsonObjectOf("id", float64(id), "text", strings.Repeat("x", length), "author", "mario", "at", float64(1_700_000_000_000+id))
}

func benchEntries(count, length int) []any {
	entries := make([]any, count)
	for id := range entries {
		entries[id] = benchEntry(id, length)
	}
	return entries
}

// benchMessages are the messages the benchmark of upstream captures per codec.
func benchMessages(b *testing.B) map[string]link.Message {
	b.Helper()
	part := func(value any) link.Part {
		text, err := link.JsonPart(value)
		requireNoError(b, err)
		return text
	}
	notes := part(delta.JsonObjectOf("entries", benchEntries(1_000, 80)))
	document := part(delta.JsonObjectOf("entries", benchEntries(500, 960)))
	bound := func(value link.Part) link.Message {
		return &link.BoundMessage{Holds: []link.BoundEntry{{Hold: 0, Variant: 0, Members: []link.BoundMember{{Member: 0, Value: value}}}}}
	}
	state := func(ops string) link.Message {
		return &link.StateMessage{Holds: []link.StateEntry{{Hold: 0, Members: []link.StateMember{{Member: 0, Ops: link.Text(ops)}}, Retired: link.NoRetired}}}
	}
	megabyte, err := link.PayloadPart(delta.JsonObjectOf("data", make([]byte, 1024*1024)), true)
	requireNoError(b, err)
	return map[string]link.Message{
		"notes list of 1,000 items: snapshot":      bound(notes),
		"500 KB document: snapshot":                bound(document),
		"one appended item: state message":         state(`[["a",["entries"],{"id":1000,"text":"xxxxxxxx","author":"mario","at":1700000001000}]]`),
		"one changed field: state message":         state(`[["s",["entries",500,"text"],"edit 1"]]`),
		"streamed token: state message":            state(`[["i",["entries",0,"text"],7," tok"]]`),
		"call, small argument: call message":       &link.CallMessage{Call: 1, Hold: 0, Method: 0, Args: link.Text(`{"n":1}`)},
		"call, small argument: result message":     &link.ResultMessage{Call: 1, Value: link.Text("2")},
		"call, 1 MiB bytes argument: call message": &link.CallMessage{Call: 1, Hold: 0, Method: 1, Args: link.Text(megabyte.Json), Segments: megabyte.Segments},
	}
}

func BenchmarkCodecs(b *testing.B) {
	for name, message := range benchMessages(b) {
		for _, codec := range codecs() {
			encoded := encode(b, codec, message)
			b.Run(name+"/"+codec.Name()+"/encode", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := codec.Encode(message); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(encoded)), "wire-bytes")
			})
			b.Run(name+"/"+codec.Name()+"/decode", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := codec.Decode(encoded); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

const benchWords = "the a model tool call file read write test error result agent session note code run build"

// prose is about length characters of words from a fixed pseudo-random sequence starting at seed, so text compresses
// like prose and code (about 3x at level 1), not like a repeated string.
func prose(seed, length int) string {
	words := strings.Fields(benchWords)
	state := seed*7919 + 1
	var text strings.Builder
	for text.Len() < length {
		state = (state*1103515245 + 12345) % 2147483648
		word := words[state%len(words)]
		if state%5 == 0 {
			word += string(rune('0' + state%10))
		}
		text.WriteString(word + " ")
	}
	return text.String()[:length]
}

func BenchmarkCompression(b *testing.B) {
	entries := make([]any, 500)
	for id := range entries {
		entries[id] = delta.JsonObjectOf("id", float64(id), "text", prose(id, 960), "author", "mario", "at", float64(1_700_000_000+id))
	}
	snapshot, err := link.JsonPart(delta.JsonObjectOf("entries", entries))
	requireNoError(b, err)
	result, err := link.JsonPart(prose(1, 1024*1024))
	requireNoError(b, err)
	for name, json := range map[string]link.Text{"500 KB snapshot": snapshot, "1 MiB result": result} {
		raw := []byte(json)
		deflated := nextnode.NodeCompression.Deflate(raw)
		b.Run(name+"/deflate at the origin", func(b *testing.B) {
			for b.Loop() {
				nextnode.NodeCompression.Deflate(raw)
			}
			b.ReportMetric(float64(len(raw)), "json-bytes")
			b.ReportMetric(float64(len(deflated)), "deflated-bytes")
		})
		b.Run(name+"/inflate, sync", func(b *testing.B) {
			for b.Loop() {
				nextnode.NodeCompression.Inflate(deflated, math.Inf(1))
			}
		})
		b.Run(name+"/inflate, async stream", func(b *testing.B) {
			meter := background
			for b.Loop() {
				link.InflateAsync(link.NewDeflatedPart(deflated), math.Inf(1), background, meter)
			}
		})
		b.Run(name+"/origin part", func(b *testing.B) {
			for b.Loop() {
				link.OriginPart(string(json), nextnode.NodeCompression, background)
			}
		})
	}
}
