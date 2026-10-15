package tools

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// A BOM that completes after earlier calls produced no output is left out of the corpus: Node's TextDecoder (ICU) then drops further U+FEFF code points
// in ways that depend on where its converter consumed the pending bytes ([e6] then [ef bb bf 42] decodes to U+FFFD 42, [e6] then [c3 ef bb bf] keeps the
// BOM), which the WHATWG decoder PiG follows does not do; the difference is numbered in the divergence question of the handoff.
//
// utf8StreamDecoder{stripBOM: true} against Node's TextDecoder with { stream: true }, chunk by chunk: random byte strings built from BOM bytes, partial and
// complete sequences, invalid lead and continuation bytes and ASCII, split at random points, then the final flush. The BOM handling is Node's (ICU-backed),
// which agrees with the WHATWG decoder except in that case.
func TestUTF8StreamDecoderMatchesNodeTextDecoder(t *testing.T) {
	rng := rand.New(rand.NewSource(61))
	alphabet := [][]byte{{0xef}, {0xbb}, {0xbf}, {0xef, 0xbb, 0xbf}, {0xef, 0xbb}, {0xe6}, {0x41}, {0x42}, {0xc3}, {0xa9}, {0xc3, 0xa9}, {0xf0, 0x9f}, {0x98, 0x80}, {0xff}, {0xc0}, {0xed}, {0xa0}, {0xe2, 0x82}, {0xac}, {0x0a}, {0xe0}, {0x80}, {0x9f}, {0xf4}, {0x90}, {0xf8}, {0xe0, 0x80}, {0xf0, 0x80}}
	type probe struct {
		Chunks [][]int `json:"chunks"`
	}
	var probes []probe
	// Excluded awaiting owner sign-off (handoffs/owner-signoff.md, "bash/tool output UTF-8 stream decoding of a split byte-order mark"): Node's ICU decoder sometimes drops this BOM and sometimes keeps it, PiG follows WHATWG. No D record or marker until the owner decides.
	// pendingBOM reports whether a call starts with bytes pending from earlier calls that produced no output and carries the bytes of a BOM.
	pendingBOM := func(chunks [][]int) bool {
		d := utf8StreamDecoder{stripBOM: true}
		produced := false
		for i, chunk := range chunks {
			bytes := make([]byte, len(chunk))
			for j, c := range chunk {
				bytes[j] = byte(c)
			}
			if i > 0 && !produced && d.bytesNeeded != 0 && strings.Contains(string(bytes), "\xef\xbb\xbf") {
				return true
			}
			if d.decode(bytes, true) != "" {
				produced = true
			}
		}
		return false
	}
	for len(probes) < 3000 {
		var data []byte
		for range 1 + rng.Intn(6) {
			data = append(data, alphabet[rng.Intn(len(alphabet))]...)
		}
		var chunks [][]int
		for len(data) > 0 {
			n := 1 + rng.Intn(len(data))
			if rng.Intn(2) == 0 {
				n = 1 + rng.Intn(min(len(data), 3))
			}
			chunk := make([]int, n)
			for i := range chunk {
				chunk[i] = int(data[i])
			}
			chunks = append(chunks, chunk)
			data = data[n:]
		}
		if !pendingBOM(chunks) {
			probes = append(probes, probe{Chunks: chunks})
		}
	}
	var want [][]string
	pioracle.Run(t, `
emit(input.map((p) => {
	const d = new TextDecoder();
	const out = p.chunks.map((chunk) => d.decode(Uint8Array.from(chunk), { stream: true }));
	out.push(d.decode());
	return out;
}));`, probes, &want)
	bad := 0
	for i, p := range probes {
		d := utf8StreamDecoder{stripBOM: true}
		var got []string
		for _, chunk := range p.Chunks {
			bytes := make([]byte, len(chunk))
			for j, c := range chunk {
				bytes[j] = byte(c)
			}
			got = append(got, d.decode(bytes, true))
		}
		got = append(got, d.decode(nil, false))
		if !reflect.DeepEqual(got, want[i]) {
			if bad++; bad <= 8 {
				t.Errorf("probe %d chunks % x:\n got %+q\nwant %+q", i, p.Chunks, got, want[i])
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
