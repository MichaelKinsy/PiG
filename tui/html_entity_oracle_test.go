package tui

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/utils/html.ts

// decodeHtmlEntityAt (html.ts:9-60) over generated entity bodies (named, decimal, hex with sign, 0x, whitespace, trailing junk, lone surrogates,
// 16-unit limit) and prefixes of multi-byte text: the pinned Pi and PiG agree on the decoded text and the length in UTF-16 units.
func TestDecodeHTMLEntityAtMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	pieces := []string{"amp", "lt", "gt", "quot", "apos", "AMP", "#", "x", "X", "0x", "0", "1", "9", "65", "a", "F", "g", "z", "+", "-", " ", "\t", "é", "😀", "e", ".", "99999999999999999999", "55296", "D800", "10FFFF", "110000", "nbsp", "&", ";"}
	prefixes := []string{"", "a", "é", "😀", "x&y"}
	var cases [][2]any
	var inputs []string
	var indexes []int
	for range 4000 {
		var body strings.Builder
		switch rng.Intn(4) {
		case 0:
			body.WriteString([]string{"amp", "lt", "gt", "quot", "apos"}[rng.Intn(5)])
		case 1:
			body.WriteString("#" + []string{"", "+", "-", " ", "0x", "00"}[rng.Intn(6)] + []string{"65", "9", "1114111", "55296", "55297", "57343", "128512", "0", "12abc", "1e3"}[rng.Intn(10)])
		case 2:
			body.WriteString("#" + []string{"x", "X"}[rng.Intn(2)] + []string{"", "+", "0x", " "}[rng.Intn(4)] + []string{"41", "1F600", "d800", "d801", "dfff", "10ffff", "110000", "zz", "1g"}[rng.Intn(9)])
		}
		for range rng.Intn(3) {
			body.WriteString(pieces[rng.Intn(len(pieces))])
		}
		prefix := prefixes[rng.Intn(len(prefixes))]
		suffix := []string{"", "tail", ";", "é;"}[rng.Intn(4)]
		text := prefix + "&" + body.String() + ";" + suffix
		byteIndex := len(prefix)
		cases = append(cases, [2]any{text, len(utf16.Encode([]rune(prefix)))})
		inputs = append(inputs, text)
		indexes = append(indexes, byteIndex)
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/html_entity.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []*struct {
		Text   string `json:"text"`
		Length int    `json:"length"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	decoded := 0
	for i, text := range inputs {
		got, length, ok := decodeHTMLEntityAt(text, indexes[i])
		if want[i] == nil {
			if ok {
				t.Errorf("decodeHTMLEntityAt(%q, %d) = %q, Pi has no entity", text, indexes[i], got)
			}
			continue
		}
		decoded++
		// Pi's length counts UTF-16 units; PiG's counts bytes of the same reference.
		units := len(utf16.Encode([]rune(text[indexes[i] : indexes[i]+length])))
		// Node writes a lone surrogate as U+FFFD; PiG keeps it as WTF-8.
		got = strings.ToValidUTF8(got, "\ufffd")
		if !ok || got != want[i].Text || units != want[i].Length {
			t.Errorf("decodeHTMLEntityAt(%q, %d) = %q, %d units, %v; Pi %q, %d", text, indexes[i], got, units, ok, want[i].Text, want[i].Length)
		}
	}
	if decoded < 200 {
		t.Fatalf("only %d of %d inputs decode; the generator misses the entity forms", decoded, len(inputs))
	}
}
