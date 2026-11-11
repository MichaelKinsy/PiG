// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
)

// farbfeldMagic is image 0.24.9's guess_format signature for farbfeld (io/free_functions.rs:285) and the magic the decoder checks
// (codecs/farbfeld.rs:48-57).
const farbfeldMagic = "farbfeld"

// isFarbfeld is image::guess_format's test for a farbfeld file.
func isFarbfeld(data []byte) bool {
	return bytes.HasPrefix(data, []byte(farbfeldMagic))
}

var errFarbfeld = errors.New("photonimage: farbfeld: truncated or invalid file")

// DecodeFarbfeld returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for a farbfeld file:
// image 0.24.9's FarbfeldDecoder (codecs/farbfeld.rs:38-216, Rgba16 as `to_rgba8` makes RGBA8 of it: every sample `(v + 128) / 257`,
// color.rs:408-421). The header is the magic and the two big endian dimensions; a file without every sample is an error (read_exact),
// bytes after the samples are ignored. An image of more than 2^29 bytes (width * height * 8, image's default allocation limit,
// imageMaxAlloc) is an error, as is an empty image whose RGBA buffer length overflows wasm32's `usize` (checkRGBAConversion).
func DecodeFarbfeld(data []byte) (*image.NRGBA, error) {
	if !isFarbfeld(data) {
		return nil, errors.New("photonimage: farbfeld: not a farbfeld file (no \"farbfeld\" magic)")
	}
	// codecs/farbfeld.rs:39-65: width and height are read with read_exact.
	if len(data) < 16 {
		return nil, errFarbfeld
	}
	width := binary.BigEndian.Uint32(data[8:])
	height := binary.BigEndian.Uint32(data[12:])
	// farbfeld.rs:67-82: check_dimension_overflow(width, height, 8) is `width * height > u64::MAX / 8`, which needs more than 2^61
	// pixels and is the same error as the limit of load_inner below.
	if err := reserveTotalBytes(width, height, 8); err != nil {
		return nil, err
	}
	if err := checkRGBAConversion(width, height, 4); err != nil {
		return nil, err
	}
	samples := data[16:]
	total := uint64(width) * uint64(height) * 4 // 16-bit samples
	if uint64(len(samples)) < 2*total {
		return nil, errFarbfeld
	}
	out := newImageNRGBA(width, height)
	for i := range out.Pix {
		out.Pix[i] = downscale16(binary.BigEndian.Uint16(samples[2*i:]))
	}
	return out, nil
}
