//go:build race

package ai

import (
	"iter"
	"runtime"
)

// pull is iter.Pull for the race detector's builds, built on a goroutine and two unbuffered channels.
//
// The race runtime leaks the detector's per-goroutine state of every goroutine that ends through iter.Pull: a coroutine exits through coroexit -> coroswitch_m -> gdestroy
// (runtime/coro.go), which never calls racegoend, as goexit1 does for every other goroutine (runtime/proc.go). Each iter.Pull coroutine then keeps its ThreadState and the trace
// parts it filled for the life of the process, and the state of a coroutine that ran a stream's turns, readiness reads and async generators is ~50 KB. Every provider stream uses five
// coroutines, so `go test -race ./ai`, whose oracle tests run 21,000 streams each, grew past 40 GiB and was killed (patching racegoend into coroexit of a copy of the Go 1.27.3
// toolchain keeps the same run under 2.1 GiB). A goroutine that ends normally releases its state.
//
// The contract is iter.Pull's: next resumes the sequence until its next yield, stop ends it, a panic or runtime.Goexit in the sequence reaches the caller of the next or stop
// that resumed it, a sequence that has returned yields nothing more, and the sequence does not start before the first next or stop. The channels give the race detector the same
// happens-before edges between the caller and the sequence as iter.Pull's race annotations. Remove this file, and pull's non-race twin, once the Go runtime ends a coroutine's
// race context.
func pull[V any](seq iter.Seq[V]) (next func() (V, bool), stop func()) {
	p := &racePull[V]{seq: seq}
	return p.next, p.stop
}

type racePullEvent[V any] struct {
	value      V
	finished   bool
	panicked   bool
	panicValue any
	goexit     bool
}

type racePull[V any] struct {
	seq     iter.Seq[V]
	started bool
	done    bool
	resume  chan bool
	events  chan racePullEvent[V]
}

// run is the sequence's goroutine. It starts on the first resume, and reports each yielded value, and at its end how the sequence ended.
func (p *racePull[V]) run() {
	end := racePullEvent[V]{finished: true}
	returned := false
	defer func() {
		if !returned {
			if recovered := recover(); recovered != nil {
				end.panicked, end.panicValue = true, recovered
			} else {
				end.goexit = true
			}
		}
		p.events <- end
	}()
	if !<-p.resume {
		returned = true
		return
	}
	p.seq(func(value V) bool {
		p.events <- racePullEvent[V]{value: value}
		return <-p.resume
	})
	returned = true
}

// switchTo resumes the sequence (or tells its pending yield to return false) and waits for its next event.
func (p *racePull[V]) switchTo(proceed bool) racePullEvent[V] {
	if !p.started {
		p.started = true
		p.resume = make(chan bool)
		p.events = make(chan racePullEvent[V])
		go p.run()
	}
	p.resume <- proceed
	return <-p.events
}

func (p *racePull[V]) propagate(end racePullEvent[V]) {
	if end.panicked {
		panic(end.panicValue)
	}
	if end.goexit {
		runtime.Goexit()
	}
}

func (p *racePull[V]) next() (V, bool) {
	var zero V
	if p.done {
		return zero, false
	}
	event := p.switchTo(true)
	if event.finished {
		p.done = true
		p.propagate(event)
		return zero, false
	}
	return event.value, true
}

func (p *racePull[V]) stop() {
	if p.done {
		return
	}
	p.done = true
	if !p.started {
		return
	}
	event := p.switchTo(false)
	// A sequence that yields again after stop is told false until it returns, as iter.Pull's yield does.
	for !event.finished {
		event = p.switchTo(false)
	}
	p.propagate(event)
}
