package subprocess

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/tui"
)

// The bytes of an image are sent once, however often a frame draws it and whatever the sizes it is drawn at one after
// the other: a further size is a placement of the held image. These tests count the transmissions at the writer a
// client gives its MountImages and expect as many as the input has distinct images (the same image at two sizes at once
// is two: see TestMountImageAtTwoSizesAtOnceIsTwoImages).

// solidJPEG is a JPEG of one colour; two shades are two refs.
func solidJPEG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{shade, 255 - shade, 128, 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func refOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// imageNodeJSON is the image node of data with members (a JSON fragment that starts with a comma, or empty).
func imageNodeJSON(data []byte, members string) string {
	return `{"kind":"image","ref":"` + refOf(data) + `","mimeType":"image/jpeg","filename":"x.jpg"` + members + `}`
}

// containerView is a view whose root is a container of kind ("vstack" or "hstack") of the children, which carry the
// image nodes of datas.
func containerView(kind string, children []string, datas ...[]byte) json.RawMessage {
	var images []string
	seen := map[string]bool{}
	for _, data := range datas {
		ref := refOf(data)
		if !seen[ref] {
			seen[ref] = true
			images = append(images, `{"ref":"`+ref+`","mimeType":"image/jpeg","data":"`+base64.StdEncoding.EncodeToString(data)+`"}`)
		}
	}
	return json.RawMessage(`{"root":{"kind":"` + kind + `","children":[` + strings.Join(children, ",") + `]},"images":[` + strings.Join(images, ",") + `]}`)
}

func distinctRefs(datas ...[]byte) int {
	seen := map[string]bool{}
	for _, data := range datas {
		seen[refOf(data)] = true
	}
	return len(seen)
}

var (
	kittyPlacementHead = regexp.MustCompile(`\x1b_Ga=([Tp]),U=1,(?:f=100,)?q=2,i=(\d+),(?:p=(\d+),)?c=(\d+),r=(\d+)`)
	kittyRowHead       = regexp.MustCompile(`\x1b\[38;2;(\d+);(\d+);(\d+)m(?:\x1b\[58;2;(\d+);(\d+);(\d+)m)?`)
)

func sent(p *imageProbe) string { return strings.Join(p.written, "") }

// transmissions is the number of times the bytes of an image were written.
func transmissions(p *imageProbe) int { return strings.Count(sent(p), ",f=100,") }

// placed is a placement of an image: the image id, the placement id (0: the one its transmission made) and its cells.
type placed struct{ id, pid, cols, rows int }

// placementsWritten are the placements the bytes written create.
func placementsWritten(written string) map[placed]bool {
	got := map[placed]bool{}
	for _, m := range kittyPlacementHead.FindAllStringSubmatch(written, -1) {
		id, _ := strconv.Atoi(m[2])
		pid, _ := strconv.Atoi(m[3])
		cols, _ := strconv.Atoi(m[4])
		rows, _ := strconv.Atoi(m[5])
		got[placed{id, pid, cols, rows}] = true
	}
	return got
}

// drawnPlacements are the placements the placeholder rows of got name, in order: rows of one image, one placement id
// and one width make one.
func drawnPlacements(t *testing.T, got []string) []placed {
	t.Helper()
	var drawn []placed
	for _, row := range got {
		if !strings.Contains(row, kittyPlaceholder) {
			continue
		}
		head := kittyRowHead.FindStringSubmatch(row)
		if head == nil {
			t.Fatalf("a placeholder row does not start with the colour of its image: %.40q", row)
		}
		n := func(i int) int { v, _ := strconv.Atoi(head[i]); return v }
		this := placed{id: n(1)<<16 | n(2)<<8 | n(3), cols: strings.Count(row, kittyPlaceholder)}
		if head[4] != "" {
			this.pid = n(4)<<16 | n(5)<<8 | n(6)
		}
		if last := len(drawn) - 1; last >= 0 && drawn[last].id == this.id && drawn[last].pid == this.pid && drawn[last].cols == this.cols {
			drawn[last].rows++
		} else {
			this.rows = 1
			drawn = append(drawn, this)
		}
	}
	return drawn
}

// requirePlacedAsDrawn checks that every placement the rows name was made by the bytes written, and that its image
// was transmitted.
func requirePlacedAsDrawn(t *testing.T, p *imageProbe, got []string) []placed {
	t.Helper()
	drawn := drawnPlacements(t, got)
	made := placementsWritten(sent(p))
	for _, d := range drawn {
		if !made[d] {
			t.Fatalf("the rows draw image %d, placement %d, %d by %d cells, which no write made: %v", d.id, d.pid, d.cols, d.rows, made)
		}
	}
	return drawn
}

func imageTransmitted(p *imageProbe, id int) bool {
	return strings.Contains(sent(p), "\x1b_Ga=T,U=1,f=100,q=2,i="+strconv.Itoa(id)+",")
}

func renderPlaced(t *testing.T, p *imageProbe, s *MountSurface, view json.RawMessage, width int) []string {
	t.Helper()
	acceptMountView(t, s, view)
	s.Render(width)
	p.run()
	return s.Render(width)
}

// hstackView is a photo of 20 cells in the first entry of an HStack and a text beside it: the stack draws the image
// twice a frame, once to measure at the width of the stack and once at the width it gives it (18 cells).
func hstackView(photo []byte) json.RawMessage {
	return containerView("hstack", []string{
		imageNodeJSON(photo, `,"maxWidthCells":20,"stack":{"basis":20,"shrink":0}`),
		`{"kind":"text","text":"side","stack":{"basis":0,"grow":1}}`,
	}, photo)
}

// An image in an HStack is drawn twice a frame, at two sizes. Both draws are of one image, which is sent once; the
// next frame draws both sizes again and sends none of its bytes, and the one after it writes nothing at all (both
// placements are held).
func TestMountImageInAnHStackIsTransmittedOncePerRef(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	photo := solidJPEG(t, 10)
	got := renderPlaced(t, p, s, hstackView(photo), 80)
	if n, want := transmissions(p), distinctRefs(photo); n != want {
		t.Fatalf("an image in an HStack was transmitted %d times, want %d (one per distinct image)", n, want)
	}
	drawn := requirePlacedAsDrawn(t, p, got)
	if len(drawn) != 1 {
		t.Fatalf("the rows draw %d placements, want 1: %v", len(drawn), drawn)
	}
	// The stack composes the rows of the image with those of its neighbour: the underline colour is set and reset in each.
	for _, row := range got {
		if strings.Contains(row, kittyPlaceholder) && !strings.Contains(row, "\x1b[58;2;") || strings.Contains(row, kittyPlaceholder) && !strings.Contains(row, "\x1b[39m\x1b[59m") {
			t.Fatalf("a row of the stack does not set and reset the placement id around its cells: %q", row)
		}
	}
	for frame := 2; frame <= 3; frame++ {
		before := len(p.written)
		again := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, hstackView(photo), 80))
		if n := transmissions(p); n != distinctRefs(photo) || len(again) != 1 || again[0] != drawn[0] {
			t.Fatalf("frame %d: %d transmissions, drew %v, want %d and %v", frame, n, again, distinctRefs(photo), drawn)
		}
		if frame == 3 && len(p.written) != before {
			t.Fatalf("frame 3 wrote %q, want nothing: both placements are held", p.written[before:])
		}
	}
}

