package tools

import (
	"context"
	"encoding/base64"
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

// INLINE_IMAGE_TYPES are the formats every provider takes inline; others, such as BMP, need converting.
var INLINE_IMAGE_TYPES = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// ToBase64 is the standard base64 of bytes.
func ToBase64(bytes []byte) string { return base64.StdEncoding.EncodeToString(bytes) }

// Base64Length is the length of the base64 of byteLength bytes.
func Base64Length(byteLength int64) int64 { return (byteLength + 2) / 3 * 4 } //portlint:allow numbers for a byte count n >= 0, (n+2)/3 is Math.ceil(n / 3) (image-processor.ts:67-68)
