// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

// Ports the extended format (VP8X) of image 0.24.9 src/codecs/webp/extended.rs (cited below as extended.rs): the header, the chunks (a
// lossy image with an ALPH chunk, an animation), the alpha chunk and the drawing of the first frame of an animation on the canvas.

// webpExtendedInfo is WebPExtendedInfo (extended.rs:55-65); the flags the decoder does not use are not kept.
type webpExtendedInfo struct {
	alpha                     bool
	canvasWidth, canvasHeight uint32
}

// webpAnimation is what ExtendedImageData::Animation and its first AnimatedFrame (extended.rs:67-75, 584-594) give the pixels.
type webpAnimation struct {
	offsetX, offsetY uint32
	width, height    uint32
	useAlphaBlending bool
	dispose          bool
	canvasBackground [4]byte // the color the canvas starts with: transparent when the VP8X flags say there is alpha, else the ANIM background color
}

// readExtendedHeader is read_extended_header (extended.rs:614-662).
func readExtendedHeader(r *webpReader) (webpExtendedInfo, error) {
	b, err := r.readExact(1)
	if err != nil {
		return webpExtendedInfo{}, err
	}
	chunkFlags := b[0]

	reservedFirst := chunkFlags & 0b11000000
	alpha := chunkFlags&0b00010000 != 0
	reservedSecond := chunkFlags & 0b00000001

	reservedThird, err := r.read3Bytes()
	if err != nil {
		return webpExtendedInfo{}, err
	}

	if reservedFirst != 0 || reservedSecond != 0 || reservedThird != 0 {
		return webpExtendedInfo{}, errWebPInfoBits
	}

	canvasWidth, err := r.read3Bytes()
	if err != nil {
		return webpExtendedInfo{}, err
	}
	canvasHeight, err := r.read3Bytes()
	if err != nil {
		return webpExtendedInfo{}, err
	}
	canvasWidth++
	canvasHeight++

	// product of canvas dimensions cannot be larger than u32 max
	if uint64(canvasWidth)*uint64(canvasHeight) > 0xffffffff {
		return webpExtendedInfo{}, errWebPImageTooLarge
	}

	return webpExtendedInfo{alpha: alpha, canvasWidth: canvasWidth, canvasHeight: canvasHeight}, nil
}

// readExtendedChunk is read_extended_chunk (extended.rs:596-612): it reads a chunk, but silently ignores unknown chunks at the end of a
// file; an unknown chunk before a known one is an error.
func readExtendedChunk(r *webpReader) (cursor *webpReader, chunk webpChunk, ok bool, err error) {
	unknownChunk := false

	for {
		chunk, ok = r.readFourCC()
		if !ok {
			return nil, 0, false, nil
		}
		cursor, err = r.readLenCursor()
		if err != nil {
			return nil, 0, false, err
		}
		if chunk != webpChunkUnknown {
			if unknownChunk {
				return nil, 0, false, errWebPChunkHeader
			}
			return cursor, chunk, true, nil
		}
		unknownChunk = true
	}
}

