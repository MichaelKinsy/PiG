// SPDX-License-Identifier: MIT

package runtimecell

import "testing"

// The budget reads the machine through pbnjay/memory. A reader that answers 0
// would silently leave every budget at the CPU count, so it must answer with
// the machine's memory on the platforms pig runs on.
func TestRustJobPhysicalMemoryReadsTheMachine(t *testing.T) {
	if got := rustJobPhysicalMemory(); got <= 0 {
		t.Fatalf("rustJobPhysicalMemory() = %d, want the machine's accessible physical memory", got)
	}
}
