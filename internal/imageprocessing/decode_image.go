package imageprocessing

import (
	"image"

	"github.com/MichaelKinsy/PiG/internal/photonimage"
)

// DecodeImage decodes the bytes Pi hands to Photon's PhotonImage.new_from_byteslice: what photonimage.Decode decodes, as image 0.24.9 decodes it. That includes the 16-bit and BI_BITFIELDS bitmaps that Windows places on the clipboard as CF_DIB for Print Screen and GDI captures, and every other layout of image's BMP decoder.
// upstream: packages/coding-agent/src/utils/image-convert.ts:convertImageBytesToPng
func DecodeImage(data []byte) (image.Image, error) {
	return photonimage.Decode(data)
}
