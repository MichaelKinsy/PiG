// SPDX-License-Identifier: MIT

// Command portlens renders a static HTML report of the port: the Pi pin and ledger health, the port map, divergences, the bug ledger, and evals and benchmarks.
//
// It reads only repository files (the interface ledgers, DIVERGENCES.md, docs/additive-features.md, docs/bug-ledger.json, the benchmark results and the perf scoreboard registry) and, for Pi source snippets, the source mirror of the pinned Pi version under .upstream. The output directory holds index.html and its assets; it needs no server and works from file://. -serve starts a local http.Server over the directory.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	root := flag.String("repo", ".", "repository root (any directory inside it)")
	out := flag.String("out", "build/portlens", "output directory")
	totals := flag.String("totals-log", "", "optional ledger totals log; only pin labels, times and counts are read for the trend")
	serve := flag.String("serve", "", "after the build, serve the output on this address (for example 127.0.0.1:8650)")
	flag.Parse()
	if err := run(*root, *out, *totals, *serve); err != nil {
		fmt.Fprintln(os.Stderr, "portlens:", err)
		os.Exit(1)
	}
}

func run(root, out, totals, serve string) error {
	rep, err := Build(Options{Root: root, Out: out, TotalsLog: totals})
	if err != nil {
		return err
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if err := WriteReport(rep, out); err != nil {
		return err
	}
	fmt.Printf("portlens: wrote %s (open %s)\n", out, filepath.Join(out, "index.html"))
	if serve == "" {
		return nil
	}
	fmt.Printf("portlens: serving %s at http://%s/\n", out, serve)
	return newServer(serve, out).ListenAndServe()
}

// newServer serves the report directory read-only.
func newServer(addr, dir string) *http.Server {
	return &http.Server{Addr: addr, Handler: http.FileServer(http.Dir(dir)), ReadHeaderTimeout: 10 * time.Second}
}
