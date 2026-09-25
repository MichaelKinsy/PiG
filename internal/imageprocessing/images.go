// Image resize + re-encode policy for CLI/LLM image attachments, plus the
// EXIF orientation reader (upstream exif-orientation.ts). PNG conversion for
// Kitty (upstream image-convert.ts) lives in image_convert.go.
//
// Mirrors upstream packages/coding-agent/src/utils/image-resize.ts using the
// Go standard library plus golang.org/x/image in place of Photon:
//  1. Decode + apply EXIF orientation once.
//  2. If needed, resize to fit within 2000x2000.
//  3. Try PNG first, then JPEG quality steps until the base64 payload fits
//     under 4.5 MB.
//  4. If still too large, shrink dimensions by 25% and retry.
package imageprocessing

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // decoder for the GIF entry of the supported-format allowlist
	"image/jpeg"  // decoder for JPEG plus the JPEG re-encoder
	"image/png"   // decoder for PNG plus the PNG re-encoder
	"math"
	"os"
	"slices"
	"strings"

	_ "golang.org/x/image/bmp" // decoder for the BMP entry of the supported-format allowlist
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decoder for the WebP entry of the supported-format allowlist

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

const (
	MaxLongestSide  = 2000
	MaxEncodedBytes = int(4.5 * 1024 * 1024) // base64 payload bytes
	JPEGQuality     = 80
)

var ErrImageTooLarge = errors.New("image: could not be resized below inline image size limit")

// errPNGConversionFailed is upstream normalizeImage's null: the declared format
// is unsupported and convertImageBytesToPng could not re-encode it.
var errPNGConversionFailed = errors.New("image: could not convert to png")

type preparedImageResult struct {
	Data           []byte
	MIME           string
	OriginalWidth  int
	OriginalHeight int
	Width          int
	Height         int
	WasResized     bool
}

// NormalizeToolResultImages processes data images as they enter history. With
// auto-resize enabled, oversized images are resized once; disabled, unsupported
// still-image formats are still converted to a supported inline format (no
// resize): mirroring upstream processImage, whose normalizeImage step runs
// before the auto-resize branch. Decode/processing failures preserve the
// original block; conversion and resize hints are appended to the tool result.
func NormalizeToolResultImages(result agent.AgentToolResult, autoResize bool) agent.AgentToolResult {
	return NormalizeToolResultImagesWithOptions(result, autoResize, nil)
}

// NormalizeToolResultImagesWithOptions applies the active model profile after
// extension hooks. Failed image processing retains the original image block.
func NormalizeToolResultImagesWithOptions(result agent.AgentToolResult, autoResize bool, options *ai.ModelImageResizeOptions) agent.AgentToolResult {
	if len(result.Images) == 0 {
		return result
	}
	images := make([]ai.ImageContent, 0, len(result.Images))
	var hints []string
	changed := false
	for _, image := range result.Images {
		if image.Data == "" {
			images = append(images, image)
			continue
		}
		decoded := DecodeNodeBase64(image.Data)
		processed, mime, hint, err := ProcessImage(decoded, image.MimeType, autoResize, options)
		if err != nil {
			images = append(images, image)
			continue
		}
		normalized := image
		normalized.Data = base64.StdEncoding.EncodeToString(processed)
		normalized.MimeType = mime
		images = append(images, normalized)
		if normalized.Data != image.Data || normalized.MimeType != image.MimeType {
			changed = true
		}
		if hint != "" {
			hints = append(hints, hint)
			changed = true
		}
	}
	if !changed {
		return result
	}
	result.Images = images
	if len(hints) > 0 {
		if result.Content != "" {
			result.Content += "\n"
		}
		result.Content += strings.Join(hints, "\n")
	}
	return result
}

