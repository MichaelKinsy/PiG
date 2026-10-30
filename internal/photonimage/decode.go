// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"

	// The decoders Decode falls back to for every format but JPEG and WebP. Go's JPEG and WebP decoders stay unregistered: they give other pixels than Photon.
	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
)

// Decode decodes the bytes Photon's `PhotonImage.new_from_byteslice` decodes: image 0.24.9 `load_from_memory` guesses the format from
// the magic bytes (io/free_functions.rs:268) and decodes JPEG with jpeg-decoder 0.3.1 (DecodeJPEG), WebP with its own decoder
// (DecodeWebP) and TIFF with tiff 0.9.1 (DecodeTIFF), all pixel for pixel. Every other format is decoded by the standard library
// decoders this package registers (PNG, GIF, BMP), which are lossless and give the same pixels as Photon's. The result is not premultiplied.
func Decode(data []byte) (image.Image, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		img, err := DecodeJPEG(data)
		if err != nil {
			return nil, err
		}
		return img, nil
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		img, err := DecodeWebP(data)
		if err != nil {
			return nil, err
		}
		return img, nil
	case isTIFF(data):
		img, err := DecodeTIFF(data)
		if err != nil {
			return nil, err
		}
		return img, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}
