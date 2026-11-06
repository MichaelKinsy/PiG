// SPDX-License-Identifier: MIT

// Command zstdfile writes the zstd frame that a go:embed file carries: zstdfile SOURCE FRAME stores SOURCE's bytes as FRAME (internal/zstdblob.Encode). A FRAME that already decodes to SOURCE's bytes is left as it is, so a library upgrade does not rewrite committed frames; the drift tests compare decoded bytes, not frame bytes.
package main

import (
	"bytes"
	"fmt"
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
	if frame, err := os.ReadFile(os.Args[2]); err == nil {
		if decoded, err := zstdblob.Decode(frame); err == nil && bytes.Equal(decoded, raw) {
			return
		}
	}
	if err := os.WriteFile(os.Args[2], zstdblob.Encode(raw), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "zstdfile:", err)
	os.Exit(1)
}