// convertToolResultImageOnly mirrors upstream processImage with
// autoResizeImages:false: normalizeImage still runs, so a supported declared
// format passes through with its canonical MIME while an unsupported one is
// re-encoded to PNG and reports the conversion: but no resize is applied, even
// when the image exceeds the inline size limit.
func convertToolResultImageOnly(decoded []byte, declaredMIME string) ([]byte, string, string, error) {
	if norm := normalizeSupportedImageMIME(declaredMIME); norm != "" {
		return decoded, norm, "", nil
	}
	png := ConvertImageBytesToPng(decoded)
	if png == nil {
		return nil, "", "", errPNGConversionFailed
	}
	return png, "image/png", imageConversionHint(declaredMIME, "image/png"), nil
}

// ResizeImageForLLM applies the upstream CLI image policy and returns the
// processed bytes plus the resulting MIME type.
func ResizeImageForLLM(in []byte) ([]byte, string, error) {
	result, err := prepareImageForLLM(in, nil)
	if err != nil {
		return nil, "", err
	}
	return result.Data, result.MIME, nil
}

// PrepareCLIImageAttachment processes one CLI image file into the attached
// image payload plus the upstream dimension note (empty when not resized).
func PrepareCLIImageAttachment(in []byte, inputMIME string) ([]byte, string, string, error) {
	return ProcessImage(in, inputMIME, true, nil)
}

// ProcessImage normalizes unsupported formats before applying a model's resize
// profile. A false autoResize flag still converts unsupported formats. Errors
// carry upstream's omission hint; callers decide whether to keep a tool image.
func ProcessImage(in []byte, inputMIME string, autoResize bool, options *ai.ModelImageResizeOptions) ([]byte, string, string, error) {
	normalized, mime, conversionHint, err := convertToolResultImageOnly(in, inputMIME)
	if err != nil {
		return nil, "", "", errors.New("[Image omitted: could not be converted to a supported inline image format.]")
	}
	if !autoResize {
		return normalized, mime, conversionHint, nil
	}
	result, err := prepareImageForLLM(normalized, options)
	if err != nil {
		return nil, "", "", errors.New("[Image omitted: could not be resized below the inline image size limit.]")
	}
	if !result.WasResized {
		result.MIME = mime
	}
	return result.Data, result.MIME, joinImageHints(imageConversionHint(inputMIME, result.MIME), formatDimensionNote(result)), nil
}

// imageConversionHint mirrors upstream image-process.ts conversionHint: it
// reports a format conversion only when the declared input MIME was an
// unsupported still-image format that had to be re-encoded to a supported one.
// A supported input that is merely re-encoded while resizing does not report a
// conversion, matching processImage's convertedFrom, which is set solely by the
// normalizeImage step.
func imageConversionHint(inputMIME, outputMIME string) string {
	from := baseImageMIME(inputMIME)
	to := baseImageMIME(outputMIME)
	if from == "" || from == to || isUpstreamSupportedImageMIME(from) {
		return ""
	}
	return fmt.Sprintf("[Image converted from %s to %s.]", from, to)
}

