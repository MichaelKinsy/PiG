package tui

import (
	"encoding/base64"
	"encoding/binary"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/tui/src/terminal-image.ts

// imageSample builds one image header of the given kind with random sizes and random trailing bytes, then truncates, corrupts or re-spells its base64 text the ways Node's
// Buffer.from(text, "base64") tolerates: missing padding, URL-safe letters, line breaks, stray characters, and text after a '='.
func imageSample(rng *rand.Rand) (string, string) {
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	u16 := func() uint16 { return uint16(rng.Intn(1 << 16)) }
	var raw []byte
	mime := pick("image/png", "image/jpeg", "image/gif", "image/webp", "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "")
	switch rng.Intn(6) {
	case 0:
		raw = append([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 'I', 'H', 'D', 'R'}, binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, rng.Uint32()), rng.Uint32())...)
	case 1:
		raw = []byte{0xff, 0xd8}
		for range rng.Intn(4) {
			raw = append(raw, 0xff, byte(0xe0+rng.Intn(16)))
			n := 2 + rng.Intn(12)
			raw = binary.BigEndian.AppendUint16(raw, uint16(n))
			for range n - 2 {
				raw = append(raw, byte(rng.Intn(256)))
			}
		}
		raw = append(raw, 0xff, byte(0xbf+rng.Intn(6)), 0, 17, 8)
		raw = binary.BigEndian.AppendUint16(raw, u16())
		raw = binary.BigEndian.AppendUint16(raw, u16())
	case 2:
		raw = append([]byte(pick("GIF87a", "GIF89a", "GIF88a")), byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256)))
	case 3:
		raw = append([]byte("RIFF\x00\x00\x00\x00WEBP"+pick("VP8 ", "VP8L", "VP8X", "VP8Z")), make([]byte, 14+rng.Intn(4))...)
		for i := 16; i < len(raw); i++ {
			raw[i] = byte(rng.Intn(256))
		}
	case 4:
		raw = make([]byte, rng.Intn(40))
		for i := range raw {
			raw[i] = byte(rng.Intn(256))
		}
	default:
		raw = []byte{0xff, 0xd8}
		for range rng.Intn(30) {
			raw = append(raw, byte(rng.Intn(256)))
		}
	}
	for range rng.Intn(3) {
		raw = append(raw, byte(rng.Intn(256)))
	}
	if rng.Intn(5) == 0 && len(raw) > 0 {
		raw = raw[:rng.Intn(len(raw)+1)]
	}
	text := base64.StdEncoding.EncodeToString(raw)
	switch rng.Intn(10) {
	case 0:
		text = strings.TrimRight(text, "=")
	case 1:
		text = strings.NewReplacer("+", "-", "/", "_").Replace(text)
	case 2:
		text = strings.NewReplacer("+", "-", "/", "_").Replace(strings.TrimRight(text, "="))
	case 3:
		if len(text) > 4 {
			at := rng.Intn(len(text))
			text = text[:at] + pick("\n", "\r\n", " ", "\t", "!", "*", "\u00e9", "\x00") + text[at:]
		}
	case 4:
		text += pick("=", "==", "===", "=AAAA", "A", "AA", "\n")
	case 5:
		if len(text) > 4 {
			at := rng.Intn(len(text))
			text = text[:at] + "=" + text[at:]
		}
	}
	return text, mime
}

