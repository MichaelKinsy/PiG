// Package leakcheck finds goroutines that outlive what started them: a snapshot of the process's goroutines before the
// server starts, and a comparison after it closed. It is a diagnostic of the desktop app's shutdown (P2_DESKTOP_LEAKCHECK=1),
// not a test-only helper, because the shutdown it checks runs inside the framework's event loop that tests cannot start.
package leakcheck

import (
	"bytes"
	"fmt"
	"runtime/pprof"
	"strings"
	"time"
)

// Snapshot is the goroutines of the process, counted by what started them.
type Snapshot map[string]int

// framework goroutines are the framework's own (MyGo's loop, purego's callbacks), the os/signal loop that the first
// signal.Notify of the process starts (MyGo's quit on SIGTERM; it never ends), and Chord's idle
// handler coroutines, which the runtime stops when it collects their loop (p2test.NoLeakedGoroutines skips them for the
// same reason).
var framework = []string{"github.com/egoist/mygo", "github.com/ebitengine/purego", "host.newCoroutine", "os/signal.Notify"}

const root = "(root)"

type group struct {
	key   string
	stack string
}

func groups() []group {
	var profile bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 2); err != nil {
		return nil
	}
	var out []group
	for _, block := range strings.Split(profile.String(), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		key := ""
		lines := strings.Split(block, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if rest, ok := strings.CutPrefix(lines[i], "created by "); ok {
				key = rest
				break
			}
		}
		if key == "" {
			// Not started by a goroutine: the main one.
			key = root
		}
		out = append(out, group{key: key, stack: block})
	}
	return out
}

// Take records the goroutines running now.
func Take() Snapshot {
	snap := Snapshot{}
	for _, g := range groups() {
		snap[g.key]++
	}
	return snap
}

func ours(key string) bool {
	if key == root {
		return false
	}
	for _, prefix := range framework {
		if strings.Contains(key, prefix) {
			return false
		}
	}
	return true
}

// Check waits up to wait for the goroutines to settle back to before, and returns the stacks of those that did not: the
// ones whose starting function now runs more goroutines than it did, other than the framework's. Goroutines of a closed host
// end shortly after Close returns, so the count is polled.
func Check(before Snapshot, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		leaked := extra(before)
		if len(leaked) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d goroutines outlived the server:\n\n%s", len(leaked), strings.Join(leaked, "\n\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func extra(before Snapshot) []string {
	seen := Snapshot{}
	var leaked []string
	for _, g := range groups() {
		seen[g.key]++
		if ours(g.key) && seen[g.key] > before[g.key] {
			leaked = append(leaked, g.stack)
		}
	}
	return leaked
}
