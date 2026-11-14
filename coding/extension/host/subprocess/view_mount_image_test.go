package subprocess

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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/photonimage"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// mountPhoto is a view of a text row, an image node holding the photograph of internal/photonimage/testdata (a JPEG of
// 512x512 pixels and 70,657 bytes) and a second text row. members is added to the image node as written.
func mountPhoto(t *testing.T, text, members string) json.RawMessage {
	t.Helper()
	return mountImageView(text, members, photoBytes(t))
}

// photoBytes are the bytes of the photograph.
func photoBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", "photonimage", "testdata", "photon-jpegdecode-tower.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// mountImageView is mountPhoto's view with data as the bytes of its image node, named a JPEG.
func mountImageView(text, members string, data []byte) json.RawMessage {
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
		Decode:       photonimage.Decode,
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

func acceptMountView(t *testing.T, s *MountSurface, view json.RawMessage) {
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
	acceptMountView(t, s, mountPhoto(t, "round 1", `,"maxWidthCells":20`))
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
// no bytes of it again and free nothing; a width that changes its cells places the held image for the new cells (no
// bytes of it); the end of the last mount frees the image.
func TestMountImageUpdateSecondMountAndEnd(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	acceptMountView(t, s, mountPhoto(t, "round 1", `,"maxWidthCells":20`))
	s.Render(40)
	p.run()
	s.Render(40)
	id, _, _ := transmission(t, p.written[0])

	acceptMountView(t, s, mountPhoto(t, "round 2", `,"maxWidthCells":20`))
	got := s.Render(40)
	if len(p.written) != 1 || p.conversions != 1 || len(p.work) != 0 {
		t.Fatalf("an update that keeps the photograph wrote %q, converted %d times, asked for %d pieces of work", p.written[1:], p.conversions-1, len(p.work))
	}
	if !strings.Contains(drawnRows(got)[0], "round 2") || len(placeholderRows(t, got, id, 20)) != 10 {
		t.Fatalf("after the update the mount draws %.80q", got)
	}

	second := p.surface()
	acceptMountView(t, second, mountPhoto(t, "another mount", `,"maxWidthCells":20`))
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
	if want := "\x1b_Ga=p,U=1,q=2,i=" + strconv.Itoa(id) + ",p=1,c=16,r=8\x1b\\"; p.written[1] != want {
		t.Fatalf("the write for the new cells is %q, want the placement %q and no bytes of the image", p.written[1], want)
	}
	if drawn := drawnPlacements(t, narrow); len(drawn) != 1 || drawn[0] != (placed{id, 1, 16, 8}) {
		t.Fatalf("the narrower mount draws %v, want image %d, placement 1, 16 by 8 cells", drawn, id)
	}

	s.Close()
	if len(p.written) != 3 || p.written[2] != tui.DeleteKittyImage(id) {
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
	acceptMountView(t, s, mountPhoto(t, "round 1", ""))
	s.Render(80)
	p.run()
	s.Render(80)
	if _, cols, rows := transmission(t, p.written[0]); cols != 24 || rows != 12 {
		t.Fatalf("the photograph is %d by %d cells, want 24 by 12", cols, rows)
	}
}

// pig additive (D614): what the image node draws per terminal. With the iTerm2 protocol one row carries the inline
// image with the JPEG as it was sent, and nothing is converted. With the Kitty protocol and no placeholders the row
// carries the image as Pi's component sends it, as a PNG, with an id that an update keeps. Without a protocol the rows
// are half-block cells, 10 of them for the square photograph in 20 columns. None of them writes outside the rows.
func TestMountImageRowsPerTerminal(t *testing.T) {
	render := func(t *testing.T, protocol tui.ImageProtocol) (*imageProbe, *MountSurface, []string) {
		imageTerminal(t, protocol)
		p := newImageProbe(false)
		s := p.surface()
		t.Cleanup(s.Close)
		acceptMountView(t, s, mountPhoto(t, "round 1", `,"maxWidthCells":20`))
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
		acceptMountView(t, s, mountPhoto(t, "round 2", `,"maxWidthCells":20`))
		_, row = count(s.Render(80), "\x1b_G")
		if again := regexp.MustCompile(`[,G]i=(\d+)`).FindStringSubmatch(row); again == nil || again[1] != id[1] || p.conversions != 1 {
			t.Fatalf("after an update the image is %v (was %v) and was converted %d times, want the same id and once", again, id, p.conversions)
		}
	})
	t.Run("no protocol", func(t *testing.T) {
		p, _, rows := render(t, "")
		if n, _ := count(rows, halfBlockGlyph); n != 10 || p.conversions != 1 || len(p.written) != 0 {
			t.Fatalf("%d rows carry half-block cells, %d conversions, %d writes; want 10, 1 and 0", n, p.conversions, len(p.written))
		}
	})
}

// halfBlockCellRows are the rows of got that hold half-block cells.
func halfBlockCellRows(got []string) []string {
	var rows []string
	for _, row := range got {
		if strings.Contains(row, halfBlockGlyph) {
			rows = append(rows, row)
		}
	}
	return rows
}

// pig additive (D614): in a terminal without an image protocol (tmux, a plain terminal) the image node of a mount is
// drawn as half-block cells. The first render draws one empty row and asks for work off the loop; the work decodes the
// photograph and draws it for the node's box; the surface then asks for a frame, whose rows are those of halfBlockRows
// for the decoded photograph in the cells halfBlockSize gives it (20 by 10 for the square photograph in a box of 20 by
// 12 cells), in the colour form of the terminal. Nothing is written outside the rows. An update that changes a text
// draws the same rows and asks for no work.
func TestMountImageIsHalfBlockCellsWithoutAProtocol(t *testing.T) {
	for name, tc := range map[string]struct {
		trueColor bool
		depth     halfBlockDepth
	}{"true colour": {true, halfBlockTrueColor}, "256 colours": {false, halfBlock256}} {
		t.Run(name, func(t *testing.T) {
			imageTerminal(t, "")
			tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: tc.trueColor})
			p := newImageProbe(false)
			s := p.surface()
			defer s.Close()
			acceptMountView(t, s, mountPhoto(t, "round 1", `,"maxWidthCells":20`))
			first := s.Render(40)
			if drawn := drawnRows(first); len(drawn) != 2 || len(p.work) != 1 || p.conversions != 0 {
				t.Fatalf("before the photograph is drawn the mount draws %q, asked for %d pieces of work and converted %d times; want its two texts, 1 and 0", first, len(p.work), p.conversions)
			}
			p.run()
			if p.repaints != 1 || p.conversions != 1 {
				t.Fatalf("after the work: %d frames asked for, %d conversions, want 1 and 1", p.repaints, p.conversions)
			}
			decoded, err := photonimage.Decode(p.images.ToPNG(photoBytes(t)))
			if err != nil {
				t.Fatal(err)
			}
			cols, rows := halfBlockSize(512, 512, 20, 12)
			if cols != 20 || rows != 10 {
				t.Fatalf("the photograph takes %d by %d cells of a box of 20 by 12, want 20 by 10", cols, rows)
			}
			want := strings.Join(halfBlockRows(decoded, cols, rows, tc.depth, nil), "\n")
			got := s.Render(40)
			if cells := halfBlockCellRows(got); strings.Join(cells, "\n") != want {
				t.Fatalf("the mount draws %d rows of half-block cells in %.200q, want the %d rows of halfBlockRows", len(cells), got, rows)
			}
			if len(got) != len(first)-1+rows || len(p.written) != 0 {
				t.Fatalf("the mount draws %d rows (%d before) and wrote %d times outside them, want %d rows more and 0", len(got), len(first), len(p.written), rows-1)
			}
			acceptMountView(t, s, mountPhoto(t, "round 2", `,"maxWidthCells":20`))
			again := s.Render(40)
			if !strings.Contains(drawnRows(again)[0], "round 2") || strings.Join(halfBlockCellRows(again), "\n") != want {
				t.Fatalf("after the update the mount draws %.200q, want the new text and the same rows of cells", again)
			}
			if len(p.work) != 0 || p.conversions != 2 || p.repaints != 1 {
				t.Fatalf("the update asked for %d pieces of work, converted %d times and asked for %d frames, want none", len(p.work), p.conversions-2, p.repaints-1)
			}
		})
	}
}

