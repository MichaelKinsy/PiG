package jsstring

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// referenceUnescapeForStringify is UnescapeForStringify as it was written before it copied spans: one byte at a time.
func referenceUnescapeForStringify(encoded []byte) []byte {
	if !bytes.Contains(encoded, []byte(`\u00`)) && !bytes.Contains(encoded, []byte(`\u202`)) {
		return encoded
	}
	output := make([]byte, 0, len(encoded))
	for i := 0; i < len(encoded); i++ {
		c := encoded[i]
		if c != '\\' || i+1 >= len(encoded) {
			output = append(output, c)
			continue
		}
		if i+6 <= len(encoded) {
			var replacement string
			switch string(encoded[i : i+6]) {
			case `\u003c`, `\u003C`:
				replacement = "<"
			case `\u003e`, `\u003E`:
				replacement = ">"
			case `\u0026`:
				replacement = "&"
			case `\u2028`:
				replacement = "\u2028"
			case `\u2029`:
				replacement = "\u2029"
			}
			if replacement != "" {
				output = append(output, replacement...)
				i += 5
				continue
			}
		}
		output = append(output, c, encoded[i+1])
		i++
	}
	return output
}

// TestUnescapeForStringifyMatchesTheByteLoop compares the span copy with the byte loop it replaced on random text built from escapes, parts
// of escapes, backslash runs and plain bytes, including a backslash at the end.
func TestUnescapeForStringifyMatchesTheByteLoop(t *testing.T) {
	pieces := []string{
		`\u003c`, `\u003C`, `\u003e`, `\u003E`, `\u0026`, `\u2028`, `\u2029`, `\u001b`, `\u00`, `\u202`, `\u20`, `\`, `\\`, `\"`, `\n`,
		"u", "0", "2", "3", "c", "x", "é", " ",
	}
	random := rand.New(rand.NewPCG(46, 47))
	for range 50000 {
		var encoded []byte
		for range random.IntN(16) {
			encoded = append(encoded, pieces[random.IntN(len(pieces))]...)
		}
		want := referenceUnescapeForStringify(bytes.Clone(encoded))
		if got := UnescapeForStringify(bytes.Clone(encoded)); !bytes.Equal(got, want) {
			t.Fatalf("UnescapeForStringify(%q) = %q, byte loop %q", encoded, got, want)
		}
	}
}
