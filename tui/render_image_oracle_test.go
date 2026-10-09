package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type directImageProbe struct {
	Protocol            string `json:"protocol"`
	CellWidth           int    `json:"cellWidth"`
	CellHeight          int    `json:"cellHeight"`
	Data                string `json:"data"`
	WidthPx             int    `json:"widthPx"`
	HeightPx            int    `json:"heightPx"`
	MaxWidthCells       *int   `json:"maxWidthCells"`
	MaxHeightCells      *int   `json:"maxHeightCells"`
	PreserveAspectRatio *bool  `json:"preserveAspectRatio"`
	ImageID             *int   `json:"imageId"`
	MoveCursor          *bool  `json:"moveCursor"`
}

type directImageResult struct {
	Sequence string `json:"sequence"`
	Columns  int    `json:"columns"`
	Rows     int    `json:"rows"`
	ImageID  *int   `json:"imageId"`
}

func directImageProbes() []directImageProbe {
	ptr := func(v int) *int { return &v }
	flag := func(v bool) *bool { return &v }
	var probes []directImageProbe
	for _, protocol := range []string{"kitty", "iterm2", ""} {
		for _, cell := range [][2]int{{9, 18}, {8, 16}, {10, 10}} {
			for _, d := range [][2]int{{1, 1}, {100, 50}, {50, 100}, {640, 480}, {4000, 3000}, {3, 2000}, {2000, 3}} {
				for _, mw := range []*int{nil, ptr(1), ptr(10), ptr(80), ptr(500)} {
					for _, mh := range []*int{nil, ptr(1), ptr(5), ptr(100)} {
						for _, preserve := range []*bool{nil, flag(true), flag(false)} {
							for _, id := range []*int{nil, ptr(5), ptr(4294967295)} {
								for _, move := range []*bool{nil, flag(true), flag(false)} {
									probes = append(probes, directImageProbe{Protocol: protocol, CellWidth: cell[0], CellHeight: cell[1], Data: "QUJDRA==", WidthPx: d[0], HeightPx: d[1], MaxWidthCells: mw, MaxHeightCells: mh, PreserveAspectRatio: preserve, ImageID: id, MoveCursor: move})
								}
							}
						}
					}
				}
			}
		}
	}
	// Payload sizes: the iTerm2 size field counts padding, and kitty splits a payload above 4096 characters into chunks.
	for _, protocol := range []string{"kitty", "iterm2"} {
		for _, n := range []int{0, 1, 2, 3, 4, 5, 4095, 4096, 4097, 8192, 8193, 12288, 12289} {
			for _, pad := range []string{"", "=", "=="} {
				for _, id := range []*int{nil, ptr(9)} {
					data := strings.Repeat("A", n) + pad
					probes = append(probes, directImageProbe{Protocol: protocol, CellWidth: 9, CellHeight: 18, Data: data, WidthPx: 100, HeightPx: 50, ImageID: id, MoveCursor: flag(false)})
				}
			}
		}
	}
	return probes
}

func directImageWithPig(probe directImageProbe) *directImageResult {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocol(probe.Protocol), TrueColor: true})
	SetCellDimensions(CellDimensions{WidthPx: probe.CellWidth, HeightPx: probe.CellHeight})
	options := ImageRenderOptions{PreserveAspectRatio: probe.PreserveAspectRatio, MoveCursor: probe.MoveCursor}
	if probe.MaxWidthCells != nil {
		options.MaxWidthCells = *probe.MaxWidthCells
	}
	if probe.MaxHeightCells != nil {
		options.MaxHeightCells = *probe.MaxHeightCells
	}
	if probe.ImageID != nil {
		options.ImageID = *probe.ImageID
	}
	result := RenderImage(probe.Data, ImageDimensions{WidthPx: probe.WidthPx, HeightPx: probe.HeightPx}, options)
	if result == nil {
		return nil
	}
	out := &directImageResult{Sequence: result.Sequence, Columns: result.Columns, Rows: result.Rows}
	if result.ImageID != 0 {
		out.ImageID = &result.ImageID
	}
	return out
}

// renderImage (terminal-image.ts) against pinned Pi over every option: the default width of 80, the width and height limits, aspect-ratio
// preservation for iTerm2, the kitty id and cursor-movement flag, three cell sizes, extreme aspect ratios and no protocol.
func TestRenderImageMatchesPi(t *testing.T) {
	probes := directImageProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/render_image.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []*directImageResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousCells := GetCapabilities(), GetCellDimensions()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetCellDimensions(previousCells)
	})
	failures := 0
	for i, probe := range probes {
		got := directImageWithPig(probe)
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 4 {
				g, _ := json.Marshal(got)
				e, _ := json.Marshal(expected[i])
				p, _ := json.Marshal(probe)
				t.Errorf("%s:\n  Pig %s\n  Pi  %s", p, g, e)
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// TestRenderImageProbeDump prints the corpus for the Pi side of the render-image parity scenario.
func TestRenderImageProbeDump(t *testing.T) {
	line, err := json.Marshal(directImageProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("renderimage-probes:%s\n", line)
}

// TestRenderImageParity prints Pig's results of the corpus, one JSON line per probe, for the render-image parity scenario.
func TestRenderImageParity(t *testing.T) {
	previousCaps, previousCells := GetCapabilities(), GetCellDimensions()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetCellDimensions(previousCells)
	})
	for _, probe := range directImageProbes() {
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(directImageWithPig(probe)); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("renderimage-observation:%s", line.String())
	}
}
