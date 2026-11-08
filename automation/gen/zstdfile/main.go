// SPDX-License-Identifier: MIT

// Command zstdfile writes the zstd frame that a go:embed file carries: zstdfile SOURCE FRAME stores SOURCE's bytes as FRAME (internal/zstdblob.Encode: the zstd command at zstdblob.PinnedCLIVersion, so run it in the tern-ci image; any other zstd makes it stop with an error). A FRAME that already decodes to SOURCE's bytes is left as it is; zstdblob's TestCommittedFramesAreWhatEncodeWrites checks, from scratch, that every committed frame is what Encode writes.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: zstdfile SOURCE FRAME")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	existing, err := os.ReadFile(os.Args[2])
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fail(err)
	}
	if frame := zstdblob.Reuse(existing, raw); !bytes.Equal(frame, existing) {
		if err := os.WriteFile(os.Args[2], frame, 0o644); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "zstdfile:", err)
	os.Exit(1)
}
