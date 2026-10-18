//go:build gc && (linux || darwin) && (amd64 || arm64)

package goroutineid

import (
	"runtime"
	"runtime/metrics"
	"sync"
	"testing"
)

// offsetStateAtInit and proofAtInit are the proof's state after the package's initialization and before any test ran. A process
// that never calls Current (pig --version, an RPC server before its first request) must not pay for the proof.
var (
	offsetStateAtInit = offsetState.Load()
	proofAtInit       = proof.n
)

func TestTheProofDoesNotRunDuringInitialization(t *testing.T) {
	if offsetStateAtInit != 0 || proofAtInit != 0 {
		t.Fatalf("offsetState = %d with %d goroutines observed after package initialization, want 0 and 0", offsetStateAtInit, proofAtInit)
	}
}

// callFromGoroutines calls f on n goroutines that the test starts, one after another, so each has an ID of its own.
func callFromGoroutines(n int, f func()) {
	for range n {
		var done sync.WaitGroup
		done.Go(f)
		done.Wait()
	}
}

// The platforms this file builds on read the ID from the goroutine record once enough distinct goroutines have called Current.
// A runtime whose record the proof cannot prove would silently return Current to the stack header's cost, which grows with stack
// depth.
func TestCallsFromDistinctGoroutinesProveTheOffsetOfTheID(t *testing.T) {
	callFromGoroutines(proofGoroutines, func() { Current() })
	state := offsetState.Load()
	if state < offsetBias {
		t.Fatalf("offsetState = %d after %d goroutines: the proof found no offset, so Current formats the stack header on every call", state, proofGoroutines)
	}
	if offset := state - offsetBias; offset%8 != 0 || offset >= probeScanLimit {
		t.Fatalf("offset %d is not a word offset inside the scanned prefix", offset)
	}
}

// One goroutine, however often it calls, proves nothing: a word that equals its ID may be a coincidence.
func TestOneGoroutineProvesNothing(t *testing.T) {
	var p offsetProof
	id := stackID()
	for range 2 * proofGoroutines {
		if decided := p.observe(id, goidAt); decided != 0 {
			t.Fatalf("one goroutine decided the proof: %d", decided)
		}
	}
	if p.n != 1 {
		t.Fatalf("one goroutine counted %d times", p.n)
	}
}

// provedOffset proves the offset with goroutines the test starts and returns it.
func provedOffset(t *testing.T) uintptr {
	t.Helper()
	callFromGoroutines(proofGoroutines, func() { Current() })
	state := offsetState.Load()
	if state < offsetBias {
		t.Skipf("no proved offset (state %d)", state)
	}
	return uintptr(state - offsetBias)
}

// observeFromGoroutines runs p.observe with read on n goroutines the test starts and returns the decision.
func observeFromGoroutines(p *offsetProof, n int, read func(uintptr) uint64) int64 {
	var decided int64
	callFromGoroutines(n, func() { decided = p.observe(stackID(), read) })
	return decided
}

// Where no single word equals the ID on every goroutine, the proof reports none, and Current formats the header.
func TestNoUniqueOffsetIsNotTrusted(t *testing.T) {
	proved := provedOffset(t)
	if proved == 0 {
		t.Skip("the proved offset 0 leaves no scan prefix that excludes it")
	}
	// A prefix that ends before the ID's word holds no word equal to it on every goroutine.
	prefix := offsetProof{words: int(proved / 8)}
	if decided := observeFromGoroutines(&prefix, proofGoroutines, goidAt); decided != slowOffset {
		t.Fatalf("a scan of the first %d bytes decided %d, want slowOffset", proved, decided)
	}
	var full offsetProof
	if decided := observeFromGoroutines(&full, proofGoroutines, goidAt); decided != int64(proved)+offsetBias {
		t.Fatalf("a full scan decided %d, want the proved offset %d", decided, proved)
	}
}

// A word that equals the ID on the first goroutines, and not on a later one, is a coincidence, and the proof must not take it for
// the ID: the later goroutines are what tell the two apart.
func TestACoincidenceOnSomeGoroutinesIsNotTheID(t *testing.T) {
	proved := provedOffset(t)
	coincidence := proved + 8
	var calls int
	var mu sync.Mutex
	read := func(offset uintptr) uint64 {
		if offset == coincidence {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls < proofGoroutines {
				return stackID()
			}
			return 0
		}
		return goidAt(offset)
	}
	var p offsetProof
	if decided := observeFromGoroutines(&p, proofGoroutines, read); decided != int64(proved)+offsetBias {
		t.Fatalf("decided %d, want the proved offset %d: a word that equalled the ID on %d goroutines was taken for it", decided, proved, proofGoroutines-1)
	}
}

// With no proved offset Current returns the stack header's ID, which is what every call returns on the other platforms.
func TestCurrentWithoutAProvedOffsetParsesTheHeader(t *testing.T) {
	Current()
	proved := offsetState.Load()
	offsetState.Store(slowOffset)
	t.Cleanup(func() { offsetState.Store(proved) })
	if got, want := Current(), stackID(); got != want || got == 0 {
		t.Fatalf("Current() = %d with no proved offset, header %d", got, want)
	}
}

// The proof starts no goroutine of its own: every process would count them (TestPrintReadToolsProcessGate counted 16 more at
// b229d49c15, whose probe started its own). A whole proof from 17 goroutines creates exactly those 17.
func TestTheProofStartsNoGoroutine(t *testing.T) {
	saved := offsetState.Load()
	t.Cleanup(func() { offsetState.Store(saved) })
	offsetState.Store(0)
	proof = offsetProof{} // no other test runs in parallel, so nothing holds the proof's lock
	runtime.GC()          // the collector starts its background workers on its first cycle; start them before counting
	created := []metrics.Sample{{Name: "/sched/goroutines-created:goroutines"}}
	metrics.Read(created)
	before := created[0].Value.Uint64()
	callFromGoroutines(proofGoroutines, func() { Current() })
	metrics.Read(created)
	if got := created[0].Value.Uint64() - before; got != proofGoroutines {
		t.Fatalf("a proof from %d goroutines created %d goroutines, want only those %d", proofGoroutines, got, proofGoroutines)
	}
	if state := offsetState.Load(); state < offsetBias {
		t.Fatalf("offsetState = %d after the proof, want a proved offset", state)
	}
}