// readExtendedChunks is ExtendedImage::read_extended_chunks (extended.rs:168-261).
func readExtendedChunks(r *webpReader, info webpExtendedInfo) (*webpImage, error) {
	var (
		animInfo       *[4]byte // the background color of the ANIM chunk
		animFirstFrame *webpAnimFrame
		staticFrame    *webpStatic
	)

	// go until end of file and while chunk headers are valid
	for {
		cursor, chunk, ok, err := readExtendedChunk(r)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}

		switch chunk {
		case webpChunkEXIF, webpChunkXMP:
			// ignore these chunks
		case webpChunkANIM:
			if animInfo == nil {
				bg, err := readAnimInfo(cursor)
				if err != nil {
					return nil, err
				}
				animInfo = &bg
			}
		case webpChunkANMF:
			// Store first frame decoded to avoid decoding it for certain function calls
			if animFirstFrame == nil {
				animFirstFrame, err = readAnimFrame(&webpReader{data: cursor.data}, info.canvasWidth, info.canvasHeight)
				if err != nil {
					return nil, err
				}
			}
		case webpChunkALPH:
			if staticFrame == nil {
				alphaChunk, err := readAlphaChunk(cursor, info.canvasWidth, info.canvasHeight)
				if err != nil {
					return nil, err
				}

				vp8Frame, err := readLossyWithChunk(r)
				if err != nil {
					return nil, err
				}

				staticFrame, err = webpStaticFromAlphaLossy(alphaChunk, vp8Frame)
				if err != nil {
					return nil, err
				}
			}
		case webpChunkICCP:
			// the profile is not used
		case webpChunkVP8:
			if staticFrame == nil {
				vp8Frame, err := decodeVP8Chunk(cursor)
				if err != nil {
					return nil, err
				}

				staticFrame = &webpStatic{kind: webpLossyWithoutAlpha, vp8: vp8Frame}
			}
		case webpChunkVP8L:
			if staticFrame == nil {
				staticFrame, err = decodeVP8LChunk(cursor, false)
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, errWebPChunkHeader
		}
	}

	img := &webpImage{canvasWidth: info.canvasWidth, canvasHeight: info.canvasHeight}
	switch {
	case animInfo != nil && animFirstFrame != nil:
		a := animFirstFrame
		img.s = a.image
		canvasBackground := *animInfo
		if info.alpha {
			canvasBackground = [4]byte{}
		}
		img.anim = &webpAnimation{
			offsetX: a.offsetX, offsetY: a.offsetY, width: a.width, height: a.height,
			useAlphaBlending: a.useAlphaBlending,
			dispose:          a.dispose,
			canvasBackground: canvasBackground,
		}
	case staticFrame != nil:
		img.s = staticFrame
	default:
		// reached end of file too early before image data was reached
		return nil, errWebPEOF
	}

	return img, nil
}

// readAnimInfo is read_anim_info (extended.rs:263-278); the background color is [blue, green, red, alpha] in the chunk.
func readAnimInfo(r *webpReader) ([4]byte, error) {
	colors, err := r.readExact(4)
	if err != nil {
		return [4]byte{}, err
	}
	if _, err := r.readExact(2); err != nil { // the loop count
		return [4]byte{}, err
	}

	return [4]byte{colors[2], colors[1], colors[0], colors[3]}, nil
}

// webpAnimFrame is AnimatedFrame (extended.rs:584-594).
type webpAnimFrame struct {
	offsetX, offsetY uint32
	width, height    uint32
	useAlphaBlending bool
	dispose          bool
	image            *webpStatic
}

// readAnimFrame is read_anim_frame (extended.rs:664-709).
func readAnimFrame(r *webpReader, canvasWidth, canvasHeight uint32) (*webpAnimFrame, error) {
	// offsets for the frames are twice the values
	frameX, err := r.read3Bytes()
	if err != nil {
		return nil, err
	}
	frameX *= 2
	frameY, err := r.read3Bytes()
	if err != nil {
		return nil, err
	}
	frameY *= 2

	frameWidth, err := r.read3Bytes()
	if err != nil {
		return nil, err
	}
	frameWidth++
	frameHeight, err := r.read3Bytes()
	if err != nil {
		return nil, err
	}
	frameHeight++

	if frameX+frameWidth > canvasWidth || frameY+frameHeight > canvasHeight {
		return nil, errWebPFrameOutside
	}

	if _, err := r.read3Bytes(); err != nil { // the duration
		return nil, err
	}

	b, err := r.readExact(1)
	if err != nil {
		return nil, err
	}
	frameInfo := b[0]
	if frameInfo&0b11111100 != 0 {
		return nil, errWebPInfoBits
	}
	useAlphaBlending := frameInfo&0b00000010 == 0
	dispose := frameInfo&0b00000001 != 0

	// read normal bitstream now
	staticImage, err := readWebPImage(r, frameWidth, frameHeight)
	if err != nil {
		return nil, err
	}

	return &webpAnimFrame{
		offsetX: frameX, offsetY: frameY,
		width: frameWidth, height: frameHeight,
		useAlphaBlending: useAlphaBlending,
		dispose:          dispose,
		image:            staticImage,
	}, nil
}