// baseImageMIME strips any parameters and normalizes case, mirroring upstream
// baseMimeType.
func baseImageMIME(mime string) string {
	base, _, _ := strings.Cut(mime, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

// normalizeSupportedImageMIME mirrors upstream normalizeSupportedImageMimeType:
// it returns the canonical MIME for a supported still-image format that
// processImage passes through without a conversion, or "" for an unsupported
// declared type.
func normalizeSupportedImageMIME(mime string) string {
	switch baseImageMIME(mime) {
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/gif":
		return "image/gif"
	case "image/webp":
		return "image/webp"
	}
	return ""
}

// isUpstreamSupportedImageMIME reports whether processImage passes the declared
// format through without a conversion.
func isUpstreamSupportedImageMIME(mime string) bool {
	return normalizeSupportedImageMIME(mime) != ""
}

// joinImageHints concatenates non-empty hints with newlines in upstream order
// (conversion hint before dimension note), matching processImage's hints array.
func joinImageHints(hints ...string) string {
	out := make([]string, 0, len(hints))
	for _, h := range hints {
		if h != "" {
			out = append(out, h)
		}
	}
	return strings.Join(out, "\n")
}

// DetectSupportedImageMimeType mirrors upstream mime.ts and returns a supported
// still-image MIME type for the provided bytes, or "" when unsupported.
//
// The formats named here are also the only decoders this package links, so a
// format outside the allowlist has no decoder to reach. Adding a MIME type here
// or to tools.SupportedImageMime without adding its decoder import decodes
// nothing; adding the decoder import widens the attack surface of the image
// pipeline to that format's parser.
func DetectSupportedImageMimeType(buffer []byte) string {
	if startsWithBytes(buffer, []byte{0xff, 0xd8, 0xff}) {
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if startsWithBytes(buffer, pngSignature) {
		if isPNG(buffer) && !isAnimatedPNG(buffer) {
			return "image/png"
		}
		return ""
	}
	if startsWithASCII(buffer, 0, "GIF87a") || startsWithASCII(buffer, 0, "GIF89a") {
		return "image/gif"
	}
	if startsWithASCII(buffer, 0, "RIFF") && startsWithASCII(buffer, 8, "WEBP") {
		return "image/webp"
	}
	if startsWithASCII(buffer, 0, "BM") && isBMP(buffer) {
		return "image/bmp"
	}
	return ""
}

// isBMP mirrors upstream mime.ts isBmp: the header sizes, color planes and
// bits per pixel must be consistent, so a text file starting with "BM" is
// not taken for an image.
func isBMP(buffer []byte) bool {
	if len(buffer) < 26 {
		return false
	}
	declaredFileSize := readUint32LE(buffer, 2)
	pixelDataOffset := readUint32LE(buffer, 10)
	dibHeaderSize := readUint32LE(buffer, 14)
	if declaredFileSize != 0 && declaredFileSize < 26 {
		return false
	}
	if pixelDataOffset < 14+dibHeaderSize {
		return false
	}
	if declaredFileSize != 0 && pixelDataOffset >= declaredFileSize {
		return false
	}
	var colorPlanes, bitsPerPixel int
	switch {
	case dibHeaderSize == 12:
		colorPlanes, bitsPerPixel = readUint16LE(buffer, 22), readUint16LE(buffer, 24)
	case dibHeaderSize >= 40 && dibHeaderSize <= 124:
		if len(buffer) < 30 {
			return false
		}
		colorPlanes, bitsPerPixel = readUint16LE(buffer, 26), readUint16LE(buffer, 28)
	default:
		return false
	}
	return colorPlanes == 1 && slices.Contains([]int{1, 4, 8, 16, 24, 32}, bitsPerPixel)
}

func readUint16LE(buffer []byte, offset int) int {
	return int(buffer[offset]) | int(buffer[offset+1])<<8
}

func readUint32LE(buffer []byte, offset int) int {
	return int(buffer[offset]) | int(buffer[offset+1])<<8 | int(buffer[offset+2])<<16 | int(buffer[offset+3])*0x1000000
}

// ImageTypeSniffBytes mirrors upstream IMAGE_TYPE_SNIFF_BYTES: detection
// looks only at a file's first 4100 bytes.
const ImageTypeSniffBytes = 4100

// DetectSupportedImageMimeTypeFromFile reads the upstream sniff window from a
// file and returns the supported still-image MIME type, or "" when unsupported.
func DetectSupportedImageMimeTypeFromFile(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, ImageTypeSniffBytes)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return ""
	}
	return DetectSupportedImageMimeType(buf[:n])
}

func prepareImageForLLM(in []byte, options *ai.ModelImageResizeOptions) (preparedImageResult, error) {
	limits := ai.ModelImageResizeOptions{MaxWidth: MaxLongestSide, MaxHeight: MaxLongestSide, MaxBytes: MaxEncodedBytes, JPEGQuality: JPEGQuality}
	if options != nil {
		if options.MaxWidth != 0 {
			limits.MaxWidth = options.MaxWidth
		}
		if options.MaxHeight != 0 {
			limits.MaxHeight = options.MaxHeight
		}
		if options.MaxBytes != 0 {
			limits.MaxBytes = options.MaxBytes
		}
		if options.JPEGQuality != 0 {
			limits.JPEGQuality = options.JPEGQuality
		}
	}
	if len(in) == 0 {
		return preparedImageResult{}, errors.New("image: empty input")
	}

	src, err := decodeAutoOriented(in)
	if err != nil {
		return preparedImageResult{}, fmt.Errorf("image: decode: %w", err)
	}
	bounds := src.Bounds()
	originalWidth, originalHeight := bounds.Dx(), bounds.Dy()
	srcFormat := detectFormat(in)
	srcMime := mimeForFormat(srcFormat)
	inputBase64Size := encodedSizeBase64(in)

	if originalWidth <= limits.MaxWidth && originalHeight <= limits.MaxHeight && inputBase64Size < limits.MaxBytes && srcFormat != "bmp" {
		return preparedImageResult{
			Data:           in,
			MIME:           srcMime,
			OriginalWidth:  originalWidth,
			OriginalHeight: originalHeight,
			Width:          originalWidth,
			Height:         originalHeight,
			WasResized:     false,
		}, nil
	}

	currentWidth, currentHeight := fitWithinBounds(originalWidth, originalHeight, limits.MaxWidth, limits.MaxHeight)
	qualitySteps := []int{limits.JPEGQuality}
	for _, quality := range []int{85, 70, 55, 40} {
		if quality != limits.JPEGQuality {
			qualitySteps = append(qualitySteps, quality)
		}
	}

	for {
		resized := resizeImage(src, currentWidth, currentHeight)
		candidates, err := encodeCandidates(resized, qualitySteps)
		if err != nil {
			return preparedImageResult{}, err
		}
		for _, candidate := range candidates {
			if candidate.EncodedSize < limits.MaxBytes {
				return preparedImageResult{
					Data:           candidate.Data,
					MIME:           candidate.MIME,
					OriginalWidth:  originalWidth,
					OriginalHeight: originalHeight,
					Width:          currentWidth,
					Height:         currentHeight,
					WasResized:     true,
				}, nil
			}
		}
		if currentWidth == 1 && currentHeight == 1 {
			break
		}
		nextWidth := currentWidth
		if nextWidth > 1 {
			nextWidth = max(1, int(float64(currentWidth)*0.75))
		}
		nextHeight := currentHeight
		if nextHeight > 1 {
			nextHeight = max(1, int(float64(currentHeight)*0.75))
		}
		if nextWidth == currentWidth && nextHeight == currentHeight {
			break
		}
		currentWidth, currentHeight = nextWidth, nextHeight
	}

	return preparedImageResult{}, ErrImageTooLarge
}

// exifOrientation is the EXIF orientation tag value, 1..8. Upstream
// getExifOrientation reports 1 (normal) whenever the tag is absent, unreadable,
// or out of range.
type exifOrientation int

const (
	orientationNormal     exifOrientation = 1
	orientationFlipH      exifOrientation = 2
	orientationRotate180  exifOrientation = 3
	orientationFlipV      exifOrientation = 4
	orientationTranspose  exifOrientation = 5
	orientationRotate270  exifOrientation = 6
	orientationTransverse exifOrientation = 7
	orientationRotate90   exifOrientation = 8
)

// decodeAutoOriented decodes an image and applies its EXIF orientation tag,
// mirroring upstream Photon decode plus applyExifOrientation. Only the formats
// whose decoders this package links are decodable; see
// DetectSupportedImageMimeType.
func decodeAutoOriented(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return fixOrientation(img, getExifOrientation(data)), nil
}

// getExifOrientation mirrors upstream exif-orientation.ts getExifOrientation:
// it locates the TIFF block of a JPEG APP1 "Exif" segment or a WebP EXIF chunk
// and reads IFD0's orientation tag. Every failure yields orientationNormal.
func getExifOrientation(data []byte) exifOrientation {
	tiffOffset := -1
	switch {
	case len(data) >= 2 && data[0] == 0xff && data[1] == 0xd8:
		tiffOffset = findJpegTiffOffset(data)
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		tiffOffset = findWebpTiffOffset(data)
	}
	if tiffOffset == -1 {
		return orientationNormal
	}
	return readOrientationFromTiff(data, tiffOffset)
}

// findJpegTiffOffset mirrors upstream: it walks JPEG marker segments, skipping
// 0xff fill bytes and any APP1 segment that is not "Exif\0\0" (for example
// XMP), and returns the TIFF header offset or -1.
func findJpegTiffOffset(data []byte) int {
	offset := 2
	for offset < len(data)-1 {
		if data[offset] != 0xff {
			return -1
		}
		marker := data[offset+1]
		if marker == 0xff {
			offset++
			continue
		}
		if marker == 0xe1 {
			if offset+4 >= len(data) {
				return -1
			}
			segmentStart := offset + 4
			if segmentStart+6 > len(data) {
				return -1
			}
			if hasExifHeader(data, segmentStart) {
				return segmentStart + 6
			}
		}
		if offset+4 > len(data) {
			return -1
		}
		length := int(data[offset+2])<<8 | int(data[offset+3])
		offset += 2 + length
	}
	return -1
}

// findWebpTiffOffset mirrors upstream: it walks RIFF chunks after the WEBP
// header and returns the TIFF offset inside the EXIF chunk, skipping an
// optional "Exif\0\0" prefix, or -1.
func findWebpTiffOffset(data []byte) int {
	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		// Upstream composes the little-endian size with 32-bit signed shifts.
		chunkSize := int(int32(binary.LittleEndian.Uint32(data[offset+4 : offset+8])))
		dataStart := offset + 8
		if chunkID == "EXIF" {
			if dataStart+chunkSize > len(data) {
				return -1
			}
			if chunkSize >= 6 && hasExifHeader(data, dataStart) {
				return dataStart + 6
			}
			return dataStart
		}
		// RIFF chunks are padded to even size.
		next := dataStart + chunkSize + chunkSize%2
		if next <= offset {
			// pig: a negative size would revisit this chunk forever upstream;
			// stop with upstream's not-found result instead.
			return -1
		}
		offset = next
	}
	return -1
}

