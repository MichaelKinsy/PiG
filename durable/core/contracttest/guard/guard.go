// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package guard bounds a native host process of the conformance gate: a request that runs too long, a heap that grows past a
// limit, or a parent that has gone. A core step is a synchronous call, so a step that never returns (a scheduler loop with no
// hop to the host) also never lets its parent time out: the parent blocks in a read, the gate kills the parent, and the child
// keeps allocating until the host is out of memory. The guard ends the child itself, names the request and writes the stack of
// every goroutine, so the row fails with its cause. Host processes only; no core package imports it.
package guard

import (
	"fmt"
	"io"
	"os"
	"runtime/metrics"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"
)

// Environment variables a gate run passes to the child (the gate forwards DCORE_*).
const (
	EnvMemoryMB = "DCORE_CORE_MEM_LIMIT_MB"
	EnvStepSecs = "DCORE_CORE_STEP_TIMEOUT_S"
)

// Exit codes of a tripped guard. ExitBound is a runaway (reported on stderr); ExitParent is silent.
const (
	ExitBound  = 3
	ExitParent = 4
)

// Default bounds. The largest legitimate request (a cold open of the 3500-turn history) takes seconds and a few hundred MiB (W1-3500 stays under 384 MiB); four parallel hosts stay inside a lane's 12 GB.
const (
	DefaultMemoryMB = 2 * 1024
	DefaultStepSecs = 120
)

// Options of a Guard. A zero field disables that bound.
type Options struct {
	Name        string        // the process, for messages
	MemoryBytes uint64        // the most the Go runtime may hold from the system
	Step        time.Duration // the longest one request may run
	Parent      int           // the parent process ID; the process ends when it changes (re-parented to init)
	Poll        time.Duration // how often the bounds are checked (default 100 ms)
	Exit        func(code int)
	Stderr      io.Writer
}

// Guard watches one process.
type Guard struct {
	o     Options
	mu    sync.Mutex
	what  string
	since time.Time
	busy  bool
	stop  chan struct{}
	once  sync.Once
}

// FromEnv is the options a gate run sets in the environment, with the defaults above.
func FromEnv(name string) Options {
	return Options{
		Name:        name,
		MemoryBytes: uint64(envInt(EnvMemoryMB, DefaultMemoryMB)) << 20,
		Step:        time.Duration(envInt(EnvStepSecs, DefaultStepSecs)) * time.Second,
		Parent:      os.Getppid(),
	}
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
		return v
	}
	return fallback
}

// Start begins watching. The watcher is a goroutine of the host process; call Stop to end it.
func Start(o Options) *Guard {
	if o.Poll <= 0 {
		o.Poll = 100 * time.Millisecond
	}
	if o.Exit == nil {
		o.Exit = os.Exit
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	g := &Guard{o: o, stop: make(chan struct{})}
	//portlint:allow golifetime the watcher lives until Stop, which the process that started the guard calls once
	go g.watch()
	return g
}

// Stop ends the watcher. A second call does nothing.
func (g *Guard) Stop() { g.once.Do(func() { close(g.stop) }) }

// Begin marks a request as running; End marks it done. Only a running request is timed.
func (g *Guard) Begin(what string) {
	g.mu.Lock()
	g.what, g.since, g.busy = what, time.Now(), true
	g.mu.Unlock()
}

// End marks the running request done.
func (g *Guard) End() {
	g.mu.Lock()
	g.busy = false
	g.mu.Unlock()
}

func (g *Guard) watch() {
	t := time.NewTicker(g.o.Poll)
	defer t.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-t.C:
		}
		if g.o.Parent > 1 && os.Getppid() != g.o.Parent {
			// An ordinary end of the host (its process closes the pipes at about the same time): no report, the run did not fail.
			g.o.Exit(ExitParent)
			return
		}
		if limit := g.o.MemoryBytes; limit > 0 {
			if held := heldBytes(); held > limit {
				g.trip(ExitBound, fmt.Sprintf("the Go runtime holds %d MiB, over the %d MiB bound (%s)", held>>20, limit>>20, g.request()), true)
				return
			}
		}
		g.mu.Lock()
		busy, since, what := g.busy, g.since, g.what
		g.mu.Unlock()
		if g.o.Step > 0 && busy && time.Since(since) > g.o.Step {
			g.trip(ExitBound, fmt.Sprintf("request %s ran %s without returning, over the %s bound (heap %d MiB)", what, time.Since(since).Round(time.Millisecond), g.o.Step, heldBytes()>>20), true)
			return
		}
	}
}

func (g *Guard) request() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.busy {
		return "no request running"
	}
	return "request " + g.what
}

func (g *Guard) trip(code int, why string, stacks bool) {
	_, _ = fmt.Fprintf(g.o.Stderr, "%s: guard: %s; exiting with %d\n", g.o.Name, why, code)
	if stacks {
		// The goroutine that is looping names the code that does not return.
		_ = pprof.Lookup("goroutine").WriteTo(g.o.Stderr, 1)
	}
	g.o.Exit(code)
}

// heldBytes is the memory the Go runtime holds from the operating system (no stop-the-world read).
func heldBytes() uint64 {
	s := []metrics.Sample{{Name: "/memory/classes/total:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}