// readWebPImage is read_image (extended.rs:737-771).
func readWebPImage(r *webpReader, width, height uint32) (*webpStatic, error) {
	cursor, chunk, ok, err := r.readChunk()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errWebPEOF
	}

	switch chunk {
	case webpChunkVP8:
		frame, err := decodeVP8Chunk(cursor)
		if err != nil {
			return nil, err
		}
		return &webpStatic{kind: webpLossyWithoutAlpha, vp8: frame}, nil
	case webpChunkVP8L:
		return decodeVP8LChunk(cursor, true)
	case webpChunkALPH:
		alphaChunk, err := readAlphaChunk(cursor, width, height)
		if err != nil {
			return nil, err
		}

		vp8Frame, err := readLossyWithChunk(r)
		if err != nil {
			return nil, err
		}

		return webpStaticFromAlphaLossy(alphaChunk, vp8Frame)
	default:
		return nil, errWebPChunkHeader
	}
}

// readLossyWithChunk is read_lossy_with_chunk (extended.rs:719-728).
func readLossyWithChunk(r *webpReader) (*vp8Frame, error) {
	cursor, chunk, ok, err := r.readChunk()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errWebPEOF
	}

	if chunk != webpChunkVP8 {
		return nil, errWebPChunkHeader
	}

	return decodeVP8Chunk(cursor)
}

// Filtering methods of the alpha chunk (extended.rs:780-786).
const (
	webpFilterNone uint8 = iota
	webpFilterHorizontal
	webpFilterVertical
	webpFilterGradient
)

// webpAlphaChunk is AlphaChunk (extended.rs:773-778).
type webpAlphaChunk struct {
	filteringMethod uint8
	data            []byte
}

// readAlphaChunk is read_alpha_chunk (extended.rs:788-868).
func readAlphaChunk(r *webpReader, width, height uint32) (*webpAlphaChunk, error) {
	b, err := r.readExact(1)
	if err != nil {
		return nil, err
	}
	infoByte := b[0]

	reserved := infoByte & 0b11000000
	preprocessing := (infoByte & 0b00110000) >> 4
	filtering := (infoByte & 0b00001100) >> 2
	compression := infoByte & 0b00000011

	if reserved != 0 {
		return nil, errWebPInfoBits
	}

	if preprocessing > 1 {
		return nil, errWebPInfoBits
	}

	if compression > 1 {
		return nil, errWebPInfoBits
	}

	framedata := r.data
	r.data = nil

	data := framedata
	if compression == 1 {
		// this is a potential problem for large images; would require rewriting lossless decoder to use u32 for width and height
		if width > 0xffff || height > 0xffff {
			return nil, errWebPImageTooLarge
		}
		// the alpha of w x h pixels goes into an RGBA8 image of the same size, over the limit it is an error
		if uint64(width)*uint64(height)*4 > webpMaxAlloc {
			return nil, errWebPLimit
		}

		frame, err := newLosslessDecoder(framedata).decodeFrameImplicitDims(uint16(width), uint16(height))
		if err != nil {
			return nil, err
		}

		data = make([]byte, int(width)*int(height))
		frame.fillGreen(data)
	}

	return &webpAlphaChunk{filteringMethod: filtering, data: data}, nil
}

// webpStaticFromAlphaLossy is WebPStatic::from_alpha_lossy (extended.rs:422-464) with get_predictor (:466-526).
func webpStaticFromAlphaLossy(alpha *webpAlphaChunk, frame *vp8Frame) (*webpStatic, error) {
	width, height := int(frame.width), int(frame.height)
	if len(alpha.data) != width*height {
		return nil, errWebPAlphaSize
	}

	imageVec := make([]byte, width*height*4)

	frame.fill(imageVec, 4)

	at := func(x, y int) int { return int(imageVec[(y*width+x)*4+3]) }

	for y := range height {
		for x := range width {
			var predictor int
			switch alpha.filteringMethod {
			case webpFilterHorizontal:
				switch {
				case x == 0 && y == 0:
				case x == 0:
					predictor = at(x, y-1)
				default:
					predictor = at(x-1, y)
				}
			case webpFilterVertical:
				switch {
				case x == 0 && y == 0:
				case y == 0:
					predictor = at(x-1, y)
				default:
					predictor = at(x, y-1)
				}
			case webpFilterGradient:
				var left, top, topLeft int
				switch {
				case x == 0 && y == 0:
				case x == 0:
					left = at(x, y-1)
					top, topLeft = left, left
				case y == 0:
					left = at(x-1, y)
					top, topLeft = left, left
				default:
					left = at(x-1, y)
					top = at(x, y-1)
					topLeft = at(x-1, y-1)
				}
				predictor = min(max(left+top-topLeft, 0), 255)
			}

			alphaVal := int(alpha.data[y*width+x])
			imageVec[(y*width+x)*4+3] = byte((predictor + alphaVal) % 256)
		}
	}

	return &webpStatic{kind: webpLossyWithAlpha, rgba: imageVec}, nil
}