func hasExifHeader(data []byte, offset int) bool {
	return offset >= 0 && offset+6 <= len(data) && string(data[offset:offset+6]) == "Exif\x00\x00"
}

// readOrientationFromTiff mirrors upstream: byte order "II" is little-endian,
// anything else big-endian; IFD0 entries are 12 bytes and the SHORT value sits
// at entry offset 8. Out-of-range values read as normal.
func readOrientationFromTiff(data []byte, tiffStart int) exifOrientation {
	if tiffStart+8 > len(data) {
		return orientationNormal
	}
	var order binary.ByteOrder = binary.BigEndian
	if data[tiffStart] == 0x49 && data[tiffStart+1] == 0x49 {
		order = binary.LittleEndian
	}
	ifdOffset := int64(order.Uint32(data[tiffStart+4 : tiffStart+8]))
	if order == binary.LittleEndian {
		// Upstream's little-endian read32 yields a signed 32-bit value.
		ifdOffset = int64(int32(ifdOffset))
	}
	ifdStart := int64(tiffStart) + ifdOffset
	if ifdStart+2 > int64(len(data)) {
		return orientationNormal
	}
	if ifdStart < 0 {
		// Upstream reads undefined bytes as a zero entry count.
		return orientationNormal
	}
	entryCount := int64(order.Uint16(data[ifdStart : ifdStart+2]))
	for i := range entryCount {
		entryPos := ifdStart + 2 + i*12
		if entryPos+12 > int64(len(data)) {
			return orientationNormal
		}
		if order.Uint16(data[entryPos:entryPos+2]) == 0x0112 {
			value := order.Uint16(data[entryPos+8 : entryPos+10])
			if value >= 1 && value <= 8 {
				return exifOrientation(value)
			}
			return orientationNormal
		}
	}
	return orientationNormal
}

