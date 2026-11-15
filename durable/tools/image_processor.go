package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/durable/src/tools/image-processor.ts.

// ImageLimits are the limits an image the model sees must keep; the defaults below are the coding agent's.
type ImageLimits struct {
	// MaxWidth is pixels. Default 2000.
	MaxWidth int
	// MaxHeight is pixels. Default 2000.
	MaxHeight int
	// MaxBytes is bytes of base64. Default 4.5 MiB, below Anthropic's 5 MB.
	MaxBytes int
}

// DEFAULT_IMAGE_LIMITS are the limits the coding agent keeps.
var DEFAULT_IMAGE_LIMITS = ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 4.5 * 1024 * 1024}

// ImageLimitsOf is imageLimits(model): the limits of images sent to model, its inputLimits.images.resize with each absent
// one DEFAULT_IMAGE_LIMITS's. The name is forced: ImageLimits is the type here, so Pi's function takes Of.
func ImageLimitsOf(model *ai.Model) ImageLimits {
	limits := DEFAULT_IMAGE_LIMITS
	if model == nil || model.InputLimits == nil || model.InputLimits.Images == nil || model.InputLimits.Images.Resize == nil {
		return limits
	}
	resize := model.InputLimits.Images.Resize
	if resize.MaxWidth != 0 {
		limits.MaxWidth = resize.MaxWidth
	}
	if resize.MaxHeight != 0 {
		limits.MaxHeight = resize.MaxHeight
	}
	if resize.MaxBytes != 0 {
		limits.MaxBytes = resize.MaxBytes
	}
	return limits
}

// ImageSize is a size in pixels.
type ImageSize struct {
	Width, Height int
}

// ImageResize is how a prepared image was resized: its size before and after, for the model to map coordinates back.
type ImageResize struct {
	From, To ImageSize
}

// PreparedImage is an image the model can take: a supported format, within the limits, as base64.
type PreparedImage struct {
	Data     string
	MimeType string
	// Resized is set when the image was resized.
	Resized *ImageResize
	// ConvertedFrom is the original format, set when it differs from MimeType: BMP becomes PNG, a large PNG may become JPEG.
	ConvertedFrom string
}

// ImageProcessor decodes, orients, resizes, and re-encodes images so the model can take them. read uses one when given;
// without one, it passes supported images within the byte limit through as they are. durable/images provides one.
type ImageProcessor interface {
	// Prepare returns the image, prepared to fit limits, or nil when it cannot be decoded or made to fit. An error is a
	// failed or cancelled preparation, not an image that cannot be prepared.
	Prepare(ctx context.Context, bytes []byte, mimeType string, limits ImageLimits) (*PreparedImage, error)
}

// PreparedNotes is what preparing changed, for the model: the format, and the size with the factor that maps coordinates back.
func PreparedNotes(image *PreparedImage) []string {
	notes := []string{}
	if image.ConvertedFrom != "" {
		notes = append(notes, fmt.Sprintf("Converted from %s to %s.", image.ConvertedFrom, image.MimeType))
	}
	if image.Resized != nil {
		from, to := image.Resized.From, image.Resized.To
		scale := jsstring.ToFixed(float64(from.Width)/float64(to.Width), 2)
		notes = append(notes, fmt.Sprintf("Resized from %dx%d to %dx%d. Multiply coordinates by %s to map them to the original.", from.Width, from.Height, to.Width, to.Height, scale))
	}
	return notes
}

// INLINE_IMAGE_TYPES are the formats every provider takes inline; others, such as BMP, need converting.
var INLINE_IMAGE_TYPES = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// ToBase64 is the standard base64 of bytes.
func ToBase64(bytes []byte) string { return base64.StdEncoding.EncodeToString(bytes) }

// FromBase64 is the bytes of standard base64 data, as atob decodes it: ASCII whitespace is ignored and the padding may
// be missing. Data that is not base64 is an error with atob's message.
func FromBase64(data string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r' {
			return -1
		}
		return r
	}, data)
	cleaned = strings.TrimRight(cleaned, "=")
	decoded, err := base64.RawStdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, errors.New("The string to be decoded is not correctly encoded.")
	}
	return decoded, nil
}

// Base64Length is the length of the base64 of byteLength bytes.
func Base64Length(byteLength int64) int64 { return (byteLength + 2) / 3 * 4 } //portlint:allow numbers for a byte count n >= 0, (n+2)/3 is Math.ceil(n / 3) (image-processor.ts:67-68)
