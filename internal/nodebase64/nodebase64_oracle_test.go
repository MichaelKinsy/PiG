package nodebase64

import (
	"encoding/hex"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Buffer.from(text, "base64") over random texts mixing both alphabets, padding anywhere, whitespace, ASCII punctuation, NUL and non-ASCII characters, at every length
// modulo four, checked against the bytes Node produces.
func TestDecodeMatchesNodeBuffer(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	pieces := []string{"+", "/", "-", "_", "=", "==", "=", "\n", "\r\n", " ", "\t", "!", "*", "\x00", "\u00e9", "\u2028", "😀", "\ufeff", "%", ".", "\u0141", "\u013d", "\u0100", "\u0241", "\u202b", "\u0e3d", "\U0001d400", "\U0001f63d"}
	var inputs []string
	for range 20000 {
		var text []byte
		for range rng.Intn(24) {
			if rng.Intn(5) == 0 {
				text = append(text, pieces[rng.Intn(len(pieces))]...)
			} else {
				text = append(text, letters[rng.Intn(len(letters))])
			}
		}
		inputs = append(inputs, string(text))
	}
	var want []string
	pioracle.Run(t, `emit(input.map((text) => Buffer.from(text, "base64").toString("hex")));`, inputs, &want)
	for i, text := range inputs {
		if got := hex.EncodeToString(Decode(text)); got != want[i] {
			t.Fatalf("Decode(%q) = %s, Node gives %s", text, got, want[i])
		}
		if Decode(text) == nil {
			t.Fatalf("Decode(%q) = nil, want a non-nil slice", text)
		}
	}
}
