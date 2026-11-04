// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

// The literal ports of the decoder of image 0.24.9 that the first version of webpdecode*.go was (commit 3c57c29), kept for the tests that
// show the faster code of the decoder gives the same results: the bit readers, the Huffman symbol reader, the inverse transforms, the
// loop filters, the YUV to RGB conversion. They are verbatim apart from the names.

// refBitReader is losslessBitReader as it was: bit by bit.
type refBitReader struct {
	buf      []byte
	index    int
	bitCount uint8
}

// refBoolReader is vp8BoolReader as it was.
type refBoolReader struct {
	buf      []byte
	index    int
	rng      uint32
	value    uint32
	bitCount uint8
}

func (b *refBoolReader) init(buf []byte) {
	b.buf = buf
	b.value = uint32(buf[0])<<8 | uint32(buf[1])
	b.index = 2
	b.rng = 255
	b.bitCount = 0
}

// Lossless.

// readBits is BitReader::read_bits (lossless.rs:690-711). The Rust function is generic over the result type, the shift of each bit is
// a shift of that type: for a 32-bit usize a count above 32 shifts by the count modulo 32, which only a hostile stream gets to. The
// callers of the narrower types never ask for more bits than the type has.
func (r *refBitReader) readBits(num uint8) (uint32, error) {
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

// readSymbol is HuffmanTree::read_symbol (huffman.rs:183-199).
func refReadSymbol(t *webpHuffmanTree, r *refBitReader) (uint16, error) {
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

// apply is TransformType::apply_transform (lossless_transform.rs:23-262): the inverse of the transform on the image data; width and
// height are those of the frame, whichever size the data of the stream has.
func refApply(t *losslessTransformType, imageData []uint32, width, height uint16) ([]uint32, error) {
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
		imageData[0] = refLosslessAddPixels(imageData[0], 0xff000000)

		for x := 1; x < w; x++ {
			imageData[x] = refLosslessAddPixels(imageData[x], imageData[x-1])
		}

		for y := 1; y < h; y++ {
			imageData[y*w] = refLosslessAddPixels(imageData[y*w], imageData[(y-1)*w])
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
					predicted = refLosslessAverage2(refLosslessAverage2(left, topRight), top)
				case 6:
					predicted = refLosslessAverage2(left, topLeft)
				case 7:
					predicted = refLosslessAverage2(left, top)
				case 8:
					predicted = refLosslessAverage2(topLeft, top)
				case 9:
					predicted = refLosslessAverage2(top, topRight)
				case 10:
					predicted = refLosslessAverage2(refLosslessAverage2(left, topLeft), refLosslessAverage2(top, topRight))
				case 11:
					predicted = refLosslessSelect(left, top, topLeft)
				case 12:
					predicted = refLosslessClampAddSubtractFull(left, top, topLeft)
				case 13:
					predicted = refLosslessClampAddSubtractHalf(refLosslessAverage2(left, top), topLeft)
				default:
					continue
				}
				imageData[index] = refLosslessAddPixels(imageData[index], predicted)
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

				imageData[index] = refLosslessTransformColor(t.data[blockIndex], imageData[index])
			}
		}
	case losslessSubtractGreen:
		w := int(width)
		for y := range int(height) {
			for x := range w {
				if y*w+x >= len(imageData) {
					return nil, errWebPTransform
				}
				imageData[y*w+x] = refLosslessAddGreen(imageData[y*w+x])
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

// refLosslessAddPixels is add_pixels (lossless_transform.rs:268-275): two pixels added mod 256 for each channel.
func refLosslessAddPixels(a, b uint32) uint32 {
	newAlpha := ((a >> 24) + (b >> 24)) & 0xff
	newRed := (((a >> 16) & 0xff) + ((b >> 16) & 0xff)) & 0xff
	newGreen := (((a >> 8) & 0xff) + ((b >> 8) & 0xff)) & 0xff
	newBlue := ((a & 0xff) + (b & 0xff)) & 0xff

	return newAlpha<<24 + newRed<<16 + newGreen<<8 + newBlue
}

// refLosslessAverage2 is average2 (lossless_transform.rs:300-313).
func refLosslessAverage2(a, b uint32) uint32 {
	var avg uint32
	for i := range 4 {
		subA := (a >> (i * 8)) & 0xff
		subB := (b >> (i * 8)) & 0xff
		avg |= ((subA + subB) / 2) << (i * 8)
	}
	return avg
}

func refLosslessByte(val uint32, b uint8) int32 {
	return int32((val >> (b * 8)) & 0xff)
}

func refLosslessAbs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// refLosslessSelect is select (lossless_transform.rs:326-346).
func refLosslessSelect(left, top, topLeft uint32) uint32 {
	predictAlpha := refLosslessByte(left, 3) + refLosslessByte(top, 3) - refLosslessByte(topLeft, 3)
	predictRed := refLosslessByte(left, 2) + refLosslessByte(top, 2) - refLosslessByte(topLeft, 2)
	predictGreen := refLosslessByte(left, 1) + refLosslessByte(top, 1) - refLosslessByte(topLeft, 1)
	predictBlue := refLosslessByte(left, 0) + refLosslessByte(top, 0) - refLosslessByte(topLeft, 0)

	predictLeft := refLosslessAbs(predictAlpha-refLosslessByte(left, 3)) +
		refLosslessAbs(predictRed-refLosslessByte(left, 2)) +
		refLosslessAbs(predictGreen-refLosslessByte(left, 1)) +
		refLosslessAbs(predictBlue-refLosslessByte(left, 0))
	predictTop := refLosslessAbs(predictAlpha-refLosslessByte(top, 3)) +
		refLosslessAbs(predictRed-refLosslessByte(top, 2)) +
		refLosslessAbs(predictGreen-refLosslessByte(top, 1)) +
		refLosslessAbs(predictBlue-refLosslessByte(top, 0))

	if predictLeft < predictTop {
		return left
	}
	return top
}

func refLosslessClamp(a int32) int32 {
	return min(max(a, 0), 255)
}

// refLosslessClampAddSubtractFull is clamp_add_subtract_full (lossless_transform.rs:370-380).
func refLosslessClampAddSubtractFull(a, b, c uint32) uint32 {
	var value uint32
	for i := range uint8(4) {
		subA := refLosslessByte(a, i)
		subB := refLosslessByte(b, i)
		subC := refLosslessByte(c, i)
		value |= uint32(refLosslessClamp(subA+subB-subC)) << (i * 8)
	}
	return value
}

// refLosslessClampAddSubtractHalf is clamp_add_subtract_half (lossless_transform.rs:383-392).
func refLosslessClampAddSubtractHalf(a, b uint32) uint32 {
	var value uint32
	for i := range uint8(4) {
		subA := refLosslessByte(a, i)
		subB := refLosslessByte(b, i)
		value |= uint32(refLosslessClamp(subA+(subA-subB)/2)) << (i * 8)
	}
	return value
}

// refLosslessColorTransformDelta is color_transform_delta (lossless_transform.rs:430-432): the product is an i16 sign extended to 32 bits
// before the logical shift.
func refLosslessColorTransformDelta(t, c int8) uint32 {
	return uint32(int32(int16(t)*int16(c))) >> 5
}

// refLosslessTransformColor is transform_color and color_transform (lossless_transform.rs:414-447) with the multiplier of colorCode
// (ColorTransformElement::from_color_code, :404-410).
func refLosslessTransformColor(colorCode, colorValue uint32) uint32 {
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
	tempRed += refLosslessColorTransformDelta(greenToRed, int8(green))
	tempBlue += refLosslessColorTransformDelta(greenToBlue, int8(green))
	tempBlue += refLosslessColorTransformDelta(redToBlue, int8(tempRed))

	return alpha<<24 + (tempRed&0xff)<<16 + green<<8 + tempBlue&0xff
}

// refLosslessAddGreen is add_green (lossless_transform.rs:452-461): green is added to red and blue.
func refLosslessAddGreen(argb uint32) uint32 {
	red := (argb >> 16) & 0xff
	green := (argb >> 8) & 0xff
	blue := argb & 0xff

	newRed := (red + green) & 0xff
	newBlue := (blue + green) & 0xff

	return (argb & 0xff00ff00) | (newRed << 16) | newBlue
}

// Lossy.

// fillSingle is Frame::fill_single (vp8.rs:928-947): the YUV to RGB conversion of the Microsoft recommendation.
func refVP8FillSingle(y, u, v uint8, rgb []byte) {
	c := int32(y) - 16
	d := int32(u) - 128
	e := int32(v) - 128

	rgb[0] = uint8(min(max((298*c+409*e+128)>>8, 0), 255))
	rgb[1] = uint8(min(max((298*c-100*d-208*e+128)>>8, 0), 255))
	rgb[2] = uint8(min(max((298*c+516*d+128)>>8, 0), 255))
}

// fill is Frame::fill_rgb and Frame::fill_rgba (vp8.rs:897-926) with stride 3 and 4: it writes the red, green and blue of each pixel
// of buf in raster order and leaves the rest of each stride-sized chunk alone (the alpha of an rgba buffer).
func refFill(f *vp8Frame, buf []byte, stride int) {
	width := int(f.width)
	chromaWidth := int(f.chromaWidth())
	chunks := min(len(f.ybuf), len(buf)/stride)
	for index := range chunks {
		y := index / width
		x := index % width
		chromaIndex := chromaWidth*(y/2) + x/2

		refVP8FillSingle(f.ybuf[index], f.ubuf[chromaIndex], f.vbuf[chromaIndex], buf[index*stride:])
	}
}

// readBool is BoolReader::read_bool (vp8.rs:762-793). A reader that has run out of bytes shifts in zeros.
func (b *refBoolReader) readBool(probability uint8) bool {
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

	for b.rng < 128 {
		b.value <<= 1
		b.rng <<= 1
		b.bitCount++

		if b.bitCount == 8 {
			b.bitCount = 0
			if b.index < len(b.buf) {
				b.value |= uint32(b.buf[b.index])
				b.index++
			}
		}
	}

	return retval
}

// readWithTree is BoolReader::read_with_tree (vp8.rs:818-832).
func (b *refBoolReader) readWithTree(tree []int8, probs []uint8, start int) int8 {
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

// readCoefficients is vp8.rs:1582-1658; it reports whether the block had any coefficient token other than an immediate end of block.
func refReadCoefficients(tokenProbs *[4][8][3][vp8NumDCTTokens - 1]uint8, br *refBoolReader, block []int32, plane, complexity int, dcq, acq int16) bool {
	first := 0
	if plane == 0 {
		first = 1
	}
	probs := &tokenProbs[plane]
	tree := vp8DCTTokenTree[:]

	hasCoefficients := false
	skip := false

	for i := first; i < 16; i++ {
		table := &probs[vp8CoeffBands[i]][complexity]

		var token int8
		if !skip {
			token = br.readWithTree(tree, table[:], 0)
		} else {
			token = br.readWithTree(tree, table[:], 2)
		}

		var absValue int32
		switch {
		case token == vp8DCTEOB:
			return hasCoefficients
		case token == vp8DCT0:
			skip = true
			hasCoefficients = true
			complexity = 0
			continue
		case token >= vp8DCT1 && token <= vp8DCT4:
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

		switch absValue {
		case 0:
			complexity = 0
		case 1:
			complexity = 1
		default:
			complexity = 2
		}

		if br.readBool(128) {
			absValue = -absValue
		}

		q := dcq
		if vp8Zigzag[i] > 0 {
			q = acq
		}
		block[vp8Zigzag[i]] = absValue * int32(q)

		hasCoefficients = true
	}

	return hasCoefficients
}

func refVP8C(val int32) int32 {
	return min(max(val, -128), 127)
}

// refVP8U2S is u2s, unsigned to signed.
func refVP8U2S(val uint8) int32 {
	return int32(val) - 128
}

// refVP8S2U is s2u, signed to unsigned.
func refVP8S2U(val int32) uint8 {
	return uint8(refVP8C(val) + 128)
}

func refVP8Diff(val1, val2 uint8) uint8 {
	if val1 > val2 {
		return val1 - val2
	}
	return val2 - val1
}

// refVP8CommonAdjust is common_adjust (loop_filter.rs:32-51, 15.2).
func refVP8CommonAdjust(useOuterTaps bool, pixels []byte, point, stride int) int32 {
	p1 := refVP8U2S(pixels[point-2*stride])
	p0 := refVP8U2S(pixels[point-stride])
	q0 := refVP8U2S(pixels[point])
	q1 := refVP8U2S(pixels[point+stride])

	// value for the outer 2 pixels
	var outer int32
	if useOuterTaps {
		outer = refVP8C(p1 - q1)
	}

	a := refVP8C(outer + 3*(q0-p0))

	b := refVP8C(a+3) >> 3

	a = refVP8C(a+4) >> 3

	pixels[point] = refVP8S2U(q0 - a)
	pixels[point-stride] = refVP8S2U(p0 + b)

	return a
}

func refVP8SimpleThreshold(filterLimit int32, pixels []byte, point, stride int) bool {
	return int32(refVP8Diff(pixels[point-stride], pixels[point]))*2+int32(refVP8Diff(pixels[point-2*stride], pixels[point+stride]))/2 <= filterLimit
}

func refVP8ShouldFilter(interiorLimit, edgeLimit uint8, pixels []byte, point, stride int) bool {
	return refVP8SimpleThreshold(int32(edgeLimit), pixels, point, stride) &&
		refVP8Diff(pixels[point-4*stride], pixels[point-3*stride]) <= interiorLimit &&
		refVP8Diff(pixels[point-3*stride], pixels[point-2*stride]) <= interiorLimit &&
		refVP8Diff(pixels[point-2*stride], pixels[point-stride]) <= interiorLimit &&
		refVP8Diff(pixels[point+3*stride], pixels[point+2*stride]) <= interiorLimit &&
		refVP8Diff(pixels[point+2*stride], pixels[point+stride]) <= interiorLimit &&
		refVP8Diff(pixels[point+stride], pixels[point]) <= interiorLimit
}

func refVP8HighEdgeVariance(threshold uint8, pixels []byte, point, stride int) bool {
	return refVP8Diff(pixels[point-2*stride], pixels[point-stride]) > threshold ||
		refVP8Diff(pixels[point+stride], pixels[point]) > threshold
}

// refVP8SimpleSegment is simple_segment (loop_filter.rs:80-86): the simple filter, which affects 4 pixels on an edge (2 each side).
func refVP8SimpleSegment(edgeLimit uint8, pixels []byte, point, stride int) {
	if refVP8SimpleThreshold(int32(edgeLimit), pixels, point, stride) {
		refVP8CommonAdjust(true, pixels, point, stride)
	}
}

// refVP8SubblockFilter is subblock_filter (loop_filter.rs:88-108): the normal filter on the edges between subblocks inside a macroblock.
func refVP8SubblockFilter(hevThreshold, interiorLimit, edgeLimit uint8, pixels []byte, point, stride int) {
	if refVP8ShouldFilter(interiorLimit, edgeLimit, pixels, point, stride) {
		hv := refVP8HighEdgeVariance(hevThreshold, pixels, point, stride)

		a := (refVP8CommonAdjust(hv, pixels, point, stride) + 1) >> 1

		if !hv {
			pixels[point+stride] = refVP8S2U(refVP8U2S(pixels[point+stride]) - a)
			pixels[point-2*stride] = refVP8S2U(refVP8U2S(pixels[point-2*stride]) - a)
		}
	}
}

// refVP8MacroblockFilter is macroblock_filter (loop_filter.rs:110-147): the normal filter on the edges between macroblocks.
func refVP8MacroblockFilter(hevThreshold, interiorLimit, edgeLimit uint8, pixels []byte, point, stride int) {
	var spixels [8]int32
	for i := range 8 {
		spixels[i] = refVP8U2S(pixels[point+i*stride-4*stride])
	}

	if refVP8ShouldFilter(interiorLimit, edgeLimit, pixels, point, stride) {
		if !refVP8HighEdgeVariance(hevThreshold, pixels, point, stride) {
			w := refVP8C(refVP8C(spixels[2]-spixels[5]) + 3*(spixels[4]-spixels[3]))

			a := refVP8C((27*w + 63) >> 7)

			pixels[point] = refVP8S2U(spixels[4] - a)
			pixels[point-stride] = refVP8S2U(spixels[3] + a)

			a = refVP8C((18*w + 63) >> 7)

			pixels[point+stride] = refVP8S2U(spixels[5] - a)
			pixels[point-2*stride] = refVP8S2U(spixels[2] + a)

			a = refVP8C((9*w + 63) >> 7)

			pixels[point+2*stride] = refVP8S2U(spixels[6] - a)
			pixels[point-3*stride] = refVP8S2U(spixels[1] + a)
		} else {
			refVP8CommonAdjust(true, pixels, point, stride)
		}
	}
}

// refVP8IDCT4x4 is idct4x4 (transform.rs:151-192); the intermediate results are int64 as in the Rust code.
func refVP8IDCT4x4(block []int32) {
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

// Alpha.

// refUnfilterAlpha is the first version of webpUnfilterAlpha, a pixel at a time: it writes the alpha channel of the RGBA image rgba: each alpha value is the sum, mod 256, of its value in data and the
// prediction of the filtering method from the pixels to the left and above, which are final already (extended.rs:438-458 and get_predictor,
// :466-526). The alpha of a pixel is at rgba[4*i+3].
func refUnfilterAlpha(method uint8, rgba, data []byte, width, height int) {
	alpha := func(i int) int { return int(rgba[i*4+3]) }

	for y := range height {
		row := y * width
		for x := range width {
			i := row + x
			var predictor int
			switch method {
			case webpFilterHorizontal:
				switch {
				case x == 0 && y == 0:
				case x == 0:
					predictor = alpha(i - width)
				default:
					predictor = alpha(i - 1)
				}
			case webpFilterVertical:
				switch {
				case x == 0 && y == 0:
				case y == 0:
					predictor = alpha(i - 1)
				default:
					predictor = alpha(i - width)
				}
			case webpFilterGradient:
				switch {
				case x == 0 && y == 0:
				case x == 0:
					predictor = alpha(i - width)
				case y == 0:
					predictor = alpha(i - 1)
				default:
					// the Rust code clamps left + top - top left to 0..255
					predictor = min(max(alpha(i-1)+alpha(i-width)-alpha(i-width-1), 0), 255)
				}
			}

			rgba[i*4+3] = byte(predictor + int(data[i]))
		}
	}
}