// drawOnCanvas is ExtendedImage::fill_buf for an animation whose first frame is not the canvas (extended.rs:369-382), which is
// draw_subimage (:280-331) on a canvas of the background color: dst is the canvas as RGBA8. It is an error (a copy_from_slice panic)
// when the frame has no alpha.
func (a *webpAnimation) drawOnCanvas(dst []byte, canvasWidth uint32, s *webpStatic) error {
	buffer := s.frameRGBA()
	if buffer == nil {
		return errWebPFrameSize
	}

	for i := 0; i < len(dst); i += 4 {
		copy(dst[i:i+4], a.canvasBackground[:])
	}

	// buffer[index..][..4] is out of bounds for the last pixels of a frame the image of which is smaller than the frame header says.
	if uint64(a.width)*uint64(a.height)*4 > uint64(len(buffer)) {
		return errWebPFrameSize
	}

	for x := range a.width {
		for y := range a.height {
			canvasIndex := (uint64(y+a.offsetY)*uint64(canvasWidth) + uint64(x+a.offsetX)) * 4
			index := (uint64(y)*uint64(a.width) + uint64(x)) * 4
			pixel := (*[4]byte)(buffer[index : index+4])
			if a.useAlphaBlending {
				blended := webpAlphaBlend(*pixel, [4]byte(dst[canvasIndex:canvasIndex+4]))
				copy(dst[canvasIndex:canvasIndex+4], blended[:])
			} else {
				copy(dst[canvasIndex:canvasIndex+4], pixel[:])
			}
		}
	}

	// draw_subimage clears the area of a frame that is to be disposed of to the background color after it has made the frame image, and
	// the canvas it was given is what fill_buf copies out (extended.rs:321-328).
	if a.dispose {
		for x := range a.width {
			for y := range a.height {
				canvasIndex := (uint64(y+a.offsetY)*uint64(canvasWidth) + uint64(x+a.offsetX)) * 4
				copy(dst[canvasIndex:canvasIndex+4], a.canvasBackground[:])
			}
		}
	}

	return nil
}

// webpFloatToByte is Rust's `as u8` on a float: it truncates and saturates.
func webpFloatToByte(v float64) byte {
	switch {
	case v != v || v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return byte(v)
	}
}

// webpAlphaBlend is do_alpha_blending (extended.rs:333-359). The products are converted to float64 on purpose: Go may fuse a product
// into the sum next to it, Rust does not.
func webpAlphaBlend(buffer, canvas [4]byte) [4]byte {
	canvasAlpha := float64(canvas[3])
	bufferAlpha := float64(buffer[3])
	blendAlphaF64 := bufferAlpha + float64(canvasAlpha*(1.0-bufferAlpha/255.0))
	// value should be between 0 and 255, this truncates the fractional part
	blendAlpha := webpFloatToByte(blendAlphaF64)

	var blendRGB [3]byte
	if blendAlpha != 0 {
		for i := range 3 {
			canvasF64 := float64(canvas[i])
			bufferF64 := float64(buffer[i])

			val := (float64(bufferF64*bufferAlpha) + float64(float64(canvasF64*canvasAlpha)*(1.0-bufferAlpha/255.0))) / blendAlphaF64
			blendRGB[i] = webpFloatToByte(val)
		}
	}

	return [4]byte{blendRGB[0], blendRGB[1], blendRGB[2], blendAlpha}
}
