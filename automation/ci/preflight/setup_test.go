// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if want := []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second}; !slices.Equal(defaultRetryDelays, want) || mergeableRetryDelay != 3*time.Second {
		fmt.Fprintf(os.Stderr, "retry delays %v and %v, want %v and 3s\n", defaultRetryDelays, mergeableRetryDelay, want)
		os.Exit(1)
	}
	defaultRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	mergeableRetryDelay = time.Millisecond
	os.Exit(m.Run())
}
