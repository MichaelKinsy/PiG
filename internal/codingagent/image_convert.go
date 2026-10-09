// PNG conversion for terminal image display.
//
// Upstream reference:
//   .upstream/current/packages/coding-agent/src/utils/image-convert.ts
//
// Image decoding and EXIF orientation live in internal/imageprocessing.

package codingagent

import (
	"encoding/base64"

	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
	"github.com/MichaelKinsy/PiG/tui"
)

// ConvertImageBytesToPng mirrors upstream convertImageBytesToPng: it decodes
// the image, applies its EXIF orientation, and re-encodes it as PNG. It returns
// nil when the bytes cannot be decoded or encoded, where upstream returns null
// (conversion failed or Photon unavailable).
func ConvertImageBytesToPng(data []byte) []byte { return imageprocessing.ConvertImageBytesToPng(data) }

// ConvertToPng mirrors upstream convertToPng: the Kitty graphics protocol
// requires PNG (f=100), so a non-PNG base64 image is converted. PNG input is
// returned unchanged; nil is upstream's null for a failed conversion.
func ConvertToPng(base64Data, mimeType string) *tui.ConvertedImage {
	if mimeType == "image/png" {
		return &tui.ConvertedImage{Data: base64Data, MimeType: mimeType}
	}
	pngBytes := ConvertImageBytesToPng(decodeNodeBase64(base64Data))
	if pngBytes == nil {
		return nil
	}
	return &tui.ConvertedImage{
		Data:     base64.StdEncoding.EncodeToString(pngBytes),
		MimeType: "image/png",
	}
}

// PngTranscoder is upstream loadPngTranscoder's transcoder: base64 image data to oriented base64 PNG data, or false when
// the data does not decode.
func PngTranscoder(base64Data, _ string) (string, bool) {
	pngBytes := ConvertImageBytesToPng(decodeNodeBase64(base64Data))
	if pngBytes == nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(pngBytes), true
}

// ensurePngTranscoder registers [PngTranscoder] as the tui image transcoder on a Kitty-protocol terminal, so non-PNG
// images render. The Go transcoder needs no asynchronous load, so it registers at once. onRegistered runs after a new
// registration, and not when a transcoder was already registered, so callers can re-render images that showed text fallbacks.
// upstream: packages/coding-agent/src/utils/image-convert.ts:ensurePngTranscoder
func ensurePngTranscoder(onRegistered func()) {
	if tui.GetCapabilities().Images != tui.ImageProtocolKitty || tui.ImageTranscoderRegistered() {
		return
	}
	tui.SetImageTranscoder(PngTranscoder)
	if onRegistered != nil {
		onRegistered()
	}
}

func init() { tui.SetImageTranscoderLoader(ensurePngTranscoder) }

func decodeNodeBase64(data string) []byte { return imageprocessing.DecodeNodeBase64(data) }

// ensurePngTranscoder lets extension images use the PNG transcoder; tool results register it themselves. A new registration
// re-renders what showed text fallbacks (interactive-mode.ts ensurePngTranscoder).
func (m *InteractiveMode) ensurePngTranscoder() {
	ensurePngTranscoder(func() {
		if m.tuiInst != nil {
			m.tuiInst.Invalidate()
			m.tuiInst.RequestRender()
		}
	})
}
