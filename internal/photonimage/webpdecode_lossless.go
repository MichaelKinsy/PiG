// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

// Ports the lossless (VP8L) WebP decoder of image 0.24.9 (crates.io image-0.24.9.crate, the version Photon's wasm embeds): src/codecs/webp/
// lossless.rs (cited below as lossless.rs), huffman.rs and lossless_transform.rs. Photon's wasm is 32-bit: the `usize` of the Rust code is a
// uint32 here wherever a hostile stream can make its wrapping visible (copy lengths and distances, bit counts), the `u16` of the Rust code is
// a uint16, and arithmetic that wraps in a release build wraps here.

const (
	losslessCodeLengthCodes   = 19
	losslessCodesPerMetaCode  = 5
	losslessNumTransformTypes = 4

	losslessGreen = 0
	losslessRed   = 1
	losslessBlue  = 2
	losslessAlpha = 3
	losslessDist  = 4

	// losslessMaxPixels is the most pixels of a lossless frame: 16384 by 16384, a gigabyte of ARGB (the canvas of an extended image can
	// be smaller than the frame it has, then Photon returns the first pixels of the frame, so a frame this large is no error by itself).
	losslessMaxPixels = 1 << 28
)

// losslessCodeLengthCodeOrder is CODE_LENGTH_CODE_ORDER (lossless.rs:20-22).
var losslessCodeLengthCodeOrder = [losslessCodeLengthCodes]uint8{17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// losslessDistanceMap is DISTANCE_MAP (lossless.rs:25-41).
var losslessDistanceMap = [120][2]int8{
	{0, 1}, {1, 0}, {1, 1}, {-1, 1}, {0, 2}, {2, 0}, {1, 2}, {-1, 2},
	{2, 1}, {-2, 1}, {2, 2}, {-2, 2}, {0, 3}, {3, 0}, {1, 3}, {-1, 3},
	{3, 1}, {-3, 1}, {2, 3}, {-2, 3}, {3, 2}, {-3, 2}, {0, 4}, {4, 0},
	{1, 4}, {-1, 4}, {4, 1}, {-4, 1}, {3, 3}, {-3, 3}, {2, 4}, {-2, 4},
	{4, 2}, {-4, 2}, {0, 5}, {3, 4}, {-3, 4}, {4, 3}, {-4, 3}, {5, 0},
	{1, 5}, {-1, 5}, {5, 1}, {-5, 1}, {2, 5}, {-2, 5}, {5, 2}, {-5, 2},
	{4, 4}, {-4, 4}, {3, 5}, {-3, 5}, {5, 3}, {-5, 3}, {0, 6}, {6, 0},
	{1, 6}, {-1, 6}, {6, 1}, {-6, 1}, {2, 6}, {-2, 6}, {6, 2}, {-6, 2},
	{4, 5}, {-4, 5}, {5, 4}, {-5, 4}, {3, 6}, {-3, 6}, {6, 3}, {-6, 3},
	{0, 7}, {7, 0}, {1, 7}, {-1, 7}, {5, 5}, {-5, 5}, {7, 1}, {-7, 1},
	{4, 6}, {-4, 6}, {6, 4}, {-6, 4}, {2, 7}, {-2, 7}, {7, 2}, {-7, 2},
	{3, 7}, {-3, 7}, {7, 3}, {-7, 3}, {5, 6}, {-5, 6}, {6, 5}, {-6, 5},
	{8, 0}, {4, 7}, {-4, 7}, {7, 4}, {-7, 4}, {8, 1}, {8, 2}, {6, 6},
	{-6, 6}, {8, 3}, {5, 7}, {-5, 7}, {7, 5}, {-7, 5}, {8, 4}, {6, 7},
	{-6, 7}, {7, 6}, {-7, 6}, {8, 5}, {7, 7}, {-7, 7}, {8, 6}, {8, 7},
}

// losslessAlphabetSize is ALPHABET_SIZE (lossless.rs:53).
var losslessAlphabetSize = [losslessCodesPerMetaCode]uint16{256 + 24, 256, 256, 256, 40}

// losslessSubsampleSize is subsample_size (lossless.rs:55-60).
func losslessSubsampleSize(size uint16, bits uint8) uint16 {
	return uint16((uint32(size) + (1 << bits) - 1) >> bits)
}

// losslessBitReader is BitReader (lossless.rs:670-712): the bits of the stream, least significant bit of each byte first.
type losslessBitReader struct {
	buf      []byte
	index    int
	bitCount uint8
}

// readBits is BitReader::read_bits (lossless.rs:690-711). The Rust function is generic over the result type, the shift of each bit is
// a shift of that type: for a 32-bit usize a count above 32 shifts by the count modulo 32, which only a hostile stream gets to. The
// callers of the narrower types never ask for more bits than the type has.
func (r *losslessBitReader) readBits(num uint8) (uint32, error) {
	var value uint32
	for i := range num {
		if len(r.buf) <= r.index {
			return 0, errWebPBitStream
		}
		bit := uint32(r.buf[r.index]>>r.bitCount) & 1
		value += bit << (i & 31)
		if r.bitCount == 7 {
			r.index++
			r.bitCount = 0
		} else {
			r.bitCount++
		}
	}
	return value, nil
}

// webpHuffmanNode is HuffmanTreeNode (huffman.rs:11-16).
type webpHuffmanNode struct {
	kind   uint8 // webpHuffmanEmpty, webpHuffmanLeaf or webpHuffmanBranch
	symbol uint16
	offset int // children of a branch
}

const (
	webpHuffmanEmpty uint8 = iota
	webpHuffmanLeaf
	webpHuffmanBranch
)

const webpHuffmanMaxCodeLength = 15

// webpHuffmanTree is HuffmanTree (huffman.rs:18-24).
type webpHuffmanTree struct {
	tree     []webpHuffmanNode
	maxNodes int
	numNodes int
}

// assignChildren is huffman.rs:32-38.
func (t *webpHuffmanTree) assignChildren(nodeIndex int) int {
	offsetIndex := t.numNodes - nodeIndex
	t.tree[nodeIndex] = webpHuffmanNode{kind: webpHuffmanBranch, offset: offsetIndex}
	t.numNodes += 2
	return offsetIndex
}

// newWebPHuffmanTree is HuffmanTree::init (huffman.rs:41-57).
func newWebPHuffmanTree(numLeaves int) (*webpHuffmanTree, error) {
	if numLeaves == 0 {
		return nil, errWebPHuffman
	}

	maxNodes := 2*numLeaves - 1
	return &webpHuffmanTree{tree: make([]webpHuffmanNode, maxNodes), maxNodes: maxNodes, numNodes: 1}, nil
}

// webpHuffmanCodeLengthsToCodes is code_lengths_to_codes (huffman.rs:60-101); a code that is not there is -1.
func webpHuffmanCodeLengthsToCodes(codeLengths []uint16) ([]int32, error) {
	var maxCodeLength uint16
	for _, l := range codeLengths {
		maxCodeLength = max(maxCodeLength, l)
	}

	if maxCodeLength > webpHuffmanMaxCodeLength {
		return nil, errWebPHuffman
	}

	var codeLengthHist [webpHuffmanMaxCodeLength + 1]uint16

	for _, length := range codeLengths {
		codeLengthHist[length]++
	}

	codeLengthHist[0] = 0

	var currCode uint16
	var nextCodes [webpHuffmanMaxCodeLength + 1]int32
	for i := range nextCodes {
		nextCodes[i] = -1
	}

	for codeLen := 1; codeLen <= int(maxCodeLength); codeLen++ {
		currCode = (currCode + codeLengthHist[codeLen-1]) << 1
		nextCodes[codeLen] = int32(currCode)
	}

	huffCodes := make([]int32, len(codeLengths))

	for symbol, length := range codeLengths {
		if length > 0 {
			huffCodes[symbol] = nextCodes[length]
			if nextCodes[length] >= 0 {
				// a u16 that wraps
				nextCodes[length] = int32(uint16(nextCodes[length]) + 1)
			}
		} else {
			huffCodes[symbol] = -1
		}
	}

	return huffCodes, nil
}

// addSymbol is huffman.rs:104-136.
func (t *webpHuffmanTree) addSymbol(symbol, code, codeLength uint16) error {
	nodeIndex := 0

	for length := int(codeLength) - 1; length >= 0; length-- {
		if nodeIndex >= t.maxNodes {
			return errWebPHuffman
		}

		node := t.tree[nodeIndex]

		var offset int
		switch node.kind {
		case webpHuffmanEmpty:
			if t.numNodes == t.maxNodes {
				return errWebPHuffman
			}
			offset = t.assignChildren(nodeIndex)
		case webpHuffmanLeaf:
			return errWebPHuffman
		default:
			offset = node.offset
		}

		nodeIndex += offset + int((code>>length)&1)
	}

	if t.tree[nodeIndex].kind != webpHuffmanEmpty {
		return errWebPHuffman
	}
	t.tree[nodeIndex] = webpHuffmanNode{kind: webpHuffmanLeaf, symbol: symbol}

	return nil
}

// buildWebPHuffmanImplicit is HuffmanTree::build_implicit (huffman.rs:139-165): a tree from code lengths alone.
func buildWebPHuffmanImplicit(codeLengths []uint16) (*webpHuffmanTree, error) {
	numSymbols := 0
	var rootSymbol uint16

	for symbol, length := range codeLengths {
		if length > 0 {
			numSymbols++
			rootSymbol = uint16(symbol)
		}
	}

	tree, err := newWebPHuffmanTree(numSymbols)
	if err != nil {
		return nil, err
	}

	if numSymbols == 1 {
		if err := tree.addSymbol(rootSymbol, 0, 0); err != nil {
			return nil, err
		}
	} else {
		codes, err := webpHuffmanCodeLengthsToCodes(codeLengths)
		if err != nil {
			return nil, err
		}

		for symbol, length := range codeLengths {
			if length > 0 && codes[symbol] >= 0 {
				if err := tree.addSymbol(uint16(symbol), uint16(codes[symbol]), length); err != nil {
					return nil, err
				}
			}
		}
	}

	return tree, nil
}

// buildWebPHuffmanExplicit is HuffmanTree::build_explicit (huffman.rs:168-180): a tree from lengths, codes and symbols.
func buildWebPHuffmanExplicit(codeLengths, codes, symbols []uint16) (*webpHuffmanTree, error) {
	tree, err := newWebPHuffmanTree(len(symbols))
	if err != nil {
		return nil, err
	}

	for i := range symbols {
		if err := tree.addSymbol(symbols[i], codes[i], codeLengths[i]); err != nil {
			return nil, err
		}
	}

	return tree, nil
}

// readSymbol is HuffmanTree::read_symbol (huffman.rs:183-199).
func (t *webpHuffmanTree) readSymbol(r *losslessBitReader) (uint16, error) {
	index := 0
	node := t.tree[index]

	for node.kind == webpHuffmanBranch {
		bit, err := r.readBits(1)
		if err != nil {
			return 0, err
		}
		index += node.offset + int(bit)
		node = t.tree[index]
	}

	if node.kind == webpHuffmanEmpty {
		return 0, errWebPHuffman
	}

	return node.symbol, nil
}

// losslessHuffmanGroup is HuffmanCodeGroup (lossless.rs:51).
type losslessHuffmanGroup [losslessCodesPerMetaCode]*webpHuffmanTree

// losslessColorCache is ColorCache (lossless.rs:650-668).
type losslessColorCache struct {
	bits  uint8
	cache []uint32
}

func (c *losslessColorCache) insert(color uint32) {
	index := (0x1e35a7bd * color) >> (32 - c.bits)
	c.cache[index] = color
}

func (c *losslessColorCache) lookup(index uint32) (uint32, error) {
	if int64(index) >= int64(len(c.cache)) {
		return 0, errWebPBitStream
	}
	return c.cache[index], nil
}

// losslessHuffmanInfo is HuffmanInfo (lossless.rs:628-648).
type losslessHuffmanInfo struct {
	xsize      uint16
	colorCache *losslessColorCache
	image      []uint32
	bits       uint8
	mask       uint16
	groups     []losslessHuffmanGroup
}

// huffIndex is HuffmanInfo::get_huff_index (lossless.rs:640-647); the position is a u16 expression that wraps.
func (h *losslessHuffmanInfo) huffIndex(x, y uint16) (int, error) {
	if h.bits == 0 {
		return 0, nil
	}
	position := int((y>>h.bits)*h.xsize + (x >> h.bits))
	if position >= len(h.image) {
		return 0, errWebPBitStream
	}
	return int(h.image[position]), nil
}

// losslessFrame is LosslessFrame (lossless.rs:714-745).
type losslessFrame struct {
	width  uint16
	height uint16
	buf    []uint32
}

// fillRGBA is LosslessFrame::fill_rgba: the pixels of the frame in raster order as RGBA8 into buf, as many as there are in both.
func (f *losslessFrame) fillRGBA(buf []byte) {
	for i, argb := range f.buf {
		if 4*i+4 > len(buf) {
			break
		}
		buf[4*i] = byte(argb >> 16)
		buf[4*i+1] = byte(argb >> 8)
		buf[4*i+2] = byte(argb)
		buf[4*i+3] = byte(argb >> 24)
	}
}

// fillGreen is LosslessFrame::fill_green.
func (f *losslessFrame) fillGreen(buf []byte) {
	for i, argb := range f.buf {
		if i >= len(buf) {
			break
		}
		buf[i] = byte(argb >> 8)
	}
}

// losslessTransformType is TransformType (lossless_transform.rs:5-19).
type losslessTransformType struct {
	kind uint8 // the transform type value 0 to 3 of the stream

	sizeBits uint8    // predictor and color transforms
	data     []uint32 // the predictor, color transform or color table image
	tableLen uint16   // color indexing
}

const (
	losslessPredictor uint8 = iota
	losslessColorTransform
	losslessSubtractGreen
	losslessColorIndexing
)

// losslessDecoder is LosslessDecoder (lossless.rs:113-133); r is the chunk or the data after the ALPH header.
type losslessDecoder struct {
	// limitFrame makes a frame the RGBA8 of which is over the memory limit an error before it is decoded. It is set where the image is
	// the frame (a plain file, a frame of an animation): Photon decodes the frame and then fails the limit check on it.
	limitFrame bool

	r              []byte
	bitReader      losslessBitReader
	frame          losslessFrame
	transforms     [losslessNumTransformTypes]*losslessTransformType
	transformOrder []uint8
}

func newLosslessDecoder(r []byte) *losslessDecoder {
	return &losslessDecoder{r: r}
}

// decodeFrame is LosslessDecoder::decode_frame (lossless.rs:136-167).
func (d *losslessDecoder) decodeFrame() (*losslessFrame, error) {
	if len(d.r) == 0 {
		return nil, errWebPEOF
	}
	signature := d.r[0]

	if signature != 0x2f {
		return nil, errWebPLosslessSignature
	}

	d.bitReader.buf = d.r[1:]

	w, err := d.bitReader.readBits(14)
	if err != nil {
		return nil, err
	}
	d.frame.width = uint16(w) + 1
	h, err := d.bitReader.readBits(14)
	if err != nil {
		return nil, err
	}
	d.frame.height = uint16(h) + 1

	if d.limitFrame && uint64(d.frame.width)*uint64(d.frame.height)*4 > webpMaxAlloc {
		return nil, errWebPLimit
	}

	if _, err := d.bitReader.readBits(1); err != nil { // alpha_used
		return nil, err
	}

	versionNum, err := d.bitReader.readBits(3)
	if err != nil {
		return nil, err
	}

	if versionNum != 0 {
		return nil, errWebPLosslessVersion
	}

	return d.decodeFrameData()
}

// decodeFrameImplicitDims is LosslessDecoder::decode_frame_implicit_dims (lossless.rs:170-192), used for the alpha data of an extended
// image: there is no header, the dimensions are those of the canvas.
func (d *losslessDecoder) decodeFrameImplicitDims(width, height uint16) (*losslessFrame, error) {
	d.bitReader.buf = d.r

	d.frame.width = width
	d.frame.height = height

	return d.decodeFrameData()
}

func (d *losslessDecoder) decodeFrameData() (*losslessFrame, error) {
	data, err := d.decodeImageStream(d.frame.width, d.frame.height, true)
	if err != nil {
		return nil, err
	}

	// transform_order is the indices (0-3) into transforms in the order decoded
	for i := len(d.transformOrder) - 1; i >= 0; i-- {
		trans := d.transforms[d.transformOrder[i]]
		data, err = trans.apply(data, d.frame.width, d.frame.height)
		if err != nil {
			return nil, err
		}
	}

	d.frame.buf = data
	return &d.frame, nil
}

// decodeImageStream is lossless.rs:198-227: an image in any of the 5 roles of the specification; the ARGB image role also has transforms
// and meta prefix codes. xsize and ysize describe the size of the blocks where each block has its own entropy code.
func (d *losslessDecoder) decodeImageStream(xsize, ysize uint16, isARGBImg bool) ([]uint32, error) {
	transXsize := xsize
	if isARGBImg {
		var err error
		transXsize, err = d.readTransforms()
		if err != nil {
			return nil, err
		}
	}

	colorCacheBits, hasColorCache, err := d.readColorCache()
	if err != nil {
		return nil, err
	}

	var colorCache *losslessColorCache
	if hasColorCache {
		colorCache = &losslessColorCache{bits: colorCacheBits, cache: make([]uint32, 1<<colorCacheBits)}
	}

	huffmanInfo, err := d.readHuffmanCodes(isARGBImg, transXsize, ysize, colorCache)
	if err != nil {
		return nil, err
	}

	return d.decodeImageData(transXsize, ysize, huffmanInfo)
}

// readTransforms is lossless.rs:230-309.
func (d *losslessDecoder) readTransforms() (uint16, error) {
	xsize := d.frame.width

	for {
		more, err := d.bitReader.readBits(1)
		if err != nil {
			return 0, err
		}
		if more != 1 {
			break
		}

		transformTypeVal, err := d.bitReader.readBits(2)
		if err != nil {
			return 0, err
		}

		if d.transforms[transformTypeVal] != nil {
			// can only have one of each transform, error
			return 0, errWebPTransform
		}

		d.transformOrder = append(d.transformOrder, uint8(transformTypeVal))

		var transformType *losslessTransformType
		switch transformTypeVal {
		case 0, 1: // predictor, color transform
			bits, err := d.bitReader.readBits(3)
			if err != nil {
				return 0, err
			}
			sizeBits := uint8(bits) + 2

			blockXsize := losslessSubsampleSize(xsize, sizeBits)
			blockYsize := losslessSubsampleSize(d.frame.height, sizeBits)

			data, err := d.decodeImageStream(blockXsize, blockYsize, false)
			if err != nil {
				return 0, err
			}

			transformType = &losslessTransformType{kind: uint8(transformTypeVal), sizeBits: sizeBits, data: data}
		case 2: // subtract green
			transformType = &losslessTransformType{kind: losslessSubtractGreen}
		default: // color indexing
			size, err := d.bitReader.readBits(8)
			if err != nil {
				return 0, err
			}
			colorTableSize := uint16(size) + 1

			colorMap, err := d.decodeImageStream(colorTableSize, 1, false)
			if err != nil {
				return 0, err
			}

			xsize = losslessSubsampleSize(xsize, losslessWidthBits(colorTableSize))

			// adjust_color_map: the color map is subtraction coded
			for i := 1; i < len(colorMap); i++ {
				colorMap[i] = losslessAddPixels(colorMap[i], colorMap[i-1])
			}

			transformType = &losslessTransformType{kind: losslessColorIndexing, tableLen: colorTableSize, data: colorMap}
		}

		d.transforms[transformTypeVal] = transformType
	}

	return xsize, nil
}

// losslessWidthBits is the number of bits the width of a color indexed image is shifted by (lossless.rs:284-292).
func losslessWidthBits(colorTableSize uint16) uint8 {
	switch {
	case colorTableSize <= 2:
		return 3
	case colorTableSize <= 4:
		return 2
	case colorTableSize <= 16:
		return 1
	default:
		return 0
	}
}

// readHuffmanCodes is lossless.rs:319-387.
func (d *losslessDecoder) readHuffmanCodes(readMeta bool, xsize, ysize uint16, colorCache *losslessColorCache) (*losslessHuffmanInfo, error) {
	numHuffGroups := uint32(1)

	var huffmanBits uint8
	huffmanXsize := uint16(1)
	var entropyImage []uint32

	if readMeta {
		meta, err := d.bitReader.readBits(1)
		if err != nil {
			return nil, err
		}

		if meta == 1 {
			// meta huffman codes
			bits, err := d.bitReader.readBits(3)
			if err != nil {
				return nil, err
			}
			huffmanBits = uint8(bits) + 2
			huffmanXsize = losslessSubsampleSize(xsize, huffmanBits)
			huffmanYsize := losslessSubsampleSize(ysize, huffmanBits)

			entropyImage, err = d.decodeImageStream(huffmanXsize, huffmanYsize, false)
			if err != nil {
				return nil, err
			}

			for i, pixel := range entropyImage {
				metaHuffCode := (pixel >> 8) & 0xffff

				entropyImage[i] = metaHuffCode

				if metaHuffCode >= numHuffGroups {
					numHuffGroups = metaHuffCode + 1
				}
			}
		}
	}

	groups := make([]losslessHuffmanGroup, 0, min(numHuffGroups, 1<<10))

	for range numHuffGroups {
		var group losslessHuffmanGroup
		for j := range losslessCodesPerMetaCode {
			alphabetSize := losslessAlphabetSize[j]
			if j == 0 && colorCache != nil {
				alphabetSize += 1 << colorCache.bits
			}

			tree, err := d.readHuffmanCode(alphabetSize)
			if err != nil {
				return nil, err
			}
			group[j] = tree
		}
		groups = append(groups, group)
	}

	huffmanMask := ^uint16(0)
	if huffmanBits != 0 {
		huffmanMask = 1<<huffmanBits - 1
	}

	return &losslessHuffmanInfo{
		xsize:      huffmanXsize,
		colorCache: colorCache,
		image:      entropyImage,
		bits:       huffmanBits,
		mask:       huffmanMask,
		groups:     groups,
	}, nil
}

// readHuffmanCode is lossless.rs:390-424: a single huffman tree.
func (d *losslessDecoder) readHuffmanCode(alphabetSize uint16) (*webpHuffmanTree, error) {
	simple, err := d.bitReader.readBits(1)
	if err != nil {
		return nil, err
	}

	if simple == 1 {
		n, err := d.bitReader.readBits(1)
		if err != nil {
			return nil, err
		}
		numSymbols := n + 1

		codeLengths := []uint16{uint16(numSymbols - 1)}
		codes := []uint16{0}
		var symbols []uint16

		isFirst8Bits, err := d.bitReader.readBits(1)
		if err != nil {
			return nil, err
		}
		s, err := d.bitReader.readBits(uint8(1 + 7*isFirst8Bits))
		if err != nil {
			return nil, err
		}
		symbols = append(symbols, uint16(s))

		if numSymbols == 2 {
			s, err := d.bitReader.readBits(8)
			if err != nil {
				return nil, err
			}
			symbols = append(symbols, uint16(s))
			codeLengths = append(codeLengths, 1)
			codes = append(codes, 1)
		}

		return buildWebPHuffmanExplicit(codeLengths, codes, symbols)
	}

	codeLengthCodeLengths := make([]uint16, losslessCodeLengthCodes)

	n, err := d.bitReader.readBits(4)
	if err != nil {
		return nil, err
	}
	numCodeLengths := 4 + int(n)
	for i := range numCodeLengths {
		v, err := d.bitReader.readBits(3)
		if err != nil {
			return nil, err
		}
		codeLengthCodeLengths[losslessCodeLengthCodeOrder[i]] = uint16(v)
	}

	newCodeLengths, err := d.readHuffmanCodeLengths(codeLengthCodeLengths, alphabetSize)
	if err != nil {
		return nil, err
	}

	return buildWebPHuffmanImplicit(newCodeLengths)
}

// readHuffmanCodeLengths is lossless.rs:427-490.
func (d *losslessDecoder) readHuffmanCodeLengths(codeLengthCodeLengths []uint16, numSymbols uint16) ([]uint16, error) {
	table, err := buildWebPHuffmanImplicit(codeLengthCodeLengths)
	if err != nil {
		return nil, err
	}

	flag, err := d.bitReader.readBits(1)
	if err != nil {
		return nil, err
	}

	maxSymbol := numSymbols
	if flag == 1 {
		n, err := d.bitReader.readBits(3)
		if err != nil {
			return nil, err
		}
		lengthNbits := uint8(2 + 2*n)
		v, err := d.bitReader.readBits(lengthNbits)
		if err != nil {
			return nil, err
		}
		maxSymbol = 2 + uint16(v) // a u16 that wraps
	}

	codeLengths := make([]uint16, numSymbols)
	prevCodeLen := uint16(8) // default code length

	var symbol uint16
	for symbol < numSymbols {
		if maxSymbol == 0 {
			break
		}
		maxSymbol--

		codeLen, err := table.readSymbol(&d.bitReader)
		if err != nil {
			return nil, err
		}

		if codeLen < 16 {
			codeLengths[symbol] = codeLen
			symbol++
			if codeLen != 0 {
				prevCodeLen = codeLen
			}
		} else {
			usePrev := codeLen == 16
			slot := codeLen - 16
			var extraBits uint8
			var repeatOffset uint16
			switch slot {
			case 0:
				extraBits, repeatOffset = 2, 3
			case 1:
				extraBits, repeatOffset = 3, 3
			case 2:
				extraBits, repeatOffset = 7, 11
			default:
				return nil, errWebPBitStream
			}

			v, err := d.bitReader.readBits(extraBits)
			if err != nil {
				return nil, err
			}
			repeat := uint16(v) + repeatOffset

			if symbol+repeat > numSymbols {
				return nil, errWebPBitStream
			}

			length := uint16(0)
			if usePrev {
				length = prevCodeLen
			}
			for ; repeat > 0; repeat-- {
				codeLengths[symbol] = length
				symbol++
			}
		}
	}

	return codeLengths, nil
}

// decodeImageData is lossless.rs:493-584: the image data, with the huffman trees, in the 3 ways (literal, backward reference, color
// cache) a pixel can be coded.
func (d *losslessDecoder) decodeImageData(width, height uint16, huffmanInfo *losslessHuffmanInfo) ([]uint32, error) {
	numValues := uint32(width) * uint32(height)
	if numValues > losslessMaxPixels {
		return nil, errWebPImageTooLarge
	}
	data := make([]uint32, numValues)

	huffIndex, err := huffmanInfo.huffIndex(0, 0)
	if err != nil {
		return nil, err
	}
	tree := &huffmanInfo.groups[huffIndex]
	var lastCached uint32
	var index uint32
	var x, y uint16
	for index < numValues {
		if x&huffmanInfo.mask == 0 {
			i, err := huffmanInfo.huffIndex(x, y)
			if err != nil {
				return nil, err
			}
			tree = &huffmanInfo.groups[i]
		}

		code, err := tree[losslessGreen].readSymbol(&d.bitReader)
		if err != nil {
			return nil, err
		}

		switch {
		case code < 256:
			// literal, so just use huffman codes and read as argb
			red, err := tree[losslessRed].readSymbol(&d.bitReader)
			if err != nil {
				return nil, err
			}
			blue, err := tree[losslessBlue].readSymbol(&d.bitReader)
			if err != nil {
				return nil, err
			}
			alpha, err := tree[losslessAlpha].readSymbol(&d.bitReader)
			if err != nil {
				return nil, err
			}

			data[index] = uint32(alpha)<<24 + uint32(red)<<16 + uint32(code)<<8 + uint32(blue)

			index++
			x++
			if x >= width {
				x = 0
				y++
			}
		case code < 256+24:
			// backward reference, so go back and use that to add image data
			lengthSymbol := code - 256
			length, err := d.copyDistance(lengthSymbol)
			if err != nil {
				return nil, err
			}

			distSymbol, err := tree[losslessDist].readSymbol(&d.bitReader)
			if err != nil {
				return nil, err
			}
			distCode, err := d.copyDistance(distSymbol)
			if err != nil {
				return nil, err
			}
			dist, err := losslessPlaneCodeToDistance(width, distCode)
			if err != nil {
				return nil, err
			}

			if index < dist || numValues-index < length {
				return nil, errWebPBitStream
			}

			for i := range length {
				data[index+i] = data[index+i-dist]
			}
			index += length
			if length > 0xffff {
				// u16::try_from(length).unwrap() panics
				return nil, errWebPBitStream
			}
			x += uint16(length)
			for x >= width {
				x -= width
				y++
			}
			if index < numValues {
				i, err := huffmanInfo.huffIndex(x, y)
				if err != nil {
					return nil, err
				}
				tree = &huffmanInfo.groups[i]
			}
		default:
			// color cache, so use previously stored pixels to get this pixel
			key := code - 256 - 24

			colorCache := huffmanInfo.colorCache
			if colorCache == nil {
				return nil, errWebPBitStream
			}
			// cache old colors
			for lastCached < index {
				colorCache.insert(data[lastCached])
				lastCached++
			}
			data[index], err = colorCache.lookup(uint32(key))
			if err != nil {
				return nil, err
			}
			index++
			x++
			if x >= width {
				x = 0
				y++
			}
		}
	}

	return data, nil
}

// readColorCache is lossless.rs:587-599.
func (d *losslessDecoder) readColorCache() (uint8, bool, error) {
	flag, err := d.bitReader.readBits(1)
	if err != nil {
		return 0, false, err
	}
	if flag != 1 {
		return 0, false, nil
	}

	bits, err := d.bitReader.readBits(4)
	if err != nil {
		return 0, false, err
	}
	if bits < 1 || bits > 11 {
		return 0, false, errWebPColorCacheBits
	}

	return uint8(bits), true, nil
}

// copyDistance is get_copy_distance (lossless.rs:602-610): the copy distance of a prefix code and the bitstream, in 32-bit usize
// arithmetic.
func (d *losslessDecoder) copyDistance(prefixCode uint16) (uint32, error) {
	if prefixCode < 4 {
		return uint32(prefixCode + 1), nil
	}
	if (prefixCode-2)>>1 > 0xff {
		// try_into::<u8>().unwrap() panics
		return 0, errWebPBitStream
	}
	extraBits := uint8((prefixCode - 2) >> 1)
	offset := (2 + uint32(prefixCode)&1) << (extraBits & 31)

	bits, err := d.bitReader.readBits(extraBits)
	if err != nil {
		return 0, err
	}
	return offset + bits + 1, nil
}

// losslessPlaneCodeToDistance is plane_code_to_distance (lossless.rs:613-625).
func losslessPlaneCodeToDistance(xsize uint16, planeCode uint32) (uint32, error) {
	if planeCode > 120 {
		return planeCode - 120, nil
	}

	if planeCode-1 >= uint32(len(losslessDistanceMap)) {
		// plane_code is 0 after the wrap of a hostile stream: DISTANCE_MAP[plane_code - 1] is out of bounds
		return 0, errWebPBitStream
	}
	offsets := losslessDistanceMap[planeCode-1]

	dist := int32(offsets[0]) + int32(offsets[1])*int32(xsize)
	if dist < 1 {
		return 1, nil
	}
	return uint32(dist), nil
}

// apply is TransformType::apply_transform (lossless_transform.rs:23-262): the inverse of the transform on the image data; width and
// height are those of the frame, whichever size the data of the stream has.
func (t *losslessTransformType) apply(imageData []uint32, width, height uint16) ([]uint32, error) {
	switch t.kind {
	case losslessPredictor:
		blockXsize := int(losslessSubsampleSize(width, t.sizeBits))
		w := int(width)
		h := int(height)

		if len(imageData) < w*h {
			return nil, errWebPTransform
		}

		// handle top and left borders specially
		// this involves ignoring mode and just setting prediction values like this
		imageData[0] = losslessAddPixels(imageData[0], 0xff000000)

		for x := 1; x < w; x++ {
			imageData[x] = losslessAddPixels(imageData[x], imageData[x-1])
		}

		for y := 1; y < h; y++ {
			imageData[y*w] = losslessAddPixels(imageData[y*w], imageData[(y-1)*w])
		}

		for y := 1; y < h; y++ {
			for x := 1; x < w; x++ {
				blockIndex := (y>>t.sizeBits)*blockXsize + (x >> t.sizeBits)
				if blockIndex >= len(t.data) {
					return nil, errWebPTransform
				}

				index := y*w + x

				green := (t.data[blockIndex] >> 8) & 0xff

				left := imageData[index-1]
				top := imageData[index-w]
				// if x == width - 1 this gets the left most pixel of the current row as described in the specification
				topRight := imageData[index-w+1]
				topLeft := imageData[index-w-1]

				var predicted uint32
				switch green {
				case 0:
					predicted = 0xff000000
				case 1:
					predicted = left
				case 2:
					predicted = top
				case 3:
					predicted = topRight
				case 4:
					predicted = topLeft
				case 5:
					predicted = losslessAverage2(losslessAverage2(left, topRight), top)
				case 6:
					predicted = losslessAverage2(left, topLeft)
				case 7:
					predicted = losslessAverage2(left, top)
				case 8:
					predicted = losslessAverage2(topLeft, top)
				case 9:
					predicted = losslessAverage2(top, topRight)
				case 10:
					predicted = losslessAverage2(losslessAverage2(left, topLeft), losslessAverage2(top, topRight))
				case 11:
					predicted = losslessSelect(left, top, topLeft)
				case 12:
					predicted = losslessClampAddSubtractFull(left, top, topLeft)
				case 13:
					predicted = losslessClampAddSubtractHalf(losslessAverage2(left, top), topLeft)
				default:
					continue
				}
				imageData[index] = losslessAddPixels(imageData[index], predicted)
			}
		}
	case losslessColorTransform:
		blockXsize := int(losslessSubsampleSize(width, t.sizeBits))
		w := int(width)
		h := int(height)

		for y := range h {
			for x := range w {
				blockIndex := (y>>t.sizeBits)*blockXsize + (x >> t.sizeBits)
				index := y*w + x

				if blockIndex >= len(t.data) || index >= len(imageData) {
					return nil, errWebPTransform
				}

				imageData[index] = losslessTransformColor(t.data[blockIndex], imageData[index])
			}
		}
	case losslessSubtractGreen:
		w := int(width)
		for y := range int(height) {
			for x := range w {
				if y*w+x >= len(imageData) {
					return nil, errWebPTransform
				}
				imageData[y*w+x] = losslessAddGreen(imageData[y*w+x])
			}
		}
	default: // color indexing
		w := int(width)
		h := int(height)
		newImageData := make([]uint32, 0, w*h)

		tableSize := uint32(t.tableLen)
		widthBits := losslessWidthBits(t.tableLen)

		bitsPerPixel := 8 >> widthBits
		mask := uint32(1)<<bitsPerPixel - 1

		src := 0

		pixelsPerByte := 1 << widthBits
		countMask := pixelsPerByte - 1
		var packedPixels uint32

		for range h {
			for x := range w {
				if x&countMask == 0 {
					if src >= len(imageData) {
						return nil, errWebPTransform
					}
					packedPixels = (imageData[src] >> 8) & 0xff
					src++
				}

				pixels := packedPixels & mask
				var newVal uint32
				if pixels < tableSize {
					newVal = t.data[pixels]
				}

				newImageData = append(newImageData, newVal)

				packedPixels >>= bitsPerPixel
			}
		}

		imageData = newImageData
	}

	return imageData, nil
}

// losslessAddPixels is add_pixels (lossless_transform.rs:268-275): two pixels added mod 256 for each channel.
func losslessAddPixels(a, b uint32) uint32 {
	newAlpha := ((a >> 24) + (b >> 24)) & 0xff
	newRed := (((a >> 16) & 0xff) + ((b >> 16) & 0xff)) & 0xff
	newGreen := (((a >> 8) & 0xff) + ((b >> 8) & 0xff)) & 0xff
	newBlue := ((a & 0xff) + (b & 0xff)) & 0xff

	return newAlpha<<24 + newRed<<16 + newGreen<<8 + newBlue
}

// losslessAverage2 is average2 (lossless_transform.rs:300-313).
func losslessAverage2(a, b uint32) uint32 {
	var avg uint32
	for i := range 4 {
		subA := (a >> (i * 8)) & 0xff
		subB := (b >> (i * 8)) & 0xff
		avg |= ((subA + subB) / 2) << (i * 8)
	}
	return avg
}

func losslessByte(val uint32, b uint8) int32 {
	return int32((val >> (b * 8)) & 0xff)
}

func losslessAbs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// losslessSelect is select (lossless_transform.rs:326-346).
func losslessSelect(left, top, topLeft uint32) uint32 {
	predictAlpha := losslessByte(left, 3) + losslessByte(top, 3) - losslessByte(topLeft, 3)
	predictRed := losslessByte(left, 2) + losslessByte(top, 2) - losslessByte(topLeft, 2)
	predictGreen := losslessByte(left, 1) + losslessByte(top, 1) - losslessByte(topLeft, 1)
	predictBlue := losslessByte(left, 0) + losslessByte(top, 0) - losslessByte(topLeft, 0)

	predictLeft := losslessAbs(predictAlpha-losslessByte(left, 3)) +
		losslessAbs(predictRed-losslessByte(left, 2)) +
		losslessAbs(predictGreen-losslessByte(left, 1)) +
		losslessAbs(predictBlue-losslessByte(left, 0))
	predictTop := losslessAbs(predictAlpha-losslessByte(top, 3)) +
		losslessAbs(predictRed-losslessByte(top, 2)) +
		losslessAbs(predictGreen-losslessByte(top, 1)) +
		losslessAbs(predictBlue-losslessByte(top, 0))

	if predictLeft < predictTop {
		return left
	}
	return top
}

func losslessClamp(a int32) int32 {
	return min(max(a, 0), 255)
}

// losslessClampAddSubtractFull is clamp_add_subtract_full (lossless_transform.rs:370-380).
func losslessClampAddSubtractFull(a, b, c uint32) uint32 {
	var value uint32
	for i := range uint8(4) {
		subA := losslessByte(a, i)
		subB := losslessByte(b, i)
		subC := losslessByte(c, i)
		value |= uint32(losslessClamp(subA+subB-subC)) << (i * 8)
	}
	return value
}

// losslessClampAddSubtractHalf is clamp_add_subtract_half (lossless_transform.rs:383-392).
func losslessClampAddSubtractHalf(a, b uint32) uint32 {
	var value uint32
	for i := range uint8(4) {
		subA := losslessByte(a, i)
		subB := losslessByte(b, i)
		value |= uint32(losslessClamp(subA+(subA-subB)/2)) << (i * 8)
	}
	return value
}

// losslessColorTransformDelta is color_transform_delta (lossless_transform.rs:430-432): the product is an i16 sign extended to 32 bits
// before the logical shift.
func losslessColorTransformDelta(t, c int8) uint32 {
	return uint32(int32(int16(t)*int16(c))) >> 5
}

// losslessTransformColor is transform_color and color_transform (lossless_transform.rs:414-447) with the multiplier of colorCode
// (ColorTransformElement::from_color_code, :404-410).
func losslessTransformColor(colorCode, colorValue uint32) uint32 {
	greenToRed := int8(colorCode & 0xff)
	greenToBlue := int8((colorCode >> 8) & 0xff)
	redToBlue := int8((colorCode >> 16) & 0xff)

	alpha := colorValue >> 24
	red := (colorValue >> 16) & 0xff
	green := (colorValue >> 8) & 0xff
	blue := colorValue & 0xff

	tempRed := red
	tempBlue := blue

	// as does the conversion from u8 to signed two's complement i8 required
	tempRed += losslessColorTransformDelta(greenToRed, int8(green))
	tempBlue += losslessColorTransformDelta(greenToBlue, int8(green))
	tempBlue += losslessColorTransformDelta(redToBlue, int8(tempRed))

	return alpha<<24 + (tempRed&0xff)<<16 + green<<8 + tempBlue&0xff
}

// losslessAddGreen is add_green (lossless_transform.rs:452-461): green is added to red and blue.
func losslessAddGreen(argb uint32) uint32 {
	red := (argb >> 16) & 0xff
	green := (argb >> 8) & 0xff
	blue := argb & 0xff

	newRed := (red + green) & 0xff
	newBlue := (blue + green) & 0xff

	return (argb & 0xff00ff00) | (newRed << 16) | newBlue
}
