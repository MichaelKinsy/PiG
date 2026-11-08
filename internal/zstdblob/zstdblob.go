// SPDX-License-Identifier: MIT

// Package zstdblob stores data that pig embeds as one zstd frame and expands it on first use. It suits large outputs (about a megabyte and up): the klauspost decoder is linked in every build except one that strips the Node runtime, the docs bundle, the changelog and every SDK (D92), a frame decodes about four times faster than the gzip stream it replaces, and the decode allocates an exactly sized result plus a fixed 125 to 200 KB of decoder buffers, where gzip's io.ReadAll doubles its buffer as it grows. For an embedded file of a few hundred KB the fixed buffers outweigh that gain, so such files stay gzip.
//
// Encode is for the generators (automation/gen/zstdfile and the bundle generators). For a large input it uses the zstd command when there is one: the frames it writes at --ultra -22 are 8 to 12 percent smaller than the library's best level makes, which is binary size. The linker drops it from a binary that never calls it.
package zstdblob

import (
	"bytes"
	"os/exec"
	"strconv"

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

// Reuse returns existing when it already decodes to raw, and Encode(raw) otherwise. A generator uses it so a library upgrade that changes the encoder's output does not rewrite committed frames; the drift tests compare decoded bytes.
func Reuse(existing, raw []byte) []byte {
	if decoded, err := Decode(existing); err == nil && bytes.Equal(decoded, raw) {
		return existing
	}
	return Encode(raw)
}

// zstdCLIMinimum is the size from which Encode asks the zstd command for its strongest level. The command's --ultra -22 frames are 8 to 12 percent smaller than the library's best level makes for the embedded files (a changelog, the SDK and docs bundles, grammar snapshots and a wasm module); a small input gains nothing worth a process.
const zstdCLIMinimum = 64 << 10

// Encode returns raw as one frame with its content size recorded. For an input of zstdCLIMinimum bytes or more it is the frame `zstd --ultra -22` writes when that command is on PATH and its frame decodes to raw, and the library's best level otherwise. The output depends on raw and on the encoder; generators keep an existing frame that decodes to raw so a change of encoder does not rewrite committed files.
func Encode(raw []byte) []byte {
	if len(raw) >= zstdCLIMinimum {
		if frame := encodeWithCLI(raw); frame != nil {
			return frame
		}
	}
	return encodeWithLibrary(raw)
}

func encodeWithCLI(raw []byte) []byte {
	path, err := exec.LookPath("zstd")
	if err != nil {
		return nil
	}
	cmd := exec.Command(path, "-q", "-c", "--ultra", "-22", "--stream-size="+strconv.Itoa(len(raw)))
	cmd.Stdin = bytes.NewReader(raw)
	frame, err := cmd.Output()
	if err != nil {
		return nil
	}
	if decoded, err := Decode(frame); err != nil || !bytes.Equal(decoded, raw) {
		return nil
	}
	if _, ok := frameSize(frame); !ok {
		return nil
	}
	return frame
}

func encodeWithLibrary(raw []byte) []byte {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithEncoderConcurrency(1))
	if err != nil {
		panic("zstdblob: " + err.Error())
	}
	defer func() { _ = encoder.Close() }()
	return encoder.EncodeAll(raw, nil)
}
