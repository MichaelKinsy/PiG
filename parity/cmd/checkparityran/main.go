// Command checkparityran answers one question: did the parity suite
// actually compare pig against a real pi, or did it just skip?
//
// TestParity (parity/runner/runner_test.go) resolves the pig-under-test and
// upstream-pi binaries before it does anything else. When PIG_PARITY_PIG_BIN
// (or PIG_BIN) is unset, or tmux/pi 0.87.1 is missing, those resolvers call
// t.Skip with an explicit reason and TestParity's body returns immediately -
// no scenario ever runs, and `--pig-parity.results` is never written. `go
// test` still exits 0 for that run, because a skipped test is not a failed
// test, so `make parity` alone cannot tell "0 scenarios ran" apart from
// "every scenario passed" without checking the results file.
//
// This command reads that results file and prints the number of scenario
// outcomes it contains. The Makefile treats 0 as a hard failure (see
// require-parity-ran in automation/make/parity.mk) unless the caller
// explicitly opts out. A missing results file counts as zero outcomes
// rather than an error, since a missing file is exactly what a wholesale
// skip produces.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// resultsFile mirrors the on-disk shape written by
// parity/runner/results_json.go's writeResultsJSON. It is kept minimal and
// independent of the (parity-build-tagged) runner package so this command
// builds and tests without tmux, a pig/pi binary, or the parity build tag.
type resultsFile struct {
	Outcomes []json.RawMessage `json:"outcomes"`
}

// countOutcomes returns the number of scenario outcomes recorded at path.
// A missing file returns (0, nil): TestParity only ever writes this file
// from a t.Cleanup registered after it resolves both binaries, so "the file
// doesn't exist" and "the file has zero outcomes" are the same condition -
// TestParity skipped before running anything.
func countOutcomes(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read results %s: %w", path, err)
	}
	var rf resultsFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return 0, fmt.Errorf("parse results %s: %w", path, err)
	}
	return len(rf.Outcomes), nil
}

func main() {
	resultsPath := flag.String("results", "", "path to the --pig-parity.results JSON file")
	flag.Parse()
	if *resultsPath == "" {
		fmt.Fprintln(os.Stderr, "checkparityran: -results is required")
		os.Exit(2)
	}
	n, err := countOutcomes(*resultsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "checkparityran:", err)
		os.Exit(2)
	}
	fmt.Println(n)
}
