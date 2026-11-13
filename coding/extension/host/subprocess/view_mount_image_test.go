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
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// mountPhoto is a view of a text row, an image node holding the photograph of internal/photonimage/testdata (a JPEG of
// 512x512 pixels and 70,657 bytes) and a second text row. members is added to the image node as written.
func mountPhoto(t *testing.T, text, members string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", "photonimage", "testdata", "photon-jpegdecode-tower.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	ref := hex.EncodeToString(sum[:])
	return json.RawMessage(`{"root":{"kind":"vstack","children":[{"kind":"text","text":` + strconv.Quote(text) + `},` +
		`{"kind":"image","ref":"` + ref + `","mimeType":"image/jpeg","filename":"tower.jpg"` + members + `},{"kind":"text","text":"below"}]},` +
		`"images":[{"ref":"` + ref + `","mimeType":"image/jpeg","data":"` + base64.StdEncoding.EncodeToString(data) + `"}]}`)
}

// imageProbe is a client's MountImages as a test sees it: the work it was asked to run off the loop, the bytes it wrote
// to the terminal, and the conversions it made.
type imageProbe struct {
	images      *MountImages
	work        []func()
	written     []string
	conversions int
	repaints    int
}

func newImageProbe(placeholders bool) *imageProbe {
	p := &imageProbe{}
	p.images = &MountImages{
		Write:        func(data string) { p.written = append(p.written, data) },
		Placeholders: placeholders,
		ToPNG: func(data []byte) []byte {
			p.conversions++
			img, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				return nil
			}
			var out bytes.Buffer
			if err := png.Encode(&out, img); err != nil {
				return nil
			}
			return out.Bytes()
		},
		Go: func(fn func()) { p.work = append(p.work, fn) },
	}
	return p
}

// surface is a mount surface of a slot of 12 rows.
func (p *imageProbe) surface() *MountSurface {
	return NewMountSurface(MountSurfaceOptions{Repaint: func() { p.repaints++ }, RowBudget: func() int { return 12 }, Images: p.images})
}

// run runs the work the surfaces asked for, as the goroutine off the loop does.
func (p *imageProbe) run() {
	for len(p.work) > 0 {
		work := p.work[0]
		p.work = p.work[1:]
		work()
	}
}

// imageTerminal makes the terminal one with the image protocol and cells of 10x20 pixels until the test ends.
func imageTerminal(t *testing.T, protocol tui.ImageProtocol) {
	t.Helper()
	caps, cell := tui.GetCapabilities(), tui.GetCellDimensions()
	t.Cleanup(func() {
		tui.SetCapabilities(caps)
		tui.SetCellDimensions(cell)
	})
	tui.SetCapabilities(tui.TerminalCapabilities{Images: protocol, TrueColor: true})
	tui.SetCellDimensions(tui.CellDimensions{WidthPx: 10, HeightPx: 20})
}

func acceptView(t *testing.T, s *MountSurface, view json.RawMessage) {
	t.Helper()
	if err := s.Accept(view, ""); err != nil {
		t.Fatalf("accept: %v", err)
	}
}

// drawnRows are the rows that are not blank, without styling and padding.
func drawnRows(rows []string) []string {
	var drawn []string
	for _, row := range rows {
		if row = strings.TrimSpace(widthx.StripAnsi(row)); row != "" {
			drawn = append(drawn, row)
		}
	}
	return drawn
}

var (
	kittyVirtualHead = regexp.MustCompile(`^\x1b_Ga=T,U=1,f=100,q=2,i=(\d+),c=(\d+),r=(\d+),m=1;`)
	kittyChunkRE     = regexp.MustCompile(`\x1b_G([^;]*);([^\x1b]*)\x1b\\`)
)

