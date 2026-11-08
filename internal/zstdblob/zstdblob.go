// SPDX-License-Identifier: MIT

// Package zstdblob stores data that pig embeds as one zstd frame and expands it on first use. It suits large outputs (about a megabyte and up): the klauspost decoder is linked in every build except one that strips the Node runtime, the docs bundle, the changelog and every SDK (D92), a frame decodes about four times faster than the gzip stream it replaces, and the decode allocates an exactly sized result plus a fixed 125 to 200 KB of decoder buffers, where gzip's io.ReadAll doubles its buffer as it grows. For an embedded file of a few hundred KB the fixed buffers outweigh that gain, so such files stay gzip.
//
// Encode is for the generators (automation/gen/zstdfile and the bundle generators) and writes the frames with one encoder: the zstd command at PinnedCLIVersion, run as `zstd --ultra -22`. Its frames are 8 to 12 percent smaller than the library's best level makes, which is binary size (about 200 KB of the pig binary), and the committed frames are byte-for-byte what it writes, so a from-scratch regeneration changes nothing (TestCommittedFramesAreWhatEncodeWrites). The command is the one automation/ci/install-zstd.sh builds from the pinned release source, found through PIG_ZSTD or the default prefix (PinnedCLI); the zstd on PATH is not trusted, since GitHub's runners, Homebrew and distributions each carry another version. A machine without it cannot regenerate a frame: Encode panics and names the provisioner. EncodeLibrary is the library's encoder, for a test fixture that is never committed. The linker drops both from a binary that never calls them.
package zstdblob

import (
	"bytes"
	"io"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// PinnedCLIVersion is the version of the zstd command that writes every committed frame: the zstd-source row of automation/dev/toolchains.lock, which install-zstd.sh builds. Another version compresses the same input to different bytes.
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

// NewReader streams the bytes frame holds, decoding no further than the caller reads. The decoder is synchronous (one block at a time, low memory) and starts no goroutine, so a caller that stops early leaves nothing running; its buffers are collected with the reader.
func NewReader(frame []byte) (io.Reader, error) {
	return zstd.NewReader(bytes.NewReader(frame), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
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

// provisioner is what an error tells a person to run.
const provisioner = "automation/ci/install-zstd.sh"

// versionOf is the version the zstd command at path reports.
func versionOf(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	// "*** Zstandard CLI (64-bit) v1.5.5, by Yann Collet ***"
	_, after, ok := strings.Cut(string(out), " v")
	if !ok {
		return "", fmt.Errorf("%s --version printed %q", path, out)
	}
	version, _, _ := strings.Cut(after, ",")
	return strings.TrimSpace(version), nil
}

// defaultPrefixBin is where install-zstd.sh puts the command without an argument: <cache>/pig-zstd/<version>/bin/zstd, <cache> being $PIG_CACHE_HOME, else $XDG_CACHE_HOME, else $HOME/.cache.
func defaultPrefixBin() string {
	cache := os.Getenv("PIG_CACHE_HOME")
	if cache == "" {
		cache = os.Getenv("XDG_CACHE_HOME")
	}
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "pig-zstd", PinnedCLIVersion, "bin", "zstd")
}

// PinnedCLI returns the path of the zstd command at PinnedCLIVersion: $PIG_ZSTD when it is set, else the default prefix of install-zstd.sh. It does not look on PATH. The error names the provisioner.
func PinnedCLI() (string, error) {
	path, source := os.Getenv("PIG_ZSTD"), "PIG_ZSTD"
	if path == "" {
		path, source = defaultPrefixBin(), "the default prefix"
	}
	hint := fmt.Sprintf("frames are written with zstd %s, which %s builds from the pinned source (run it, then set PIG_ZSTD to the path it prints, or leave it for the default prefix)", PinnedCLIVersion, provisioner)
	if path == "" {
		return "", fmt.Errorf("zstdblob: no home directory for the default prefix: %s", hint)
	}
	version, err := versionOf(path)
	switch {
	case err != nil:
		return "", fmt.Errorf("zstdblob: %s names %s, which cannot be run (%v): %s", source, path, err, hint)
	case version != PinnedCLIVersion:
		return "", fmt.Errorf("zstdblob: %s names %s, which is zstd %s: %s", source, path, version, hint)
	}
	return path, nil
}

// Encode returns raw as one frame, with its content size recorded, as `zstd --ultra -22` at PinnedCLIVersion writes it. It panics when PinnedCLI finds no such command, and when the frame does not decode to raw. The output depends on raw alone: the command runs without ZSTD_NBTHREADS, ZSTD_CLEVEL and the other ZSTD_ variables.
func Encode(raw []byte) []byte {
	path, err := PinnedCLI()
	if err != nil {
		panic(err.Error())
	}
	cmd := exec.Command(path, "-q", "-c", "--ultra", "-22", "--stream-size="+strconv.Itoa(len(raw)))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "ZSTD_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
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
