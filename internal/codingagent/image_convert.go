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

func decodeNodeBase64(data string) []byte { return imageprocessing.DecodeNodeBase64(data) }

// maybeConvertImagesForKitty mirrors upstream tool-execution.ts
// maybeConvertImagesForKitty. Upstream starts one unawaited convertToPng per
// pending image and applies each result on the event loop; here each
// conversion runs on its own goroutine and hands its result to the main loop
// through runOnMain, which drops it once the run context ends. The component
// ignores a result whose source image was replaced meanwhile.
func (m *InteractiveMode) maybeConvertImagesForKitty(comp *tui.ToolExecutionComponent) {
	for _, req := range comp.PendingKittyImageConversions() {
		go func() {
			converted := ConvertToPng(req.Data, req.MimeType)
			m.runOnMain(m.runCtx, func() {
				if comp.ApplyConvertedImage(req, converted) && m.tuiInst != nil {
					m.tuiInst.RequestRender()
				}
			})
		}()
	}
}
