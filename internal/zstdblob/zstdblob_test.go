// SPDX-License-Identifier: MIT

package zstdblob

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// committedFrames are the zstd frames the repository embeds, relative to the module root. A frame that is not listed fails TestEveryCommittedFrameIsListed, so it cannot escape TestCommittedFramesAreWhatEncodeWrites.
var committedFrames = []string{
	"CHANGELOG.md.zst",
	"extensions/sdk-lisp/sdk.zst",
	"extensions/sdk-py/sdk.zst",
	"extensions/sdk-rs/sdk.zst",
	"internal/gosdkbundle/sdk.zst",
	"internal/pigdocs/pages.zst",
}

func TestRoundTrip(t *testing.T) {
	for _, raw := range [][]byte{[]byte("x"), bytes.Repeat([]byte("embedded text "), 5000)} {
		frame := EncodeLibrary(raw)
		got, err := Decode(frame)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("round trip of %d bytes returned %d bytes", len(raw), len(got))
		}
	}
}

func TestDecodeRejectsDamage(t *testing.T) {
	frame := EncodeLibrary(bytes.Repeat([]byte("embedded text "), 5000))
	if _, err := Decode(frame[:len(frame)/2]); err == nil {
		t.Fatal("a truncated frame decoded")
	}
	if _, err := Decode([]byte("not a frame")); err == nil {
		t.Fatal("garbage decoded")
	}
}

// Decoding must leave nothing behind: no goroutine and a result of exactly the recorded size.
func TestDecodeSizesResultExactlyAndLeavesNoGoroutines(t *testing.T) {
	raw := bytes.Repeat([]byte("embedded text "), 5000)
	frame := EncodeLibrary(raw)
	before := runtime.NumGoroutine()
	got, err := Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if after := runtime.NumGoroutine(); after != before {
		t.Fatalf("%d goroutines before Decode, %d after", before, after)
	}
	if cap(got) != len(raw) {
		t.Fatalf("result capacity %d, want %d", cap(got), len(raw))
	}
}

// pinnedCLI returns when the zstd command that writes the committed frames is on PATH. Without it the test is skipped on a developer's machine, where the generators say what to do (RequirePinnedCLI), and fails where CI sets the environment: a CI that has lost the pinned command would otherwise stop checking the frames.
func pinnedCLI(t *testing.T) {
	t.Helper()
	err := RequirePinnedCLI()
	if err == nil {
		return
	}
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		t.Fatal(err)
	}
	t.Skip(err)
}

func TestPinnedCLIVersionIsInstalledWhereCIRuns(t *testing.T) {
	pinnedCLI(t)
	if version, err := CLIVersion(); err != nil || version != PinnedCLIVersion {
		t.Fatalf("zstd %q, %v; want %s", version, err, PinnedCLIVersion)
	}
}

// Encode is what every committed frame is: decoding a frame and encoding the result from scratch (no Reuse, which keeps any frame that decodes to the same bytes) gives the same bytes. A frame written by another zstd version, by the library or by hand differs, and a regeneration on a machine with the pinned command would then change it.
func TestCommittedFramesAreWhatEncodeWrites(t *testing.T) {
	pinnedCLI(t)
	root := filepath.Join("..", "..")
	for _, name := range committedFrames {
		t.Run(name, func(t *testing.T) {
			frame, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := Decode(frame)
			if err != nil {
				t.Fatal(err)
			}
			if again := Encode(raw); !bytes.Equal(again, frame) {
				t.Fatalf("%s is %d bytes and Encode of its content is %d: regenerate it in the tern-ci image (zstd %s), e.g. go generate for the file's package", name, len(frame), len(again), PinnedCLIVersion)
			}
		})
	}
}

// A frame added to the repository joins committedFrames, so the check above covers it.
func TestEveryCommittedFrameIsListed(t *testing.T) {
	root := filepath.Join("..", "..")
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".upstream", ".cache", "target":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".zst") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(found)
	want := slices.Clone(committedFrames)
	slices.Sort(want)
	if !slices.Equal(found, want) {
		t.Fatalf("zstd files in the repository %v, listed in committedFrames %v", found, want)
	}
}
