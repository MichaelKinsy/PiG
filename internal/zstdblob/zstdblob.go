// SPDX-License-Identifier: MIT

// Package zstdblob stores the data that pig embeds as one zstd frame and expands it on first use. A frame decodes about four times faster than the gzip stream it replaces and into half the allocations (an exactly sized result and no doubling buffer), and compresses these files 5 to 15 percent smaller: the klauspost decoder is already in the binary for the Node runtime pack.
//
// Encode is for the generators (automation/gen/zstdfile, automation/gen/embed-bundle.go); the linker drops it from a binary that never calls it.
package zstdblob

import (
	"github.com/klauspost/compress/zstd"
)

// Decode returns the bytes frame holds. It builds a decoder for the call and closes it, so no decoder buffers stay on the heap once the caller has its data.
func Decode(frame []byte) ([]byte, error) {
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	var dst []byte
	if size, ok := frameSize(frame); ok {
		dst = make([]byte, 0, size)
	}
	return decoder.DecodeAll(frame, dst)
}

// frameSize is the content size the frame header records. Encode records it for any input of a few bytes or more, so Decode sizes the result exactly.
func frameSize(frame []byte) (int, bool) {
	var header zstd.Header
	if err := header.Decode(frame); err != nil || !header.HasFCS {
		return 0, false
	}
	return int(header.FrameContentSize), true
}

// Encode returns raw as one frame at the best compression level, with its content size recorded. The output depends only on raw and on the encoder's version; generators keep an existing frame that decodes to raw so a library upgrade does not rewrite committed files.
func Encode(raw []byte) []byte {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithEncoderConcurrency(1))
	if err != nil {
		panic("zstdblob: " + err.Error())
	}
	defer func() { _ = encoder.Close() }()
	return encoder.EncodeAll(raw, nil)
}
