package ai

import (
	"fmt"
	"strings"
	"testing"
)

func unsentText(i, size int) string {
	return fmt.Sprintf("// file %d\n", i) + strings.Repeat("func f() { return \"q\" } // <x> & y\n", size/36)
}

// A process that reads files and sends no request keeps the encodings of the last few reads and not the cache's whole limit.
func TestEncodingsMadeByReadsAreHeldToTheUnsentBudget(t *testing.T) {
	c := newQuotedStringCache(requestSpliceCacheBytes)
	const size = 50 << 10
	for i := range 2000 {
		if _, ok := c.quotePlain(unsentText(i, size)); !ok {
			t.Fatal("a plain text was not encoded")
		}
	}
	if c.held > requestSpliceUnsentBytes+3*size {
		t.Fatalf("held %d bytes after reads that no request used, want at most about %d", c.held, requestSpliceUnsentBytes)
	}
	if c.heldUnsent != c.held {
		t.Fatalf("heldUnsent %d, held %d: every entry here is unsent", c.heldUnsent, c.held)
	}
	if got, want := c.pending.Len(), c.recency.Len(); got != want {
		t.Fatalf("%d unsent entries listed for %d entries", got, want)
	}
}

// A request build that uses an unsent entry makes it an ordinary one: it is not trimmed by later reads, and what the request sends stays encoded.
func TestARequestMakesAnUnsentEncodingOrdinary(t *testing.T) {
	c := newQuotedStringCache(requestSpliceCacheBytes)
	const size = 50 << 10
	kept := unsentText(-1, size)
	if _, ok := c.quotePlain(kept); !ok {
		t.Fatal("not encoded")
	}
	c.beginRequest()
	if quoted := c.quote(kept); len(quoted) == 0 {
		t.Fatal("no encoding")
	}
	if c.heldUnsent != 0 || c.pending.Len() != 0 {
		t.Fatalf("after a request used it: heldUnsent %d, pending %d", c.heldUnsent, c.pending.Len())
	}
	for i := range 500 {
		c.quotePlain(unsentText(i, size))
	}
	if _, held := c.known(kept); !held {
		t.Fatal("reads after the request forgot the entry the request used")
	}
	if c.heldUnsent > requestSpliceUnsentBytes+2*size {
		t.Fatalf("unsent bytes %d beyond the budget", c.heldUnsent)
	}
}
