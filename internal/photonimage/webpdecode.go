// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"image"
	"runtime"
)

// DecodeWebP decodes a WebP file to the RGBA8 pixels (not premultiplied) that Photon's `PhotonImage.new_from_byteslice(bytes)` followed by
// `get_raw_pixels()` returns, and fails exactly where Photon throws. Photon (photon-rs 0.3.3 lib.rs:93-105) calls image 0.24.9
// `load_from_memory(bytes).unwrap()` and then `to_rgba8()`; the format is guessed from the first bytes (a file that starts with "RIFF" is
// WebP, io/free_functions.rs:271), `WebPDecoder::new(..)` reads the file (codecs/webp/decoder.rs:131-137) and `DynamicImage::from_decoder`
// asks it for the pixels. This is that decoder, ported from image 0.24.9 (crates.io image-0.24.9.crate, the version Photon's wasm embeds):
//
//   - decoder.rs (cited below as decoder.rs): the RIFF container, the three kinds of image (VP8, VP8L, VP8X), the color types;
//   - extended.rs: VP8X, the alpha chunk (the ALPH chunk of a lossy image), animation (the first frame of an animated file is what
//     `load_from_memory` returns, composed onto the canvas when it does not fill it);
//   - webpdecode_vp8.go: the lossy decoder vp8.rs, loop_filter.rs, transform.rs;
//   - webpdecode_lossless.go: the lossless decoder lossless.rs, huffman.rs, lossless_transform.rs.
//
// The pixels of a lossy image are YUV 4:2:0 converted by the 8-bit recommendation of Microsoft with each chroma sample repeated over its 2x2
// block, they differ from what golang.org/x/image/webp (and libwebp) produce; the lossless decoder differs in the corner cases of
// malformed files. The limits are those of Photon: an image whose RGB or RGBA8 would take more than 512 MiB
// (image::Limits::default().max_alloc, io/mod.rs:63-72) is an error, and so is every panic of the Rust code (an index out of bounds, an
// unwrap): a Go runtime error of this package is returned as an error.
func DecodeWebP(data []byte) (img *image.NRGBA, err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(runtime.Error); !ok {
				panic(r)
			}
			// Index out of range and the like: a panic of the Rust decoder at the same place, which Photon throws as an exception.
			img, err = nil, errWebPPanic
		}
	}()

	return decodeWebP(data)
}

// webpError is a failure of DecodeWebP.
type webpError string

func (e webpError) Error() string {
	return "photonimage: WebP: " + string(e)
}

const (
	errWebPEOF                  = webpError("unexpected end of file")
	errWebPRiffSignature        = webpError("invalid RIFF signature")
	errWebPWebpSignature        = webpError("invalid WebP signature")
	errWebPChunkHeader          = webpError("invalid chunk header")
	errWebPInfoBits             = webpError("info bits invalid")
	errWebPAlphaSize            = webpError("alpha chunk doesn't match the size of the frame")
	errWebPImageTooLarge        = webpError("image is too large to be decoded")
	errWebPFrameOutside         = webpError("frame is too large and would go outside the image")
	errWebPLimit                = webpError("the image needs more memory than the 512 MiB limit")
	errWebPPanic                = webpError("the decoder panicked")
	errWebPVP8MagicInvalid      = webpError("invalid VP8 magic")
	errWebPVP8NotEnoughInitData = webpError("expected at least 2 bytes of VP8 decoder initialization data")
	errWebPVP8ColorSpaceInvalid = webpError("invalid non-YUV VP8 color space")
	errWebPVP8NonKeyframe       = webpError("non-keyframe frames are not supported")
	errWebPLosslessSignature    = webpError("invalid lossless signature")
	errWebPLosslessVersion      = webpError("invalid lossless version number")
	errWebPColorCacheBits       = webpError("invalid color cache bits")
	errWebPHuffman              = webpError("error building Huffman tree")
	errWebPBitStream            = webpError("error while reading bitstream")
	errWebPTransform            = webpError("error while reading or writing transforms")
	errWebPFrameSize            = webpError("frame size does not fit the canvas")
)

// webpMaxAlloc is image::Limits::default().max_alloc (io/mod.rs:63-72), the most bytes DynamicImage::from_decoder takes for the pixels.
const webpMaxAlloc = 512 * 1024 * 1024

// webpReader is the std::io::Read the Rust code reads a file or a chunk through.
type webpReader struct {
	data []byte
}

// readExact is Read::read_exact; the bytes are not copied.
func (r *webpReader) readExact(n int) ([]byte, error) {
	if n > len(r.data) {
		r.data = nil
		return nil, errWebPEOF
	}
	b := r.data[:n:n]
	r.data = r.data[n:]
	return b, nil
}

