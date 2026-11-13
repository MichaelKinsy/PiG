package codingagent

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	rows := drawn[0].Render(40)
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
