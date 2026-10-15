package tools

import (
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
			whole := utf8StreamDecoder{stripBOM: stripBOM}
			want := whole.decode(data, false)
			streamed := utf8StreamDecoder{stripBOM: stripBOM}
			var got []byte
			rest := data
			for len(rest) > 0 {
				n := 1 + rng.IntN(len(rest))
				got = streamed.appendDecode(got, rest[:n], true)
				rest = rest[n:]
			}
			got = streamed.appendDecode(got, nil, false)
			if string(got) != want {
				t.Fatalf("run %d stripBOM %v: %x decoded in chunks to %q, at once to %q", run, stripBOM, data, got, want)
			}
		}
	}
}
