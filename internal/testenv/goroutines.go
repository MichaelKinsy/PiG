// SPDX-License-Identifier: MIT

package testenv

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// GoroutineBaseline is the set of goroutines alive when it was taken. A test takes one, runs an owner (a host, a daemon, a client, a harness) through its work and its Close, and then calls [GoroutineBaseline.RequireEnded]: any goroutine the owner started that is still alive fails the test. It complements [LeakedGoroutines], which only sees goroutines blocked on a primitive that nothing can reach: a goroutine that outlives its owner and still waits on a reachable channel, a timer, a socket or a child process is invisible to that profile.
type GoroutineBaseline struct {
	ids map[uint64]struct{}
}

// ignoredGoroutineFrames are frames of goroutines that no owner under test starts: the test framework and runtime services.
var ignoredGoroutineFrames = []string{
	"testing.tRunner",
	"testing.(*M).",
	"testing.runTests",
	"os/signal.signal_recv",
	"os/signal.loop",
	"runtime.ensureSigM",
	"runtime/trace.",
	"runtime.gcBgMarkWorker",
	"runtime.bgsweep",
	"runtime.bgscavenge",
	"runtime.forcegchelper",
	"runtime.runfinq",
	"runtime.runCleanups",
	"runtime.unique_runtime_registerUniqueMapCleanup",
}

// BaselineGoroutines records the goroutines alive now.
func BaselineGoroutines() GoroutineBaseline {
	baseline := GoroutineBaseline{ids: map[uint64]struct{}{}}
	for _, g := range liveGoroutines() {
		baseline.ids[g.id] = struct{}{}
	}
	return baseline
}

// Outliving returns the stack of each goroutine that was started after the baseline and is still alive, in goroutine-id order, without the test framework's and the runtime's own goroutines.
func (b GoroutineBaseline) Outliving() []string {
	var stacks []string
	for _, g := range liveGoroutines() {
		if _, existed := b.ids[g.id]; existed || ignoredGoroutine(g.stack) {
			continue
		}
		stacks = append(stacks, g.stack)
	}
	return stacks
}

// RequireEnded fails t when a goroutine started after the baseline is still alive once grace has passed. A goroutine leaves the runtime's table a moment after it returns, so the check polls until the set is empty or grace ends; the failure names every survivor's stack.
func (b GoroutineBaseline) RequireEnded(t testing.TB, grace time.Duration) {
	t.Helper()
	b.RequireAtMost(t, 0, grace)
}

// RequireAtMost fails t when more than limit goroutines started after the baseline are alive once grace has passed. A test that replaces an owner's instance (a reload) takes the live count of the first instance as the limit: the replacement must not add goroutines of its own on top of the first instance's.
func (b GoroutineBaseline) RequireAtMost(t testing.TB, limit int, grace time.Duration) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	for {
		survivors := b.Outliving()
		if len(survivors) <= limit {
			return
		}
		select { //portlint:allow golifetime deadline.C is the stop case: it returns or fails the test
		case <-ticker.C:
		case <-deadline.C:
			survivors = b.Outliving()
			if len(survivors) <= limit {
				return
			}
			t.Fatalf("%d goroutine(s) outlived their owner after %s (limit %d):\n\n%s", len(survivors), grace, limit, strings.Join(survivors, "\n\n"))
			return
		}
	}
}

type goroutineStack struct {
	id    uint64
	stack string
}

func ignoredGoroutine(stack string) bool {
	return slices.ContainsFunc(ignoredGoroutineFrames, func(frame string) bool { return strings.Contains(stack, frame) })
}

// liveGoroutines parses runtime.Stack's all-goroutine dump.
func liveGoroutines() []goroutineStack {
	buffer := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			buffer = buffer[:n]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	var goroutines []goroutineStack
	for block := range strings.SplitSeq(string(buffer), "\n\n") {
		header, _, _ := strings.Cut(block, "\n")
		rest, ok := strings.CutPrefix(header, "goroutine ")
		if !ok {
			continue
		}
		idText, _, _ := strings.Cut(rest, " ")
		id, err := strconv.ParseUint(idText, 10, 64)
		if err != nil {
			panic(fmt.Sprintf("testenv: goroutine header %q has no id", header))
		}
		goroutines = append(goroutines, goroutineStack{id: id, stack: strings.TrimSpace(block)})
	}
	return goroutines
}
