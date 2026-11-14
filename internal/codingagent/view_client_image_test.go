package codingagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// viewPhoto is a view of a text row, an image node holding the photograph of internal/photonimage/testdata (a JPEG of
// 512x512 pixels and 70,657 bytes) and a second text row. node's members are added to the image node.
func viewPhoto(t *testing.T, node map[string]any) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "photonimage", "testdata", "photon-jpegdecode-tower.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	ref := hex.EncodeToString(sum[:])
	image := map[string]any{"kind": "image", "ref": ref, "mimeType": "image/jpeg", "filename": "tower.jpg"}
	maps.Copy(image, node)
	raw, err := json.Marshal(map[string]any{
		"root": map[string]any{"kind": "vstack", "children": []any{
			map[string]any{"kind": "text", "text": "above the photograph"}, image, map[string]any{"kind": "text", "text": "below the photograph"},
		}},
		"images": []any{map[string]any{"ref": ref, "mimeType": "image/jpeg", "data": base64.StdEncoding.EncodeToString(data)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// pig additive (D614): the terminal client declares the image node and the formats it draws.
func TestViewClientDeclaresTheImageNodeAndItsFormats(t *testing.T) {
	f := newViewFixture(t)
	a := f.host.reg.Abilities()
	if !slices.Contains(a.Nodes, "image") {
		t.Errorf("declared node kinds = %v, want image among them", a.Nodes)
	}
	if want := []string{"png", "jpeg", "gif", "webp"}; !slices.Equal(a.ImageFormats, want) {
		t.Errorf("declared image formats = %v, want %v", a.ImageFormats, want)
	}
}

// pig additive (D614): a mount whose view holds an image node with a photograph is accepted and drawn in the panel:
// in a terminal with the iTerm2 protocol one row carries the inline image with the photograph's bytes, between the
// rows of the texts around it, inside the slot's rows, and the plugin reads no notice.
func TestViewClientDrawsAPhotographInAPanel(t *testing.T) {
	previous := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previous) })
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolITerm2, TrueColor: true})
	f := newViewFixture(t)
	f.mount("photo", uiview.SlotPanel, uiview.Content{View: viewPhoto(t, map[string]any{"maxWidthCells": 20, "maxHeightCells": 6})})
	drawn := f.loop.drawn()
	if len(drawn) != 1 {
		t.Fatalf("the layout holds %d mounts, want the photograph's", len(drawn))
	}
	// The photograph is measured off the loop: the mount draws an empty row for it until then.
	var rows []string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		// The loop is told that the photograph is ready.
		f.loop.run()
		rows = drawn[0].Render(40)
		if slices.ContainsFunc(rows, func(row string) bool { return strings.Contains(row, "\x1b]1337;File=") }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mount draws no inline image: %.200q", rows)
		}
	}
	if len(rows) > 12 {
		t.Fatalf("the mount draws %d rows in a slot of 12", len(rows))
	}
	above, image, below := -1, -1, -1
	for i, row := range rows {
		switch {
		case strings.Contains(row, "\x1b]1337;File="):
			if image >= 0 {
				t.Fatalf("rows %d and %d both carry an inline image", image, i)
			}
			image = i
			if !strings.Contains(row, "/9j/") {
				t.Errorf("the inline image does not carry the JPEG's bytes: %.80q", row)
			}
		case strings.Contains(widthx.StripAnsi(row), "above the photograph"):
			above = i
		case strings.Contains(widthx.StripAnsi(row), "below the photograph"):
			below = i
		}
	}
	if above < 0 || image <= above || below <= image {
		t.Fatalf("rows above, image, below = %d, %d, %d, want them in that order", above, image, below)
	}
	if notices := f.notices("photo"); len(notices) != 0 {
		t.Fatalf("the plugin reads notices %+v, want none", notices)
	}
}

