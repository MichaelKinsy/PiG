// SPDX-License-Identifier: MIT

package cli

import (
	"flag"
	"os"
	"runtime"
	"strings"
	"testing"
)

// pig additive (D627): a process that runs the pig command samples no heap allocations unless it asked for a heap profile. The cli package
// imports internal/memprofrate for this; the test fails if the import goes.
func TestCommandProcessSamplesNoAllocations(t *testing.T) {
	if profile := flag.Lookup("test.memprofile"); profile != nil && profile.Value.String() != "" {
		t.Skip("go test -memprofile sets the rate itself")
	}
	if strings.Contains(os.Getenv("GODEBUG"), "memprofilerate") || strings.Contains(os.Getenv("PIG_PROFILE"), "heap") || strings.Contains(os.Getenv("PIG_PROFILE"), "allocs") {
		t.Skip("the environment asks for heap-profile sampling")
	}
	if runtime.MemProfileRate != 0 {
		t.Fatalf("runtime.MemProfileRate = %d, want 0", runtime.MemProfileRate)
	}
}
