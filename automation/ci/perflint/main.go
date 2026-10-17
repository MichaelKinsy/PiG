// SPDX-License-Identifier: MIT

// Command perflint finds known Go performance antipatterns in non-test code and ratchets them like anytrap and port-lint:
//
//	json-roundtrip     json.Marshal whose result is Unmarshalled in the same function (a clone or a convert)
//	marshal-compare    bytes.Equal or == on Marshal results, or reflect.DeepEqual followed by a Marshal in one function
//	compile-in-func    regexp.MustCompile/Compile or a template Parse inside a function body
//	history-clone      slices.Clone or append([]T(nil), x...) of a message slice
//	env-stat           os.Getenv, os.LookupEnv, os.Stat, os.Lstat (packages agent, ai, coding, extensions/sdk, internal/codingagent)
//	loop-format        fmt.Sprintf or string += inside a loop (same packages)
//	decode-own-encode  the JSON text of an encoder (Marshal*, MarshalJSON, *Stringify*) decoded again in the same function (Unmarshal*, Decode*, a json/jsontext NewDecoder); json.Marshal into json.Unmarshal stays json-roundtrip's
//	nested-marshal     a MarshalJSON method that calls json.Marshal or another MarshalJSON on anything but its receiver: the outer encoder validates and compacts that output again
//	pool-uncapped      sync.Pool.Put of a slice or buffer (anything with a slice field or a Cap method) in a function that never reads a capacity (cap or Cap): one huge payload stays pooled
//	timer-per-call     time.NewTimer/After/NewTicker/Tick or context.WithTimeout/WithDeadline in a hot function, or in a loop of the hot packages
//	chan-hop           a channel send or receive, range over a channel, make(chan), a go statement, time.AfterFunc or context.AfterFunc in a hot function: a goroutine hand-off per event
//	open-per-call      os.Open/OpenFile/Create/ReadFile/WriteFile/OpenInRoot in a hot function: a descriptor per event instead of one kept open
//
// A hot function runs once per streamed event, frame, keystroke or chunk. Mark it with a `//perf:hot` directive in its doc comment (an optional reason may follow); portlint's `//pig:hotpath` counts as hot too. Use `//perf:hot` for new marks: `//pig:hotpath` also enables portlint's hotpath passes and their baseline. A hot function's closures count as hot; the body of its `go` statements does not.
//
// A site is allowed by a `//perf:ok <reason>` comment on its line or the line above; the reason is required. Remaining sites are ratcheted in baseline.toml: a site it does not list fails, and an entry whose count fell fails too, so a fix lowers the baseline in the same change. Entries of every pattern except marshal-compare, compile-in-func, env-stat and loop-format carry a class: "cold[:<why>]", "hot-owned:<branch>" or "hot-kept:<reason>".
//
// Run with `make perf-lint`. -report prints every finding with file:line; -print prints baseline entries.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String("baseline", "", "baseline file (default <root>/automation/ci/perflint/baseline.toml)")
	report := flag.Bool("report", false, "print every finding with file:line; do not compare with the baseline")
	printBaseline := flag.Bool("print", false, "print every finding as a baseline entry")
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "perf-lint:", err)
		os.Exit(2)
	}
	if *baselinePath == "" {
		*baselinePath = filepath.Join(abs, "automation", "ci", "perflint", "baseline.toml")
	}
	os.Exit(run(abs, *baselinePath, patterns, *report, *printBaseline))
}
