package utf8stream

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// The decoder appends a well-formed chunk prefix that ends at a character boundary as it is and walks only the rest, so the split points of a stream
// change which path a byte takes. Decoding any bytes in any chunks must still equal decoding them at once: valid text, characters split across chunks,
// invalid and truncated sequences, surrogates, overlongs and a leading BOM.
func TestUTF8StreamDecoderChunkingMatchesWholeDecode(t *testing.T) {
	pieces := [][]byte{
		[]byte("ascii "), []byte("\n"), []byte("é"), []byte("日本"), []byte("😀"), []byte("\uFEFF"), {0xEF, 0xBB}, {0xBF},
		{0xFF}, {0x80}, {0xC0, 0xAF}, {0xED, 0xA0, 0x80}, {0xF4, 0x90, 0x80, 0x80}, {0xE2, 0x82}, {0xF0, 0x9F}, {0xC3},
	}
	rng := rand.New(rand.NewPCG(3, 5))
	for run := range 5000 {
		var data []byte
		for range rng.IntN(12) {
			data = append(data, pieces[rng.IntN(len(pieces))]...)
		}
		for _, stripBOM := range []bool{true, false} {
			whole := Decoder{StripBOM: stripBOM}
			want := whole.Decode(data, false)
			streamed := Decoder{StripBOM: stripBOM}
			var got []byte
			rest := data
			for len(rest) > 0 {
				n := 1 + rng.IntN(len(rest))
				got = streamed.AppendDecode(got, rest[:n], true)
				rest = rest[n:]
			}
			got = streamed.AppendDecode(got, nil, false)
			if string(got) != want {
				t.Fatalf("run %d stripBOM %v: %x decoded in chunks to %q, at once to %q", run, stripBOM, data, got, want)
			}
		}
	}
}

// Passthrough takes a chunk as decoded only when it is its own decoding, and leaves the decoder as AppendDecode would.
func TestUTF8StreamDecoderPassthroughMatchesAppendDecode(t *testing.T) {
	pieces := [][]byte{
		[]byte("ascii "), []byte("\n"), []byte("é"), []byte("日本"), []byte("😀"), []byte("\uFEFF"), {0xFF}, {0xE2, 0x82}, {0xF0, 0x9F}, {0xC3},
	}
	rng := rand.New(rand.NewPCG(7, 11))
	for run := range 5000 {
		for _, stripBOM := range []bool{true, false} {
			taking, decoding := Decoder{StripBOM: stripBOM}, Decoder{StripBOM: stripBOM}
			var got, want []byte
			for range 1 + rng.IntN(5) {
				var chunk []byte
				for range rng.IntN(5) {
					chunk = append(chunk, pieces[rng.IntN(len(pieces))]...)
				}
				if taking.Passthrough(chunk) {
					got = append(got, chunk...)
				} else {
					got = taking.AppendDecode(got, chunk, true)
				}
				want = decoding.AppendDecode(want, chunk, true)
			}
			got = taking.AppendDecode(got, nil, false)
			want = decoding.AppendDecode(want, nil, false)
			if !bytes.Equal(got, want) {
				t.Fatalf("run %d stripBOM %v: %q, want %q", run, stripBOM, got, want)
			}
		}
	}
}