// fixOrientation applies the transform that makes an EXIF-tagged image upright.
// Rotations are counter-clockwise, matching the EXIF tag definitions.
func fixOrientation(img image.Image, o exifOrientation) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	switch o {
	case orientationFlipH:
		return remapPixels(img, w, h, func(x, y int) (int, int) { return w - 1 - x, y })
	case orientationFlipV:
		return remapPixels(img, w, h, func(x, y int) (int, int) { return x, h - 1 - y })
	case orientationRotate180:
		return remapPixels(img, w, h, func(x, y int) (int, int) { return w - 1 - x, h - 1 - y })
	case orientationTranspose:
		return remapPixels(img, h, w, func(x, y int) (int, int) { return y, x })
	case orientationTransverse:
		return remapPixels(img, h, w, func(x, y int) (int, int) { return w - 1 - y, h - 1 - x })
	case orientationRotate90:
		return remapPixels(img, h, w, func(x, y int) (int, int) { return w - 1 - y, x })
	case orientationRotate270:
		return remapPixels(img, h, w, func(x, y int) (int, int) { return y, h - 1 - x })
	case orientationNormal:
		return img
	}
	return img
}

// remapPixels builds a dstW×dstH NRGBA image whose pixel (x,y) is the source
// pixel named by src, expressed in source coordinates relative to the source
// bounds origin. NRGBA output keeps alpha non-premultiplied so imageHasAlpha
// reads the same channel the encoders write.
func remapPixels(img image.Image, dstW, dstH int, src func(x, y int) (int, int)) *image.NRGBA {
	srcNRGBA := toNRGBA(img)
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for y := range dstH {
		for x := range dstW {
			sx, sy := src(x, y)
			s := srcNRGBA.PixOffset(sx, sy)
			d := dst.PixOffset(x, y)
			copy(dst.Pix[d:d+4], srcNRGBA.Pix[s:s+4])
		}
	}
	return dst
}

