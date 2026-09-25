// Image format detection for the read tool.
//
// upstream: coding-agent/src/utils/mime.ts detectSupportedImageMimeTypeFromFile
package tools

import "github.com/MichaelKinsy/PiG/internal/imageprocessing"

// SupportedImageMime returns the supported still-image MIME type of a file's
// contents, or "" when it is not one. Like upstream
// detectSupportedImageMimeTypeFromFile it looks only at the first
// IMAGE_TYPE_SNIFF_BYTES.
//
// The read tool is one of only two producers of image bytes that later reach
// the image decoders, so this allowlist is what keeps formats outside it out
// of them.
func SupportedImageMime(data []byte) string {
	return imageprocessing.DetectSupportedImageMimeType(data[:min(len(data), imageprocessing.ImageTypeSniffBytes)])
}
