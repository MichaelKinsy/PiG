package tui

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type imageRenderProbe struct {
	Protocol       string  `json:"protocol"`
	CellWidth      int     `json:"cellWidth"`
	CellHeight     int     `json:"cellHeight"`
	Data           string  `json:"data"`
	Mime           string  `json:"mime"`
	MaxWidthCells  *int    `json:"maxWidthCells"`
	MaxHeightCells *int    `json:"maxHeightCells"`
	Filename       string  `json:"filename"`
	ImageID        *int    `json:"imageId"`
	Dims           *[2]int `json:"dims"`
	Steps          []any   `json:"steps"`
}

type imageRenderResult struct {
	Frames []*[]string `json:"frames"`
	ID     *int        `json:"id"`
}

func imageHeaderPNG(w, h int) string {
	b := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 8)...)
	binary.BigEndian.PutUint32(b[16:], uint32(w))
	binary.BigEndian.PutUint32(b[20:], uint32(h))
	return base64.StdEncoding.EncodeToString(append(b, make([]byte, 20)...))
}

func imageHeaderGIF(w, h int) string {
	b := []byte("GIF89a\x00\x00\x00\x00")
	binary.LittleEndian.PutUint16(b[6:], uint16(w))
	binary.LittleEndian.PutUint16(b[8:], uint16(h))
	return base64.StdEncoding.EncodeToString(append(b, make([]byte, 8)...))
}

func imageHeaderJPEG(w, h int) string {
	b := []byte{0xff, 0xd8, 0xff, 0xc0, 0x00, 0x11, 0x08, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[7:], uint16(h))
	binary.BigEndian.PutUint16(b[9:], uint16(w))
	return base64.StdEncoding.EncodeToString(append(b, make([]byte, 12)...))
}

func imageHeaderWEBP(w, h int) string {
	b := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00"), make([]byte, 6)...)
	put := func(offset, v int) { b[offset], b[offset+1], b[offset+2] = byte(v-1), byte((v-1)>>8), byte((v-1)>>16) }
	put(24, w)
	put(27, h)
	return base64.StdEncoding.EncodeToString(append(b, make([]byte, 8)...))
}

func imageRenderProbes() []imageRenderProbe {
	type source struct{ data, mime string }
	var sources []source
	for _, d := range [][2]int{{1, 1}, {100, 50}, {50, 100}, {640, 480}, {4000, 3000}, {64, 64}} {
		sources = append(sources, source{imageHeaderPNG(d[0], d[1]), "image/png"}, source{imageHeaderJPEG(d[0], d[1]), "image/jpeg"})
	}
	sources = append(sources, source{imageHeaderGIF(120, 80), "image/gif"}, source{imageHeaderWEBP(300, 200), "image/webp"},
		source{base64.StdEncoding.EncodeToString([]byte("not an image")), "image/png"}, source{"AAAA", "image/bmp"})
	var probes []imageRenderProbe
	for _, protocol := range []string{"kitty", "iterm2", ""} {
		for _, cell := range [][2]int{{9, 18}, {8, 16}} {
			for _, s := range sources {
				for _, mw := range []*int{nil, new(5), new(30), new(100)} {
					for _, mh := range []*int{nil, new(1), new(6)} {
						for _, id := range []*int{nil, new(7)} {
							probes = append(probes, imageRenderProbe{Protocol: protocol, CellWidth: cell[0], CellHeight: cell[1], Data: s.data, Mime: s.mime, MaxWidthCells: mw, MaxHeightCells: mh, ImageID: id,
								Filename: map[bool]string{true: "pic.png", false: ""}[mw == nil && id == nil], Steps: []any{1, 3, 10, 40, 40, "invalidate", 80, 200, 10}})
						}
					}
				}
			}
		}
	}
	// An explicit dimensions argument wins over the header.
	for _, protocol := range []string{"kitty", "iterm2", ""} {
		probes = append(probes, imageRenderProbe{Protocol: protocol, CellWidth: 9, CellHeight: 18, Data: imageHeaderPNG(10, 10), Mime: "image/png", Dims: &[2]int{200, 100}, Filename: "x.png", Steps: []any{40, 3}})
	}
	return probes
}

