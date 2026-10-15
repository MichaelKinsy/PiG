// SPDX-License-Identifier: MIT

// Command cargotarget prints the persistent Cargo target directory a test package uses (internal/testenv.SharedCargoTargetDir), so make test-warm builds the Rust SDK dependencies into the same directory the tests reuse.
//
// Usage: go run ./automation/ci/cargotarget <package> <sdk-rs dir>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: cargotarget <package> <sdk-rs dir>")
		os.Exit(2)
	}
	dir, err := testenv.SharedCargoTargetDir(os.Args[1], filepath.Join(os.Args[2], "Cargo.lock"), filepath.Join(os.Args[2], "Cargo.toml"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(dir)
}