// pig additive (D614): in a terminal without an image protocol the production client draws a JPEG, a GIF and a PNG as
// rows of half-block cells, through the client's own ToPNG and Decode (photonimage): none is the text fallback or the
// line that names a missing decoder.
func TestViewClientDrawsImagesAsHalfBlocksWithPhotonimage(t *testing.T) {
	previous, cell := tui.GetCapabilities(), tui.GetCellDimensions()
	t.Cleanup(func() { tui.SetCapabilities(previous); tui.SetCellDimensions(cell) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetCellDimensions(tui.CellDimensions{WidthPx: 10, HeightPx: 20})
	data, err := os.ReadFile(filepath.Join("..", "photonimage", "testdata", "photon-jpegdecode-tower.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var asGIF, asPNG bytes.Buffer
	if err := gif.Encode(&asGIF, decoded, nil); err != nil {
		t.Fatal(err)
	}
	// An 8-bit PNG of a corner that fits the client's snapshot limit.
	corner := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	draw.Draw(corner, corner.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	if err := png.Encode(&asPNG, corner); err != nil {
		t.Fatal(err)
	}
	for mime, picture := range map[string][]byte{"image/jpeg": data, "image/gif": asGIF.Bytes(), "image/png": asPNG.Bytes()} {
		t.Run(mime, func(t *testing.T) {
			f := newViewFixture(t)
			sum := sha256.Sum256(picture)
			ref := hex.EncodeToString(sum[:])
			view, err := json.Marshal(map[string]any{
				"root":   map[string]any{"kind": "image", "ref": ref, "mimeType": mime, "maxWidthCells": 20, "maxHeightCells": 6},
				"images": []any{map[string]any{"ref": ref, "mimeType": mime, "data": base64.StdEncoding.EncodeToString(picture)}},
			})
			if err != nil {
				t.Fatal(err)
			}
			f.mount("photo", uiview.SlotPanel, uiview.Content{View: view})
			drawn := f.loop.drawn()
			if len(drawn) != 1 {
				t.Fatalf("the layout holds %d mounts, want the image's", len(drawn))
			}
			var rows []string
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
				f.loop.run()
				rows = drawn[0].Render(40)
				if slices.ContainsFunc(rows, func(row string) bool { return strings.Contains(row, "\u2580") }) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the mount draws no half-block cells: %.300q", rows)
				}
			}
			if joined := strings.Join(rows, "\n"); strings.Contains(joined, "decoder") {
				t.Fatalf("the mount names a missing decoder: %.300q", rows)
			}
		})
	}
}

// pig additive (D614): the terminals whose environment says they draw Kitty Unicode placeholders are kitty and Ghostty.
// A terminal that names itself otherwise is not one, even with kitty's variables in its environment, and neither is a
// pane of Herdr.
func TestViewKittyPlaceholdersByEnvironment(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want bool
	}{
		"kitty":                      {map[string]string{"KITTY_WINDOW_ID": "1", "TERM": "xterm-kitty"}, true},
		"kitty by TERM alone":        {map[string]string{"TERM": "xterm-kitty"}, true},
		"ghostty":                    {map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-ghostty"}, true},
		"ghostty by resources":       {map[string]string{"GHOSTTY_RESOURCES_DIR": "/usr/share/ghostty", "TERM": "xterm-256color"}, true},
		"wezterm":                    {map[string]string{"TERM_PROGRAM": "WezTerm", "WEZTERM_PANE": "0"}, false},
		"rex":                        {map[string]string{"TERM_PROGRAM": "rex"}, false},
		"kitty variables in wezterm": {map[string]string{"TERM_PROGRAM": "WezTerm", "KITTY_WINDOW_ID": "1"}, false},
		"herdr over kitty":           {map[string]string{"HERDR_ENV": "1", "KITTY_WINDOW_ID": "1", "TERM": "xterm-kitty"}, false},
		"iterm2":                     {map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0"}, false},
		"plain":                      {map[string]string{"TERM": "xterm-256color"}, false},
	} {
		if got := viewKittyPlaceholders(func(key string) string { return tc.env[key] }); got != tc.want {
			t.Errorf("%s: placeholders = %v, want %v", name, got, tc.want)
		}
	}
}
