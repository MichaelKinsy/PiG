package goroutineid

import "testing"

func TestCurrentIdentifiesTheCallingGoroutine(t *testing.T) {
	here := Current()
	if here == 0 || Current() != here {
		t.Fatalf("Current() = %d, not stable and non-zero on one goroutine", here)
	}
	other := make(chan uint64)
	go func() { other <- Current() }()
	if id := <-other; id == 0 || id == here {
		t.Fatalf("another goroutine has ID %d, this one %d", id, here)
	}
}
