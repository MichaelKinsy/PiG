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
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// A Kitty terminal holds one image per ref: the bytes of an image are sent once, whatever the number of sizes it is
// drawn at, and a further size is a placement of the held image. These tests count the transmissions at the writer a
// client gives its MountImages, and expect as many as the input has distinct refs.

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

// An image in an HStack is drawn twice a frame: once to measure at the width of the stack and once at the width the
// stack gives it. Both draws are of one image, which is sent once.
func TestMountImageInAnHStackIsTransmittedOncePerRef(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	photo := solidJPEG(t, 10)
	view := containerView("hstack", []string{
		imageNodeJSON(photo, `,"maxWidthCells":20,"stack":{"basis":20,"shrink":0}`),
		`{"kind":"text","text":"side","stack":{"basis":0,"grow":1}}`,
	}, photo)
	got := renderPlaced(t, p, s, view, 80)
	if n, want := transmissions(p), distinctRefs(photo); n != want {
		t.Fatalf("an image in an HStack was transmitted %d times, want %d (one per distinct image)", n, want)
	}
	drawn := requirePlacedAsDrawn(t, p, got)
	if len(drawn) != 1 {
		t.Fatalf("the rows draw %d placements, want 1: %v", len(drawn), drawn)
	}
	// The stack composes the rows of the image with those of its neighbour: the underline colour is set and reset in each.
	for _, row := range got {
		if strings.Contains(row, kittyPlaceholder) && drawn[0].pid != 0 && !strings.Contains(row, "\x1b[39m\x1b[59m") {
			t.Fatalf("a row of the stack does not reset both colours after its cells: %q", row)
		}
	}
}

// The same image at two sizes at once is one transmission and two placements; the cells of each name their own.
func TestMountImageAtTwoSizesIsOneTransmissionAndTwoPlacements(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	photo := solidJPEG(t, 10)
	view := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":20`), imageNodeJSON(photo, `,"maxWidthCells":10`)}, photo, photo)
	got := renderPlaced(t, p, s, view, 40)
	if n, want := transmissions(p), distinctRefs(photo, photo); n != want {
		t.Fatalf("one image at two sizes was transmitted %d times, want %d", n, want)
	}
	drawn := requirePlacedAsDrawn(t, p, got)
	if len(drawn) != 2 || drawn[0].id != drawn[1].id || drawn[0].pid == drawn[1].pid || drawn[0].cols != 20 || drawn[1].cols != 10 {
		t.Fatalf("the rows draw %v, want one image in two placements of 20 and 10 cells", drawn)
	}
}

// A resize lays the held image out at new cells: no image bytes again, the placement it leaves is deleted (the image
// stays), and a size the image had before is its placement again.
func TestMountImageResizeSendsNoBytesAndDeletesTheOldPlacement(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	photo := solidJPEG(t, 10)
	view := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":100`)}, photo)
	first := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, view, 40))
	id := first[0].id
	second := requirePlacedAsDrawn(t, p, s.Render(18))
	third := requirePlacedAsDrawn(t, p, s.Render(14))
	if second[0].id != id || third[0].id != id || first[0].cols == second[0].cols || second[0].cols == third[0].cols {
		t.Fatalf("the three widths drew %v, %v and %v, want one image in three sizes", first, second, third)
	}
	if n, want := transmissions(p), distinctRefs(photo); n != want {
		t.Fatalf("three sizes of one image were transmitted %d times, want %d", n, want)
	}
	lower := "\x1b_Ga=d,d=i,i=" + strconv.Itoa(id) + ",p=" + strconv.Itoa(second[0].pid) + ",q=2\x1b\\"
	if !strings.Contains(sent(p), lower) || strings.Contains(sent(p), tui.DeleteKittyImage(id)) {
		t.Fatalf("the placement of the second size was not deleted alone, or the image was freed: %q", sent(p)[len(sent(p))-80:])
	}
	before := len(p.written)
	again := requirePlacedAsDrawn(t, p, s.Render(40))
	dropped := "\x1b_Ga=d,d=i,i=" + strconv.Itoa(id) + ",p=" + strconv.Itoa(third[0].pid) + ",q=2\x1b\\"
	if again[0] != first[0] || strings.Join(p.written[before:], "") != dropped || transmissions(p) != distinctRefs(photo) {
		t.Fatalf("the first size again drew %v and wrote %q, want %v and only the delete of the third placement", again, p.written[before:], first)
	}
}

