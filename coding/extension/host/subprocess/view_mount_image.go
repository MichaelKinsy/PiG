package subprocess

import (
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D614): the image node of a mount. Pi's Image component, which a D107 view draws, measures and converts
// its bytes on the first render and sends them in its line. A mount is drawn in a panel or a dialog that the client
// redraws, moves and closes, so its image node is this component instead:
//
//   - the bytes are measured and, for the Kitty protocol, converted to PNG off the loop, once per image; Render reads
//     the result and draws one empty row until it is there;
//   - in a terminal that draws Kitty Unicode placeholders the image is transmitted once, with a virtual placement, and
//     the rows are placeholder cells: text, which the terminal scrolls, covers and erases like any other text;
//   - in another Kitty terminal and in an iTerm2 terminal the rows are those of Pi's component, built from the prepared
//     bytes, with one image id per image;
//   - in a terminal without an image protocol the row is Pi's text fallback.

const (
	// mountImageRefs bounds the prepared images a MountImages keeps for the frames to come.
	mountImageRefs = 16
	// kittyChunk is the most base64 characters one chunk of a Kitty transmission carries.
	kittyChunk = 4096
	// kittyPlaceholder is the character a Kitty terminal draws a cell of a virtual placement for.
	kittyPlaceholder = "\U0010EEEE"
	// mountImageFirstID is below the first image id of a MountImages. A placeholder names its image by the 24 bits of
	// its foreground colour, so the ids stay below 1<<24.
	mountImageFirstID = 0x500000
)

// MountImages is what the mount surfaces of one client share to draw their image nodes: the prepared images, and the
// images the terminal holds. Its exported fields are set before the first surface uses it.
type MountImages struct {
	// Write writes data to the terminal ahead of the frame being drawn. It is called on the loop, from Render and
	// Close. Nil: no image is transmitted outside its rows.
	Write func(data string)
	// Placeholders reports that the terminal draws Kitty Unicode placeholders.
	Placeholders bool
	// ToPNG converts the bytes of an image to PNG and returns nil when it cannot. It is called off the loop. Nil: an
	// image that is not PNG is not drawn through the Kitty protocol.
	ToPNG func(data []byte) []byte
	// Go runs fn off the loop, never inside the call. Nil: a goroutine.
	Go func(fn func())
	// Post runs fn on the loop; it is called off the loop, when an image is prepared. Nil: fn runs where the work ran,
	// which a caller whose Repaint may be called from any goroutine can take.
	Post func(fn func())

	mu       sync.Mutex
	prepared map[string]*mountImageData
	recent   []string
	ids      map[string]int
	placed   map[mountPlacement]*mountPlaced
	lastID   int
}

// mountImageData is one image as prepared for the terminal. MountImages.mu guards it.
type mountImageData struct {
	// dims is the size of the image, nil when the bytes are not an image the terminal measures.
	dims *tui.ImageDimensions
	// png is the image as base64 PNG, "" when it was not converted; pngDims is its size, which differs from dims when
	// the conversion applied a rotation.
	png                          string
	pngDims                      *tui.ImageDimensions
	measured, converted, running bool
	waiters                      []func()
}

// mountPlacement is an image the terminal holds for placeholder cells: the image and the cells of its virtual placement.
type mountPlacement struct {
	ref        string
	cols, rows int
}

type mountPlaced struct{ id, users int }

func (m *MountImages) nextIDLocked() int {
	if m.lastID == 0 {
		m.lastID = mountImageFirstID
	}
	m.lastID++
	return m.lastID
}

// id returns the Kitty image id of the image ref, the same one for every frame and mount that draws it in its rows.
func (m *MountImages) id(ref string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id := m.ids[ref]; id != 0 {
		return id
	}
	if m.ids == nil {
		m.ids = map[string]int{}
	}
	id := m.nextIDLocked()
	m.ids[ref] = id
	return id
}

