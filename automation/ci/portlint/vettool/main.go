// SPDX-License-Identifier: MIT

// Command vettool runs the portlint analyzers as a `go vet -vettool`, for an editor or an ad hoc run on one package:
//
//	go build -o /tmp/portlint-vet ./automation/ci/portlint/vettool
//	go vet -vettool=/tmp/portlint-vet ./ai/...
//
// It reports every finding. It does not read the baseline, so `make port-lint` stays the gate.
package main

import (
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/unitchecker"

	"github.com/MichaelKinsy/PiG/automation/ci/portlint/checks"
)

func main() {
	all := checks.All()
	analyzers := make([]*analysis.Analyzer, len(all))
	for i, check := range all {
		analyzers[i] = check.Analyzer
	}
	unitchecker.Main(analyzers...)
}