// transmission checks that data is one Kitty transmission of a PNG with a virtual placement and returns its image id
// and its cells.
func transmission(t *testing.T, data string) (id, cols, rows int) {
	t.Helper()
	head := kittyVirtualHead.FindStringSubmatch(data)
	if head == nil {
		t.Fatalf("the bytes written are not a transmission with a virtual placement: %.90q", data)
	}
	id, _ = strconv.Atoi(head[1])
	cols, _ = strconv.Atoi(head[2])
	rows, _ = strconv.Atoi(head[3])
	var payload strings.Builder
	chunks := kittyChunkRE.FindAllStringSubmatch(data, -1)
	for i, chunk := range chunks {
		if len(chunk[2]) > 4096 {
			t.Fatalf("chunk %d carries %d characters, more than 4096", i, len(chunk[2]))
		}
		if last := i == len(chunks)-1; strings.HasSuffix(chunk[1], "m=0") != last {
			t.Fatalf("chunk %d of %d has the keys %q", i, len(chunks), chunk[1])
		}
		payload.WriteString(chunk[2])
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil || !bytes.HasPrefix(decoded, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("the transmission does not carry a PNG: %v, %.16q", err, decoded)
	}
	if rest := kittyChunkRE.ReplaceAllString(data, ""); rest != "" {
		t.Fatalf("the transmission carries other bytes: %.60q", rest)
	}
	return id, cols, rows
}

// placeholderRows returns the rows of got that are placeholder cells, after checking that each is cols cells of the
// image id, numbered by its row and its columns.
func placeholderRows(t *testing.T, got []string, id, cols int) []string {
	t.Helper()
	color := "\x1b[38;2;" + strconv.Itoa(id>>16&0xff) + ";" + strconv.Itoa(id>>8&0xff) + ";" + strconv.Itoa(id&0xff) + "m"
	var rows []string
	for _, row := range got {
		if !strings.Contains(row, kittyPlaceholder) {
			continue
		}
		want := color
		for col := range cols {
			want += kittyPlaceholder + string(kittyDiacritics[len(rows)]) + string(kittyDiacritics[col])
		}
		if want += "\x1b[39m"; row != want {
			t.Fatalf("placeholder row %d = %.60q, want %.60q", len(rows), row, want)
		}
		if w := widthx.VisibleWidth(row); w != cols {
			t.Fatalf("placeholder row %d is %d cells wide to the TUI, want %d", len(rows), w, cols)
		}
		rows = append(rows, row)
	}
	return rows
}

// pig additive (D614): the image node of a mount is prepared off the loop. The first render draws one empty row for
// it, converts nothing and writes nothing; the work it asked for measures and converts the photograph, after which the
// surface asks for a frame and its revision changes. In a terminal that draws Kitty Unicode placeholders that frame
// transmits the photograph once, as a PNG with a virtual placement, and draws placeholder cells; a frame after it
// transmits and converts nothing.
func TestMountImageIsPreparedOffTheLoopAndTransmittedOnce(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	acceptView(t, s, mountPhoto(t, "round 1", `,"maxWidthCells":20`))
	first := s.Render(40)
	if drawn := drawnRows(first); len(drawn) != 2 || !strings.Contains(drawn[0], "round 1") || !strings.Contains(drawn[1], "below") {
		t.Fatalf("before the photograph is prepared the mount draws %q, want its two texts and an empty row", first)
	}
	if p.conversions != 0 || len(p.written) != 0 || len(p.work) != 1 {
		t.Fatalf("the first render converted %d times, wrote %d times and asked for %d pieces of work, want 0, 0 and 1", p.conversions, len(p.written), len(p.work))
	}
	rev := s.Revision()
	p.run()
	if p.repaints != 1 || s.Revision() == rev || p.conversions != 1 {
		t.Fatalf("after the work: %d frames asked for, revision changed %v, %d conversions, want 1, true and 1", p.repaints, s.Revision() != rev, p.conversions)
	}
	got := s.Render(40)
	if len(p.written) != 1 {
		t.Fatalf("the frame wrote %d times to the terminal, want the one transmission", len(p.written))
	}
	id, cols, rows := transmission(t, p.written[0])
	if id <= 0 || id >= 1<<24 || cols != 20 || rows != 10 {
		t.Fatalf("the transmission is image %d of %d by %d cells, want an id of 24 bits and 20 by 10 (a square photograph in cells of 10x20 pixels)", id, cols, rows)
	}
	if n := len(placeholderRows(t, got, id, cols)); n != rows || len(got) != len(first)-1+rows || len(drawnRows(got)) != rows+2 {
		t.Fatalf("the mount draws %d placeholder rows in %d rows (%d before), want %d in the place of the empty row", n, len(got), len(first), rows)
	}
	s.Render(40)
	s.Render(40)
	if len(p.written) != 1 || p.conversions != 1 || len(p.work) != 0 {
		t.Fatalf("later frames wrote %d times, converted %d times and asked for %d pieces of work", len(p.written)-1, p.conversions-1, len(p.work))
	}
}

// pig additive (D614): an update that keeps the photograph, a second mount of it and the end of one of two mounts send
// no bytes of it again and free nothing; a width that changes its cells transmits it for the new cells and frees the
// old image in the same write; the end of the last mount frees the image.
func TestMountImageUpdateSecondMountAndEnd(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	acceptView(t, s, mountPhoto(t, "round 1", `,"maxWidthCells":20`))
	s.Render(40)
	p.run()
	s.Render(40)
	id, _, _ := transmission(t, p.written[0])

	acceptView(t, s, mountPhoto(t, "round 2", `,"maxWidthCells":20`))
	got := s.Render(40)
	if len(p.written) != 1 || p.conversions != 1 || len(p.work) != 0 {
		t.Fatalf("an update that keeps the photograph wrote %q, converted %d times, asked for %d pieces of work", p.written[1:], p.conversions-1, len(p.work))
	}
	if !strings.Contains(drawnRows(got)[0], "round 2") || len(placeholderRows(t, got, id, 20)) != 10 {
		t.Fatalf("after the update the mount draws %.80q", got)
	}

	second := p.surface()
	acceptView(t, second, mountPhoto(t, "another mount", `,"maxWidthCells":20`))
	if got := second.Render(40); len(placeholderRows(t, got, id, 20)) != 10 || len(p.written) != 1 {
		t.Fatalf("a second mount of the photograph wrote %q", p.written[1:])
	}
	second.Close()
	if len(p.written) != 1 {
		t.Fatalf("the end of one of two mounts wrote %q", p.written[1:])
	}

	narrow := s.Render(18)
	if len(p.written) != 2 {
		t.Fatalf("a narrower mount wrote %d times, want once", len(p.written)-1)
	}
	freed := tui.DeleteKittyImage(id)
	if !strings.HasSuffix(p.written[1], freed) {
		t.Fatalf("the write for the new cells does not free image %d: %.60q", id, p.written[1][max(0, len(p.written[1])-60):])
	}
	id2, cols, rows := transmission(t, strings.TrimSuffix(p.written[1], freed))
	if id2 == id || cols != 16 || rows != 8 || len(placeholderRows(t, narrow, id2, cols)) != rows {
		t.Fatalf("the narrower mount is image %d (was %d) of %d by %d cells, want another id and 16 by 8", id2, id, cols, rows)
	}

	s.Close()
	if len(p.written) != 3 || p.written[2] != tui.DeleteKittyImage(id2) {
		t.Fatalf("the end of the last mount wrote %q, want the image freed", p.written[2:])
	}
	if p.conversions != 1 {
		t.Fatalf("the photograph was converted %d times, want once", p.conversions)
	}
}

// pig additive (D614): an image with no bound of its own is no taller than its slot: in a slot of 12 rows the square
// photograph is 12 rows and, its aspect kept, 24 columns.
func TestMountImageIsNoTallerThanItsSlot(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	acceptView(t, s, mountPhoto(t, "round 1", ""))
	s.Render(80)
	p.run()
	s.Render(80)
	if _, cols, rows := transmission(t, p.written[0]); cols != 24 || rows != 12 {
		t.Fatalf("the photograph is %d by %d cells, want 24 by 12", cols, rows)
	}
}

// pig additive (D614): what the image node draws per terminal. With the iTerm2 protocol one row carries the inline
// image with the JPEG as it was sent, and nothing is converted. With the Kitty protocol and no placeholders the row
// carries the image as Pi's component sends it, as a PNG, with an id that an update keeps. Without a protocol the row
// is Pi's text fallback. None of them writes outside the rows.
func TestMountImageRowsPerTerminal(t *testing.T) {
	render := func(t *testing.T, protocol tui.ImageProtocol) (*imageProbe, *MountSurface, []string) {
		imageTerminal(t, protocol)
		p := newImageProbe(false)
		s := p.surface()
		t.Cleanup(s.Close)
		acceptView(t, s, mountPhoto(t, "round 1", `,"maxWidthCells":20`))
		s.Render(80)
		p.run()
		return p, s, s.Render(80)
	}
	count := func(rows []string, text string) (n int, row string) {
		for _, r := range rows {
			if strings.Contains(r, text) {
				n, row = n+1, r
			}
		}
		return n, row
	}
	t.Run("iterm2", func(t *testing.T) {
		p, _, rows := render(t, tui.ImageProtocolITerm2)
		n, row := count(rows, "\x1b]1337;File=")
		if n != 1 || !strings.Contains(row, "/9j/") || p.conversions != 0 || len(p.written) != 0 {
			t.Fatalf("%d rows carry an inline image (%.60q), %d conversions, %d writes; want one with the JPEG, 0 and 0", n, row, p.conversions, len(p.written))
		}
	})
	t.Run("kitty without placeholders", func(t *testing.T) {
		p, s, rows := render(t, tui.ImageProtocolKitty)
		n, row := count(rows, "\x1b_G")
		id := regexp.MustCompile(`[,G]i=(\d+)`).FindStringSubmatch(row)
		if n != 1 || id == nil || !strings.Contains(row, "iVBOR") || p.conversions != 1 || len(p.written) != 0 {
			t.Fatalf("%d rows carry a Kitty image (%.60q), %d conversions, %d writes; want one PNG with an id, 1 and 0", n, row, p.conversions, len(p.written))
		}
		acceptView(t, s, mountPhoto(t, "round 2", `,"maxWidthCells":20`))
		_, row = count(s.Render(80), "\x1b_G")
		if again := regexp.MustCompile(`[,G]i=(\d+)`).FindStringSubmatch(row); again == nil || again[1] != id[1] || p.conversions != 1 {
			t.Fatalf("after an update the image is %v (was %v) and was converted %d times, want the same id and once", again, id, p.conversions)
		}
	})
	t.Run("no protocol", func(t *testing.T) {
		p, _, rows := render(t, "")
		drawn := drawnRows(rows)
		want := tui.ImageFallback("image/jpeg", &tui.ImageDimensions{WidthPx: 512, HeightPx: 512}, "tower.jpg")
		if len(drawn) != 3 || drawn[1] != want || p.conversions != 0 || len(p.written) != 0 {
			t.Fatalf("the mount draws %q, %d conversions, %d writes; want the text fallback %q between its texts, 0 and 0", drawn, p.conversions, len(p.written), want)
		}
	})
}
