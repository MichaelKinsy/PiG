// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import "math/bits"

// Ports the lossy WebP decoder of image 0.24.9 (crates.io image-0.24.9.crate, the version Photon's wasm embeds): src/codecs/webp/vp8.rs
// (cited below as vp8.rs), loop_filter.rs and transform.rs. It is a literal port, quirks included: the chroma planes are upsampled by
// repeating each sample (no fancy upsampling), a stream without segment feature data ignores its base quantizer index, the intra
// prediction of the chroma planes reads the final pixels of the neighbours, a partition that runs out of data reads zeros. What Photon
// throws on, DecodeWebP returns as an error; what it tolerates, DecodeWebP tolerates the same way.

const (
	vp8MaxSegments  = 4
	vp8NumDCTTokens = 12
)

// Prediction modes (vp8.rs:33-48).
const (
	vp8DCPred int8 = iota
	vp8VPred
	vp8HPred
	vp8TMPred
	vp8BPred
)

const (
	vp8BDCPred int8 = iota
	vp8BTMPred
	vp8BVEPred
	vp8BHEPred
	vp8BLDPred
	vp8BRDPred
	vp8BVRPred
	vp8BVLPred
	vp8BHDPred
	vp8BHUPred
)

// vp8SegmentIDTree is SEGMENT_ID_TREE (vp8.rs:106).
var vp8SegmentIDTree = [6]int8{2, 4, -0, -1, -2, -3}

// vp8KeyframeYModeTree is KEYFRAME_YMODE_TREE (vp8.rs:110).
var vp8KeyframeYModeTree = [8]int8{-vp8BPred, 2, 4, 6, -vp8DCPred, -vp8VPred, -vp8HPred, -vp8TMPred}

// vp8KeyframeYModeProbs is KEYFRAME_YMODE_PROBS (vp8.rs:113).
var vp8KeyframeYModeProbs = [4]uint8{145, 156, 163, 128}

// vp8KeyframeBPredModeTree is KEYFRAME_BPRED_MODE_TREE (vp8.rs:116-119).
var vp8KeyframeBPredModeTree = [18]int8{
	-vp8BDCPred, 2, -vp8BTMPred, 4, -vp8BVEPred, 6, 8, 12, -vp8BHEPred, 10, -vp8BRDPred, -vp8BVRPred,
	-vp8BLDPred, 14, -vp8BVLPred, 16, -vp8BHDPred, -vp8BHUPred,
}

// vp8KeyframeUVModeTree is KEYFRAME_UV_MODE_TREE (vp8.rs:246).
var vp8KeyframeUVModeTree = [6]int8{-vp8DCPred, 2, -vp8VPred, 4, -vp8HPred, -vp8TMPred}

// vp8KeyframeUVModeProbs is KEYFRAME_UV_MODE_PROBS (vp8.rs:249).
var vp8KeyframeUVModeProbs = [3]uint8{142, 114, 183}

// DCT tokens (vp8.rs:600-611).
const (
	vp8DCT0 int8 = iota
	vp8DCT1
	vp8DCT2
	vp8DCT3
	vp8DCT4
	vp8DCTCat1
	vp8DCTCat2
	vp8DCTCat3
	vp8DCTCat4
	vp8DCTCat5
	vp8DCTCat6
	vp8DCTEOB
)

// vp8DCTTokenTree is DCT_TOKEN_TREE (vp8.rs:613-616).
var vp8DCTTokenTree = [22]int8{
	-vp8DCTEOB, 2, -vp8DCT0, 4, -vp8DCT1, 6, 8, 12, -vp8DCT2, 10, -vp8DCT3, -vp8DCT4, 14, 16, -vp8DCTCat1,
	-vp8DCTCat2, 18, 20, -vp8DCTCat3, -vp8DCTCat4, -vp8DCTCat5, -vp8DCTCat6,
}

