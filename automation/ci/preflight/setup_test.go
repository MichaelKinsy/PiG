// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

// TestMain holds the retry delays to what was decided (three retries after 2, 4 and 6 seconds, one mergeable retry
// after 3) and then shortens them, so no test, nor a mutant that retries what it should not, waits for real.
func TestMain(m *testing.M) {
	if want := []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second}; !slices.Equal(defaultRetryDelays, want) || mergeableRetryDelay != 3*time.Second {
		fmt.Fprintf(os.Stderr, "retry delays %v and %v, want %v and 3s\n", defaultRetryDelays, mergeableRetryDelay, want)
		os.Exit(1)
	}
	defaultRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	mergeableRetryDelay = time.Millisecond
	os.Exit(m.Run())
}
