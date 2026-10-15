package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// visual-truncate.ts truncateToVisualLines and VisualLinePreview.render (hint placement and its truncation to the width) against pinned Pi: wrapped, multi-line, empty and styled text, both ends, paddings and
// limits including 0 and a limit above the line count.
func TestTruncateToVisualLinesMatchesPi(t *testing.T) {
	type probe struct {
		Text  string `json:"text"`
		Max   int    `json:"max"`
		Width int    `json:"width"`
		Pad   int    `json:"pad"`
		Keep  string `json:"keep"`
	}
	texts := []string{"", "one", "a\nb\nc\nd\ne", strings.Repeat("word ", 30), "x\n\ny\n\n\nz", "\x1b[31mred text that wraps around the width\x1b[39m\nsecond", "界🙂界🙂界🙂界🙂界🙂", "\n\n", "tab\tseparated\ttext"}
	var probes []probe
	for _, text := range texts {
		for _, max := range []int{0, 1, 2, 3, 10} {
			for _, width := range []int{8, 20} {
				for _, pad := range []int{0, 1} {
					for _, keep := range []string{"end", "start"} {
						probes = append(probes, probe{text, max, width, pad, keep})
					}
				}
			}
		}
	}
	// Random texts: pieces joined with and without newlines (CR, LF, CRLF, U+2028), wide, combining, ANSI, tab and very long text at widths from 1 to 100.
	rng := rand.New(rand.NewSource(20261024))
	pieces := []string{"", "x", "word ", "line\n", "\r\n", "\r", "\u2028", "\t", "日本語", "e\u0301", "😀", "\x1b[31mred\x1b[0m", "\x1b[1m", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", strings.Repeat("w", 90), "  ", "\n\n", "a-b_c ", "\x00", "\x1b[2K"}
	for range 600 {
		var b strings.Builder
		for range rng.Intn(12) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		probes = append(probes, probe{b.String(), rng.Intn(12), 1 + rng.Intn(100), rng.Intn(4), []string{"end", "start"}[rng.Intn(2)]})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/visual_truncate.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		VisualLines  []string `json:"visualLines"`
		SkippedCount int      `json:"skippedCount"`
		Preview      []string `json:"preview"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, p := range probes {
		preview := NewVisualLinePreview(VisualLinePreviewOptions{Text: p.Text, MaxVisualLines: p.Max, Keep: VisualKeep(p.Keep), FormatHint: func(hidden int) string {
			return fmt.Sprintf("\x1b[2m... %d more visual lines, expand to see all of them at once\x1b[22m", hidden)
		}}).Render(p.Width)
		if preview == nil {
			preview = []string{}
		}
		if !reflect.DeepEqual(preview, expected[i].Preview) {
			if failures++; failures <= 5 {
				t.Errorf("preview %+v:\n  Pig %q\n  Pi  %q", p, preview, expected[i].Preview)
			}
		}
		got := TruncateToVisualLinesKeeping(p.Text, p.Max, p.Width, p.Pad, VisualKeep(p.Keep))
		lines := got.VisualLines
		if lines == nil {
			lines = []string{}
		}
		if !reflect.DeepEqual(lines, expected[i].VisualLines) || got.SkippedCount != expected[i].SkippedCount {
			if failures++; failures <= 5 {
				t.Errorf("%+v:\n  Pig %q skipped %d\n  Pi  %q skipped %d", p, lines, got.SkippedCount, expected[i].VisualLines, expected[i].SkippedCount)
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