// getPngDimensions, getJpegDimensions, getGifDimensions, getWebpDimensions and getImageDimensions over image headers whose base64 text Node decodes leniently.
func TestImageDimensionsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(401))
	type sample struct {
		Data string `json:"data"`
		Mime string `json:"mime"`
	}
	var inputs []sample
	for range 6000 {
		data, mime := imageSample(rng)
		inputs = append(inputs, sample{data, mime})
	}
	type dims struct {
		W int `json:"w"`
		H int `json:"h"`
	}
	type result struct {
		Png  *dims `json:"png"`
		Jpeg *dims `json:"jpeg"`
		Gif  *dims `json:"gif"`
		Webp *dims `json:"webp"`
		Any  *dims `json:"any"`
	}
	var want []result
	pioracle.Run(t, `
const m = await load("pi-tui/terminal-image.js");
const d = (v) => v === null ? null : { w: v.widthPx, h: v.heightPx };
emit(input.map((s) => ({
	png: d(m.getPngDimensions(s.data)),
	jpeg: d(m.getJpegDimensions(s.data)),
	gif: d(m.getGifDimensions(s.data)),
	webp: d(m.getWebpDimensions(s.data)),
	any: d(m.getImageDimensions(s.data, s.mime)),
})));
`, inputs, &want)
	conv := func(v *ImageDimensions) *dims {
		if v == nil {
			return nil
		}
		return &dims{v.WidthPx, v.HeightPx}
	}
	same := func(a, b *dims) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	bad := 0
	for i, s := range inputs {
		got := result{conv(GetPNGDimensions(s.Data)), conv(GetJPEGDimensions(s.Data)), conv(GetGIFDimensions(s.Data)), conv(GetWebPDimensions(s.Data)), conv(GetImageDimensions(s.Data, s.Mime))}
		w := want[i]
		if !same(got.Png, w.Png) || !same(got.Jpeg, w.Jpeg) || !same(got.Gif, w.Gif) || !same(got.Webp, w.Webp) || !same(got.Any, w.Any) {
			bad++
			if bad <= 8 {
				t.Errorf("%q (%s): got %+v want %+v", s.Data, s.Mime, got, w)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d samples differ from Pi", bad, len(inputs))
	}
}

// calculateImageCellSize, calculateImageRows, encodeKitty (single and chunked payloads, every option) and encodeITerm2 (padding-sensitive size, numeric and string width and
// height, a UTF-8 name, both boolean switches).
func TestImageSizingAndEncodingMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(402))
	type size struct {
		W, H, MaxW, MaxH, CellW, CellH int
		Optimize                       bool
	}
	type encode struct {
		Data                   string
		Columns, Rows, ImageID int
		MoveCursor             *bool
		Width, Height          any
		Name                   string
		Preserve, Inline       *bool
	}
	type input struct {
		Sizes   []size
		Encodes []encode
	}
	pickBool := func() *bool {
		switch rng.Intn(3) {
		case 0:
			return nil
		case 1:
			v := true
			return &v
		}
		v := false
		return &v
	}
	var in input
	for range 6000 {
		dim := func() int { return []int{0, 1, 2, 7, 9, 18, 100, 640, 1024, 4000, rng.Intn(5000)}[rng.Intn(11)] }
		s := size{W: dim(), H: dim(), MaxW: rng.Intn(260) - 3, CellW: 1 + rng.Intn(30), CellH: 1 + rng.Intn(60), Optimize: rng.Intn(2) == 0}
		if rng.Intn(3) != 0 {
			s.MaxH = 1 + rng.Intn(70)
		}
		in.Sizes = append(in.Sizes, s)
	}
	for range 1500 {
		n := []int{0, 1, 100, 4095, 4096, 4097, 8192, 8193, 12288, 12289, rng.Intn(20000)}[rng.Intn(11)]
		e := encode{Data: strings.Repeat("Ab+/", n/4+1)[:n], Columns: rng.Intn(120), Rows: rng.Intn(60), ImageID: []int{0, 1, 7, 4294967295, rng.Intn(1 << 30)}[rng.Intn(5)], MoveCursor: pickBool(), Preserve: pickBool(), Inline: pickBool()}
		if rng.Intn(2) == 0 {
			e.Data = e.Data[:len(e.Data)/4*4] + []string{"", "=", "==", "A", "AA="}[rng.Intn(5)]
		}
		switch rng.Intn(4) {
		case 0:
			e.Width = rng.Intn(200)
		case 1:
			e.Width = []string{"auto", "50%", "10px", "", "12"}[rng.Intn(5)]
		}
		switch rng.Intn(4) {
		case 0:
			e.Height = rng.Intn(200)
		case 1:
			e.Height = []string{"auto", "50%", "10px", "", "12"}[rng.Intn(5)]
		}
		e.Name = []string{"", "a.png", "ключ.png", "emoji😀.png", "a b;c:d.png"}[rng.Intn(5)]
		in.Encodes = append(in.Encodes, e)
	}
	type wantResult struct {
		Sizes   [][3]int    `json:"sizes"`
		Encodes [][3]string `json:"encodes"`
	}
	var want wantResult
	pioracle.Run(t, `
const m = await load("pi-tui/terminal-image.js");
emit({
	sizes: input.Sizes.map((s) => {
		const dims = { widthPx: s.W, heightPx: s.H };
		const cell = { widthPx: s.CellW, heightPx: s.CellH };
		const r = m.calculateImageCellSize(dims, s.MaxW, s.MaxH === 0 ? undefined : s.MaxH, cell, s.Optimize);
		return [r.columns, r.rows, m.calculateImageRows(dims, s.MaxW, cell)];
	}),
	encodes: input.Encodes.map((e) => [
		m.encodeKitty(e.Data, { columns: e.Columns, rows: e.Rows, imageId: e.ImageID, moveCursor: e.MoveCursor === null ? undefined : e.MoveCursor }),
		m.encodeITerm2(e.Data, { width: e.Width === null ? undefined : e.Width, height: e.Height === null ? undefined : e.Height, name: e.Name, preserveAspectRatio: e.Preserve === null ? undefined : e.Preserve, inline: e.Inline === null ? undefined : e.Inline }),
		m.hyperlink(e.Name, e.Data.slice(0, 20)),
	]),
});
`, in, &want)
	bad := 0
	for i, s := range in.Sizes {
		cell := CellDimensions{WidthPx: s.CellW, HeightPx: s.CellH}
		dims := ImageDimensions{WidthPx: s.W, HeightPx: s.H}
		got := CalculateImageCellSize(dims, s.MaxW, s.MaxH, cell, s.Optimize)
		rows := CalculateImageRows(dims, s.MaxW, cell)
		if w := want.Sizes[i]; got.Columns != w[0] || got.Rows != w[1] || rows != w[2] {
			bad++
			if bad <= 8 {
				t.Errorf("size %+v: got %d x %d (rows %d), want %v", s, got.Columns, got.Rows, rows, w)
			}
		}
	}
	for i, e := range in.Encodes {
		kitty := EncodeKitty(e.Data, e.Columns, e.Rows, e.ImageID, e.MoveCursor == nil || *e.MoveCursor)
		iterm := EncodeITerm2(e.Data, ITerm2Options{Width: e.Width, Height: e.Height, Name: e.Name, PreserveAspectRatio: e.Preserve, Inline: e.Inline})
		link := Hyperlink(e.Name, e.Data[:min(20, len(e.Data))])
		if w := want.Encodes[i]; kitty != w[0] || iterm != w[1] || link != w[2] {
			bad++
			if bad <= 8 {
				t.Errorf("encode %d (len %d %+v): kitty equal %v, iterm2 equal %v, link equal %v", i, len(e.Data), e, kitty == w[0], iterm == w[1], link == w[2])
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d cases differ from Pi", bad)
	}
}
