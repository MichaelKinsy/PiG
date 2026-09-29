// Command sdksurface generates docs/extension-sdk-surface.md: every
// extension-facing surface of Pi's extension API, read from the pinned
// upstream types.ts and the declarations it re-exports or exposes, with one
// column per PiG extension runtime (the Node runtime and the Go, Rust and
// Python SDKs).
//
// A cell's status comes from symbol detection in that runtime's source: the
// Go SDK's exported identifiers (go/ast), the Rust SDK's pub items, the
// Python SDK's classes and members, and the Node runtime's classes, `pi`
// object keys and property reads. test/parity/sdk-surface.toml names which symbol
// realizes a surface where the language's naming differs from the default
// rule, and why a realization is a stand-in; it never decides a status on its
// own. Fields that cross the subprocess wire are also checked against the
// host's Go decoding types, so a field the SDK sends but the host drops is
// missing.
//
//	go run ./test/parity/cmd/sdksurface          # rewrite the matrix
//	go run ./test/parity/cmd/sdksurface -check   # fail if it is stale
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "fail if the checked-in matrix is stale or has unexcepted missing cells")
	flag.Parse()
	if err := run(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, "sdksurface:", err)
		os.Exit(1)
	}
}

func run(root string, check bool) error {
	m, err := buildMatrix(root)
	if err != nil {
		return err
	}
	out := m.render()
	path := filepath.Join(root, matrixPath)
	if check {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, out) {
			return fmt.Errorf("%s is stale: run `go run ./test/parity/cmd/sdksurface`", matrixPath)
		}
		return m.validate()
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	return m.validate()
}
