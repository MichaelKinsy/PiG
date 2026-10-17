package imageprocessing

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type dimensionNoteProbe struct {
	OriginalWidth  int  `json:"originalWidth"`
	OriginalHeight int  `json:"originalHeight"`
	Width          int  `json:"width"`
	Height         int  `json:"height"`
	WasResized     bool `json:"wasResized"`
}

// formatDimensionNote (utils/image-resize.ts) against pinned Pi over 4000 seeded resize results: ratios that round either way at two decimals (exact halves, binary-inexact ties), zero and negative sizes, and sizes past 2^31, resized or not.
func TestFormatDimensionNoteMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 3))
	sizes := []int{0, 1, 2, 3, 7, 100, 101, 640, 800, 1024, 1999, 2000, 2001, 4096, 7999, 8000, 65535, 1 << 20, 1<<31 - 1, 1 << 31, 1 << 40, -1, -640}
	pick := func() int {
		if rng.IntN(3) == 0 {
			return rng.IntN(10000)
		}
		return sizes[rng.IntN(len(sizes))]
	}
	var probes []dimensionNoteProbe
	for range 4000 {
		probes = append(probes, dimensionNoteProbe{OriginalWidth: pick(), OriginalHeight: pick(), Width: pick(), Height: pick(), WasResized: rng.IntN(5) != 0})
	}
	// Quotients of the form k + 0.005 and k + 0.015 sit on the rounding boundary of toFixed(2).
	for width := 1; width <= 400; width++ {
		for _, numerator := range []int{width + 1, width*3 + 1, width*2 + width/200, width*201/200 + 1} {
			probes = append(probes, dimensionNoteProbe{OriginalWidth: numerator, OriginalHeight: 9, Width: width, Height: 5, WasResized: true})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/dimension_note.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []*string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	mismatches, notes := 0, 0
	for i, probe := range probes {
		got := formatDimensionNote(ResizedImage{OriginalWidth: probe.OriginalWidth, OriginalHeight: probe.OriginalHeight, Width: probe.Width, Height: probe.Height, WasResized: probe.WasResized})
		expected := ""
		if want[i] != nil {
			expected = *want[i]
			notes++
		}
		if got != expected {
			if mismatches++; mismatches <= 6 {
				t.Errorf("%+v: Go %q, Pi %q", probe, got, expected)
			}
		}
	}
	if mismatches > 6 {
		t.Errorf("%d of %d probes differ from Pi", mismatches, len(probes))
	}
	if notes < 3000 {
		t.Errorf("only %d notes", notes)
	}
}