// The same image at two sizes at once is two transmissions, as it always was, and each is drawn whole. Kitty draws the
// cells of placement 0 (what a transmission makes) half once the image has a second placement (RUN, real kitty:
// xd6702 ~/lanes/ImageSentTwice/kitty-out/new/b.jpg is half, var/b3.jpg with explicit ids is whole), so a second size
// is a placement only where the first is not drawn. Do not make this one transmission.
func TestMountImageAtTwoSizesAtOnceIsTwoImages(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	photo := solidJPEG(t, 10)
	view := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":20`), imageNodeJSON(photo, `,"maxWidthCells":10`)}, photo, photo)
	got := renderPlaced(t, p, s, view, 40)
	drawn := requirePlacedAsDrawn(t, p, got)
	if len(drawn) != 2 || drawn[0].id == drawn[1].id || drawn[0].pid != 0 || drawn[1].pid != 0 || drawn[0].cols != 20 || drawn[1].cols != 10 {
		t.Fatalf("the rows draw %v, want two images of 20 and 10 cells, each by its first placement", drawn)
	}
	if n := transmissions(p); n != 2 {
		t.Fatalf("one image at two sizes at once was transmitted %d times, want 2", n)
	}
}

// A resize lays the held image out at new cells: no image bytes again, the placement of a size it had is reused
// without a write, the oldest unused placements beyond mountKeptPlacements are deleted (the image stays), and the
// first size coming back is a placement like the others, drawn whole.
func TestMountImageResizeSendsNoBytesAndKeepsFewPlacements(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	photo := solidJPEG(t, 10)
	view := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":100`)}, photo)
	first := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, view, 40))
	id := first[0].id
	var drawn []placed
	for _, width := range []int{18, 14, 10, 8, 6, 5} {
		got := requirePlacedAsDrawn(t, p, s.Render(width))
		if got[0].id != id || got[0].pid == 0 || slices.Contains(drawn, got[0]) {
			t.Fatalf("width %d drew %v, want a new placement of image %d (drawn so far %v)", width, got, id, drawn)
		}
		drawn = append(drawn, got[0])
	}
	if n, want := transmissions(p), distinctRefs(photo); n != want {
		t.Fatalf("seven sizes of one image were transmitted %d times, want %d", n, want)
	}
	oldest := "\x1b_Ga=d,d=i,i=" + strconv.Itoa(id) + ",p=" + strconv.Itoa(drawn[0].pid) + ",q=2\x1b\\"
	if strings.Count(sent(p), oldest) != 1 || strings.Contains(sent(p), tui.DeleteKittyImage(id)) {
		t.Fatalf("the oldest unused placement was not deleted alone (once), or the image was freed: %q", p.written)
	}
	before := len(p.written)
	if got := requirePlacedAsDrawn(t, p, s.Render(8)); got[0] != drawn[3] || len(p.written) != before {
		t.Fatalf("a size that has a placement drew %v and wrote %q, want %v and nothing", got, p.written[before:], drawn[3])
	}
	if got := requirePlacedAsDrawn(t, p, s.Render(40)); got[0].id != id || got[0].pid == 0 || got[0].cols != first[0].cols || got[0].rows != first[0].rows || transmissions(p) != distinctRefs(photo) {
		t.Fatalf("the first size coming back drew %v, want image %d in a placement of its own of %d by %d cells and no new bytes", got, id, first[0].cols, first[0].rows)
	}
}

