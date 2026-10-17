// Package timings is the startup timing instrumentation Pi enables with PI_TIMING=1.
//
// Ports packages/coding-agent/src/core/timings.ts
package timings

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Label names a timing namespace (timings.ts TimingLabel).
type Label string

// The namespaces Pi records.
const (
	Main       Label = "main"
	Extensions Label = "extensions"
)

type entry struct {
	label string
	ms    int64
}

type namespace struct {
	timings  []entry
	lastTime int64
}

var (
	mu sync.Mutex
	// enabled is timings.ts ENABLED: read once, when the module loads.
	enabled = os.Getenv("PI_TIMING") == "1"
	// stderr is where console.error writes.
	stderr io.Writer = os.Stderr
	// namespaces is a JS Map: iteration follows first insertion, and set on an existing key keeps its place.
	order      []Label
	namespaces = map[Label]*namespace{}
	now        = func() int64 { return time.Now().UnixMilli() }
)

func resetLocked(label Label) {
	if _, ok := namespaces[label]; !ok {
		order = append(order, label)
	}
	namespaces[label] = &namespace{lastTime: now()}
}

// ResetTimings starts the namespace's timeline at the current time, discarding what it recorded (default namespace "main").
func ResetTimings(label ...Label) {
	if !enabled {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	resetLocked(pick(label))
}

// Time records the milliseconds since the previous mark in the namespace (default "main") under name.
func Time(name string, label ...Label) {
	if !enabled {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	at, ns := now(), pick(label)
	if _, ok := namespaces[ns]; !ok {
		resetLocked(ns)
	}
	n := namespaces[ns]
	n.timings = append(n.timings, entry{label: name, ms: at - n.lastTime})
	n.lastTime = at
}

func pick(label []Label) Label {
	if len(label) > 0 {
		return label[0]
	}
	return Main
}

func printGroup(title string, timings []entry) {
	var printable []entry
	for _, t := range timings {
		if t.ms >= 0 {
			printable = append(printable, t)
		}
	}
	if len(printable) == 0 {
		return
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\n--- %s ---\n", title)
	var total int64
	for _, t := range printable {
		fmt.Fprintf(&out, "  %s: %dms\n", t.label, t.ms)
		total += t.ms
	}
	fmt.Fprintf(&out, "  TOTAL: %dms\n", total)
	// console.error appends a newline after the dashes' own.
	fmt.Fprintf(&out, "%s\n\n", strings.Repeat("-", len(title)+8))
	_, _ = io.WriteString(stderr, out.String())
}

// PrintTimings writes every namespace's report to stderr, in the order the namespaces were first used.
func PrintTimings() {
	if !enabled {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for _, label := range order {
		printGroup("Startup Timings: "+string(label), namespaces[label].timings)
	}
}