// readU32 is ReadBytesExt::read_u32::<LittleEndian>.
func (r *webpReader) readU32() (uint32, error) {
	b, err := r.readExact(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

// read3Bytes is read_3_bytes (extended.rs:711-717).
func (r *webpReader) read3Bytes() (uint32, error) {
	b, err := r.readExact(3)
	if err != nil {
		return 0, err
	}
	return uint32(b[2])<<16 | uint32(b[1])<<8 | uint32(b[0]), nil
}

// readLenCursor is read_len_cursor (decoder.rs:221-242): the data of a chunk, whose length is taken from the file. A chunk the file
// ends in is not an error, it is the data there is; RIFF chunks of an odd length are followed by a padding byte, which is dropped.
func (r *webpReader) readLenCursor() (*webpReader, error) {
	unpaddedLen, err := r.readU32()
	if err != nil {
		return nil, err
	}

	length := uint64(unpaddedLen) + uint64(unpaddedLen%2)
	n := int(min(length, uint64(len(r.data))))
	framedata := r.data[:n:n]
	r.data = r.data[n:]

	// remove padding byte
	if unpaddedLen%2 == 1 && len(framedata) > 0 {
		framedata = framedata[:len(framedata)-1]
	}

	return &webpReader{data: framedata}, nil
}

// webpChunk is WebPRiffChunk (decoder.rs:64-114).
type webpChunk uint8

const (
	webpChunkUnknown webpChunk = iota
	webpChunkRIFF
	webpChunkWEBP
	webpChunkVP8
	webpChunkVP8L
	webpChunkVP8X
	webpChunkANIM
	webpChunkANMF
	webpChunkALPH
	webpChunkICCP
	webpChunkEXIF
	webpChunkXMP
)

func webpChunkFromFourCC(fourcc []byte) webpChunk {
	switch string(fourcc) {
	case "RIFF":
		return webpChunkRIFF
	case "WEBP":
		return webpChunkWEBP
	case "VP8 ":
		return webpChunkVP8
	case "VP8L":
		return webpChunkVP8L
	case "VP8X":
		return webpChunkVP8X
	case "ANIM":
		return webpChunkANIM
	case "ANMF":
		return webpChunkANMF
	case "ALPH":
		return webpChunkALPH
	case "ICCP":
		return webpChunkICCP
	case "EXIF":
		return webpChunkEXIF
	case "XMP ":
		return webpChunkXMP
	default:
		return webpChunkUnknown
	}
}

// readFourCC is read_fourcc (decoder.rs:247-264): ok is false exactly when the end of the file is hit reading the four character code.
func (r *webpReader) readFourCC() (chunk webpChunk, ok bool) {
	b, err := r.readExact(4)
	if err != nil {
		return 0, false
	}
	return webpChunkFromFourCC(b), true
}

// readChunk is read_chunk (decoder.rs:269-280): a chunk with a known four character code; ok is false at the end of the file.
func (r *webpReader) readChunk() (cursor *webpReader, chunk webpChunk, ok bool, err error) {
	chunk, ok = r.readFourCC()
	if !ok {
		return nil, 0, false, nil
	}
	if chunk == webpChunkUnknown {
		return nil, 0, false, errWebPChunkHeader
	}
	cursor, err = r.readLenCursor()
	if err != nil {
		return nil, 0, false, err
	}
	return cursor, chunk, true, nil
}

// webpStatic is WebPStatic (extended.rs:414-419) and the frames of WebPImage::Lossy and ::Lossless (decoder.rs:116-120).
type webpStatic struct {
	kind uint8

	vp8      *vp8Frame      // webpLossyWithoutAlpha
	rgba     []byte         // webpLossyWithAlpha: the pixels of the frame, RGBA8
	lossless *losslessFrame // webpLossless
}

const (
	webpLossyWithoutAlpha uint8 = iota
	webpLossyWithAlpha
	webpLossless
)

// hasAlpha is WebPStatic::has_alpha (extended.rs:570-575); the color type is Rgba8 with alpha and Rgb8 without.
func (s *webpStatic) hasAlpha() bool {
	return s.kind != webpLossyWithoutAlpha
}

// bytesPerPixel is ColorType::bytes_per_pixel of WebPStatic::color_type.
func (s *webpStatic) bytesPerPixel() int {
	if s.hasAlpha() {
		return 4
	}
	return 3
}

// fillBuf is WebPStatic::fill_buf (extended.rs:540-552) and the fill_rgb / fill_rgba of the other two kinds of image, into the RGBA8
// buffer dst of canvasPixels pixels: dst has the size of the canvas, the image the size it has, and a lossy image of another size is a
// panic of copy_from_slice, a lossless image of another size fills as many pixels as there are in both.
func (s *webpStatic) fillBuf(dst []byte, canvasPixels int) error {
	switch s.kind {
	case webpLossyWithoutAlpha:
		if len(s.vp8.ybuf) != canvasPixels {
			return errWebPFrameSize
		}
		s.vp8.fill(dst, 4)
		for i := 3; i < len(dst); i += 4 {
			dst[i] = 255
		}
	case webpLossyWithAlpha:
		if len(s.rgba) != len(dst) {
			return errWebPFrameSize
		}
		copy(dst, s.rgba)
	default:
		s.lossless.fillRGBA(dst)
	}
	return nil
}

// frameRGBA is the RGBA8 buffer of the image as WebPStatic::get_buf_size and fill_buf give it to draw_subimage (extended.rs:285-286);
// it is nil for an image without alpha, which draw_subimage cannot hand to the copy that follows it.
func (s *webpStatic) frameRGBA() []byte {
	switch s.kind {
	case webpLossyWithAlpha:
		return s.rgba
	case webpLossless:
		buf := make([]byte, 4*int(s.lossless.width)*int(s.lossless.height))
		s.lossless.fillRGBA(buf)
		return buf
	default:
		return nil
	}
}

// decodeVP8Chunk is read_lossy (extended.rs:730-735).
func decodeVP8Chunk(cursor *webpReader) (*vp8Frame, error) {
	return newVP8Decoder(cursor.data).decodeFrame()
}

// decodeVP8LChunk is the LosslessDecoder part of read_image (extended.rs:749-756) and read_frame (decoder.rs:169-174).
func decodeVP8LChunk(cursor *webpReader, limitFrame bool) (*webpStatic, error) {
	decoder := newLosslessDecoder(cursor.data)
	decoder.limitFrame = limitFrame
	frame, err := decoder.decodeFrame()
	if err != nil {
		return nil, err
	}
	return &webpStatic{kind: webpLossless, lossless: frame}, nil
}

// decodeWebP is WebPDecoder::new, ImageDecoder::dimensions, ::color_type, ::read_image of decoder.rs, and DynamicImage::from_decoder
// followed by to_rgba8.
func decodeWebP(data []byte) (*image.NRGBA, error) {
	r := &webpReader{data: data}

	// read_riff_header (decoder.rs:140-156)
	riff, err := r.readExact(4)
	if err != nil {
		return nil, err
	}
	if string(riff) != "RIFF" {
		return nil, errWebPRiffSignature
	}
	if _, err := r.readU32(); err != nil {
		return nil, err
	}
	webp, err := r.readExact(4)
	if err != nil {
		return nil, err
	}
	if string(webp) != "WEBP" {
		return nil, errWebPWebpSignature
	}

	// read_frame (decoder.rs:159-187)
	cursor, chunk, ok, err := r.readChunk()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errWebPEOF
	}

	var img *webpImage

	switch chunk {
	case webpChunkVP8:
		frame, err := decodeVP8Chunk(cursor)
		if err != nil {
			return nil, err
		}
		img = &webpImage{s: &webpStatic{kind: webpLossyWithoutAlpha, vp8: frame}, canvasWidth: uint32(frame.width), canvasHeight: uint32(frame.height)}
	case webpChunkVP8L:
		s, err := decodeVP8LChunk(cursor, true)
		if err != nil {
			return nil, err
		}
		img = &webpImage{s: s, canvasWidth: uint32(s.lossless.width), canvasHeight: uint32(s.lossless.height)}
	case webpChunkVP8X:
		info, err := readExtendedHeader(cursor)
		if err != nil {
			return nil, err
		}
		img, err = readExtendedChunks(r, info)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errWebPChunkHeader
	}

	return img.pixels()
}

// webpImage is WebPImage (decoder.rs:116-120): the image of a file, with the dimensions ImageDecoder::dimensions gives it, which are the
// canvas of an extended image.
type webpImage struct {
	s *webpStatic // the image, or the image of the first frame of an animation

	canvasWidth, canvasHeight uint32

	anim *webpAnimation // not nil for an animation
}

// pixels is the end of decodeWebP: the limit check of load_inner (io/free_functions.rs:101-110) and the pixels of decoder_to_image
// (dynimage.rs:1043-1056) and to_rgba8, which is `read_image` (decoder.rs:342-357) for the color type the image has.
func (i *webpImage) pixels() (*image.NRGBA, error) {
	canvasPixels := uint64(i.canvasWidth) * uint64(i.canvasHeight)
	if canvasPixels*uint64(i.s.bytesPerPixel()) > webpMaxAlloc {
		return nil, errWebPLimit
	}

	img := image.NewNRGBA(image.Rect(0, 0, int(i.canvasWidth), int(i.canvasHeight)))

	if i.anim != nil && (i.canvasWidth != i.anim.width || i.canvasHeight != i.anim.height) {
		// ExtendedImage::fill_buf (extended.rs:361-388): the first frame drawn on the canvas.
		if err := i.anim.drawOnCanvas(img.Pix, i.canvasWidth, i.s); err != nil {
			return nil, err
		}
		return img, nil
	}

	if err := i.s.fillBuf(img.Pix, int(canvasPixels)); err != nil {
		return nil, err
	}
	return img, nil
}
