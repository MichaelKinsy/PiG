// SPDX-License-Identifier: MIT

// Command anytrap counts (outside test, build and example tooling, and vendored third-party code) the places that decode JSON into an interface-typed value: encoding/json's (and the extension SDK copy's, extensions/sdk/json) Unmarshal, UnmarshalRead, UnmarshalDecode and Decoder.Decode with an `any`, `map[string]any`, `[]any` or a type parameter as the destination. Each such decode allocates a boxed value, a map and a string for every JSON value, and on a per-frame, per-keystroke or per-tool-call path that is the cost the streaming benchmarks measure.
//
// The baseline (baseline.toml) is a ratchet like port-lint's: a decode it does not list fails, a listed decode whose count rose fails, and an entry whose count fell or vanished fails too, so a fix lowers or deletes its entry in the same change. Never add an entry for new code: decode into a struct, a json.RawMessage, or use a scanner such as the resumable argument parser in ai/.
//
// Run it with `make any-trap`. -report prints every decode; -print prints the findings as baseline entries.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String("baseline", "", "baseline file (default <root>/automation/ci/anytrap/baseline.toml)")
	report := flag.Bool("report", false, "print every decode; do not compare with the baseline")
	printBaseline := flag.Bool("print", false, "print every decode as a baseline entry")
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "any-trap:", err)
		os.Exit(2)
	}
	if *baselinePath == "" {
		*baselinePath = filepath.Join(abs, "automation", "ci", "anytrap", "baseline.toml")
	}
	os.Exit(run(abs, *baselinePath, patterns, *report, *printBaseline))
}
