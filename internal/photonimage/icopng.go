// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// The part of the png crate (png 0.17.14, the version Photon's wasm embeds) that image 0.24.9's `PngDecoder::new` and `read_image` use for the
// PNG of an ICO entry: `Decoder::new_with_limits(r, no limits)`, text chunks ignored, the EXPAND transformation, read to the end of
// the image data. Go's image/png differs from it where an image is damaged in ways the crate tolerates or refuses otherwise: a PNG that
// is complete up to its image data and the header of the chunk after it is whole to the crate whatever follows (the IEND chunk is
// never read), a tRNS chunk longer than the palette is ignored, an RGB tRNS chunk may be longer than six bytes, the checksum of
// the zlib stream is not verified, and so on.

var errPNG = errors.New("photonimage: ico: png: invalid or truncated PNG")

// pngInfo is the parts of `Info` (common.rs) that decoding needs.
type pngInfo struct {
	width, height uint32
	depth         byte
	colorType     byte
	interlaced    bool

	palette    []byte
	hasPalette bool
	trns       []byte
	hasTRNS    bool

	// Chunks that may appear once before the image data.
	phys, chrm, gama, srgb bool

	// fcTL of the default image (an animated PNG whose first frame is the IDAT data): the size of the subframe.
	frame           bool
	frameW, frameH  uint32
	seq             uint32
	haveSeq         bool
	readyForFdat    bool
	animationFrames uint32
}

// icoPNG is what `read_info` leaves: the header and where the image data starts.
type icoPNG struct {
	info      pngInfo
	idatStart int  // the start of the first IDAT (or fdAT) chunk's header
	fdat      bool // the image data is in fdAT chunks, which an fcTL before the data allows
}

// pngSamples is `ColorType::samples`.
func pngSamples(colorType byte) uint64 {
	switch colorType {
	case 0, 3:
		return 1
	case 2:
		return 3
	case 4:
		return 2
	}
	return 4
}

// outputRGBA is whether `output_color_type` with EXPAND is Rgba, 8 bits: RGBA at 8 bits, RGB at 8 bits with tRNS and palette images with tRNS.
func (i *pngInfo) outputRGBA() bool {
	switch i.colorType {
	case 6:
		return i.depth == 8
	case 2:
		return i.depth == 8 && i.hasTRNS
	case 3:
		return i.hasTRNS
	}
	return false
}

// icoPNGReadInfo is `Decoder::read_info` (decoder/mod.rs:201-238, `read_until_image_data` :405-453) over the chunks before the first IDAT.
func icoPNGReadInfo(data []byte) (*icoPNG, error) {
	if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil, errPNG
	}
	p := &icoPNG{}
	info := &p.info
	haveInfo := false
	pos := 8
	for {
		if pos+8 > len(data) {
			return nil, errPNG
		}
		length := uint64(binary.BigEndian.Uint32(data[pos:]))
		kind := string(data[pos+4 : pos+8])
		if !haveInfo && kind != "IHDR" {
			return nil, errors.New("photonimage: ico: png: chunk before IHDR")
		}
		if kind == "IDAT" {
			p.idatStart = pos
			return p, nil
		}
		if kind == "fdAT" {
			// Image data of a frame: only valid after an fcTL, and then it is the data the reader starts with (stream.rs:819-834).
			if !info.readyForFdat || length < 4 {
				return nil, errPNG
			}
			p.idatStart, p.fdat = pos, true
			return p, nil
		}
		body := uint64(pos) + 8
		if body+length+4 > uint64(len(data)) {
			return nil, errPNG
		}
		chunk := data[body : body+length]
		crc := crc32.NewIEEE()
		crc.Write(data[pos+4 : pos+8])
		crc.Write(chunk)
		// A checksum that does not match is an error for a critical chunk (an upper case first letter) only: the crate skips ancillary
		// chunks with a bad checksum by default (`skip_ancillary_crc_failures`, stream.rs:441, :875-880), after it has parsed them.
		badCRC := binary.BigEndian.Uint32(data[body+length:]) != crc.Sum32()
		pos = int(body + length + 4)

		if length == 0 && kind != "IEND" {
			// An empty chunk is never parsed (the reader goes from the chunk's header straight to its checksum, stream.rs:710-712).
			continue
		}
		if badCRC && data[body-4]&32 == 0 {
			return nil, errors.New("photonimage: ico: png: CRC mismatch")
		}
		switch kind {
		case "IHDR":
			if haveInfo || len(chunk) < 13 {
				return nil, errPNG
			}
			if err := info.parseIHDR(chunk); err != nil {
				return nil, err
			}
			haveInfo = true
		case "PLTE":
			if info.hasPalette {
				return nil, errPNG
			}
			info.palette, info.hasPalette = chunk, true
		case "tRNS":
			if err := info.parseTRNS(chunk); err != nil {
				return nil, err
			}
		case "pHYs":
			if info.phys || len(chunk) < 9 || chunk[8] > 1 {
				return nil, errPNG
			}
			info.phys = true
		case "cHRM":
			if info.chrm || len(chunk) < 32 {
				return nil, errPNG
			}
			info.chrm = true
		case "gAMA":
			if info.gama || len(chunk) < 4 {
				return nil, errPNG
			}
			info.gama = true
		case "sRGB":
			if info.srgb || len(chunk) < 1 || chunk[0] > 3 {
				return nil, errPNG
			}
			info.srgb = true
		case "acTL":
			if len(chunk) < 8 {
				return nil, errPNG
			}
			info.animationFrames = binary.BigEndian.Uint32(chunk)
		case "fcTL":
			if err := info.parseFCTL(chunk); err != nil {
				return nil, err
			}
		case "IEND":
			// The end of the image before any image data.
			return nil, errors.New("photonimage: ico: png: no image data")
		}
		// iCCP is parsed but its errors are ignored, the text chunks are ignored, and every other chunk (known or not) is skipped.
	}
}

