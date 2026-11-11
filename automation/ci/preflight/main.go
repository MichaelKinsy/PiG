// SPDX-License-Identifier: MIT

// Command preflight runs the cheap checks a pull request must pass before any heavy CI starts. Each subcommand is one
// workflow step:
//
//	preflight fetch     decide the mode from the previous state and fetch every data source the relevant checks need,
//	                    once, into one JSON
//	preflight evaluate  decide each check from that data, reusing verdicts whose inputs did not change
//	preflight prepare   write the scan steps' inputs: the added lines, the file list, the head when a scan needs it
//	preflight verdict   record one scan step's exit status for its check
//	preflight record    write the new state, the status and the report
//
// The checks are declared in config.yaml; config.go validates them.
package main

import (
	"fmt"
	"io"
	"os"
)

// sourceNames are the data sources config.yaml may name.
var sourceNames = []string{"pull", "files", "commits", "compare-main", "compare-previous", "depends-on", "dependency-graph"}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight:", err)
		return 2
	}
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: preflight fetch|evaluate|prepare|verdict|record|checks")
		return 2
	}
	switch args[0] {
	case "fetch":
		return runFetch(cfg, args[1:], stdout, stderr)
	case "evaluate":
		return runEvaluate(cfg, args[1:], stdout, stderr)
	case "prepare":
		return runPrepare(cfg, args[1:], stdout, stderr)
	case "verdict":
		return runVerdict(args[1:], stdout, stderr)
	case "record":
		return runRecord(cfg, args[1:], stdout, stderr)
	case "checks":
		for _, c := range cfg.Checks {
			_, _ = fmt.Fprintf(stdout, "%s v%d\n", c.Name, c.Version)
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "preflight: unknown subcommand %q\n", args[0])
		return 2
	}
}
