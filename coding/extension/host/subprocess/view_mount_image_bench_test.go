package subprocess

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/photonimage"
	"github.com/MichaelKinsy/PiG/tui"
)

// The cost of a photograph in a mount (D614) in a terminal that draws Kitty Unicode placeholders, with the photograph
// of internal/photonimage/testdata (70,657 bytes). "mount" is a mount from nothing to the frame that transmits the
// photograph, the conversion to PNG left out (the PNG is made once, before the loop). The update rows are a view whose
// text changed, accepted and drawn: with the photograph named by its ref, with its bytes sent again, and with a text
// node in its place, which is what an update costs without an image. term-B/op is what one operation writes to the
// terminal outside the rows: an update writes nothing.
func BenchmarkMountImage(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", "photonimage", "testdata", "photon-jpegdecode-tower.jpg"))
	if err != nil {
		b.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}
	var converted bytes.Buffer
	if err := png.Encode(&converted, decoded); err != nil {
		b.Fatal(err)
	}
	sum := sha256.Sum256(data)
	ref := hex.EncodeToString(sum[:])
	image := `{"kind":"image","ref":"` + ref + `","mimeType":"image/jpeg","filename":"tower.jpg","maxWidthCells":20}`
	bytesOfImage := `,"images":[{"ref":"` + ref + `","mimeType":"image/jpeg","data":"` + base64.StdEncoding.EncodeToString(data) + `"}]`
	view := func(text, node, images string) json.RawMessage {
		return json.RawMessage(`{"root":{"kind":"vstack","children":[{"kind":"text","text":` + strconv.Quote(text) + `},` + node + `,{"kind":"text","text":"below"}]}` + images + `}`)
	}
	caps, cell := tui.GetCapabilities(), tui.GetCellDimensions()
	b.Cleanup(func() {
		tui.SetCapabilities(caps)
		tui.SetCellDimensions(cell)
	})
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true})
	tui.SetCellDimensions(tui.CellDimensions{WidthPx: 10, HeightPx: 20})

	written := 0
	var work []func()
	images := func() *MountImages {
		return &MountImages{
			Write:        func(data string) { written += len(data) },
			Placeholders: true,
			ToPNG:        func([]byte) []byte { return converted.Bytes() },
			Decode:       photonimage.Decode,
			Go:           func(fn func()) { work = append(work, fn) },
		}
	}
	mount := func(images *MountImages, first json.RawMessage) *MountSurface {
		s := NewMountSurface(MountSurfaceOptions{RowBudget: func() int { return 12 }, Images: images})
		if err := s.Accept(first, ""); err != nil {
			b.Fatal(err)
		}
		s.Render(40)
		for _, fn := range work {
			fn()
		}
		work = work[:0]
		s.Render(40)
		return s
	}

	b.Run("mount", func(b *testing.B) {
		first := view("round 1", image, bytesOfImage)
		written = 0
		b.ReportAllocs()
		for b.Loop() {
			mount(images(), first).Close()
		}
		b.ReportMetric(float64(written)/float64(b.N), "term-B/op")
	})
	for _, row := range []struct {
		name         string
		node, images string
	}{
		{"update/ref", image, ""},
		{"update/bytes", image, bytesOfImage},
		{"update/text", `{"kind":"text","text":"in the place of the image"}`, ""},
	} {
		b.Run(row.name, func(b *testing.B) {
			shared := images()
			s := mount(shared, view("round 0", image, bytesOfImage))
			defer s.Close()
			views := [2]json.RawMessage{view("round 1", row.node, row.images), view("round 2", row.node, row.images)}
			for _, v := range views {
				if err := s.Accept(v, ""); err != nil {
					b.Fatal(err)
				}
				s.Render(40)
			}
			written = 0
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				if err := s.Accept(views[i&1], ""); err != nil {
					b.Fatal(err)
				}
				s.Render(40)
				i++
			}
			b.ReportMetric(float64(written)/float64(b.N), "term-B/op")
		})
	}
}
