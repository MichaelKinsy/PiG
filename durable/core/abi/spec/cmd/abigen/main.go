// SPDX-License-Identifier: MIT

// Command abigen writes durable/core/abi/id.go (the abi_id constant the core exports) and durable/core/abi/abi.json (the
// tables and abi_id the JavaScript hosts read) from the tables in package spec. With -check it fails when either file
// is stale instead of writing.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/durable/core/abi/spec"
)

func main() {
	dir := flag.String("dir", ".", "the durable/core/abi directory")
	check := flag.Bool("check", false, "fail when a generated file is stale")
	flag.Parse()
	data, err := spec.JSON()
	if err != nil {
		fail(err)
	}
	files := map[string][]byte{
		"id.go":    []byte(spec.IDSource()),
		"abi.json": data,
	}
	for _, name := range []string{"id.go", "abi.json"} {
		path := filepath.Join(*dir, name)
		if *check {
			have, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(have, files[name]) {
				fail(fmt.Errorf("%s is stale; run go generate ./durable/core/abi", path))
			}
			continue
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "abigen:", err)
	os.Exit(1)
}
