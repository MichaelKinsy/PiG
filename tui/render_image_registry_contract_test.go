package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type renderImageRegistryProbe struct {
	Protocol       string `json:"protocol"`
	CellWidth      int    `json:"cellWidth"`
	CellHeight     int    `json:"cellHeight"`
	Data           string `json:"data"`
	WidthPx        int    `json:"widthPx"`
	HeightPx       int    `json:"heightPx"`
	MaxWidthCells  *int   `json:"maxWidthCells"`
	MaxHeightCells *int   `json:"maxHeightCells"`
	ImageID        *int   `json:"imageId"`
}

type renderImageRegistryPlacement struct {
	ImageID                int    `json:"imageId"`
	TransmissionGeneration int    `json:"transmissionGeneration"`
	TransmissionBytes      int    `json:"transmissionBytes"`
	EstimatedDecodedBytes  int    `json:"estimatedDecodedBytes"`
	Rows                   int    `json:"rows"`
	Sequence               string `json:"sequence"`
	ReplacementLine        string `json:"replacementLine"`
}

type renderImageRegistryResult struct {
	Rendered  bool                          `json:"rendered"`
	Placement *renderImageRegistryPlacement `json:"placement"`
	MaxRows   int                           `json:"maxRows"`
}

// renderImageRegistryProbes is one sequence of renderImage calls whose Kitty registrations accumulate: an id registers the image (a repeated id registers it
// again), a call without an id, an iTerm2 call and a call with no protocol register nothing, and the tallest image so far is kept when a shorter one follows.
func renderImageRegistryProbes() []renderImageRegistryProbe {
	ptr := func(v int) *int { return &v }
	call := func(protocol string, widthPx, heightPx int, maxWidth, maxHeight, id *int) renderImageRegistryProbe {
		return renderImageRegistryProbe{Protocol: protocol, CellWidth: 9, CellHeight: 18, Data: "QUJDRA==", WidthPx: widthPx, HeightPx: heightPx, MaxWidthCells: maxWidth, MaxHeightCells: maxHeight, ImageID: id}
	}
	return []renderImageRegistryProbe{
		call("kitty", 100, 50, nil, nil, nil),
		call("kitty", 100, 50, nil, nil, ptr(7)),
		call("kitty", 100, 50, nil, nil, ptr(7)),
		call("kitty", 900, 900, ptr(10), nil, ptr(8)),
		call("kitty", 100, 50, nil, nil, ptr(9)),
		call("kitty", 100, 50, nil, nil, nil),
		call("iterm2", 100, 5000, ptr(10), nil, ptr(11)),
		call("iterm2", 100, 50, nil, nil, nil),
		call("", 100, 50, nil, nil, ptr(12)),
		call("kitty", 640, 480, ptr(40), ptr(3), ptr(13)),
		call("kitty", 1, 4000, ptr(5), ptr(50), ptr(14)),
		call("kitty", 4000, 1, nil, nil, ptr(15)),
		call("kitty", 640, 480, nil, nil, ptr(8)),
		call("kitty", 100, 50, nil, nil, ptr(4294967295)),
	}
}

// TestRenderImageRegistersKittyMetadataLikePi compares what renderImage registers (placement of each result's sequence, tallest image) in the call sequence of
// renderImageRegistryProbes with the pinned pi-tui.
// Pi source: packages/tui/src/terminal-image.ts
func TestRenderImageRegistersKittyMetadataLikePi(t *testing.T) {
	probes := renderImageRegistryProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/render_image_registry.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []renderImageRegistryResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousCells := GetCapabilities(), GetCellDimensions()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetCellDimensions(previousCells)
		resetKittyRegistry(t)
		resetMaxKittyImageRows()
	})
	resetKittyRegistry(t)
	resetMaxKittyImageRows()
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{Images: ImageProtocol(probe.Protocol), TrueColor: true})
		SetCellDimensions(CellDimensions{WidthPx: probe.CellWidth, HeightPx: probe.CellHeight})
		var options ImageRenderOptions
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
		got := renderImageRegistryResult{Rendered: result != nil, MaxRows: GetMaxKittyImageRows()}
		if result != nil {
			if placement, ok := GetKittyImagePlacement(result.Sequence); ok {
				got.Placement = &renderImageRegistryPlacement{
					ImageID: placement.ImageID, TransmissionGeneration: placement.TransmissionGeneration, TransmissionBytes: placement.TransmissionBytes,
					EstimatedDecodedBytes: placement.EstimatedDecodedBytes, Rows: placement.Rows, Sequence: placement.Sequence, ReplacementLine: placement.ReplacementLine,
				}
			}
		}
		if !reflect.DeepEqual(got, expected[i]) {
			g, _ := json.Marshal(got)
			e, _ := json.Marshal(expected[i])
			p, _ := json.Marshal(probe)
			t.Errorf("call %d %s:\n  Pig %s\n  Pi  %s", i, p, g, e)
		}
	}
}

// resetMaxKittyImageRows clears the tallest registered image, which resetKittyRegistry keeps.
func resetMaxKittyImageRows() {
	kittyMetadataMu.Lock()
	maxKittyImageRows = 0
	kittyMetadataMu.Unlock()
}