// vp8ProbDCTCat is PROB_DCT_CAT (vp8.rs:618-625).
var vp8ProbDCTCat = [6][12]uint8{
	{159, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{165, 145, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{173, 148, 140, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{176, 155, 140, 135, 0, 0, 0, 0, 0, 0, 0, 0},
	{180, 157, 141, 134, 130, 0, 0, 0, 0, 0, 0, 0},
	{254, 254, 243, 230, 196, 177, 153, 140, 133, 130, 129, 0},
}

// vp8DCTCatBase is DCT_CAT_BASE (vp8.rs:627).
var vp8DCTCatBase = [6]uint8{5, 7, 11, 19, 35, 67}

// vp8CoeffBands is COEFF_BANDS (vp8.rs:628).
var vp8CoeffBands = [16]uint8{0, 1, 2, 3, 6, 4, 5, 6, 6, 6, 6, 6, 6, 6, 6, 7}

// vp8Zigzag is ZIGZAG (vp8.rs:670).
var vp8Zigzag = [16]uint8{0, 1, 4, 8, 5, 2, 3, 6, 9, 12, 13, 10, 7, 11, 14, 15}

// vp8BoolReader is BoolReader (vp8.rs:727-837).
type vp8BoolReader struct {
	buf      []byte
	index    int
	rng      uint32
	value    uint32
	bitCount uint8
}

// init is BoolReader::init (vp8.rs:747-760); fewer than two bytes is DecoderError::NotEnoughInitData.
func (b *vp8BoolReader) init(buf []byte) error {
	if len(buf) < 2 {
		return errWebPVP8NotEnoughInitData
	}
	b.buf = buf
	b.value = uint32(buf[0])<<8 | uint32(buf[1])
	b.index = 2
	b.rng = 255
	b.bitCount = 0
	return nil
}

// readBool is BoolReader::read_bool (vp8.rs:762-793). A reader that has run out of bytes shifts in zeros. The Rust code normalizes one bit at
// a time; the shift is the same all at once: a byte is taken in at the step the bit count reaches 8.
func (b *vp8BoolReader) readBool(probability uint8) bool {
	split := 1 + (((b.rng - 1) * uint32(probability)) >> 8)
	bigsplit := split << 8

	var retval bool
	if b.value >= bigsplit {
		b.rng -= split
		b.value -= bigsplit
		retval = true
	} else {
		b.rng = split
	}

	if b.rng < 128 {
		n := uint(bits.LeadingZeros8(uint8(b.rng)))
		b.rng <<= n
		count := uint(b.bitCount) + n
		if count >= 8 {
			b.value <<= 8 - uint(b.bitCount)
			if b.index < len(b.buf) {
				b.value |= uint32(b.buf[b.index])
				b.index++
			}
			b.value <<= count - 8
			b.bitCount = uint8(count - 8)
		} else {
			b.value <<= n
			b.bitCount = uint8(count)
		}
	}

	return retval
}

// readToken is read_with_tree (vp8.rs:818-832) with DCT_TOKEN_TREE and the probabilities of a context, the tree unrolled; after a zero
// token (afterZero) the tree is entered at its second node, where an end of block cannot be.
func (b *vp8BoolReader) readToken(p *[vp8NumDCTTokens - 1]uint8, afterZero bool) int8 {
	if !afterZero && !b.readBool(p[0]) {
		return vp8DCTEOB
	}
	if !b.readBool(p[1]) {
		return vp8DCT0
	}
	if !b.readBool(p[2]) {
		return vp8DCT1
	}
	if !b.readBool(p[3]) {
		if !b.readBool(p[4]) {
			return vp8DCT2
		}
		if !b.readBool(p[5]) {
			return vp8DCT3
		}
		return vp8DCT4
	}
	if !b.readBool(p[6]) {
		if !b.readBool(p[7]) {
			return vp8DCTCat1
		}
		return vp8DCTCat2
	}
	if !b.readBool(p[8]) {
		if !b.readBool(p[9]) {
			return vp8DCTCat3
		}
		return vp8DCTCat4
	}
	if !b.readBool(p[10]) {
		return vp8DCTCat5
	}
	return vp8DCTCat6
}

// readLiteral is BoolReader::read_literal (vp8.rs:795-805).
func (b *vp8BoolReader) readLiteral(n uint8) uint8 {
	var v uint8
	for ; n != 0; n-- {
		v <<= 1
		if b.readBool(128) {
			v++
		}
	}
	return v
}

// readMagnitudeAndSign is BoolReader::read_magnitude_and_sign (vp8.rs:807-816).
func (b *vp8BoolReader) readMagnitudeAndSign(n uint8) int32 {
	magnitude := b.readLiteral(n)
	sign := b.readLiteral(1)
	if sign == 1 {
		return -int32(magnitude)
	}
	return int32(magnitude)
}

// readWithTree is BoolReader::read_with_tree (vp8.rs:818-832).
func (b *vp8BoolReader) readWithTree(tree []int8, probs []uint8, start int) int8 {
	index := start
	for {
		a := 0
		if b.readBool(probs[index>>1]) {
			a = 1
		}
		index = int(tree[index+a])
		if index <= 0 {
			break
		}
	}
	return int8(-index)
}

// readFlag is BoolReader::read_flag (vp8.rs:834-836).
func (b *vp8BoolReader) readFlag() bool {
	return b.readLiteral(1) != 0
}

// vp8MacroBlock is MacroBlock (vp8.rs:839-847).
type vp8MacroBlock struct {
	bpred        [16]int8
	complexity   [9]uint8
	lumaMode     int8
	chromaMode   int8
	segmentID    uint8
	coeffsSkiped bool
}

// vp8Frame is Frame (vp8.rs:849-884).
type vp8Frame struct {
	width  uint16
	height uint16
	ybuf   []byte
	ubuf   []byte
	vbuf   []byte

	keyframe   bool
	version    uint8
	forDisplay bool
	pixelType  uint8

	filterType     bool // true is the simple filter
	filterLevel    uint8
	sharpnessLevel uint8
}

func (f *vp8Frame) chromaWidth() uint16 {
	return (f.width + 1) / 2
}

func (f *vp8Frame) chromaHeight() uint16 {
	return (f.height + 1) / 2
}

// vp8Tables are the terms of Frame::fill_single (vp8.rs:928-947), the YUV to RGB conversion of the Microsoft recommendation:
// r = (298 c + 409 e + 128) >> 8, g = (298 c - 100 d - 208 e + 128) >> 8, b = (298 c + 516 d + 128) >> 8 with c = y - 16, d = u - 128, e = v - 128,
// each clamped to 0 to 255.
var (
	vp8LumaTerm   [256]int32 // 298 (y - 16) + 128
	vp8RedV       [256]int32 // 409 (v - 128)
	vp8GreenU     [256]int32 // -100 (u - 128)
	vp8GreenV     [256]int32 // -208 (v - 128)
	vp8BlueU      [256]int32 // 516 (u - 128)
	vp8ClampTable [vp8ClampSize]uint8
)

// The shifted sums go from -276 (blue) to 534 (blue): vp8ClampTable is indexed by the sum plus vp8ClampOffset.
const (
	vp8ClampOffset = 300
	vp8ClampSize   = 900
)

func init() {
	for i := range 256 {
		vp8LumaTerm[i] = 298*(int32(i)-16) + 128
		vp8RedV[i] = 409 * (int32(i) - 128)
		vp8GreenU[i] = -100 * (int32(i) - 128)
		vp8GreenV[i] = -208 * (int32(i) - 128)
		vp8BlueU[i] = 516 * (int32(i) - 128)
	}
	for i := range vp8ClampTable {
		vp8ClampTable[i] = uint8(min(max(i-vp8ClampOffset, 0), 255))
	}
}

// vp8FillSingle is Frame::fill_single.
func vp8FillSingle(y, u, v uint8, rgb []byte) {
	luma := vp8LumaTerm[y]
	rgb[0] = vp8ClampTable[(luma+vp8RedV[v])>>8+vp8ClampOffset]
	rgb[1] = vp8ClampTable[(luma+vp8GreenU[u]+vp8GreenV[v])>>8+vp8ClampOffset]
	rgb[2] = vp8ClampTable[(luma+vp8BlueU[u])>>8+vp8ClampOffset]
}

// fill is Frame::fill_rgb and Frame::fill_rgba (vp8.rs:897-926) with stride 3 and 4: it writes the red, green and blue of each pixel of buf
// in raster order, and 255 in the alpha of an rgba buffer (the Rust code leaves it alone). Each chroma sample serves the 2x2 pixels it covers.
func (f *vp8Frame) fill(buf []byte, stride int) {
	width := int(f.width)
	if width == 0 {
		return
	}
	chromaWidth := int(f.chromaWidth())
	pixels := min(len(f.ybuf), len(buf)/stride)

	for y := 0; y*width < pixels; y++ {
		n := min(width, pixels-y*width)
		yRow := f.ybuf[y*width : y*width+n]
		uRow := f.ubuf[(y/2)*chromaWidth:]
		vRow := f.vbuf[(y/2)*chromaWidth:]
		out := buf[y*width*stride : (y*width+n)*stride]

		for x := 0; x < n; x += 2 {
			// the red, green and blue terms the two pixels share
			u, v := uRow[x>>1], vRow[x>>1]
			redV, greenUV, blueU := vp8RedV[v], vp8GreenU[u]+vp8GreenV[v], vp8BlueU[u]

			luma := vp8LumaTerm[yRow[x]]
			o := out[x*stride : x*stride+3]
			o[0] = vp8ClampTable[(luma+redV)>>8+vp8ClampOffset]
			o[1] = vp8ClampTable[(luma+greenUV)>>8+vp8ClampOffset]
			o[2] = vp8ClampTable[(luma+blueU)>>8+vp8ClampOffset]
			if stride == 4 {
				out[x*4+3] = 255
			}

			if x+1 < n {
				luma = vp8LumaTerm[yRow[x+1]]
				o = out[(x+1)*stride : (x+1)*stride+3]
				o[0] = vp8ClampTable[(luma+redV)>>8+vp8ClampOffset]
				o[1] = vp8ClampTable[(luma+greenUV)>>8+vp8ClampOffset]
				o[2] = vp8ClampTable[(luma+blueU)>>8+vp8ClampOffset]
				if stride == 4 {
					out[(x+1)*4+3] = 255
				}
			}
		}
	}
}

// vp8Segment is Segment (vp8.rs:955-970).
type vp8Segment struct {
	ydc  int16
	yac  int16
	y2dc int16
	y2ac int16
	uvdc int16
	uvac int16

	deltaValues bool

	quantizerLevel  int8
	loopfilterLevel int8
}

// vp8Decoder is Vp8Decoder (vp8.rs:975-1009); r is what is left of the chunk the reader of the Rust decoder has not consumed.
type vp8Decoder struct {
	r []byte
	b vp8BoolReader

	mbwidth     uint16
	mbheight    uint16
	macroblocks []vp8MacroBlock

	frame vp8Frame

	segmentsEnabled   bool
	segmentsUpdateMap bool
	segment           [vp8MaxSegments]vp8Segment

	refDelta  [4]int32
	modeDelta [4]int32

	partitions    [8]vp8BoolReader
	numPartitions uint8

	segmentTreeProbs [3]uint8
	tokenProbs       *[4][8][3][vp8NumDCTTokens - 1]uint8

	probSkipFalse    uint8
	hasProbSkipFalse bool

	top  []vp8MacroBlock
	left vp8MacroBlock

	topBorder  []byte
	leftBorder []byte

	blocks [384]int32 // the residues of the macroblock being decoded
}

// newVP8Decoder is Vp8Decoder::new (vp8.rs:1014-1063).
func newVP8Decoder(r []byte) *vp8Decoder {
	tokenProbs := vp8CoeffProbs
	return &vp8Decoder{
		r:                r,
		numPartitions:    1,
		segmentTreeProbs: [3]uint8{255, 255, 255},
		tokenProbs:       &tokenProbs,
	}
}

// readU24 is byteorder's read_u24::<LittleEndian>.
func (d *vp8Decoder) readU24() (uint32, error) {
	if len(d.r) < 3 {
		d.r = nil
		return 0, errWebPEOF
	}
	v := uint32(d.r[0]) | uint32(d.r[1])<<8 | uint32(d.r[2])<<16
	d.r = d.r[3:]
	return v, nil
}

// readExact is Read::read_exact over the remaining chunk; the buffer is not copied.
func (d *vp8Decoder) readExact(n int) ([]byte, error) {
	if n > len(d.r) {
		d.r = nil
		return nil, errWebPEOF
	}
	buf := d.r[:n:n]
	d.r = d.r[n:]
	return buf, nil
}

// updateTokenProbabilities is vp8.rs:1065-1078.
func (d *vp8Decoder) updateTokenProbabilities() {
	for i := range vp8CoeffUpdateProbs {
		for j := range vp8CoeffUpdateProbs[i] {
			for k := range vp8CoeffUpdateProbs[i][j] {
				for t, prob := range vp8CoeffUpdateProbs[i][j][k] {
					if d.b.readBool(prob) {
						d.tokenProbs[i][j][k][t] = d.b.readLiteral(8)
					}
				}
			}
		}
	}
}

// initPartitions is vp8.rs:1080-1102.
func (d *vp8Decoder) initPartitions(n int) error {
	if n > 1 {
		sizes, err := d.readExact(3*n - 3)
		if err != nil {
			return err
		}

		for i := range n - 1 {
			size := int(uint32(sizes[3*i]) | uint32(sizes[3*i+1])<<8 | uint32(sizes[3*i+2])<<16)

			buf, err := d.readExact(size)
			if err != nil {
				return err
			}

			if err := d.partitions[i].init(buf); err != nil {
				return err
			}
		}
	}

	buf := d.r
	d.r = nil
	return d.partitions[n-1].init(buf)
}

// vp8DCQuantAt and vp8ACQuantAt are dc_quant and ac_quant of read_quantization_indices (vp8.rs:1105-1111).
func vp8DCQuantAt(index int32) int16 {
	return vp8DCQuant[min(max(index, 0), 127)]
}

func vp8ACQuantAt(index int32) int16 {
	return vp8ACQuant[min(max(index, 0), 127)]
}

// readQuantizationIndices is vp8.rs:1104-1174.
func (d *vp8Decoder) readQuantizationIndices() {
	yacAbs := d.b.readLiteral(7)

	delta := func() int32 {
		if d.b.readFlag() {
			return d.b.readMagnitudeAndSign(4)
		}
		return 0
	}
	ydcDelta := delta()
	y2dcDelta := delta()
	y2acDelta := delta()
	uvdcDelta := delta()
	uvacDelta := delta()

	n := 1
	if d.segmentsEnabled {
		n = vp8MaxSegments
	}
	for i := range n {
		s := &d.segment[i]
		var base int32
		if !s.deltaValues {
			base = int32(int16(s.quantizerLevel))
		} else {
			base = int32(int16(s.quantizerLevel) + int16(yacAbs))
		}

		s.ydc = vp8DCQuantAt(base + ydcDelta)
		s.yac = vp8ACQuantAt(base)

		s.y2dc = vp8DCQuantAt(base+y2dcDelta) * 2
		// The intermediate result (max 284*155) can be larger than the i16 range.
		s.y2ac = int16(int32(vp8ACQuantAt(base+y2acDelta)) * 155 / 100)

		s.uvdc = vp8DCQuantAt(base + uvdcDelta)
		s.uvac = vp8ACQuantAt(base + uvacDelta)

		if s.y2ac < 8 {
			s.y2ac = 8
		}

		if s.uvdc > 132 {
			s.uvdc = 132
		}
	}
}

// readLoopFilterAdjustments is vp8.rs:1176-1198.
func (d *vp8Decoder) readLoopFilterAdjustments() {
	if d.b.readFlag() {
		for i := range 4 {
			d.refDelta[i] = 0
			if d.b.readFlag() {
				d.refDelta[i] = d.b.readMagnitudeAndSign(6)
			}
		}

		for i := range 4 {
			d.modeDelta[i] = 0
			if d.b.readFlag() {
				d.modeDelta[i] = d.b.readMagnitudeAndSign(6)
			}
		}
	}
}

// readSegmentUpdates is vp8.rs:1200-1240.
func (d *vp8Decoder) readSegmentUpdates() {
	// Section 9.3
	d.segmentsUpdateMap = d.b.readFlag()
	updateSegmentFeatureData := d.b.readFlag()

	if updateSegmentFeatureData {
		segmentFeatureMode := d.b.readFlag()

		for i := range vp8MaxSegments {
			d.segment[i].deltaValues = !segmentFeatureMode
		}

		for i := range vp8MaxSegments {
			var v int32
			if d.b.readFlag() {
				v = d.b.readMagnitudeAndSign(7)
			}
			d.segment[i].quantizerLevel = int8(v)
		}

		for i := range vp8MaxSegments {
			var v int32
			if d.b.readFlag() {
				v = d.b.readMagnitudeAndSign(6)
			}
			d.segment[i].loopfilterLevel = int8(v)
		}
	}

	if d.segmentsUpdateMap {
		for i := range 3 {
			d.segmentTreeProbs[i] = 255
			if d.b.readFlag() {
				d.segmentTreeProbs[i] = d.b.readLiteral(8)
			}
		}
	}
}

// readFrameHeader is vp8.rs:1242-1356. A frame that is not a keyframe ends in an error in the Rust code after the header bits it reads
// (an unsupported feature, or an earlier read error), so it is an error here without reading them.
func (d *vp8Decoder) readFrameHeader() error {
	tag, err := d.readU24()
	if err != nil {
		return err
	}

	d.frame.keyframe = tag&1 == 0
	d.frame.version = uint8((tag >> 1) & 7)
	d.frame.forDisplay = (tag>>4)&1 != 0

	firstPartitionSize := int(tag >> 5)

	if !d.frame.keyframe {
		return errWebPVP8NonKeyframe
	}

	magic, err := d.readExact(3)
	if err != nil {
		return err
	}
	if magic[0] != 0x9d || magic[1] != 0x01 || magic[2] != 0x2a {
		return errWebPVP8MagicInvalid
	}

	dims, err := d.readExact(4)
	if err != nil {
		return err
	}
	w := uint16(dims[0]) | uint16(dims[1])<<8
	h := uint16(dims[2]) | uint16(dims[3])<<8

	d.frame.width = w & 0x3FFF
	d.frame.height = h & 0x3FFF

	// Photon decodes the frame and then fails the memory limit on its RGB (image::Limits::default().max_alloc): a frame of more than 2^29
	// bytes of RGB never ends in an image, whatever file it is in (the canvas of an extended image has as many pixels, or the image has
	// alpha and 4 bytes a pixel).
	if uint64(d.frame.width)*uint64(d.frame.height)*3 > webpMaxAlloc {
		return errWebPLimit
	}

	d.top = vp8InitTopMacroblocks(int(d.frame.width))
	// Almost always the first macro block, except when non exists (i.e. width == 0)
	d.left = vp8MacroBlock{}
	if len(d.top) > 0 {
		d.left = d.top[0]
	}

	d.mbwidth = (d.frame.width + 15) / 16
	d.mbheight = (d.frame.height + 15) / 16

	d.frame.ybuf = make([]byte, int(d.frame.width)*int(d.frame.height))
	d.frame.ubuf = make([]byte, int(d.frame.chromaWidth())*int(d.frame.chromaHeight()))
	d.frame.vbuf = make([]byte, int(d.frame.chromaWidth())*int(d.frame.chromaHeight()))

	d.topBorder = make([]byte, int(d.frame.width)+4+16)
	for i := range d.topBorder {
		d.topBorder[i] = 127
	}
	d.leftBorder = vp8NewLeftBorder()

	buf, err := d.readExact(firstPartitionSize)
	if err != nil {
		return err
	}

	// initialise binary decoder
	if err := d.b.init(buf); err != nil {
		return err
	}

	colorSpace := d.b.readLiteral(1)
	d.frame.pixelType = d.b.readLiteral(1)

	if colorSpace != 0 {
		return errWebPVP8ColorSpaceInvalid
	}

	d.segmentsEnabled = d.b.readFlag()
	if d.segmentsEnabled {
		d.readSegmentUpdates()
	}

	d.frame.filterType = d.b.readFlag()
	d.frame.filterLevel = d.b.readLiteral(6)
	d.frame.sharpnessLevel = d.b.readLiteral(3)

	if d.b.readFlag() {
		d.readLoopFilterAdjustments()
	}

	d.numPartitions = uint8(1 << d.b.readLiteral(2))
	if err := d.initPartitions(int(d.numPartitions)); err != nil {
		return err
	}

	d.readQuantizationIndices()

	// Refresh entropy probs ?????
	_ = d.b.readLiteral(1)

	d.updateTokenProbabilities()

	if d.b.readLiteral(1) == 1 {
		d.probSkipFalse = d.b.readLiteral(8)
		d.hasProbSkipFalse = true
	} else {
		d.hasProbSkipFalse = false
	}

	return nil
}

func vp8NewLeftBorder() []byte {
	border := make([]byte, 1+16)
	for i := range border {
		border[i] = 129
	}
	return border
}

// readMacroblockHeader is vp8.rs:1358-1438 for a keyframe (the only frame that gets this far).
func (d *vp8Decoder) readMacroblockHeader(mbx int) vp8MacroBlock {
	var mb vp8MacroBlock

	if d.segmentsEnabled && d.segmentsUpdateMap {
		mb.segmentID = uint8(d.b.readWithTree(vp8SegmentIDTree[:], d.segmentTreeProbs[:], 0))
	}

	if d.hasProbSkipFalse {
		mb.coeffsSkiped = d.b.readBool(d.probSkipFalse)
	}

	// intra prediction
	mb.lumaMode = d.b.readWithTree(vp8KeyframeYModeTree[:], vp8KeyframeYModeProbs[:], 0)

	if mb.lumaMode == vp8BPred {
		// `LumaMode::B` - This is predicted individually
		for y := range 4 {
			for x := range 4 {
				top := d.top[mbx].bpred[12+x]
				left := d.left.bpred[y]
				bmode := d.b.readWithTree(vp8KeyframeBPredModeTree[:], vp8KeyframeBPredModeProbs[top][left][:], 0)
				mb.bpred[x+y*4] = bmode

				d.top[mbx].bpred[12+x] = bmode
				d.left.bpred[y] = bmode
			}
		}
	} else {
		mode := vp8LumaIntra(mb.lumaMode)
		for i := range 4 {
			mb.bpred[12+i] = mode
			d.left.bpred[i] = mode
		}
	}

	mb.chromaMode = d.b.readWithTree(vp8KeyframeUVModeTree[:], vp8KeyframeUVModeProbs[:], 0)

	d.top[mbx].chromaMode = mb.chromaMode
	d.top[mbx].lumaMode = mb.lumaMode
	d.top[mbx].bpred = mb.bpred

	return mb
}

// vp8LumaIntra is LumaMode::into_intra (vp8.rs:2120-2128) for the modes that have a sub-block equivalent.
func vp8LumaIntra(mode int8) int8 {
	switch mode {
	case vp8VPred:
		return vp8BVEPred
	case vp8HPred:
		return vp8BHEPred
	case vp8TMPred:
		return vp8BTMPred
	default:
		return vp8BDCPred
	}
}

// vp8InitTopMacroblocks is init_top_macroblocks (vp8.rs:2161-2172).
func vp8InitTopMacroblocks(width int) []vp8MacroBlock {
	mbWidth := (width + 15) / 16

	// Section 11.3 #3
	mb := vp8MacroBlock{lumaMode: vp8DCPred}
	for i := range mb.bpred {
		mb.bpred[i] = vp8BDCPred
	}

	top := make([]vp8MacroBlock, mbWidth)
	for i := range top {
		top[i] = mb
	}
	return top
}

// intraPredictLuma is vp8.rs:1440-1484.
func (d *vp8Decoder) intraPredictLuma(mbx, mby int, mb *vp8MacroBlock, mask uint32) {
	resdata := &d.blocks
	const stride = 1 + 16 + 4
	w := int(d.frame.width)
	mw := int(d.mbwidth)
	ws := vp8CreateBorderLuma(mbx, mby, mw, d.topBorder, d.leftBorder)

	switch mb.lumaMode {
	case vp8VPred:
		vp8PredictVPred(ws[:], 16, 1, 1, stride)
	case vp8HPred:
		vp8PredictHPred(ws[:], 16, 1, 1, stride)
	case vp8TMPred:
		vp8PredictTMPred(ws[:], 16, 1, 1, stride)
	case vp8DCPred:
		vp8PredictDCPred(ws[:], 16, stride, mby != 0, mbx != 0)
	default:
		vp8Predict4x4(ws[:], stride, mb.bpred[:], resdata[:], mask)
	}

	if mb.lumaMode != vp8BPred {
		for y := range 4 {
			for x := range 4 {
				i := x + y*4
				if mask&(1<<i) == 0 {
					continue
				}
				y0 := 1 + y*4
				x0 := 1 + x*4

				vp8AddResidue(ws[:], (*[16]int32)(resdata[i*16:i*16+16]), y0, x0, stride)
			}
		}
	}

	d.leftBorder[0] = ws[16]

	for i := range 16 {
		d.topBorder[mbx*16+i] = ws[16*stride+1+i]
		d.leftBorder[i+1] = ws[(i+1)*stride+16]
	}

	// Length is the remainder to the border, but maximally the current chunk.
	ylength := min(int(d.frame.height)-mby*16, 16)
	xlength := min(int(d.frame.width)-mbx*16, 16)

	for y := range ylength {
		for x := range xlength {
			d.frame.ybuf[(mby*16+y)*w+mbx*16+x] = ws[(1+y)*stride+1+x]
		}
	}
}

// intraPredictChroma is vp8.rs:1486-1580.
func (d *vp8Decoder) intraPredictChroma(mbx, mby int, mb *vp8MacroBlock, mask uint32) {
	resdata := &d.blocks
	const stride = 1 + 8

	w := int(d.frame.chromaWidth())

	// 8x8 with left top border of 1
	var uws, vws [(8 + 1) * (8 + 1)]byte

	ylength := min(int(d.frame.chromaHeight())-mby*8, 8)
	xlength := min(int(d.frame.chromaWidth())-mbx*8, 8)

	// left border
	for y := range 8 {
		var uy, vy byte = 129, 129
		if mbx != 0 && y < ylength {
			index := (mby*8+y)*w + ((mbx-1)*8 + 7)
			uy, vy = d.frame.ubuf[index], d.frame.vbuf[index]
		}

		uws[(y+1)*stride] = uy
		vws[(y+1)*stride] = vy
	}
	// top border
	for x := range 8 {
		var ux, vx byte = 127, 127
		if mby != 0 && x < xlength {
			index := ((mby-1)*8+7)*w + (mbx*8 + x)
			ux, vx = d.frame.ubuf[index], d.frame.vbuf[index]
		}

		uws[x+1] = ux
		vws[x+1] = vx
	}

	// top left point
	var u1, v1 byte
	switch {
	case mby == 0:
		u1, v1 = 127, 127
	case mbx == 0:
		u1, v1 = 129, 129
	default:
		index := ((mby-1)*8+7)*w + (mbx-1)*8 + 7
		if index >= len(d.frame.ubuf) {
			u1, v1 = 127, 127
		} else {
			u1, v1 = d.frame.ubuf[index], d.frame.vbuf[index]
		}
	}

	uws[0] = u1
	vws[0] = v1

	switch mb.chromaMode {
	case vp8DCPred:
		vp8PredictDCPred(uws[:], 8, stride, mby != 0, mbx != 0)
		vp8PredictDCPred(vws[:], 8, stride, mby != 0, mbx != 0)
	case vp8VPred:
		vp8PredictVPred(uws[:], 8, 1, 1, stride)
		vp8PredictVPred(vws[:], 8, 1, 1, stride)
	case vp8HPred:
		vp8PredictHPred(uws[:], 8, 1, 1, stride)
		vp8PredictHPred(vws[:], 8, 1, 1, stride)
	default:
		vp8PredictTMPred(uws[:], 8, 1, 1, stride)
		vp8PredictTMPred(vws[:], 8, 1, 1, stride)
	}

	for y := range 2 {
		for x := range 2 {
			i := x + y*2

			y0 := 1 + y*4
			x0 := 1 + x*4
			if mask&(1<<(16+i)) != 0 {
				vp8AddResidue(uws[:], (*[16]int32)(resdata[16*16+i*16:16*16+i*16+16]), y0, x0, stride)
			}

			if mask&(1<<(20+i)) != 0 {
				vp8AddResidue(vws[:], (*[16]int32)(resdata[20*16+i*16:20*16+i*16+16]), y0, x0, stride)
			}
		}
	}

	for y := range ylength {
		for x := range xlength {
			d.frame.ubuf[(mby*8+y)*w+mbx*8+x] = uws[(1+y)*stride+1+x]
			d.frame.vbuf[(mby*8+y)*w+mbx*8+x] = vws[(1+y)*stride+1+x]
		}
	}
}

// readCoefficients is vp8.rs:1582-1658; it reports whether the block had any coefficient token other than an immediate end of block, and
// whether a coefficient other than the first is not zero (the block is then a flat one: only its first coefficient counts).
func (d *vp8Decoder) readCoefficients(block []int32, p, plane, complexity int, dcq, acq int16) (hasCoefficients, hasAC bool) {
	first := 0
	if plane == 0 {
		first = 1
	}
	probs := &d.tokenProbs[plane]
	br := &d.partitions[p]

	skip := false

	for i := first; i < 16; i++ {
		token := br.readToken(&probs[vp8CoeffBands[i]][complexity], skip)

		var absValue int32
		switch {
		case token == vp8DCTEOB:
			return hasCoefficients, hasAC
		case token == vp8DCT0:
			skip = true
			hasCoefficients = true
			complexity = 0
			continue
		case token <= vp8DCT4:
			absValue = int32(token)
		default: // DCT_CAT1..=DCT_CAT6
			t := &vp8ProbDCTCat[token-vp8DCTCat1]

			var extra int16
			for j := 0; t[j] > 0; j++ {
				extra += extra
				if br.readBool(t[j]) {
					extra++
				}
			}

			absValue = int32(int16(vp8DCTCatBase[token-vp8DCTCat1]) + extra)
		}

		skip = false

		// the value is at least 1: the complexity is 1 for 1 and 2 above (vp8.rs:1639-1645)
		complexity = 2
		if absValue == 1 {
			complexity = 1
		}

		if br.readBool(128) {
			absValue = -absValue
		}

		q := dcq
		if i > 0 {
			q = acq
			hasAC = true
		}
		block[vp8Zigzag[i]] = absValue * int32(q)

		hasCoefficients = true
	}

	return hasCoefficients, hasAC
}

func vp8Bit(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// readResidualData is vp8.rs:1660-1735. It reads the coefficients of the macroblock into d.blocks (the 16 luma blocks, then the 4 of u and
// the 4 of v), turns them into residues and returns which of the 24 blocks have one: the others are all zeros, adding them to the
// prediction does nothing.
func (d *vp8Decoder) readResidualData(mb *vp8MacroBlock, mbx, p int) uint32 {
	sindex := int(mb.segmentID)
	blocks := &d.blocks
	*blocks = [384]int32{}
	var mask uint32
	plane := 1
	if mb.lumaMode == vp8BPred {
		plane = 3
	}

	if plane == 1 {
		complexity := d.top[mbx].complexity[0] + d.left.complexity[0]
		var block [16]int32
		dcq := d.segment[sindex].y2dc
		acq := d.segment[sindex].y2ac
		n, _ := d.readCoefficients(block[:], p, plane, int(complexity), dcq, acq)

		d.left.complexity[0] = vp8Bit(n)
		d.top[mbx].complexity[0] = vp8Bit(n)

		vp8IWHT4x4(block[:])

		for k := range 16 {
			blocks[16*k] = block[k]
		}

		plane = 0
	}

	for y := range 4 {
		left := d.left.complexity[y+1]
		for x := range 4 {
			i := x + y*4
			block := blocks[i*16 : i*16+16]

			complexity := d.top[mbx].complexity[x+1] + left
			dcq := d.segment[sindex].ydc
			acq := d.segment[sindex].yac

			n, ac := d.readCoefficients(block, p, plane, int(complexity), dcq, acq)

			if block[0] != 0 || n {
				vp8Residue(block, ac)
				mask |= 1 << i
			}

			left = vp8Bit(n)
			d.top[mbx].complexity[x+1] = vp8Bit(n)
		}

		d.left.complexity[y+1] = left
	}

	plane = 2

	for _, j := range [2]int{5, 7} {
		for y := range 2 {
			left := d.left.complexity[y+j]

			for x := range 2 {
				i := x + y*2
				if j == 5 {
					i += 16
				} else {
					i += 20
				}
				block := blocks[i*16 : i*16+16]

				complexity := d.top[mbx].complexity[x+j] + left
				dcq := d.segment[sindex].uvdc
				acq := d.segment[sindex].uvac

				n, ac := d.readCoefficients(block, p, plane, int(complexity), dcq, acq)
				if block[0] != 0 || n {
					vp8Residue(block, ac)
					mask |= 1 << i
				}

				left = vp8Bit(n)
				d.top[mbx].complexity[x+j] = vp8Bit(n)
			}

			d.left.complexity[y+j] = left
		}
	}

	return mask
}

// vp8Residue is idct4x4 of a block (vp8.rs:1696-1698); a block with no coefficient but the first is flat: every sample of its inverse
// transform is the first coefficient plus 4, shifted down by 3.
func vp8Residue(block []int32, hasAC bool) {
	if hasAC {
		vp8IDCT4x4(block)
		return
	}
	flat := int32((int64(block[0]) + 4) >> 3)
	for i := range block[:16] {
		block[i] = flat
	}
}

// loopFilter is vp8.rs:1738-2000.
func (d *vp8Decoder) loopFilter(mbx, mby int, mb *vp8MacroBlock) {
	lumaW := int(d.frame.width)
	lumaH := int(d.frame.height)
	chromaW := int(d.frame.chromaWidth())
	chromaH := int(d.frame.chromaHeight())

	filterLevel, interiorLimit, hevThreshold := d.calculateFilterParameters(mb)

	if filterLevel == 0 {
		return
	}

	mbedgeLimit := (filterLevel+2)*2 + interiorLimit
	subBedgeLimit := (filterLevel * 2) + interiorLimit

	lumaYLength := min(lumaH-16*mby, 16)
	lumaXLength := min(lumaW-16*mbx, 16)

	chromaYLength := min(chromaH-8*mby, 8)
	chromaXLength := min(chromaW-8*mbx, 8)

	ybuf, ubuf, vbuf := d.frame.ybuf, d.frame.ubuf, d.frame.vbuf

	// filter across left of macroblock
	if mbx > 0 {
		// simple loop filtering
		if d.frame.filterType {
			if lumaXLength >= 2 {
				for y := range lumaYLength {
					y0 := mby*16 + y
					x0 := mbx * 16

					vp8SimpleSegment(mbedgeLimit, ybuf, y0*lumaW+x0, 1)
				}
			}
		} else {
			if lumaXLength >= 4 {
				for y := range lumaYLength {
					y0 := mby*16 + y
					x0 := mbx * 16

					vp8MacroblockFilter(hevThreshold, interiorLimit, mbedgeLimit, ybuf, y0*lumaW+x0, 1)
				}
			}

			if chromaXLength >= 4 {
				for y := range chromaYLength {
					y0 := mby*8 + y
					x0 := mbx * 8

					vp8MacroblockFilter(hevThreshold, interiorLimit, mbedgeLimit, ubuf, y0*chromaW+x0, 1)
					vp8MacroblockFilter(hevThreshold, interiorLimit, mbedgeLimit, vbuf, y0*chromaW+x0, 1)
				}
			}
		}
	}

	// filter across vertical subblocks in macroblock
	if mb.lumaMode == vp8BPred || !mb.coeffsSkiped {
		if d.frame.filterType {
			for x := 4; x < lumaXLength-1; x += 4 {
				for y := range lumaYLength {
					y0 := mby*16 + y
					x0 := mbx*16 + x

					vp8SimpleSegment(subBedgeLimit, ybuf, y0*lumaW+x0, 1)
				}
			}
		} else {
			if lumaXLength > 3 {
				for x := 4; x < lumaXLength-3; x += 4 {
					for y := range lumaYLength {
						y0 := mby*16 + y
						x0 := mbx*16 + x

						vp8SubblockFilter(hevThreshold, interiorLimit, subBedgeLimit, ybuf, y0*lumaW+x0, 1)
					}
				}
			}

			if chromaXLength == 8 {
				for y := range chromaYLength {
					y0 := mby*8 + y
					x0 := mbx*8 + 4

					vp8SubblockFilter(hevThreshold, interiorLimit, subBedgeLimit, ubuf, y0*chromaW+x0, 1)
					vp8SubblockFilter(hevThreshold, interiorLimit, subBedgeLimit, vbuf, y0*chromaW+x0, 1)
				}
			}
		}
	}

	// filter across top of macroblock
	if mby > 0 {
		if d.frame.filterType {
			if lumaYLength >= 2 {
				for x := range lumaXLength {
					y0 := mby * 16
					x0 := mbx*16 + x

					vp8SimpleSegment(mbedgeLimit, ybuf, y0*lumaW+x0, lumaW)
				}
			}
		} else {
			// if bottom macroblock, can only filter if there is 3 pixels below
			if lumaYLength >= 4 {
				for x := range lumaXLength {
					y0 := mby * 16
					x0 := mbx*16 + x

					vp8MacroblockFilter(hevThreshold, interiorLimit, mbedgeLimit, ybuf, y0*lumaW+x0, lumaW)
				}
			}

			if chromaYLength >= 4 {
				for x := range chromaXLength {
					y0 := mby * 8
					x0 := mbx*8 + x

					vp8MacroblockFilter(hevThreshold, interiorLimit, mbedgeLimit, ubuf, y0*chromaW+x0, chromaW)
					vp8MacroblockFilter(hevThreshold, interiorLimit, mbedgeLimit, vbuf, y0*chromaW+x0, chromaW)
				}
			}
		}
	}

	// filter across horizontal subblock edges within the macroblock
	if mb.lumaMode == vp8BPred || !mb.coeffsSkiped {
		if d.frame.filterType {
			for y := 4; y < lumaYLength-1; y += 4 {
				for x := range lumaXLength {
					y0 := mby*16 + y
					x0 := mbx*16 + x

					vp8SimpleSegment(subBedgeLimit, ybuf, y0*lumaW+x0, lumaW)
				}
			}
		} else {
			if lumaYLength > 3 {
				for y := 4; y < lumaYLength-3; y += 4 {
					for x := range lumaXLength {
						y0 := mby*16 + y
						x0 := mbx*16 + x

						vp8SubblockFilter(hevThreshold, interiorLimit, subBedgeLimit, ybuf, y0*lumaW+x0, lumaW)
					}
				}
			}

			if chromaYLength == 8 {
				for x := range chromaXLength {
					y0 := mby*8 + 4
					x0 := mbx*8 + x

					vp8SubblockFilter(hevThreshold, interiorLimit, subBedgeLimit, ubuf, y0*chromaW+x0, chromaW)
					vp8SubblockFilter(hevThreshold, interiorLimit, subBedgeLimit, vbuf, y0*chromaW+x0, chromaW)
				}
			}
		}
	}
}

// calculateFilterParameters is vp8.rs:2003-2059; it returns the filter level, interior limit and hev threshold.
func (d *vp8Decoder) calculateFilterParameters(macroblock *vp8MacroBlock) (uint8, uint8, uint8) {
	segment := d.segment[macroblock.segmentID]
	filterLevel := int32(d.frame.filterLevel)

	if d.segmentsEnabled {
		if segment.deltaValues {
			filterLevel += int32(segment.loopfilterLevel)
		} else {
			filterLevel = int32(segment.loopfilterLevel)
		}
	}

	filterLevel = min(max(filterLevel, 0), 63)

	if macroblock.lumaMode == vp8BPred {
		filterLevel += d.modeDelta[0]
	}

	level := uint8(min(max(filterLevel, 0), 63))

	// interior limit
	interiorLimit := level

	if d.frame.sharpnessLevel > 0 {
		if d.frame.sharpnessLevel > 4 {
			interiorLimit >>= 2
		} else {
			interiorLimit >>= 1
		}

		if interiorLimit > 9-d.frame.sharpnessLevel {
			interiorLimit = 9 - d.frame.sharpnessLevel
		}
	}

	if interiorLimit == 0 {
		interiorLimit = 1
	}

	// high edge variance threshold; the frame is a keyframe (a frame that is not one is an error before the first macroblock)
	hevThreshold := uint8(1)
	if level >= 40 {
		hevThreshold = 2
	}

	return level, interiorLimit, hevThreshold
}

// decodeFrame is Vp8Decoder::decode_frame (vp8.rs:2062-2105).
func (d *vp8Decoder) decodeFrame() (*vp8Frame, error) {
	if err := d.readFrameHeader(); err != nil {
		return nil, err
	}

	d.macroblocks = make([]vp8MacroBlock, 0, int(d.mbwidth)*int(d.mbheight))

	for mby := range int(d.mbheight) {
		p := mby % int(d.numPartitions)
		d.left = vp8MacroBlock{}

		for mbx := range int(d.mbwidth) {
			mb := d.readMacroblockHeader(mbx)
			var mask uint32
			if !mb.coeffsSkiped {
				mask = d.readResidualData(&mb, mbx, p)
			} else {
				if mb.lumaMode != vp8BPred {
					d.left.complexity[0] = 0
					d.top[mbx].complexity[0] = 0
				}

				for i := 1; i < 9; i++ {
					d.left.complexity[i] = 0
					d.top[mbx].complexity[i] = 0
				}

			}

			d.intraPredictLuma(mbx, mby, &mb, mask)
			d.intraPredictChroma(mbx, mby, &mb, mask)

			d.macroblocks = append(d.macroblocks, mb)
		}

		d.leftBorder = vp8NewLeftBorder()
	}

	// do loop filtering
	for mby := range int(d.mbheight) {
		for mbx := range int(d.mbwidth) {
			mb := d.macroblocks[mby*int(d.mbwidth)+mbx]
			d.loopFilter(mbx, mby, &mb)
		}
	}

	return &d.frame, nil
}

// vp8CreateBorderLuma is create_border_luma (vp8.rs:2174-2229): the 16x16 luma block with its border row above (and the four pixels
// above and to the right), border column to the left and the pixel above and to the left.
func vp8CreateBorderLuma(mbx, mby, mbw int, top, left []byte) [357]byte {
	const stride = 1 + 16 + 4
	var ws [(1 + 16) * (1 + 16 + 4)]byte

	// A
	above := ws[1:stride]
	if mby == 0 {
		for i := range above {
			above[i] = 127
		}
	} else {
		for i := range 16 {
			above[i] = top[mbx*16+i]
		}

		if mbx == mbw-1 {
			for i := 16; i < len(above); i++ {
				above[i] = top[mbx*16+15]
			}
		} else {
			for i := 16; i < len(above); i++ {
				above[i] = top[mbx*16+i]
			}
		}
	}

	for i := 17; i < stride; i++ {
		ws[4*stride+i] = ws[i]
		ws[8*stride+i] = ws[i]
		ws[12*stride+i] = ws[i]
	}

	// L
	if mbx == 0 {
		for i := range 16 {
			ws[(i+1)*stride] = 129
		}
	} else {
		for i := range 16 {
			ws[(i+1)*stride] = left[i+1]
		}
	}

	// P
	switch {
	case mby == 0:
		ws[0] = 127
	case mbx == 0:
		ws[0] = 129
	default:
		ws[0] = left[0]
	}

	return ws
}

func vp8Avg3(left, this, right uint8) uint8 {
	return uint8((uint16(left) + 2*uint16(this) + uint16(right) + 2) >> 2)
}

func vp8Avg2(this, right uint8) uint8 {
	return uint8((uint16(this) + uint16(right) + 1) >> 1)
}

// vp8AddResidue is add_residue (vp8.rs:2243-2251).
func vp8AddResidue(pblock []byte, rblock *[16]int32, y0, x0, stride int) {
	pos := y0*stride + x0
	for row := range 4 {
		p := pblock[pos : pos+4]
		for i := range 4 {
			p[i] = uint8(min(max(rblock[row*4+i]+int32(p[i]), 0), 255))
		}
		pos += stride
	}
}

// vp8Predict4x4 is predict_4x4 (vp8.rs:2253-2277).
func vp8Predict4x4(ws []byte, stride int, modes []int8, resdata []int32, mask uint32) {
	for sby := range 4 {
		for sbx := range 4 {
			i := sbx + sby*4
			y0 := sby*4 + 1
			x0 := sbx*4 + 1

			switch modes[i] {
			case vp8BTMPred:
				vp8PredictTMPred(ws, 4, x0, y0, stride)
			case vp8BVEPred:
				vp8PredictBVEPred(ws, x0, y0, stride)
			case vp8BHEPred:
				vp8PredictBHEPred(ws, x0, y0, stride)
			case vp8BDCPred:
				vp8PredictBDCPred(ws, x0, y0, stride)
			case vp8BLDPred:
				vp8PredictBLDPred(ws, x0, y0, stride)
			case vp8BRDPred:
				vp8PredictBRDPred(ws, x0, y0, stride)
			case vp8BVRPred:
				vp8PredictBVRPred(ws, x0, y0, stride)
			case vp8BVLPred:
				vp8PredictBVLPred(ws, x0, y0, stride)
			case vp8BHDPred:
				vp8PredictBHDPred(ws, x0, y0, stride)
			case vp8BHUPred:
				vp8PredictBHUPred(ws, x0, y0, stride)
			}

			if mask&(1<<i) != 0 {
				vp8AddResidue(ws, (*[16]int32)(resdata[i*16:i*16+16]), y0, x0, stride)
			}
		}
	}
}

func vp8PredictVPred(a []byte, size, x0, y0, stride int) {
	for y := range size {
		for x := range size {
			a[(x+x0)+stride*(y+y0)] = a[(x+x0)+stride*(y0+y-1)]
		}
	}
}

func vp8PredictHPred(a []byte, size, x0, y0, stride int) {
	for y := range size {
		for x := range size {
			a[(x+x0)+stride*(y+y0)] = a[(x+x0-1)+stride*(y0+y)]
		}
	}
}

func vp8PredictDCPred(a []byte, size, stride int, above, left bool) {
	var sum uint32
	shf := uint32(3)
	if size == 8 {
		shf = 2
	}

	if left {
		for y := range size {
			sum += uint32(a[(y+1)*stride])
		}

		shf++
	}

	if above {
		for x := range size {
			sum += uint32(a[x+1])
		}

		shf++
	}

	var dcval uint32 = 128
	if left || above {
		dcval = (sum + (1 << (shf - 1))) >> shf
	}

	for y := range size {
		for x := range size {
			a[(x+1)+stride*(y+1)] = uint8(dcval)
		}
	}
}

func vp8PredictTMPred(a []byte, size, x0, y0, stride int) {
	for y := range size {
		for x := range size {
			pred := int32(a[(y0+y)*stride+x0-1]) + int32(a[(y0-1)*stride+x0+x]) - int32(a[(y0-1)*stride+x0-1])

			a[(x+x0)+stride*(y+y0)] = uint8(min(max(pred, 0), 255))
		}
	}
}

func vp8PredictBDCPred(a []byte, x0, y0, stride int) {
	v := uint32(4)
	for i := range 4 {
		v += uint32(a[(y0+i)*stride+x0-1]) + uint32(a[(y0-1)*stride+x0+i])
	}

	v >>= 3
	for y := range 4 {
		for x := range 4 {
			a[x+x0+stride*(y+y0)] = uint8(v)
		}
	}
}

func vp8TopLeftPixel(a []byte, x0, y0, stride int) uint8 {
	return a[(y0-1)*stride+x0-1]
}

func vp8TopPixels(a []byte, x0, y0, stride int) [8]uint8 {
	pos := (y0-1)*stride + x0
	return [8]uint8(a[pos : pos+8])
}

func vp8LeftPixels(a []byte, x0, y0, stride int) [4]uint8 {
	return [4]uint8{
		a[y0*stride+x0-1],
		a[(y0+1)*stride+x0-1],
		a[(y0+2)*stride+x0-1],
		a[(y0+3)*stride+x0-1],
	}
}

func vp8EdgePixels(a []byte, x0, y0, stride int) [9]uint8 {
	pos := (y0-1)*stride + x0 - 1
	aSlice := a[pos : pos+5]
	return [9]uint8{
		a[pos+4*stride],
		a[pos+3*stride],
		a[pos+2*stride],
		a[pos+stride],
		aSlice[0],
		aSlice[1],
		aSlice[2],
		aSlice[3],
		aSlice[4],
	}
}

func vp8PredictBVEPred(a []byte, x0, y0, stride int) {
	p := vp8TopLeftPixel(a, x0, y0, stride)
	t := vp8TopPixels(a, x0, y0, stride)
	avg := [4]uint8{
		vp8Avg3(p, t[0], t[1]),
		vp8Avg3(t[0], t[1], t[2]),
		vp8Avg3(t[1], t[2], t[3]),
		vp8Avg3(t[2], t[3], t[4]),
	}

	pos := y0*stride + x0
	for range 4 {
		copy(a[pos:pos+4], avg[:])
		pos += stride
	}
}

func vp8PredictBHEPred(a []byte, x0, y0, stride int) {
	p := vp8TopLeftPixel(a, x0, y0, stride)
	l := vp8LeftPixels(a, x0, y0, stride)

	avgs := [4]uint8{
		vp8Avg3(p, l[0], l[1]),
		vp8Avg3(l[0], l[1], l[2]),
		vp8Avg3(l[1], l[2], l[3]),
		vp8Avg3(l[2], l[3], l[3]),
	}

	pos := y0*stride + x0
	for _, avg := range avgs {
		for i := range 4 {
			a[pos+i] = avg
		}
		pos += stride
	}
}

func vp8PredictBLDPred(a []byte, x0, y0, stride int) {
	t := vp8TopPixels(a, x0, y0, stride)

	avgs := [7]uint8{
		vp8Avg3(t[0], t[1], t[2]),
		vp8Avg3(t[1], t[2], t[3]),
		vp8Avg3(t[2], t[3], t[4]),
		vp8Avg3(t[3], t[4], t[5]),
		vp8Avg3(t[4], t[5], t[6]),
		vp8Avg3(t[5], t[6], t[7]),
		vp8Avg3(t[6], t[7], t[7]),
	}

	pos := y0*stride + x0

	for i := range 4 {
		copy(a[pos:pos+4], avgs[i:i+4])
		pos += stride
	}
}

func vp8PredictBRDPred(a []byte, x0, y0, stride int) {
	e := vp8EdgePixels(a, x0, y0, stride)

	avgs := [7]uint8{
		vp8Avg3(e[0], e[1], e[2]),
		vp8Avg3(e[1], e[2], e[3]),
		vp8Avg3(e[2], e[3], e[4]),
		vp8Avg3(e[3], e[4], e[5]),
		vp8Avg3(e[4], e[5], e[6]),
		vp8Avg3(e[5], e[6], e[7]),
		vp8Avg3(e[6], e[7], e[8]),
	}
	pos := y0*stride + x0

	for i := range 4 {
		copy(a[pos:pos+4], avgs[3-i:7-i])
		pos += stride
	}
}

func vp8PredictBVRPred(a []byte, x0, y0, stride int) {
	e := vp8EdgePixels(a, x0, y0, stride)

	a[(y0+3)*stride+x0] = vp8Avg3(e[1], e[2], e[3])
	a[(y0+2)*stride+x0] = vp8Avg3(e[2], e[3], e[4])
	a[(y0+3)*stride+x0+1] = vp8Avg3(e[3], e[4], e[5])
	a[(y0+1)*stride+x0] = vp8Avg3(e[3], e[4], e[5])
	a[(y0+2)*stride+x0+1] = vp8Avg2(e[4], e[5])
	a[y0*stride+x0] = vp8Avg2(e[4], e[5])
	a[(y0+3)*stride+x0+2] = vp8Avg3(e[4], e[5], e[6])
	a[(y0+1)*stride+x0+1] = vp8Avg3(e[4], e[5], e[6])
	a[(y0+2)*stride+x0+2] = vp8Avg2(e[5], e[6])
	a[y0*stride+x0+1] = vp8Avg2(e[5], e[6])
	a[(y0+3)*stride+x0+3] = vp8Avg3(e[5], e[6], e[7])
	a[(y0+1)*stride+x0+2] = vp8Avg3(e[5], e[6], e[7])
	a[(y0+2)*stride+x0+3] = vp8Avg2(e[6], e[7])
	a[y0*stride+x0+2] = vp8Avg2(e[6], e[7])
	a[(y0+1)*stride+x0+3] = vp8Avg3(e[6], e[7], e[8])
	a[y0*stride+x0+3] = vp8Avg2(e[7], e[8])
}

func vp8PredictBVLPred(a []byte, x0, y0, stride int) {
	t := vp8TopPixels(a, x0, y0, stride)

	a[y0*stride+x0] = vp8Avg2(t[0], t[1])
	a[(y0+1)*stride+x0] = vp8Avg3(t[0], t[1], t[2])
	a[(y0+2)*stride+x0] = vp8Avg2(t[1], t[2])
	a[y0*stride+x0+1] = vp8Avg2(t[1], t[2])
	a[(y0+1)*stride+x0+1] = vp8Avg3(t[1], t[2], t[3])
	a[(y0+3)*stride+x0] = vp8Avg3(t[1], t[2], t[3])
	a[(y0+2)*stride+x0+1] = vp8Avg2(t[2], t[3])
	a[y0*stride+x0+2] = vp8Avg2(t[2], t[3])
	a[(y0+3)*stride+x0+1] = vp8Avg3(t[2], t[3], t[4])
	a[(y0+1)*stride+x0+2] = vp8Avg3(t[2], t[3], t[4])
	a[(y0+2)*stride+x0+2] = vp8Avg2(t[3], t[4])
	a[y0*stride+x0+3] = vp8Avg2(t[3], t[4])
	a[(y0+3)*stride+x0+2] = vp8Avg3(t[3], t[4], t[5])
	a[(y0+1)*stride+x0+3] = vp8Avg3(t[3], t[4], t[5])
	a[(y0+2)*stride+x0+3] = vp8Avg3(t[4], t[5], t[6])
	a[(y0+3)*stride+x0+3] = vp8Avg3(t[5], t[6], t[7])
}

func vp8PredictBHDPred(a []byte, x0, y0, stride int) {
	e := vp8EdgePixels(a, x0, y0, stride)

	a[(y0+3)*stride+x0] = vp8Avg2(e[0], e[1])
	a[(y0+3)*stride+x0+1] = vp8Avg3(e[0], e[1], e[2])
	a[(y0+2)*stride+x0] = vp8Avg2(e[1], e[2])
	a[(y0+3)*stride+x0+2] = vp8Avg2(e[1], e[2])
	a[(y0+2)*stride+x0+1] = vp8Avg3(e[1], e[2], e[3])
	a[(y0+3)*stride+x0+3] = vp8Avg3(e[1], e[2], e[3])
	a[(y0+2)*stride+x0+2] = vp8Avg2(e[2], e[3])
	a[(y0+1)*stride+x0] = vp8Avg2(e[2], e[3])
	a[(y0+2)*stride+x0+3] = vp8Avg3(e[2], e[3], e[4])
	a[(y0+1)*stride+x0+1] = vp8Avg3(e[2], e[3], e[4])
	a[(y0+1)*stride+x0+2] = vp8Avg2(e[3], e[4])
	a[y0*stride+x0] = vp8Avg2(e[3], e[4])
	a[(y0+1)*stride+x0+3] = vp8Avg3(e[3], e[4], e[5])
	a[y0*stride+x0+1] = vp8Avg3(e[3], e[4], e[5])
	a[y0*stride+x0+2] = vp8Avg3(e[4], e[5], e[6])
	a[y0*stride+x0+3] = vp8Avg3(e[5], e[6], e[7])
}

func vp8PredictBHUPred(a []byte, x0, y0, stride int) {
	l := vp8LeftPixels(a, x0, y0, stride)

	a[y0*stride+x0] = vp8Avg2(l[0], l[1])
	a[y0*stride+x0+1] = vp8Avg3(l[0], l[1], l[2])
	a[y0*stride+x0+2] = vp8Avg2(l[1], l[2])
	a[(y0+1)*stride+x0] = vp8Avg2(l[1], l[2])
	a[y0*stride+x0+3] = vp8Avg3(l[1], l[2], l[3])
	a[(y0+1)*stride+x0+1] = vp8Avg3(l[1], l[2], l[3])
	a[(y0+1)*stride+x0+2] = vp8Avg2(l[2], l[3])
	a[(y0+2)*stride+x0] = vp8Avg2(l[2], l[3])
	a[(y0+1)*stride+x0+3] = vp8Avg3(l[2], l[3], l[3])
	a[(y0+2)*stride+x0+1] = vp8Avg3(l[2], l[3], l[3])
	a[(y0+2)*stride+x0+2] = l[3]
	a[(y0+2)*stride+x0+3] = l[3]
	a[(y0+3)*stride+x0] = l[3]
	a[(y0+3)*stride+x0+1] = l[3]
	a[(y0+3)*stride+x0+2] = l[3]
	a[(y0+3)*stride+x0+3] = l[3]
}

// Loop filter, loop_filter.rs.

func vp8C(val int32) int32 {
	return min(max(val, -128), 127)
}

// vp8U2S is u2s, unsigned to signed.
func vp8U2S(val uint8) int32 {
	return int32(val) - 128
}

// vp8S2U is s2u, signed to unsigned.
func vp8S2U(val int32) uint8 {
	return uint8(vp8C(val) + 128)
}

func vp8Diff(val1, val2 uint8) uint8 {
	if val1 > val2 {
		return val1 - val2
	}
	return val2 - val1
}

// vp8AdjustTaps is common_adjust (loop_filter.rs:32-51, 15.2) on the four pixels around an edge: it returns the new values of the two next to
// the edge and the adjustment a.
func vp8AdjustTaps(useOuterTaps bool, p1b, p0b, q0b, q1b uint8) (newP0, newQ0 uint8, a int32) {
	p1 := vp8U2S(p1b)
	p0 := vp8U2S(p0b)
	q0 := vp8U2S(q0b)
	q1 := vp8U2S(q1b)

	// value for the outer 2 pixels
	var outer int32
	if useOuterTaps {
		outer = vp8C(p1 - q1)
	}

	a = vp8C(outer + 3*(q0-p0))

	b := vp8C(a+3) >> 3

	a = vp8C(a+4) >> 3

	return vp8S2U(p0 + b), vp8S2U(q0 - a), a
}

// vp8SimpleSegment is simple_segment (loop_filter.rs:80-86): the simple filter, which affects 4 pixels on an edge (2 each side).
func vp8SimpleSegment(edgeLimit uint8, pixels []byte, point, stride int) {
	p1 := pixels[point-2*stride]
	p0 := pixels[point-stride]
	q0 := pixels[point]
	q1 := pixels[point+stride]

	if int32(vp8Diff(p0, q0))*2+int32(vp8Diff(p1, q1))/2 <= int32(edgeLimit) {
		pixels[point-stride], pixels[point], _ = vp8AdjustTaps(true, p1, p0, q0, q1)
	}
}

// vp8SubblockFilter is subblock_filter (loop_filter.rs:88-108): the normal filter on the edges between subblocks inside a macroblock. The
// Rust code reads the pixels where it needs them, all eight are read here at once: they are always inside the plane for the edges the
// decoder filters.
func vp8SubblockFilter(hevThreshold, interiorLimit, edgeLimit uint8, pixels []byte, point, stride int) {
	p1 := pixels[point-2*stride]
	p0 := pixels[point-stride]
	q0 := pixels[point]
	q1 := pixels[point+stride]

	if int32(vp8Diff(p0, q0))*2+int32(vp8Diff(p1, q1))/2 > int32(edgeLimit) || vp8Diff(p1, p0) > interiorLimit || vp8Diff(q1, q0) > interiorLimit {
		return
	}

	p3 := pixels[point-4*stride]
	p2 := pixels[point-3*stride]
	q2 := pixels[point+2*stride]
	q3 := pixels[point+3*stride]

	if vp8Diff(p3, p2) > interiorLimit || vp8Diff(p2, p1) > interiorLimit || vp8Diff(q3, q2) > interiorLimit || vp8Diff(q2, q1) > interiorLimit {
		return
	}

	hv := vp8Diff(p1, p0) > hevThreshold || vp8Diff(q1, q0) > hevThreshold

	newP0, newQ0, a := vp8AdjustTaps(hv, p1, p0, q0, q1)
	pixels[point-stride], pixels[point] = newP0, newQ0

	if !hv {
		a = (a + 1) >> 1
		pixels[point+stride] = vp8S2U(vp8U2S(q1) - a)
		pixels[point-2*stride] = vp8S2U(vp8U2S(p1) - a)
	}
}

// vp8MacroblockFilter is macroblock_filter (loop_filter.rs:110-147): the normal filter on the edges between macroblocks.
func vp8MacroblockFilter(hevThreshold, interiorLimit, edgeLimit uint8, pixels []byte, point, stride int) {
	p1 := pixels[point-2*stride]
	p0 := pixels[point-stride]
	q0 := pixels[point]
	q1 := pixels[point+stride]

	if int32(vp8Diff(p0, q0))*2+int32(vp8Diff(p1, q1))/2 > int32(edgeLimit) || vp8Diff(p1, p0) > interiorLimit || vp8Diff(q1, q0) > interiorLimit {
		return
	}

	p3 := pixels[point-4*stride]
	p2 := pixels[point-3*stride]
	q2 := pixels[point+2*stride]
	q3 := pixels[point+3*stride]

	if vp8Diff(p3, p2) > interiorLimit || vp8Diff(p2, p1) > interiorLimit || vp8Diff(q3, q2) > interiorLimit || vp8Diff(q2, q1) > interiorLimit {
		return
	}

	if vp8Diff(p1, p0) > hevThreshold || vp8Diff(q1, q0) > hevThreshold {
		pixels[point-stride], pixels[point], _ = vp8AdjustTaps(true, p1, p0, q0, q1)
		return
	}

	s1, s2, s3, s4, s5, s6 := vp8U2S(p2), vp8U2S(p1), vp8U2S(p0), vp8U2S(q0), vp8U2S(q1), vp8U2S(q2)

	w := vp8C(vp8C(s2-s5) + 3*(s4-s3))

	a := vp8C((27*w + 63) >> 7)

	pixels[point] = vp8S2U(s4 - a)
	pixels[point-stride] = vp8S2U(s3 + a)

	a = vp8C((18*w + 63) >> 7)

	pixels[point+stride] = vp8S2U(s5 - a)
	pixels[point-2*stride] = vp8S2U(s2 + a)

	a = vp8C((9*w + 63) >> 7)

	pixels[point+2*stride] = vp8S2U(s6 - a)
	pixels[point-3*stride] = vp8S2U(s1 + a)
}

// Transforms, transform.rs.

const (
	vp8Const1 int64 = 20091
	vp8Const2 int64 = 35468
)

// vp8IDCT4x4 is idct4x4 (transform.rs:151-192); the intermediate results are int64 as in the Rust code.
func vp8IDCT4x4(block []int32) {
	fetch := func(idx int) int64 { return int64(block[idx]) }

	for i := range 4 {
		a1 := fetch(i) + fetch(8+i)
		b1 := fetch(i) - fetch(8+i)

		t1 := (fetch(4+i) * vp8Const2) >> 16
		t2 := fetch(12+i) + ((fetch(12+i) * vp8Const1) >> 16)
		c1 := t1 - t2

		t1 = fetch(4+i) + ((fetch(4+i) * vp8Const1) >> 16)
		t2 = (fetch(12+i) * vp8Const2) >> 16
		d1 := t1 + t2

		block[i] = int32(a1 + d1)
		block[4+i] = int32(b1 + c1)
		block[4*3+i] = int32(a1 - d1)
		block[4*2+i] = int32(b1 - c1)
	}

	for i := range 4 {
		a1 := fetch(4*i) + fetch(4*i+2)
		b1 := fetch(4*i) - fetch(4*i+2)

		t1 := (fetch(4*i+1) * vp8Const2) >> 16
		t2 := fetch(4*i+3) + ((fetch(4*i+3) * vp8Const1) >> 16)
		c1 := t1 - t2

		t1 = fetch(4*i+1) + ((fetch(4*i+1) * vp8Const1) >> 16)
		t2 = (fetch(4*i+3) * vp8Const2) >> 16
		d1 := t1 + t2

		block[4*i] = int32((a1 + d1 + 4) >> 3)
		block[4*i+3] = int32((a1 - d1 + 4) >> 3)
		block[4*i+1] = int32((b1 + c1 + 4) >> 3)
		block[4*i+2] = int32((b1 - c1 + 4) >> 3)
	}
}

// vp8IWHT4x4 is iwht4x4 (transform.rs:195-224, 14.3).
func vp8IWHT4x4(block []int32) {
	for i := range 4 {
		a1 := block[i] + block[12+i]
		b1 := block[4+i] + block[8+i]
		c1 := block[4+i] - block[8+i]
		d1 := block[i] - block[12+i]

		block[i] = a1 + b1
		block[4+i] = c1 + d1
		block[8+i] = a1 - b1
		block[12+i] = d1 - c1
	}

	for i := range 4 {
		a1 := block[4*i] + block[4*i+3]
		b1 := block[4*i+1] + block[4*i+2]
		c1 := block[4*i+1] - block[4*i+2]
		d1 := block[4*i] - block[4*i+3]

		a2 := a1 + b1
		b2 := c1 + d1
		c2 := a1 - b1
		d2 := d1 - c1

		block[4*i] = (a2 + 3) >> 3
		block[4*i+1] = (b2 + 3) >> 3
		block[4*i+2] = (c2 + 3) >> 3
		block[4*i+3] = (d2 + 3) >> 3
	}
}
