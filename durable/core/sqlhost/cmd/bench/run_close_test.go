// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/harness"
)

// A run that fails after opening its session still closes it: the session's host goroutine must not outlive the run. A refused
// fixture used to return from finish before Close, and the leaked host read the clock the next run reset (a data race under -race).
func TestARunThatFailsClosesItsSession(t *testing.T) {
	refused := errors.New("fixture refused")
	o := k8sOptions(filepath.Join(t.TempDir(), "store.sqlite"), workloadStandard, func(context.Context, harness.Harness, *script, measured) error {
		return refused
	})
	o.turns = 1
	if err := run(context.Background(), o); !errors.Is(err, refused) {
		t.Fatalf("run = %v, want the finish error", err)
	}
	// Close waits for the host's done channel, which run closes in its last deferred call, so the goroutine is gone at most a
	// scheduling step later.
	deadline := time.Now().Add(5 * time.Second)
	for hostGoroutines() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d sqlhost host goroutines still run after run returned", hostGoroutines())
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

// hostGoroutines counts goroutines running sqlhost's (*Host).run. The package's tests do not run in parallel, so any is a leak.
func hostGoroutines() int {
	buf := make([]byte, 1<<22)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "sqlhost.(*Host).run(")
}