// parseIHDR is `parse_ihdr` (decoder/stream.rs:1335-1429).
func (i *pngInfo) parseIHDR(c []byte) error {
	i.width, i.height = binary.BigEndian.Uint32(c), binary.BigEndian.Uint32(c[4:])
	if i.width == 0 || i.height == 0 {
		return errPNG
	}
	i.depth, i.colorType = c[8], c[9]
	switch i.depth {
	case 1, 2, 4, 8, 16:
	default:
		return errPNG
	}
	switch i.colorType {
	case 0, 2, 3, 4, 6:
	default:
		return errPNG
	}
	// is_combination_invalid (common.rs:71-79).
	if i.depth < 8 && (i.colorType == 2 || i.colorType == 4 || i.colorType == 6) || i.depth == 16 && i.colorType == 3 {
		return errPNG
	}
	if c[10] != 0 || c[11] != 0 || c[12] > 1 {
		return errPNG
	}
	i.interlaced = c[12] == 1
	return nil
}

// parseTRNS is `parse_trns` (decoder/stream.rs:1077-1138).
func (i *pngInfo) parseTRNS(c []byte) error {
	if i.hasTRNS {
		return errPNG
	}
	v := append([]byte(nil), c...)
	switch i.colorType {
	case 0:
		if len(v) < 2 {
			return errPNG
		}
		if i.depth < 16 {
			v = v[1:2]
		}
	case 2:
		if len(v) < 6 {
			return errPNG
		}
		if i.depth < 16 {
			v = []byte{v[1], v[3], v[5]}
		}
	case 3:
		if !i.hasPalette {
			return errPNG
		}
	default:
		return errPNG
	}
	i.trns, i.hasTRNS = v, true
	return nil
}

// parseFCTL is `parse_fctl` (decoder/stream.rs:980-1044) and `Info::validate` (:1529-1550).
func (i *pngInfo) parseFCTL(c []byte) error {
	if len(c) < 26 {
		return errPNG
	}
	seq := binary.BigEndian.Uint32(c)
	if i.haveSeq {
		if seq != i.seq+1 {
			return errPNG
		}
	} else if seq != 0 {
		return errPNG
	}
	i.seq, i.haveSeq = seq, true
	i.readyForFdat = true
	width, height := binary.BigEndian.Uint32(c[4:]), binary.BigEndian.Uint32(c[8:])
	xOffset, yOffset := binary.BigEndian.Uint32(c[12:]), binary.BigEndian.Uint32(c[16:])
	dispose, blend := c[24], c[25]
	if dispose > 2 || blend > 1 {
		return errPNG
	}
	if width == 0 || height == 0 || xOffset > i.width || width > i.width-xOffset || yOffset > i.height || height > i.height-yOffset {
		return errPNG
	}
	i.frame, i.frameW, i.frameH = true, width, height
	return nil
}

