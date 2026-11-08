// SPDX-License-Identifier: MIT

// Package zstdblob stores data that pig embeds as one zstd frame and expands it on first use. It suits large outputs (about a megabyte and up): the klauspost decoder is linked in every build except one that strips the Node runtime, the docs bundle, the changelog and every SDK (D92), a frame decodes about four times faster than the gzip stream it replaces, and the decode allocates an exactly sized result plus a fixed 125 to 200 KB of decoder buffers, where gzip's io.ReadAll doubles its buffer as it grows. For an embedded file of a few hundred KB the fixed buffers outweigh that gain, so such files stay gzip.
//
// Encode is for the generators (automation/gen/zstdfile and the bundle generators) and writes the frames with one encoder: the zstd command at PinnedCLIVersion, run as `zstd --ultra -22`. Its frames are 8 to 12 percent smaller than the library's best level makes, which is binary size (about 200 KB of the pig binary), and the committed frames are byte-for-byte what it writes, so a from-scratch regeneration changes nothing (TestCommittedFramesAreWhatEncodeWrites). A machine without that exact command cannot regenerate a frame: Encode panics and says how to get it (the tern-ci image carries it). EncodeLibrary is the library's encoder, for a test fixture that is never committed. The linker drops both from a binary that never calls them.
package zstdblob

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// PinnedCLIVersion is the version of the zstd command that writes every committed frame. tern-ci's image installs Ubuntu 24.04's zstd 1.5.5; a newer or older zstd compresses the same input to different bytes.
const PinnedCLIVersion = "1.5.5"

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

// Reuse returns existing when it already decodes to raw, and Encode(raw) otherwise. A generator uses it so that regenerating an unchanged source rewrites nothing; TestCommittedFramesAreWhatEncodeWrites, not Reuse, is what keeps the committed bytes those of the pinned encoder.
func Reuse(existing, raw []byte) []byte {
	if decoded, err := Decode(existing); err == nil && bytes.Equal(decoded, raw) {
		return existing
	}
	return Encode(raw)
}

// CLIVersion is the version of the zstd command on PATH, "" with the error when there is none.
func CLIVersion() (string, error) {
	path, err := exec.LookPath("zstd")
	if err != nil {
		return "", err
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	// "*** Zstandard CLI (64-bit) v1.5.5, by Yann Collet ***"
	_, after, ok := strings.Cut(string(out), " v")
	if !ok {
		return "", fmt.Errorf("zstd --version printed %q", out)
	}
	version, _, _ := strings.Cut(after, ",")
	return strings.TrimSpace(version), nil
}

// RequirePinnedCLI returns nil when the zstd command on PATH is PinnedCLIVersion and an error that says how to get it otherwise.
func RequirePinnedCLI() error {
	version, err := CLIVersion()
	switch {
	case err != nil:
		return fmt.Errorf("zstdblob: frames are written with the zstd command %s, and it cannot be run (%v): run the generator in the tern-ci image (automation/ci/build-ci-image.sh), which carries it", PinnedCLIVersion, err)
	case version != PinnedCLIVersion:
		return fmt.Errorf("zstdblob: frames are written with the zstd command %s, and PATH has %s: run the generator in the tern-ci image (automation/ci/build-ci-image.sh), which carries %[1]s", PinnedCLIVersion, version)
	}
	return nil
}

// Encode returns raw as one frame, with its content size recorded, as `zstd --ultra -22` at PinnedCLIVersion writes it. It panics when that command is missing or another version (RequirePinnedCLI), and when the frame does not decode to raw. The output depends on raw alone.
func Encode(raw []byte) []byte {
	if err := RequirePinnedCLI(); err != nil {
		panic(err.Error())
	}
	cmd := exec.Command("zstd", "-q", "-c", "--ultra", "-22", "--stream-size="+strconv.Itoa(len(raw)))
	cmd.Stdin = bytes.NewReader(raw)
	frame, err := cmd.Output()
	if err != nil {
		panic("zstdblob: zstd --ultra -22: " + err.Error())
	}
	if decoded, err := Decode(frame); err != nil || !bytes.Equal(decoded, raw) {
		panic("zstdblob: the frame zstd wrote does not decode to its input")
	}
	return frame
}

// EncodeLibrary returns raw as one frame the library's best level writes, for a fixture no one commits. Its output depends on the library's version; nothing embedded is written with it.
func EncodeLibrary(raw []byte) []byte {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithEncoderConcurrency(1))
	if err != nil {
		panic("zstdblob: " + err.Error())
	}
	defer func() { _ = encoder.Close() }()
	return encoder.EncodeAll(raw, nil)
}