// pig additive (D614): bytes that cannot be decoded are Pi's text fallback where the terminal has no image protocol,
// here the first half of the photograph's file.
func TestMountImageThatCannotBeDecodedIsTheTextFallback(t *testing.T) {
	imageTerminal(t, "")
	p := newImageProbe(false)
	s := p.surface()
	defer s.Close()
	data := photoBytes(t)
	data = data[:len(data)/2]
	acceptMountView(t, s, mountImageView("round 1", `,"maxWidthCells":20`, data))
	s.Render(40)
	p.run()
	got := s.Render(40)
	want := tui.ImageFallback("image/jpeg", tui.GetImageDimensions(base64.StdEncoding.EncodeToString(data), "image/jpeg"), "tower.jpg")
	if drawn := drawnRows(got); len(drawn) != 3 || drawn[1] != want || len(halfBlockCellRows(got)) != 0 || len(p.written) != 0 {
		t.Fatalf("the mount draws %q and wrote %d times, want the text fallback %q between its texts and 0", drawn, len(p.written), want)
	}
}

// pig additive (D614): an image that the client's conversion to PNG could not read is decoded by MountImages.Decode
// (the terminal view client sets photonimage.Decode, the port of Pi's Photon decode) and drawn as the half-block rows of
// the decoded image: a JPEG, a GIF and a PNG. Where the set Decode is nil the node is one line of text that names the
// missing decoder and the field: not an empty row, and not the fallback of an image that cannot be decoded.
func TestMountImageNotConvertedIsReadByDecode(t *testing.T) {
	photo, err := jpeg.Decode(bytes.NewReader(photoBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	// An 8-bit PNG: Go writes a 16-bit one for the decoded JPEG, whose low byte Go's decoder keeps and Photon's drops.
	eight := image.NewNRGBA(photo.Bounds())
	draw.Draw(eight, eight.Bounds(), photo, photo.Bounds().Min, draw.Src)
	var asGIF, asPNG bytes.Buffer
	if err := gif.Encode(&asGIF, photo, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&asPNG, eight); err != nil {
		t.Fatal(err)
	}
	for mime, data := range map[string][]byte{"image/jpeg": photoBytes(t), "image/gif": asGIF.Bytes(), "image/png": asPNG.Bytes()} {
		t.Run(mime, func(t *testing.T) {
			imageTerminal(t, "")
			draw := func(decode func([]byte) (image.Image, error)) []string {
				p := newImageProbe(false)
				p.images.ToPNG = func([]byte) []byte { return nil }
				p.images.Decode = decode
				s := p.surface()
				defer s.Close()
				acceptMountView(t, s, json.RawMessage(strings.ReplaceAll(string(mountImageView("round 1", `,"maxWidthCells":20`, data)), "image/jpeg", mime)))
				s.Render(160)
				p.run()
				return s.Render(160)
			}
			got := draw(nil)
			want := "no " + mime + " decoder is set on the MountImages of this program: set MountImages.Decode"
			if drawn := drawnRows(got); len(drawn) != 3 || !strings.HasPrefix(drawn[1], want) || len(halfBlockCellRows(got)) != 0 {
				t.Fatalf("without Decode the mount draws %.300q, want one line between its texts that starts %q", drawn, want)
			}
			decoded, err := photonimage.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			cols, rows := halfBlockSize(512, 512, 20, 12)
			wantRows := strings.Join(halfBlockRows(decoded, cols, rows, halfBlockTrueColor, nil), "\n")
			got = draw(photonimage.Decode)
			if cells := halfBlockCellRows(got); strings.Join(cells, "\n") != wantRows {
				t.Fatalf("with Decode the mount draws %d rows of half-block cells in %.200q, want the %d rows of halfBlockRows for the photonimage pixels", len(cells), got, rows)
			}
			if mime == "image/png" || mime == "image/gif" {
				// An 8-bit PNG and a palette GIF are read to the same pixels by Go's decoder and photonimage: the rows are those of the old path.
				goDecoded, err := png.Decode(bytes.NewReader(data))
				if mime == "image/gif" {
					goDecoded, err = gif.Decode(bytes.NewReader(data))
				}
				if err != nil {
					t.Fatal(err)
				}
				if old := strings.Join(halfBlockRows(goDecoded, cols, rows, halfBlockTrueColor, nil), "\n"); old != wantRows {
					t.Errorf("%s: the rows of Go's decoder differ from the rows of photonimage", mime)
				}
			}
		})
	}
}

// pig additive (D614): bytes that photonimage cannot decode are the text fallback of the image, not a blank.
func TestMountImageUndecodableIsTheFallback(t *testing.T) {
	data := photoBytes(t)
	data = append(append([]byte{}, data[:len(data)/3]...), bytes.Repeat([]byte{0}, 64)...)
	imageTerminal(t, "")
	p := newImageProbe(false)
	p.images.ToPNG = func([]byte) []byte { return nil }
	s := p.surface()
	defer s.Close()
	acceptMountView(t, s, mountImageView("round 1", `,"maxWidthCells":20`, data))
	s.Render(160)
	p.run()
	got := s.Render(160)
	if _, err := photonimage.Decode(data); err == nil {
		t.Fatal("photonimage decodes the damaged JPEG: the test needs bytes it refuses")
	}
	drawn := drawnRows(got)
	if len(drawn) != 3 || !strings.Contains(drawn[1], "[Image") || len(halfBlockCellRows(got)) != 0 {
		t.Fatalf("the mount draws %.300q, want the text fallback of the image between its texts", drawn)
	}
}
