package jsstring

import (
	"encoding/hex"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// FromUTF8 against Node's Buffer.toString("utf8") (the WHATWG decoder with replacement) over random byte strings built from valid sequences, every kind of truncation, overlong and surrogate encodings, and stray continuation bytes.
func TestFromUTF8MatchesNode(t *testing.T) {
	rng := rand.New(rand.NewSource(20261010))
	pieces := [][]byte{
		[]byte("a"), []byte("\n"), {0x7f}, {0xc2, 0xa9}, {0xe6, 0x9c, 0xac}, {0xf0, 0x9f, 0x98, 0x80}, {0xc0, 0x80}, {0xc1, 0xbf}, {0xe0, 0x80, 0x80}, {0xe0, 0x9f, 0xbf}, {0xe0, 0xa0, 0x80},
		{0xed, 0xa0, 0x80}, {0xed, 0x9f, 0xbf}, {0xf0, 0x80, 0x80, 0x80}, {0xf0, 0x8f, 0xbf, 0xbf}, {0xf0, 0x90, 0x80, 0x80}, {0xf4, 0x8f, 0xbf, 0xbf}, {0xf4, 0x90, 0x80, 0x80}, {0xf5, 0x80, 0x80, 0x80},
		{0xff}, {0xfe}, {0x80}, {0xbf}, {0xc2}, {0xe6}, {0xe6, 0x9c}, {0xf0}, {0xf0, 0x9f}, {0xf0, 0x9f, 0x98}, {0xef, 0xbb, 0xbf}, {0xef, 0xbf, 0xbd},
	}
	var inputs []string
	for range 6000 {
		var data []byte
		for range 1 + rng.Intn(8) {
			piece := pieces[rng.Intn(len(pieces))]
			if rng.Intn(4) == 0 {
				piece = piece[:rng.Intn(len(piece)+1)]
			}
			data = append(data, piece...)
		}
		if len(data) > 0 && rng.Intn(5) == 0 {
			data[rng.Intn(len(data))] = byte(rng.Intn(256))
		}
		inputs = append(inputs, hex.EncodeToString(data))
	}
	var want []string
	pioracle.Run(t, `emit(input.map((text) => Buffer.from(text, "hex").toString("utf8")));`, inputs, &want)
	for i, text := range inputs {
		data, _ := hex.DecodeString(text)
		if got := FromUTF8(data); got != want[i] {
			t.Errorf("FromUTF8(%s) = %q, Node %q", text, got, want[i])
		}
	}
}
