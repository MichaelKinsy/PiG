package ai

import (
	"math/rand/v2"
	"testing"
)

// Random byte streams built from BOMs, multi-byte characters, U+FFFD, surrogate encodings, overlong and truncated sequences, split at random points: each decode call and the final flush return what the rune-by-rune decode returns.
func TestWebTextDecoderMatchesRuneDecode(t *testing.T) {
	pieces := [][]byte{[]byte("\uFEFF"), []byte("a"), []byte("data: {\"x\":1}\n\n"), []byte("é"), []byte("€"), []byte("😀"), []byte("\uFFFD"), {0xed, 0xa0, 0x80}, {0xc0, 0xaf}, {0xe2, 0x82}, {0xf0, 0x9f}, {0x80}, {0xff}, []byte("\r\n")}
	random := rand.New(rand.NewPCG(1, 2))
	for run := range 20000 {
		var stream []byte
		for range random.IntN(12) {
			stream = append(stream, pieces[random.IntN(len(pieces))]...)
		}
		var fast webTextDecoder
		var reference referenceWebTextDecoder
		for len(stream) > 0 {
			cut := random.IntN(len(stream) + 1)
			chunk := stream[:cut]
			stream = stream[cut:]
			if got, want := fast.decode(chunk, true), reference.decode(chunk, true); got != want {
				t.Fatalf("run %d: decode(%q) = %q, want %q", run, chunk, got, want)
			}
		}
		if got, want := fast.decode(nil, false), reference.decode(nil, false); got != want || fast.started != reference.started {
			t.Fatalf("run %d: flush = %q (started %v), want %q (started %v)", run, got, fast.started, want, reference.started)
		}
	}
}

// A well-formed chunk with nothing carried over costs one allocation, its string.
func TestWebTextDecoderWellFormedChunkAllocations(t *testing.T) {
	chunk := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"héllo wörld\"}}\n\n")
	var decoder webTextDecoder
	if allocs := testing.AllocsPerRun(100, func() { _ = decoder.decode(chunk, true) }); allocs > 1 {
		t.Fatalf("decode cost %.0f allocations, want 1", allocs)
	}
}
