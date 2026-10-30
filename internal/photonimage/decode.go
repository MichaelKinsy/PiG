// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"

	// The decoders Decode falls back to for the lossless formats. Go's JPEG, WebP and TIFF decoders stay unregistered: they give other pixels than Photon.
	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
)

// Decode decodes the bytes Photon's `PhotonImage.new_from_byteslice` decodes: image 0.24.9 `load_from_memory` guesses the format from the
// magic bytes (guess_format, io/free_functions.rs:266-312) and decodes it, then Photon converts the result with `to_rgba8`. Every format
// Photon enables, but PNG, GIF and BMP, is decoded by a port of the Rust decoder that gives its pixels or its error:
//
//	JPEG      DecodeJPEG      jpeg-decoder 0.3.1
//	WebP      DecodeWebP      image 0.24.9
//	TIFF      DecodeTIFF      tiff 0.9.1
//	DDS       DecodeDDS       image 0.24.9 (DXT1, DXT3 and DXT5)
//	ICO       DecodeICO       image 0.24.9, over its BMP decoder and the png 0.17.14 reader
//	Radiance  DecodeHDR       image 0.24.9
//	PNM       DecodePNM       image 0.24.9
//	farbfeld  DecodeFarbfeld  image 0.24.9
//
// PNG, GIF and BMP are decoded by the standard library decoders this package registers, which are lossless and give the same pixels as
// Photon's. Bytes that no signature of guess_format matches are an error, as in Photon (TGA has no signature, io/free_functions.rs:295, and
// AVIF, OpenEXR and QOI are not compiled into Photon's wasm). The result is not premultiplied.
//
// The cases follow the order of guess_format's table, whose first matching signature wins; no signature is a prefix of another, so the
// order only documents it.
func Decode(data []byte) (image.Image, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return decodeNRGBA(DecodeJPEG, data)
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return decodeNRGBA(DecodeWebP, data)
	case isTIFF(data):
		return decodeNRGBA(DecodeTIFF, data)
	case isDDS(data):
		return decodeNRGBA(DecodeDDS, data)
	case isICO(data):
		return decodeNRGBA(DecodeICO, data)
	case isHDR(data):
		return decodeNRGBA(DecodeHDR, data)
	case isPNM(data):
		return decodeNRGBA(DecodePNM, data)
	case isFarbfeld(data):
		return decodeNRGBA(DecodeFarbfeld, data)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// decodeNRGBA is a Decode case: an *image.NRGBA that is nil on error must not become a non-nil image.Image.
func decodeNRGBA(decode func([]byte) (*image.NRGBA, error), data []byte) (image.Image, error) {
	img, err := decode(data)
	if err != nil {
		return nil, err
	}
	return img, nil
}