// toNRGBA returns img as an origin-anchored NRGBA image, reusing it when it is
// already one.
func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok && n.Bounds().Min == (image.Point{}) {
		return n
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out
}

// resizeImage scales img to width×height with the CatmullRom kernel, the
// highest-quality resampler in golang.org/x/image/draw.
func resizeImage(img image.Image, width, height int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	return dst
}

type encodedCandidate struct {
	Data        []byte
	EncodedSize int
	MIME        string
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

func imageHasAlpha(img image.Image) bool {
	switch m := img.(type) {
	case *image.RGBA:
		for i := 3; i < len(m.Pix); i += 4 {
			if m.Pix[i] != 0xff {
				return true
			}
		}
		return false
	case *image.NRGBA:
		for i := 3; i < len(m.Pix); i += 4 {
			if m.Pix[i] != 0xff {
				return true
			}
		}
		return false
	case *image.RGBA64, *image.NRGBA64:
		return true
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 16 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 16 {
			_, _, _, a := img.At(x, y).RGBA()
			if a < 0xffff {
				return true
			}
		}
	}
	return false
}

func encodeCandidates(img image.Image, jpegQualities []int) ([]encodedCandidate, error) {
	pngBytes, err := encodePNG(img)
	if err != nil {
		return nil, fmt.Errorf("image: encode png: %w", err)
	}
	candidates := []encodedCandidate{{
		Data:        pngBytes,
		EncodedSize: encodedSizeBase64(pngBytes),
		MIME:        "image/png",
	}}
	for _, quality := range jpegQualities {
		jpegBytes, err := encodeJPEG(img, quality)
		if err != nil {
			continue
		}
		candidates = append(candidates, encodedCandidate{
			Data:        jpegBytes,
			EncodedSize: encodedSizeBase64(jpegBytes),
			MIME:        "image/jpeg",
		})
	}
	return candidates, nil
}

func fitWithinBounds(width, height, maxWidth, maxHeight int) (int, int) {
	targetWidth, targetHeight := width, height
	if targetWidth > maxWidth {
		targetHeight = int(math.Round(float64(targetHeight) * float64(maxWidth) / float64(targetWidth)))
		targetWidth = maxWidth
	}
	if targetHeight > maxHeight {
		targetWidth = int(math.Round(float64(targetWidth) * float64(maxHeight) / float64(targetHeight)))
		targetHeight = maxHeight
	}
	return max(1, targetWidth), max(1, targetHeight)
}

func formatDimensionNote(result preparedImageResult) string {
	if !result.WasResized {
		return ""
	}
	scale := float64(result.OriginalWidth) / float64(result.Width)
	return fmt.Sprintf("[Image: original %dx%d, displayed at %dx%d. Multiply coordinates by %.2f to map to original image.]",
		result.OriginalWidth, result.OriginalHeight, result.Width, result.Height, scale)
}

func encodedSizeBase64(data []byte) int {
	return base64.StdEncoding.EncodedLen(len(data))
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// detectFormat sniffs the source format from magic bytes. Returns
// "png" / "jpeg" / "webp" / "gif" / "bmp" / "" (unknown).
func detectFormat(b []byte) string {
	if startsWithBytes(b, pngSignature) {
		return "png"
	}
	if startsWithBytes(b, []byte{0xff, 0xd8, 0xff}) {
		return "jpeg"
	}
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		return "webp"
	}
	if startsWithASCII(b, 0, "GIF87a") || startsWithASCII(b, 0, "GIF89a") {
		return "gif"
	}
	if startsWithBytes(b, []byte{'B', 'M'}) {
		return "bmp"
	}
	return ""
}

func mimeForFormat(f string) string {
	switch f {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "bmp":
		return "image/bmp"
	}
	return ""
}

func isPNG(buffer []byte) bool {
	return len(buffer) >= 16 && readUint32BE(buffer, len(pngSignature)) == 13 && startsWithASCII(buffer, 12, "IHDR")
}

func isAnimatedPNG(buffer []byte) bool {
	offset := len(pngSignature)
	for offset+8 <= len(buffer) {
		chunkLength := readUint32BE(buffer, offset)
		chunkTypeOffset := offset + 4
		if startsWithASCII(buffer, chunkTypeOffset, "acTL") {
			return true
		}
		if startsWithASCII(buffer, chunkTypeOffset, "IDAT") {
			return false
		}
		nextOffset := offset + 8 + chunkLength + 4
		if nextOffset <= offset || nextOffset > len(buffer) {
			return false
		}
		offset = nextOffset
	}
	return false
}

func readUint32BE(buffer []byte, offset int) int {
	return int(buffer[offset])*0x1000000 + int(buffer[offset+1])<<16 + int(buffer[offset+2])<<8 + int(buffer[offset+3])
}

func startsWithBytes(buffer, prefix []byte) bool {
	return len(buffer) >= len(prefix) && bytes.Equal(buffer[:len(prefix)], prefix)
}

func startsWithASCII(buffer []byte, offset int, text string) bool {
	if len(buffer) < offset+len(text) {
		return false
	}
	for i := range len(text) {
		if buffer[offset+i] != text[i] {
			return false
		}
	}
	return true
}
