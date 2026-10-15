package tui

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestEditorWordWrapLineMatchesPi compares wordWrapLine (components/editor.ts) with pinned Pi over seeded lines of letters, spaces, tabs, wide, emoji and combining
// graphemes at widths 1 to 7: every chunk's text, startIndex and endIndex must agree. Where Pi throws RangeError (a single grapheme wider than the width), PiG keeps
// the grapheme as one chunk (D159): the chunks must rejoin to the line, and a chunk wider than the width must be that one grapheme.
func TestEditorWordWrapLineMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(159))
	atoms := []string{"a", "b", "x", " ", "  ", "\t", "-", "日", "本", "😀", "👍🏽", "é", "\u0301"}
	type wrapCase struct {
		line  string
		width int
	}
	var cases []wrapCase
	var input [][2]any
	for range 2500 {
		var line strings.Builder
		for range rng.Intn(14) {
			line.WriteString(atoms[rng.Intn(len(atoms))])
		}
		c := wrapCase{line.String(), 1 + rng.Intn(7)}
		cases = append(cases, c)
		input = append(input, [2]any{c.line, c.width})
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_word_wrap.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(encoded)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []json.RawMessage
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(cases) {
		t.Fatalf("Pi returned %d results for %d cases", len(want), len(cases))
	}
	type chunkJSON struct {
		Text       string `json:"text"`
		StartIndex int    `json:"startIndex"`
		EndIndex   int    `json:"endIndex"`
	}
	failures, thrown := 0, 0
	for i, c := range cases {
		got := []chunkJSON{}
		joined := ""
		for _, chunk := range wordWrapLine(c.line, c.width, nil) {
			got = append(got, chunkJSON{chunk.text, chunk.startIndex, chunk.endIndex})
			joined += chunk.text
		}
		if string(want[i]) == `"RangeError"` {
			thrown++
			if joined != c.line {
				t.Errorf("%q at width %d: chunks %+v rejoin to %q (D159 keeps the over-wide grapheme as one chunk)", c.line, c.width, got, joined)
			}
			for _, chunk := range got {
				if widthx.VisibleWidth(chunk.Text) > c.width && len(editorSegments(graphemeSegmentData(chunk.Text), chunk.Text)) != 1 {
					t.Errorf("%q at width %d: chunk %q is wider than the width but is not one grapheme (D159)", c.line, c.width, chunk.Text)
				}
			}
			continue
		}
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var compact bytes.Buffer
		if json.Compact(&compact, want[i]) != nil || !bytes.Equal(gotJSON, compact.Bytes()) {
			if failures++; failures <= 5 {
				t.Errorf("%q at width %d:\n PiG %s\n Pi  %s", c.line, c.width, gotJSON, want[i])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d cases differ from Pi", failures, len(cases))
	}
	if thrown == 0 || thrown > len(cases)/2 {
		t.Fatalf("Pi threw for %d of %d cases; the corpus must reach both the D159 case and ordinary wrapping", thrown, len(cases))
	}
}
