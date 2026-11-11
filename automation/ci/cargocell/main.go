// SPDX-License-Identifier: MIT

// Command cargocell writes the Rust cell crate make test-warm builds into each package's shared Cargo target, rendered by the same runtimecell code that renders a real cell's manifest, so the warm build compiles the units a cell reuses.
//
// Usage: go run ./automation/ci/cargocell <sdk-rs dir> <output dir>
package main

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: cargocell <sdk-rs dir> <output dir>")
		os.Exit(2)
	}
	if err := runtimecell.WriteRustWarmCell(os.Args[2], os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
