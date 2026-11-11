// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import "fmt"

// decodeJPEGForTIFF is the JPEG branch of tiff 0.9.1 Image::create_reader (decoder/image.rs:376-427): jpeg-decoder 0.3.1's `decode` of
// the strip (with the JPEGTables prepended by the caller) with `set_color_transform` chosen by the photometric interpretation, as the
// bytes of the decoded pixels. Unlike DecodeJPEG it does not convert to RGBA, and the 512 MiB limit of image's `load_inner` does not
// apply (tiff calls jpeg-decoder itself; the limit of the TIFF's own size is checked by the caller).
func decodeJPEGForTIFF(data []byte, photometric uint16) (pixels []byte, err error) {
	var transform jpegColorTransform
	switch photometric {
	case tiffRGB:
		transform = jpegTransformRGB
	case tiffWhiteIsZero, tiffBlackIsZero, 4: // 4 is the transparency mask
		transform = jpegTransformNone
	case tiffCMYK:
		transform = jpegTransformCMYK
	case tiffYCbCr:
		transform = jpegTransformYCbCr
	default:
		return nil, tiffUnsupported("JPEG compression with photometric interpretation %d", photometric)
	}
	defer func() {
		if r := recover(); r != nil {
			pixels, err = nil, fmt.Errorf("photonimage: tiff: jpeg: decoder panic: %v", r)
		}
	}()
	d := &jpegDecoder{r: jpegReader{data: data}, raw: true, colorTransform: &transform}
	if _, err := d.decode(); err != nil {
		return nil, err
	}
	return d.rawPixels, nil
}
