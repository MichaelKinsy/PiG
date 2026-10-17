// Package images is the image processor of pi-durable: it prepares the images read returns so the model can take them.
//
// Ports packages/durable/src/images/index.ts.
package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"slices"

	// The decoders of the formats read accepts: PNG and JPEG, GIF and WebP, and BMP, which is converted.
	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"

	"github.com/MichaelKinsy/PiG/durable/tools"
)

// jpegQualities are the JPEG qualities tried in order.
var jpegQualities = []int{80, 70, 55, 40}

// photon prepares images with no ordering of its own; CreatePhotonImages puts it behind a sequential queue.
type photon struct{}

// CreatePhotonImages is the tools.ImageProcessor for tools.CreateCodingTools and tools.CreateReadTool.
//
// An image already upright, in a format providers take inline, and within the limits is returned as it is, once it decodes.
// Otherwise it is turned upright by its EXIF orientation, scaled to the maximum width and height, and encoded as PNG or JPEG
// at falling qualities (JPEG first for JPEG sources), then at smaller dimensions, 3/4 at a time, until the base64 fits.
// Formats providers do not take inline (BMP) become PNG.
//
// Decoding takes the image's RGBA size in memory. The work runs on the calling goroutine, one call at a time in call order
// as on Pi's one thread, and stops at the next step when its context is cancelled.
func CreatePhotonImages() tools.ImageProcessor { return newSequential(photon{}) }

// Prepare is the image prepared to fit limits, or nil when it cannot be decoded or made to fit.
func (photon) Prepare(ctx context.Context, data []byte, mimeType string, limits tools.ImageLimits) (*tools.PreparedImage, error) {
	orientation := ExifOrientation(data)
	inline := tools.INLINE_IMAGE_TYPES[mimeType]
	// pig divergence (D165): Go's image decoders stand for Photon's `new_from_byteslice`.
	// Decoded even when it is returned as it is: a damaged file is an error, not an image the provider rejects.
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || decoded.Bounds().Empty() {
		return nil, nil
	}
	if bounds := decoded.Bounds(); inline && orientation == 1 && bounds.Dx() <= limits.MaxWidth && bounds.Dy() <= limits.MaxHeight &&
		tools.Base64Length(int64(len(data))) <= int64(limits.MaxBytes) {
		return &tools.PreparedImage{Data: tools.ToBase64(data), MimeType: mimeType}, nil
	}
	upright := orient(toNRGBA(decoded), orientation)
	bounds := upright.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	scale := math.Min(1, math.Min(float64(limits.MaxWidth)/float64(width), float64(limits.MaxHeight)/float64(height)))
	target := tools.ImageSize{Width: max(1, jsRound(float64(width)*scale)), Height: max(1, jsRound(float64(height)*scale))}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		encoded, err := encodeWithin(ctx, upright, target, limits.MaxBytes, mimeType == "image/jpeg")
		if err != nil {
			return nil, err
		}
		if encoded != nil {
			if encoded.MimeType != mimeType {
				encoded.ConvertedFrom = mimeType
			}
			if target.Width != width || target.Height != height {
				encoded.Resized = &tools.ImageResize{From: tools.ImageSize{Width: width, Height: height}, To: target}
			}
			return encoded, nil
		}
		if target.Width == 1 && target.Height == 1 {
			return nil, nil
		}
		//portlint:allow numbers Math.floor of a positive finite size (index.ts:79-80): truncation is the floor and the size fits an int
		target = tools.ImageSize{Width: max(1, int(float64(target.Width)*0.75)), Height: max(1, int(float64(target.Height)*0.75))}
	}
}

// jsRound is Math.round: halves round up.
func jsRound(x float64) int { return int(math.Floor(x + 0.5)) } //portlint:allow numbers callers pass a finite image size (index.ts:66), so the floored value is an integer that fits an int

// encodeWithin is image at size in the first encoding whose base64 fits maxBytes: PNG, then JPEG at falling qualities, or the
// JPEG qualities first when jpegFirst; nil if none fits.
func encodeWithin(ctx context.Context, img *image.NRGBA, size tools.ImageSize, maxBytes int, jpegFirst bool) (*tools.PreparedImage, error) {
	scaled := img
	if bounds := img.Bounds(); size.Width != bounds.Dx() || size.Height != bounds.Dy() {
		scaled = resizeLanczos3(img, size.Width, size.Height)
	}
	type candidate struct {
		mimeType string
		encode   func() ([]byte, error)
	}
	pngCandidate := candidate{"image/png", func() ([]byte, error) { return encodePhotonPNG(scaled), nil }}
	var jpegs []candidate
	for _, quality := range jpegQualities {
		jpegs = append(jpegs, candidate{"image/jpeg", func() ([]byte, error) { return encodeJPEG(scaled, quality) }})
	}
	order := slices.Concat([]candidate{pngCandidate}, jpegs)
	if jpegFirst {
		order = slices.Concat(jpegs, []candidate{pngCandidate})
	}
	for _, c := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		encoded, err := c.encode()
		if err != nil {
			return nil, nil
		}
		if tools.Base64Length(int64(len(encoded))) <= int64(maxBytes) {
			return &tools.PreparedImage{Data: base64.StdEncoding.EncodeToString(encoded), MimeType: c.mimeType}, nil
		}
	}
	return nil, nil
}

// pig divergence (D165): Go's JPEG encoder, not Photon's `get_bytes_jpeg(quality)`; the quantization differs.
// encodeJPEG drops the alpha channel, as Photon's JPEG encoder does, rather than darkening transparent pixels.
func encodeJPEG(img *image.NRGBA, quality int) ([]byte, error) {
	opaque := image.NewNRGBA(img.Bounds())
	copy(opaque.Pix, img.Pix)
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 0xff
	}
	var out bytes.Buffer
	err := jpeg.Encode(&out, opaque, &jpeg.Options{Quality: quality})
	return out.Bytes(), err
}

func toNRGBA(img image.Image) *image.NRGBA {
	bounds := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA))
		}
	}
	return out
}

// orient is img turned upright for EXIF orientation: 2 mirrors it, 3 turns it half way, 4 flips it vertically, and 5 to 8
// swap its width and height.
func orient(img *image.NRGBA, orientation int) *image.NRGBA {
	if orientation == 1 {
		return img
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	outWidth, outHeight := width, height
	if orientation >= 5 {
		outWidth, outHeight = height, width
	}
	out := image.NewNRGBA(image.Rect(0, 0, outWidth, outHeight))
	for y := range height {
		for x := range width {
			var outX, outY int
			switch orientation {
			case 2:
				outX, outY = width-1-x, y
			case 3:
				outX, outY = width-1-x, height-1-y
			case 4:
				outX, outY = x, height-1-y
			default:
				// Where (x, y) lands in the upright image, which is height wide.
				outX, outY = y, x
				if orientation == 6 || orientation == 7 {
					outX = height - 1 - y
				}
				if orientation == 7 || orientation == 8 {
					outY = width - 1 - x
				}
			}
			copy(out.Pix[outY*out.Stride+outX*4:outY*out.Stride+outX*4+4], img.Pix[y*img.Stride+x*4:y*img.Stride+x*4+4])
		}
	}
	return out
}