// An image is freed when no placement of it has a user: dropping one of two placements frees that placement alone,
// whichever it is, and the other still draws; the end of the last one, or another image in its place, frees the image.
func TestMountImageIsFreedAtItsLastPlacement(t *testing.T) {
	imageTerminal(t, tui.ImageProtocolKitty)
	p := newImageProbe(true)
	s := p.surface()
	defer s.Close()
	photo, other := solidJPEG(t, 10), solidJPEG(t, 200)
	both := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":20`), imageNodeJSON(photo, `,"maxWidthCells":10`)}, photo, photo)
	drawn := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, both, 40))
	if len(drawn) != 2 {
		t.Fatalf("two placements are drawn as %v", drawn)
	}
	id, big, small := drawn[0].id, drawn[0], drawn[1]
	freed := tui.DeleteKittyImage(id)

	onlySmall := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":10`)}, photo)
	if rows := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, onlySmall, 40)); len(rows) != 1 || rows[0] != small {
		t.Fatalf("after the large one was dropped the rows draw %v, want %v", rows, small)
	}
	if strings.Contains(sent(p), freed) {
		t.Fatalf("the image was freed while a placement of it was in use: %q", p.written)
	}

	if rows := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, both, 40)); len(rows) != 2 {
		t.Fatalf("after both were drawn again the rows draw %v", rows)
	}
	onlyBig := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":20`)}, photo)
	if rows := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, onlyBig, 40)); len(rows) != 1 || rows[0] != big {
		t.Fatalf("after the small one was dropped the rows draw %v, want %v", rows, big)
	}
	if strings.Contains(sent(p), freed) || !strings.Contains(sent(p), "\x1b_Ga=d,d=i,i="+strconv.Itoa(id)+",p="+strconv.Itoa(small.pid)+",q=2\x1b\\") {
		t.Fatalf("the small placement was not deleted alone: %q", p.written)
	}

	replaced := containerView("vstack", []string{imageNodeJSON(other, `,"maxWidthCells":20`)}, other)
	next := requirePlacedAsDrawn(t, p, renderPlaced(t, p, s, replaced, 40))
	if n, want := transmissions(p), distinctRefs(photo, other); n != want || next[0].id == id || !imageTransmitted(p, next[0].id) {
		t.Fatalf("after another image took its place: %d transmissions, want %d; drawn %v (was image %d)", n, want, next, id)
	}
	if strings.Count(sent(p), freed) != 1 {
		t.Fatalf("the replaced image was freed %d times, want once", strings.Count(sent(p), freed))
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
	view := containerView("vstack", []string{imageNodeJSON(photo, `,"maxWidthCells":20`), imageNodeJSON(photo, `,"maxWidthCells":10`)}, photo, photo)
	rows := renderPlaced(t, p, s, view, 40)
	drawn := requirePlacedAsDrawn(t, p, rows)
	if len(drawn) != 2 || drawn[1].pid == 0 {
		t.Fatalf("the rows draw %v, want a second placement with an id", drawn)
	}
	var out bytes.Buffer
	screen := tui.NewTuiAltScreenWithOutput(&out, 40, 40, tui.TuiAltScreenOptions{})
	screen.SetRenderDispatcher(func(func()) {})
	screen.Add(s)
	screen.Start()
	screen.Render()
	screen.StopWithOptions(tui.StopOptions{PreserveScreen: true})
	frame := out.String()

	underline := "\x1b[58;2;" + strconv.Itoa(drawn[1].pid>>16&0xff) + ";" + strconv.Itoa(drawn[1].pid>>8&0xff) + ";" + strconv.Itoa(drawn[1].pid&0xff) + "m"
	if n := strings.Count(frame, underline); n != drawn[1].rows {
		t.Fatalf("the frame carries the placement id on %d rows, want %d", n, drawn[1].rows)
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
			rest = rest[len(string([]rune(rest)[0])):]
			rest = rest[len(string([]rune(rest)[0])):]
			cells++
		}
		if cells != drawn[1].cols || !strings.HasPrefix(rest, "\x1b[39m\x1b[59m") {
			t.Fatalf("a row of the placement has %d cells (want %d) and ends in %.30q, want the reset of both colours", cells, drawn[1].cols, rest)
		}
	}
	if strings.Contains(frame, "\x1b[58") != (strings.Count(frame, "\x1b[59m") > 0) {
		t.Fatalf("the frame sets or resets an underline colour without the other")
	}
}
