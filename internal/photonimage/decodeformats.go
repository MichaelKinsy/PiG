// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"errors"
	"image"
	"math"
	"strings"
)

// What the decoders of image 0.24.9 that this package ports share when Photon (photon-rs 0.3.3 lib.rs:93-105,
// `image::load_from_memory(bytes).unwrap()` and `to_rgba8()`) drives them: the allocation limit, the `ImageBuffer` size arithmetic of a
// wasm32 build and the sample conversions of `to_rgba8`.

// imageMaxAlloc is `Limits::default().max_alloc`, 512 MiB (image 0.24.9 io/mod.rs:63-71). `load_inner` reserves the decoder's
// `total_bytes()` (io/free_functions.rs:104-109, `Limits::reserve` io/mod.rs:114-126) before it decodes.
const imageMaxAlloc = 512 * 1024 * 1024

var errImageLimits = errors.New("photonimage: image exceeds the 512 MiB allocation limit of image 0.24.9")

// errImageBufferOverflow is `ImageBuffer::from_raw` returning `None` (`DimensionMismatch`) or `ImageBuffer::new` panicking, where the
// number of samples does not fit the 32 bits of wasm32's `usize`.
var errImageBufferOverflow = errors.New("photonimage: image buffer length overflows the usize of wasm32")

// reserveTotalBytes is the decoder's `total_bytes()` (image.rs:795-800, saturating) against the allocation limit.
func reserveTotalBytes(width, height uint32, bytesPerPixel uint64) error {
	total := uint64(width) * uint64(height) // cannot overflow: both are below 2^32
	if bytesPerPixel != 0 && total > math.MaxUint64/bytesPerPixel {
		return errImageLimits
	}
	if total*bytesPerPixel > imageMaxAlloc {
		return errImageLimits
	}
	return nil
}

// checkBufferLen is `ImageBuffer::image_buffer_len` (buffer.rs:807-811) in wasm32's 32-bit `usize`: `channels * width` and then `* height`
// are checked multiplications. `from_raw` takes the colour type's channel count; `to_rgba8` allocates a new RGBA image of the same size,
// where the same overflow is a panic (`ImageBuffer::new`).
func checkBufferLen(width, height uint32, channels uint64) error {
	n := channels * uint64(width)
	if n > math.MaxUint32 {
		return errImageBufferOverflow
	}
	if n*uint64(height) > math.MaxUint32 {
		return errImageBufferOverflow
	}
	return nil
}

// checkRGBAConversion is the two `ImageBuffer` lengths of `decoder_to_image` (from_raw with the decoder's channel count) and `to_rgba8`
// (a new RGBA buffer).
func checkRGBAConversion(width, height uint32, channels uint64) error {
	if err := checkBufferLen(width, height, channels); err != nil {
		return err
	}
	return checkBufferLen(width, height, 4)
}

// newImageNRGBA is the RGBA8 result of `to_rgba8()` as an image.NRGBA of the same size.
func newImageNRGBA(width, height uint32) *image.NRGBA {
	return image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
}

// downscale16 is image color.rs:408-421 `FromPrimitive<u16> for u8`, `(c + 128) / 257`.
func downscale16(v uint16) byte {
	return byte((uint32(v) + 128) / 257)
}

// rustParseUint is `str::parse::<u8>`, `<u16>` and `<u32>` (bits 8, 16, 32): an optional `+` and then decimal digits that fit. A `-`,
// even of zero, an empty string, whitespace and every other character are errors.
func rustParseUint(s string, bits int) (uint64, bool) {
	s = strings.TrimPrefix(s, "+")
	if s == "" {
		return 0, false
	}
	limit := uint64(1)<<bits - 1
	var v uint64
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + uint64(c-'0')
		if v > limit {
			return 0, false
		}
	}
	return v, true
}