// prepare returns img as prepared, and true when its size is known and, with png, its conversion was tried. Otherwise
// it starts the work off the loop, unless it is running, and calls ready (when not nil) after it ended.
func (m *MountImages) prepare(img *viewImage, png bool, ready func()) (*mountImageData, bool) {
	m.mu.Lock()
	d := m.prepared[img.ref]
	if d == nil {
		if m.prepared == nil {
			m.prepared = map[string]*mountImageData{}
		}
		d = &mountImageData{}
		m.prepared[img.ref] = d
	}
	if i := slices.Index(m.recent, img.ref); i >= 0 {
		m.recent = slices.Delete(m.recent, i, i+1)
	}
	m.recent = append(m.recent, img.ref)
	for len(m.recent) > mountImageRefs {
		// A component keeps the data it draws; only later frames prepare a dropped image again.
		delete(m.prepared, m.recent[0])
		m.recent = m.recent[1:]
	}
	if d.measured && (!png || d.converted) {
		m.mu.Unlock()
		return d, true
	}
	if ready != nil {
		d.waiters = append(d.waiters, ready)
	}
	start, measure, convert := !d.running, !d.measured, png && !d.converted
	d.running = true
	m.mu.Unlock()
	if !start {
		return d, false
	}
	work := func() {
		var dims, pngDims *tui.ImageDimensions
		var encoded string
		if measure {
			dims = tui.GetImageDimensions(img.b64, img.mimeType)
		}
		if convert {
			switch {
			case img.mimeType == "image/png":
				encoded = img.b64
			case m.ToPNG != nil:
				if out := m.ToPNG(img.data); len(out) > 0 {
					encoded = base64.StdEncoding.EncodeToString(out)
					pngDims = tui.GetPNGDimensions(encoded)
				}
			}
		}
		m.mu.Lock()
		if measure {
			d.dims, d.measured = dims, true
		}
		if convert {
			d.png, d.pngDims, d.converted = encoded, pngDims, true
		}
		d.running = false
		waiters := d.waiters
		d.waiters = nil
		m.mu.Unlock()
		for _, ready := range waiters {
			if m.Post != nil {
				m.Post(ready)
			} else {
				ready()
			}
		}
	}
	if m.Go != nil {
		m.Go(work)
	} else {
		go work()
	}
	return d, false
}

// place makes next the placement a component holds in the place of prev (nil: it held none) and returns the id of its
// image. An image no component holds yet is transmitted with its virtual placement; one that a component of the frame
// before held is not sent again. Images no component holds any more are freed.
func (m *MountImages) place(prev *mountPlacement, next mountPlacement, png string) int {
	var out strings.Builder
	m.mu.Lock()
	if prev != nil {
		m.dropLocked(*prev)
	}
	p := m.placed[next]
	if p == nil {
		if m.placed == nil {
			m.placed = map[mountPlacement]*mountPlaced{}
		}
		p = &mountPlaced{id: m.nextIDLocked()}
		m.placed[next] = p
		writeKittyVirtualImage(&out, p.id, next.cols, next.rows, png)
	}
	p.users++
	id := p.id
	m.sweepLocked(&out)
	m.mu.Unlock()
	if out.Len() > 0 {
		m.Write(out.String())
	}
	return id
}

// release gives up a held placement. The terminal keeps the image until the next sweep, so the frame that follows an
// update takes it over without a transmission.
func (m *MountImages) release(held mountPlacement) {
	m.mu.Lock()
	m.dropLocked(held)
	m.mu.Unlock()
}

func (m *MountImages) dropLocked(held mountPlacement) {
	if p := m.placed[held]; p != nil && p.users > 0 {
		p.users--
	}
}

// sweep frees the images no component holds. It is called on the loop.
func (m *MountImages) sweep() {
	var out strings.Builder
	m.mu.Lock()
	m.sweepLocked(&out)
	m.mu.Unlock()
	if out.Len() > 0 && m.Write != nil {
		m.Write(out.String())
	}
}

