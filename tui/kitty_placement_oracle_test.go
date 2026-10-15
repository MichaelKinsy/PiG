package tui

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// registerKittyImageMetadata, getKittyImageMetadata, getKittyImagePlacementRows, getKittyImagePlacement (chunked transmissions, malformed lines, non-ASCII text around
// the sequence) and cropKittyImageLine over random lines, with the registry (insertion order, generation counter, 1000-entry cap) starting empty on both sides.
func TestKittyPlacementMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	type meta struct{ ImageID, Columns, Rows, WidthPx, HeightPx int }
	type crop struct{ Hidden, Visible int }
	type input struct {
		Metas []meta
		Lines []string
		Crops []crop
	}
	var in input
	for range 2500 {
		height := []int{0, 1, 2, 3, 5, rng.Intn(3000), rng.Intn(3000)}[rng.Intn(7)]
		in.Metas = append(in.Metas, meta{ImageID: 1 + rng.Intn(1500), Columns: rng.Intn(100), Rows: rng.Intn(40), WidthPx: rng.Intn(3000), HeightPx: height})
	}
	prefixes := []string{"", "abc", "\x1b[3A", "é😀", "\x1b[2K\x1b[1G", "\u0141\u013d"}
	controlSets := []string{"a=T,f=100,q=2,i=%d,c=10,r=%d,m=%d", "i=%d", "a=T,i=%d,r=%d", "a=T,i=%d,r=0,c=5", "a=T,i=%d,i=%d,m=%d", "x=1,y=2,w=3,h=4,X=5,Y=6,i=%d,C=1,U=1,z=-1,P=7,Q=8,H=9,V=10,k=1,t=d,m=%d", "a=T,i=0%d,r=%d", "ii=%d,a=T", "a=T,,i=%d"}
	pad := func(s string) string { return strings.Repeat(s, rng.Intn(3)) }
	for range 4000 {
		id := 1 + rng.Intn(1500)
		controls := controlSets[rng.Intn(len(controlSets))]
		args := make([]any, 0, 3)
		for range strings.Count(controls, "%d") {
			if len(args) == 0 {
				args = append(args, id)
			} else {
				args = append(args, rng.Intn(5))
			}
		}
		controls = fmt.Sprintf(controls, args...)
		var b strings.Builder
		b.WriteString(prefixes[rng.Intn(len(prefixes))])
		chunks := 1
		if strings.Contains(controls, "m=1") {
			chunks += rng.Intn(3)
		}
		b.WriteString("\x1b_G" + controls + ";" + pad("AAAA") + "\x1b\\")
		for c := 1; c < chunks; c++ {
			switch rng.Intn(6) {
			case 0:
				b.WriteString("junk")
			case 1:
				b.WriteString("\x1b_Gm=1;BBBB")
			default:
				b.WriteString("\x1b_Gm=" + fmt.Sprint(rng.Intn(2)) + ";" + pad("CCCC") + "\x1b\\")
			}
		}
		if rng.Intn(10) == 0 {
			b.WriteString("\x1b_Gno-semicolon")
		}
		b.WriteString([]string{"", "tail", "é😀 tail", "\x1b[0m"}[rng.Intn(4)])
		if rng.Intn(15) == 0 {
			in.Lines = append(in.Lines, "no image here")
		} else {
			in.Lines = append(in.Lines, b.String())
		}
	}
	for range 4000 {
		in.Crops = append(in.Crops, crop{Hidden: rng.Intn(45) - 2, Visible: rng.Intn(45) - 2})
	}
	type placement struct {
		Found                                                               bool
		ImageID, Generation, TransmissionBytes, EstimatedDecodedBytes, Rows int
		Sequence, ReplacementLine                                           string
	}
	type result struct {
		Meta      []*meta     `json:"meta"`
		RowsOf    []*int      `json:"rowsOf"`
		Placement []placement `json:"placement"`
		Crop      []string    `json:"crop"`
	}
	var want result
	pioracle.Run(t, `
const m = await load("pi-tui/terminal-image.js");
for (const x of input.Metas) m.registerKittyImageMetadata({ imageId: x.ImageID, columns: x.Columns, rows: x.Rows, widthPx: x.WidthPx, heightPx: x.HeightPx });
emit({
	meta: input.Lines.map((l) => { const r = m.getKittyImageMetadata(l); return r ? { ImageID: r.imageId, Columns: r.columns, Rows: r.rows, WidthPx: r.widthPx, HeightPx: r.heightPx } : null; }),
	rowsOf: input.Lines.map((l) => { const r = m.getKittyImagePlacementRows(l); return r === undefined ? null : r; }),
	placement: input.Lines.map((l) => { const p = m.getKittyImagePlacement(l); return p ? { Found: true, ImageID: p.imageId, Generation: p.transmissionGeneration, TransmissionBytes: p.transmissionBytes, EstimatedDecodedBytes: p.estimatedDecodedBytes, Rows: p.rows, Sequence: p.sequence, ReplacementLine: p.replacementLine } : { Found: false }; }),
	crop: input.Lines.map((l, i) => m.cropKittyImageLine(l, input.Crops[i].Hidden, input.Crops[i].Visible)),
});
`, in, &want)

	kittyMetadataMu.Lock()
	kittyImageMetadata = map[int]registeredKittyImageMetadata{}
	kittyImageMetadataOrder = nil
	kittyTransmissionGeneration = 0
	kittyMetadataMu.Unlock()
	for _, x := range in.Metas {
		RegisterKittyImageMetadata(KittyImageMetadata(x))
	}
	bad := 0
	report := func(format string, args ...any) {
		bad++
		if bad <= 10 {
			t.Errorf(format, args...)
		}
	}
	for i, line := range in.Lines {
		if m, w := GetKittyImageMetadata(line), want.Meta[i]; (m == nil) != (w == nil) || m != nil && (m.ImageID != w.ImageID || m.Rows != w.Rows || m.Columns != w.Columns || m.WidthPx != w.WidthPx || m.HeightPx != w.HeightPx) {
			report("metadata %q: got %+v, want %+v", line, m, w)
		}
		if r, ok := GetKittyImagePlacementRows(line); (want.RowsOf[i] == nil) == ok || ok && r != *want.RowsOf[i] {
			report("placement rows %q: got %d/%v, want %v", line, r, ok, want.RowsOf[i])
		}
		p, ok := GetKittyImagePlacement(line)
		w := want.Placement[i]
		if ok != w.Found || ok && (p.ImageID != w.ImageID || p.TransmissionGeneration != w.Generation || p.TransmissionBytes != w.TransmissionBytes || p.EstimatedDecodedBytes != w.EstimatedDecodedBytes || p.Rows != w.Rows || p.Sequence != w.Sequence || p.ReplacementLine != w.ReplacementLine) {
			report("placement %q:\n got %v %+v\nwant %+v", line, ok, p, w)
		}
		if got := CropKittyImageLine(line, in.Crops[i].Hidden, in.Crops[i].Visible); got != want.Crop[i] {
			report("crop %q (%+v): got %q, want %q", line, in.Crops[i], got, want.Crop[i])
		}
	}
	if bad > 0 {
		t.Fatalf("%d cases differ from Pi", bad)
	}
}