// An image is freed when no placement of it has a user: of two mounts of an image in explicit placements, the end of
// one frees none and the other still draws; the end of the last one, or another image in its place, frees the image.
func TestMountImageIsFreedAtItsLastPlacement(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	photo, other := solidJPEG(t, 10), solidJPEG(t, 200)
	view := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":100`)}, photo)
	renderPlaced(t, p, s, view, 40)
	mine := requirePlacedAsDrawn(t, p, s.Render(18))
	id := mine[0].id
	if mine[0].pid == 0 {
		t.Fatalf("a resized image is drawn as %v, want an explicit placement", mine)
	}
	second := p.surface()
	got := requirePlacedAsDrawn(t, p, renderPlaced(t, p, second, view, 14))
	if got[0].id != id || got[0].pid == 0 || got[0].pid == mine[0].pid {
		t.Fatalf("a second mount of the image at another size drew %v, want image %d in another explicit placement than %v", got, id, mine)
	}
	freed := tui.DeleteKittyImage(id)
	second.Close()
	if strings.Contains(sent(p), freed) {
		t.Fatalf("the end of one of two mounts freed the image: %q", p.written)
	}
	if again := requirePlacedAsDrawn(t, p, s.Render(18)); again[0] != mine[0] {
		t.Fatalf("the other mount draws %v after the first one ended, want %v", again, mine)
	}

	replaced := containerView("vstack", []string{imageNodeJSON(other, `,"maxWidthCells":20`)}, other)
	next := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, replaced, 40))
	if n, want := transmissions(p), distinctRefs(photo, other); n != want || next[0].id == id || !imageTransmitted(p, next[0].id) {
		t.Fatalf("after another image took its place: %d transmissions, want %d; drawn %v (was image %d)", n, want, next, id)
	}
	if strings.Count(sent(p), freed) != 1 {
		t.Fatalf("the replaced image was freed %d times, want once", strings.Count(sent(p), freed))
	}
	s.Close()
	if !strings.HasSuffix(sent(p), tui.DeleteKittyImage(next[0].id)) {
		t.Fatalf("the end of the last mount did not free the image: %q", p.written)
	}
}