func (m *MountImages) sweepLocked(out *strings.Builder) {
	var ids []int
	for key, p := range m.placed {
		if p.users == 0 {
			ids = append(ids, p.id)
			delete(m.placed, key)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		out.WriteString(tui.DeleteKittyImage(id))
	}
}

// writeKittyVirtualImage writes the transmission of a PNG as image id with a virtual placement of cols by rows cells
// (a=T,U=1): the terminal stores the image and draws it wherever placeholder cells name it. q=2 asks for no reply.
func writeKittyVirtualImage(out *strings.Builder, id, cols, rows int, png string) {
	head := "a=T,U=1,f=100,q=2,i=" + strconv.Itoa(id) + ",c=" + strconv.Itoa(cols) + ",r=" + strconv.Itoa(rows)
	for first := true; first || png != ""; first = false {
		chunk := png
		if len(chunk) > kittyChunk {
			chunk = chunk[:kittyChunk]
		}
		png = png[len(chunk):]
		more := "0"
		if png != "" {
			more = "1"
		}
		out.WriteString("\x1b_G")
		if first {
			out.WriteString(head + ",")
		}
		out.WriteString("m=" + more + ";" + chunk + "\x1b\\")
	}
}

// kittyPlaceholderRows returns rows of cols placeholder cells for the image id. Each cell carries its row and its
// column, so a row that is cut or covered in part still draws the right part of the image; the foreground colour is
// the id.
func kittyPlaceholderRows(id, cols, rows int) []string {
	color := "\x1b[38;2;" + strconv.Itoa(id>>16&0xff) + ";" + strconv.Itoa(id>>8&0xff) + ";" + strconv.Itoa(id&0xff) + "m"
	lines := make([]string, rows)
	var b strings.Builder
	for row := range rows {
		b.Reset()
		b.WriteString(color)
		for col := range cols {
			b.WriteString(kittyPlaceholder)
			b.WriteRune(kittyDiacritics[row])
			b.WriteRune(kittyDiacritics[col])
		}
		b.WriteString("\x1b[39m")
		lines[row] = b.String()
	}
	return lines
}

// mountImage is the component of one image node. The surface's lock guards it: Render and Invalidate run under it.
type mountImage struct {
	s                   *viewSurface
	img                 *viewImage
	filename            string
	maxWidth, maxHeight int
	imageID             int
	fallback            func(string) string

	waiting bool
	held    *mountPlacement
	heldID  int
	key     mountImageKey
	lines   []string
}

// mountImageKey is what the rows of a mountImage were drawn for.
type mountImageKey struct {
	width, maxHeight int
	protocol         tui.ImageProtocol
	cell             tui.CellDimensions
	data             *mountImageData
}

var mountImagePending = []string{""}

// mountImage returns the component of the image node n, whose bytes are img.
func (b *viewBuilder) mountImage(n *ViewNode, img *viewImage, fallback func(string) string) tui.Component {
	c := &mountImage{s: b.s, img: img, filename: n.Filename, maxWidth: intOr(n.MaxWidthCells, 0), maxHeight: intOr(n.MaxHeightCells, 0), imageID: intOr(n.ImageID, 0), fallback: fallback}
	b.disposers = append(b.disposers, c.dispose)
	return c
}

func (c *mountImage) Invalidate() { c.lines = nil }

// dispose runs when the frame of the component is replaced or its surface closes.
func (c *mountImage) dispose() {
	c.s.mu.Lock()
	held := c.held
	c.held = nil
	c.s.mu.Unlock()
	if held != nil {
		c.s.mountImages.release(*held)
	}
}

// wake returns what runs once the image is prepared: the surface draws again. It returns nil while one is pending.
func (c *mountImage) wake() func() {
	if c.waiting {
		return nil
	}
	c.waiting = true
	return func() {
		s := c.s
		s.mu.Lock()
		c.waiting = false
		if s.frame != nil && s.frame.root != nil {
			s.frame.root.Invalidate()
		}
		c.lines = nil
		s.rev++
		repaint := s.repaint
		s.mu.Unlock()
		if repaint != nil {
			repaint()
		}
	}
}

func (c *mountImage) Render(width int) []string {
	images := c.s.mountImages
	caps := tui.GetCapabilities()
	kitty := caps.Images == tui.ImageProtocolKitty
	data, ok := images.prepare(c.img, kitty, c.wake())
	if !ok {
		return mountImagePending
	}
	// The width and the default height are those of Pi's component (image.ts render).
	maxWidth := 60 // upstream: packages/tui/src/components/image.ts:render
	if c.maxWidth > 0 {
		maxWidth = c.maxWidth
	}
	maxWidth = max(1, min(width-2, maxWidth))
	cell := tui.GetCellDimensions()
	maxHeight := c.maxHeight
	if maxHeight <= 0 {
		maxHeight = max(1, (maxWidth*cell.WidthPx+cell.HeightPx-1)/cell.HeightPx)
	}
	// A mount is cut to the rows of its slot, so its image is at most that tall.
	if c.s.rowBudget != nil {
		if budget := c.s.rowBudget(); budget > 0 {
			maxHeight = min(maxHeight, budget)
		}
	}
	key := mountImageKey{width: width, maxHeight: maxHeight, protocol: caps.Images, cell: cell, data: data}
	if c.lines != nil && c.key == key {
		return c.lines
	}
	c.key = key
	dims, b64, mime := data.dims, c.img.b64, c.img.mimeType
	if kitty && data.png != "" {
		b64, mime = data.png, "image/png"
		if data.pngDims != nil {
			dims = data.pngDims
		}
	}
	switch {
	case dims == nil || caps.Images == "" || (kitty && data.png == ""):
		c.unplace()
		c.lines = []string{widthx.TruncateToWidth(c.fallback(tui.ImageFallback(c.img.mimeType, data.dims, c.filename)), width, "...", false)}
	case kitty && images.Placeholders && images.Write != nil:
		size := tui.CalculateImageCellSize(*dims, maxWidth, maxHeight, cell)
		next := mountPlacement{ref: c.img.ref, cols: min(size.Columns, len(kittyDiacritics)), rows: min(size.Rows, len(kittyDiacritics))}
		if c.held == nil || *c.held != next {
			c.heldID = images.place(c.held, next, data.png)
			c.held = &next
		}
		c.lines = kittyPlaceholderRows(c.heldID, next.cols, next.rows)
	default:
		c.unplace()
		id := c.imageID
		if id == 0 && kitty {
			id = images.id(c.img.ref)
		}
		inner := tui.NewImage(b64, mime, tui.ImageTheme{FallbackColor: c.fallback}, tui.ImageOptions{MaxWidthCells: c.maxWidth, MaxHeightCells: maxHeight, Filename: c.filename, ImageID: id}, dims)
		c.lines = inner.Render(width)
	}
	return c.lines
}

// unplace gives up the placeholder image the component held, for rows that do not use it.
func (c *mountImage) unplace() {
	if c.held != nil {
		c.s.mountImages.release(*c.held)
		c.held = nil
	}
}