// imageFallback with and without OSC 8 hyperlinks: home shortening, isAbsolute, and the file URL Node's pathToFileURL builds for paths with spaces, reserved
// characters, dots, trailing slashes, backslashes, control characters and non-ASCII text.
func TestImageFallbackMatchesPi(t *testing.T) {
	const home = "/home/oracle"
	// Node's os.homedir() and PiG's os.UserHomeDir() read USERPROFILE on Windows and HOME elsewhere.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	segments := []string{"a", "b c", "d%e", "f?g", "h#i", "j[k]", "l{m}", "n|o", "p^q", "r\"s", "t'u", "v<w>", "x`y", "z;=,@&+$!*()~", "é", "日本", "😀", "..", ".", "", "a\\b", "tab\there", "line\nbreak", "100%", "%41", "colon:x", "back\u007fdel"}
	rng := rand.New(rand.NewSource(5))
	type item struct {
		MIME      string
		Width     int
		Height    int
		HasDims   bool
		Filename  string
		Hyperlink bool
	}
	var items []item
	for range 3000 {
		var name string
		switch rng.Intn(5) {
		case 0:
			name = ""
		case 1:
			name = []string{"relative/x.png", "./x.png", "~", "~/x.png", "x.png"}[rng.Intn(5)]
		default:
			prefix := []string{"/", home + "/", home, home + "\\", "/tmp/", "/home/oracle2/"}[rng.Intn(6)]
			name = prefix
			for range 1 + rng.Intn(4) {
				name += segments[rng.Intn(len(segments))] + "/"
			}
			name = strings.TrimSuffix(name, "/")
			if rng.Intn(4) == 0 {
				name += "/"
			}
		}
		items = append(items, item{MIME: []string{"image/png", "image/jpeg", ""}[rng.Intn(3)], Width: rng.Intn(5000), Height: rng.Intn(5000), HasDims: rng.Intn(2) == 0, Filename: name, Hyperlink: rng.Intn(2) == 0})
	}
	// On Windows Pi's pathToFileURL throws ERR_INVALID_URL for a path that resolves to a UNC path with an invalid host
	// ("//colon:x/..."), which Pi does not catch; the probe reports those as null and PiG, which does not throw, is not compared.
	var want []*string
	pioracle.Run(t, `
const m = await load("pi-tui/terminal-image.js");
emit(input.map((x) => {
	m.setCapabilities({ images: null, trueColor: true, hyperlinks: x.Hyperlink });
	try {
		return m.imageFallback(x.MIME, x.HasDims ? { widthPx: x.Width, heightPx: x.Height } : undefined, x.Filename === "" ? undefined : x.Filename);
	} catch (error) {
		return null;
	}
}));
`, items, &want, "HOME="+home, "USERPROFILE="+home)
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	bad := 0
	for i, x := range items {
		SetCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: x.Hyperlink})
		var dims *ImageDimensions
		if x.HasDims {
			dims = &ImageDimensions{WidthPx: x.Width, HeightPx: x.Height}
		}
		if want[i] == nil {
			if runtime.GOOS != "windows" {
				bad++
				t.Errorf("Pi threw for ImageFallback(%q, hyperlinks=%v) on %s", x.Filename, x.Hyperlink, runtime.GOOS)
			}
			continue
		}
		if got := ImageFallback(x.MIME, dims, x.Filename); got != *want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("ImageFallback(%q, hyperlinks=%v):\n got %q\nwant %q", x.Filename, x.Hyperlink, got, *want[i])
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d cases differ from Pi", bad)
	}
}