func renderImageProbe(probe imageRenderProbe) imageRenderResult {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocol(probe.Protocol), TrueColor: true})
	SetCellDimensions(CellDimensions{WidthPx: probe.CellWidth, HeightPx: probe.CellHeight})
	options := ImageOptions{Filename: probe.Filename}
	if probe.MaxWidthCells != nil {
		options.MaxWidthCells = *probe.MaxWidthCells
	}
	if probe.MaxHeightCells != nil {
		options.MaxHeightCells = *probe.MaxHeightCells
	}
	if probe.ImageID != nil {
		options.ImageID = *probe.ImageID
	}
	var dims *ImageDimensions
	if probe.Dims != nil {
		dims = &ImageDimensions{WidthPx: probe.Dims[0], HeightPx: probe.Dims[1]}
	}
	image := NewImage(probe.Data, probe.Mime, ImageTheme{FallbackColor: func(s string) string { return "<fb>" + s + "</fb>" }}, options, dims)
	var result imageRenderResult
	for _, step := range probe.Steps {
		if step == "invalidate" {
			image.Invalidate()
			result.Frames = append(result.Frames, nil)
			continue
		}
		lines := image.Render(int(step.(int)))
		result.Frames = append(result.Frames, &lines)
	}
	if id := image.GetImageID(); id != 0 {
		result.ID = &id
	}
	return result
}

// Image.render (components/image.ts) against pinned Pi: the kitty, iTerm2 and no-image paths for PNG, JPEG, GIF and WebP headers (sized by their
// own dimensions, an explicit size or the 800 by 600 default), the width, height and 60-cell limits, two cell sizes, the cursor-up line of
// iTerm2, the blank rows kitty keeps, the text fallback with and without a file name, the render cache and invalidate.
func TestImageRenderMatchesPi(t *testing.T) {
	probes := imageRenderProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/image_render.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []imageRenderResult
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
		got := renderImageProbe(probe)
		// Pi allocates the kitty id from Math.random (stubbed to 0.5) and Pig from its own source, so an allocated id is compared only by being set.
		if probe.Protocol == "kitty" && probe.ImageID == nil {
			for f := range got.Frames {
				if got.Frames[f] != nil {
					normalizeKittyImageIDs(*got.Frames[f])
					normalizeKittyImageIDs(*expected[i].Frames[f])
				}
			}
			if (got.ID == nil) != (expected[i].ID == nil) {
				t.Errorf("probe %d: image id set %v, Pi %v", i, got.ID != nil, expected[i].ID != nil)
			}
			got.ID, expected[i].ID = nil, nil
		}
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 4 {
				p := probe
				p.Data = fmt.Sprintf("%.12s", p.Data)
				t.Errorf("%+v:\n  Pig %s\n  Pi  %s", p, imageResultString(got), imageResultString(expected[i]))
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

var kittyImageIDField = regexp.MustCompile(`([,;]i=)\d+`)

func normalizeKittyImageIDs(lines []string) {
	for i, line := range lines {
		lines[i] = kittyImageIDField.ReplaceAllString(line, "${1}ID")
	}
}

func imageResultString(r imageRenderResult) string {
	b, _ := json.Marshal(r)
	return string(b)
}

// TestImageRenderProbeDump prints the corpus for the Pi side of the image-render parity scenario.
func TestImageRenderProbeDump(t *testing.T) {
	line, err := json.Marshal(imageRenderProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("image-probes:%s\n", line)
}

// TestImageRenderParity prints Pig's renders of the corpus, one JSON line per probe, with the kitty image id normalised like the oracle test.
func TestImageRenderParity(t *testing.T) {
	previousCaps, previousCells := GetCapabilities(), GetCellDimensions()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetCellDimensions(previousCells)
	})
	for _, probe := range imageRenderProbes() {
		got := renderImageProbe(probe)
		for f := range got.Frames {
			if got.Frames[f] != nil {
				normalizeKittyImageIDs(*got.Frames[f])
			}
		}
		got.ID = nil
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(got); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("image-observation:%s", line.String())
	}
}