// An image drawn once, as a VStack does, is written as it always was: the transmission with its virtual placement
// and rows of cells without a placement id. goldenSingleImage is the SHA-256 of those bytes before placements were
// added.
func TestMountImageDrawnOnceIsWrittenAsBefore(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	photo := photoBytes(t)
	view := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":20`)}, photo)
	got := renderPlaced(t, p, s, view, 40)
	if len(p.written) != 1 || transmissions(p) != distinctRefs(photo) {
		t.Fatalf("one image drawn once wrote %d times, %d transmissions", len(p.written), transmissions(p))
	}
	sum := sha256.Sum256([]byte(strings.Join(p.written, "") + "\x00" + strings.Join(got, "\n")))
	if hex.EncodeToString(sum[:]) != goldenSingleImage {
		t.Fatalf("the bytes of one image drawn once changed: sha256 %x", sum)
	}
}

const goldenSingleImage = "deffc5d5222c9be7cd88102a67793a8b3d1c455a8f771c380f2ee01a40f8a029"

// The placement id of a row is an underline colour, which must reach the terminal from pig's own frame: on every row
// of the placement, before its cells, and reset after them, so that the colour does not stay for what follows.
func TestMountImagePlacementIDReachesTheTerminalThroughTheFrame(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	photo := solidJPEG(t, 10)
	rows := renderPlaced(t, p, s, hstackView(photo), 80)
	drawn := requirePlacedAsDrawn(t, p, rows)
	if len(drawn) != 1 || drawn[0].pid == 0 {
		t.Fatalf("the rows draw %v, want one placement with an id", drawn)
	}
	var out bytes.Buffer
	screen := tui.NewTuiAltScreenWithOutput(&out, 80, 40, tui.TuiAltScreenOptions{})
	screen.SetRenderDispatcher(func(func()) {})
	screen.Add(s)
	screen.Start()
	screen.Render()
	screen.StopWithOptions(tui.StopOptions{PreserveScreen: true})
	frame := out.String()

	underline := "\x1b[58;2;" + strconv.Itoa(drawn[0].pid>>16&0xff) + ";" + strconv.Itoa(drawn[0].pid>>8&0xff) + ";" + strconv.Itoa(drawn[0].pid&0xff) + "m"
	if n := strings.Count(frame, underline); n != drawn[0].rows {
		t.Fatalf("the frame carries the placement id on %d rows, want %d", n, drawn[0].rows)
	}
	for rest := frame; ; {
		i := strings.Index(rest, underline)
		if i < 0 {
			break
		}
		rest = rest[i+len(underline):]
		cells := 0
		for strings.HasPrefix(rest, kittyPlaceholder) {
			rest = rest[len(kittyPlaceholder):]
			for range 2 { // the row and the column diacritic
				_, n := utf8.DecodeRuneInString(rest)
				rest = rest[n:]
			}
			cells++
		}
		if cells != drawn[0].cols || !strings.HasPrefix(rest, "\x1b[39m\x1b[59m") {
			t.Fatalf("a row of the placement has %d cells (want %d) and ends in %.30q, want the reset of both colours", cells, drawn[0].cols, rest)
		}
	}
	if strings.Contains(frame, "\x1b[58") != (strings.Count(frame, "\x1b[59m") > 0) {
		t.Fatalf("the frame sets or resets an underline colour without the other")
	}
}
