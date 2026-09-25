// Package sessiontesting holds runner-independent session test fixtures and
// storage decorators shared by every backend's conformance and benchmarks.
package sessiontesting

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// StorageFixture is a fresh backend storage instance owned by one test or
// benchmark case.
type StorageFixture struct {
	Storage session.Storage
	Close   func() error
}

// ConformanceCase is a runner-independent conformance case; register each
// with t.Run(testCase.Group+"/"+testCase.Name, testCase.Run).
type ConformanceCase struct {
	Group string
	Name  string
	Run   func(t *testing.T)
}

// RunConformance registers every case as a subtest grouped by Group.
func RunConformance(t *testing.T, cases []ConformanceCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.Group+"/"+testCase.Name, testCase.Run)
	}
}
