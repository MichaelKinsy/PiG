//go:build gc && (linux || darwin) && (amd64 || arm64)

package goroutineid

import "testing"

// offsetStateAtInit is the state after the package's initialization and before any test ran. The probe starts 16 goroutines and
// formats 17 stack headers, and a process that never calls Current (pig --version, an RPC server before its first request) must
// not pay for it.
var offsetStateAtInit = offsetState.Load()

func TestTheProbeDoesNotRunDuringInitialization(t *testing.T) {
	if offsetStateAtInit != 0 {
		t.Fatalf("offsetState = %d after package initialization, want 0: the probe ran before the first call of Current", offsetStateAtInit)
	}
}

// The platforms this file builds on read the ID from the goroutine record. A runtime whose record the probe cannot prove would
// silently return Current to the stack header's cost, which grows with stack depth.
func TestTheFirstCallProvesTheOffsetOfTheIDInTheRuntimeRecord(t *testing.T) {
	Current()
	state := offsetState.Load()
	if state < offsetBias {
		t.Fatalf("offsetState = %d: the probe could not prove an offset, so Current formats the stack header on every call", state)
	}
	if offset := state - offsetBias; offset%8 != 0 || offset >= probeScanLimit {
		t.Fatalf("offset %d is not a word offset inside the scanned prefix", offset)
	}
}

// Where no single word equals the ID on every probed goroutine, the probe reports none, and Current formats the header.
func TestNoUniqueOffsetIsNotTrusted(t *testing.T) {
	Current()
	proved := int(offsetState.Load() - offsetBias)
	if proved <= 0 {
		t.Skipf("the proved offset %d leaves no scan prefix that excludes it", proved)
	}
	// A prefix that ends before the ID's word holds no word equal to it on every goroutine.
	if offset := probeOffset(proved, goidAt); offset != -1 {
		t.Fatalf("a scan of the first %d bytes found offset %d, which is not the ID's", proved, offset)
	}
	if offset := probeOffset(probeScanLimit, goidAt); offset != proved {
		t.Fatalf("a full scan found offset %d, want the proved %d", offset, proved)
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

// A word that equals the ID on the goroutine that runs the probe, and on no other, is a coincidence, and the probe must not
// take it for the ID: the goroutines it starts are what tell the two apart.
func TestACoincidenceOnOneGoroutineIsNotTheID(t *testing.T) {
	Current()
	proved := uintptr(offsetState.Load() - offsetBias)
	coincidence, probing := proved+8, stackID()
	read := func(offset uintptr) uint64 {
		if offset == coincidence {
			if id := stackID(); id == probing {
				return id
			}
			return 0
		}
		return goidAt(offset)
	}
	// The word after the ID's reads as the ID on the probing goroutine, so on that goroutine alone two offsets match.
	if got := probeOffset(probeScanLimit, read); got != int(proved) {
		t.Fatalf("probe found offset %d, want %d: it took a word that equals the ID on one goroutine for the ID", got, proved)
	}
}