// icoPNGDecode is `Reader::next_frame` over the image data: the zlib stream of the consecutive IDAT chunks, the filters and the transformation
// to RGBA8 into a buffer of width * height * 4 bytes.
func icoPNGDecode(data []byte, p *icoPNG) ([]byte, error) {
	info := &p.info
	// The consecutive IDAT chunks, each whole with a valid CRC; the first chunk after them has to start (the `ImageDataFlushed` of the
	// reader needs its length and type).
	runKind := "IDAT"
	if p.fdat {
		runKind = "fdAT"
	}
	var z bytes.Buffer
	pos := p.idatStart
	seq := info.seq
	for {
		if pos+8 > len(data) {
			return nil, errPNG
		}
		length := uint64(binary.BigEndian.Uint32(data[pos:]))
		if string(data[pos+4:pos+8]) != runKind {
			break
		}
		body := uint64(pos) + 8
		if body+length+4 > uint64(len(data)) {
			return nil, errPNG
		}
		chunk := data[body : body+length]
		crc := crc32.NewIEEE()
		crc.Write(data[pos+4 : pos+8])
		crc.Write(chunk)
		// IDAT is critical, fdAT is not: its checksum is not enforced.
		if !p.fdat && binary.BigEndian.Uint32(data[body+length:]) != crc.Sum32() {
			return nil, errors.New("photonimage: ico: png: CRC mismatch")
		}
		if p.fdat {
			// Every fdAT chunk starts with the next sequence number of the animation.
			if length < 4 || binary.BigEndian.Uint32(chunk) != seq+1 {
				return nil, errPNG
			}
			seq++
			chunk = chunk[4:]
		}
		z.Write(chunk)
		pos = int(body + length + 4)
	}

	width, height := info.width, info.height
	subW, subH := width, height
	if info.frame {
		subW, subH = info.frameW, info.frameH
	}
	if info.colorType == 3 {
		if !info.hasPalette {
			return nil, errors.New("photonimage: ico: png: palette required")
		}
		if len(info.palette)%3 != 0 || len(info.palette)/3 > 256 {
			return nil, errors.New("photonimage: ico: png: the palette does not fit the table (the png crate panics)")
		}
	}

	// The zlib stream: a header, a deflate stream that ends, and the four bytes of the checksum (not verified).
	zb := z.Bytes()
	if len(zb) < 2 || zb[0]&0x0f != 8 || zb[0]&0xf0 > 0x70 || zb[1]&0x20 != 0 || (uint(zb[0])<<8|uint(zb[1]))%31 != 0 {
		return nil, errPNG
	}
	br := bytes.NewReader(zb[2:])
	inflated := flate.NewReader(br)

	out := make([]byte, uint64(width)*uint64(height)*4)
	rd := &pngRowReader{info: info, inflater: inflated}
	if err := rd.decode(out, subW, subH); err != nil {
		return nil, err
	}
	// The rest of the stream (finish_decoding): it has to be a whole deflate stream with the room for the checksum.
	if _, err := io.Copy(io.Discard, inflated); err != nil {
		return nil, errPNG
	}
	if br.Len() < 4 {
		return nil, errPNG
	}
	return out, nil
}

// pngRowReader unfilters and transforms the rows of an image.
type pngRowReader struct {
	info     *pngInfo
	inflater io.Reader
}

// pngPassGeometry is `Adam7Iterator`: for each pass the first column and row, the steps, and the size.
type pngPass struct{ x0, y0, dx, dy, width, lines uint32 }

func pngAdam7(width, height uint32) []pngPass {
	var passes []pngPass
	for _, p := range [7][4]uint32{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}} {
		x0, y0, dx, dy := p[0], p[1], p[2], p[3]
		if width <= x0 || height <= y0 {
			continue
		}
		passes = append(passes, pngPass{x0, y0, dx, dy, (width - x0 + dx - 1) / dx, (height - y0 + dy - 1) / dy})
	}
	return passes
}

