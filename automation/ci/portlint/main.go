// SPDX-License-Identifier: MIT

// Command portlint runs the porting anti-pattern analyzers over the module and
// fails on any finding the baseline does not list. It exists because a Go port
// of TypeScript drifts in the same few ways: map key order, nil versus empty
// JSON, UTF-16 versus byte lengths, goroutine ownership, request-body reuse
// across retries, and real state touched by tests.
//
// The baseline (baseline.toml) is a ratchet like divergence-guard's: a finding
// it does not list fails, and an entry that no longer matches fails too, so a
// fix deletes its entry in the same change. A finding is allowed at the source
// by `//portlint:allow <check> <reason>` on its line or the line above; the
// reason is required.
//
// Run it with `make port-lint`. -report prints counts per check and every
// finding without failing; -print prints the findings as baseline entries.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String("baseline", "", "baseline file (default <root>/automation/ci/portlint/baseline.toml)")
	report := flag.Bool("report", false, "print counts per check and every finding; do not compare with the baseline")
	printBaseline := flag.Bool("print", false, "print every finding as a baseline entry")
	tags := flag.String("tags", "integration,live,parity", "build tags for a second pass over tagged files, as make lint builds them; empty scans only the default build")
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "port-lint:", err)
		os.Exit(2)
	}
	if *baselinePath == "" {
		*baselinePath = filepath.Join(abs, "automation", "ci", "portlint", "baseline.toml")
	}
	os.Exit(run(abs, *baselinePath, *tags, patterns, *report, *printBaseline))
}