func (r *pngRowReader) decode(out []byte, subW, subH uint32) error {
	info := r.info
	bitsPerPixel := uint64(info.depth) * pngSamples(info.colorType)
	bpp := int(max(bitsPerPixel/8, 1)) // bytes per pixel for the filters
	rowLen := func(w uint32) int { return int((uint64(w)*bitsPerPixel + 7) / 8) }

	var rgbaPalette [256][4]byte
	if info.colorType == 3 {
		rgbaPalette = pngRGBAPalette(info)
	}
	transform := func(row []byte, w uint32, dst []byte) {
		switch info.colorType {
		case 6:
			copy(dst, row)
		case 2: // 8 bits with tRNS
			for x := range int(w) {
				dst[4*x], dst[4*x+1], dst[4*x+2] = row[3*x], row[3*x+1], row[3*x+2]
				dst[4*x+3] = 0xFF
				if bytes.Equal(row[3*x:3*x+3], info.trns) {
					dst[4*x+3] = 0
				}
			}
		case 3:
			depth := int(info.depth)
			mask := byte(1<<depth - 1)
			for x := range int(w) {
				var idx byte
				if depth == 8 {
					idx = row[x]
				} else {
					bit := x * depth
					idx = row[bit/8] >> (8 - depth - bit%8) & mask
				}
				copy(dst[4*x:4*x+4], rgbaPalette[idx][:])
			}
		}
	}

	stride := int(info.width) * 4
	if !info.interlaced {
		n := rowLen(subW)
		prev := make([]byte, n)
		row := make([]byte, n)
		line := make([]byte, 1)
		for y := range int(subH) {
			if _, err := io.ReadFull(r.inflater, line); err != nil {
				return errPNG
			}
			if _, err := io.ReadFull(r.inflater, row); err != nil {
				return errPNG
			}
			if line[0] > 4 {
				return errPNG
			}
			pngUnfilter(line[0], bpp, prev, row)
			transform(row, subW, out[y*int(subW)*4:(y+1)*int(subW)*4])
			prev, row = row, prev
		}
		return nil
	}
	for _, pass := range pngAdam7(subW, subH) {
		n := rowLen(pass.width)
		prev := make([]byte, n)
		row := make([]byte, n)
		line := make([]byte, 1)
		pixel := make([]byte, int(pass.width)*4)
		for l := range pass.lines {
			if _, err := io.ReadFull(r.inflater, line); err != nil {
				return errPNG
			}
			if _, err := io.ReadFull(r.inflater, row); err != nil {
				return errPNG
			}
			if line[0] > 4 {
				return errPNG
			}
			pngUnfilter(line[0], bpp, prev, row)
			transform(row, pass.width, pixel)
			y := int(pass.y0 + l*pass.dy)
			for i := range int(pass.width) {
				x := int(pass.x0) + i*int(pass.dx)
				copy(out[y*stride+x*4:y*stride+x*4+4], pixel[4*i:4*i+4])
			}
			prev, row = row, prev
		}
	}
	return nil
}

// pngRGBAPalette is `create_rgba_palette` (decoder/transform/palette.rs:39-91): an invalid tRNS (more entries than the palette) is ignored,
// the entries of the palette without a tRNS value are opaque, and the entries after the palette are opaque black.
func pngRGBAPalette(info *pngInfo) [256][4]byte {
	var table [256][4]byte
	for i := range table {
		table[i] = [4]byte{0, 0, 0, 0xFF}
	}
	entries := len(info.palette) / 3
	trns := info.trns
	if len(trns) > entries {
		trns = nil
	}
	for i := range entries {
		table[i] = [4]byte{info.palette[3*i], info.palette[3*i+1], info.palette[3*i+2], 0xFF}
		if i < len(trns) {
			table[i][3] = trns[i]
		}
	}
	return table
}

// pngUnfilter reverses the PNG filter of a row in place; prev is the unfiltered row above (zeros for the first row of a pass).
func pngUnfilter(filter byte, bpp int, prev, row []byte) {
	switch filter {
	case 1: // Sub
		for i := bpp; i < len(row); i++ {
			row[i] += row[i-bpp]
		}
	case 2: // Up
		for i := range row {
			row[i] += prev[i]
		}
	case 3: // Average
		for i := range row {
			var left int
			if i >= bpp {
				left = int(row[i-bpp])
			}
			row[i] += byte((left + int(prev[i])) / 2)
		}
	case 4: // Paeth
		for i := range row {
			var a, c int
			if i >= bpp {
				a, c = int(row[i-bpp]), int(prev[i-bpp])
			}
			b := int(prev[i])
			p := a + b - c
			pa, pb, pc := abs(p-a), abs(p-b), abs(p-c)
			switch {
			case pa <= pb && pa <= pc:
				row[i] += byte(a)
			case pb <= pc:
				row[i] += byte(b)
			default:
				row[i] += byte(c)
			}
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
