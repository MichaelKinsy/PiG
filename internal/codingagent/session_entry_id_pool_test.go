package codingagent

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
)

// countingRandom fills each read with the next bytes of a counting sequence and counts the reads.
type countingRandom struct {
	next  byte
	reads int
	sizes []int
	err   error
}

func (r *countingRandom) Read(b []byte) (int, error) {
	r.reads++
	r.sizes = append(r.sizes, len(b))
	if r.err != nil {
		return 0, r.err
	}
	for i := range b {
		b[i] = r.next
		r.next++
	}
	return len(b), nil
}

// TestEntryIDPoolDrawsFromOneReadPer64IDs pins how entry IDs consume randomness: one 256-byte read serves 64 consecutive IDs, each takes the
// next four bytes in order, and the 65th ID reads again. An ID is still four random bytes; only the number of getrandom calls changes.
func TestEntryIDPoolDrawsFromOneReadPer64IDs(t *testing.T) {
	source := &countingRandom{}
	pool := &entryIDPool{fill: source.Read}
	var want [256]byte
	for i := range want {
		want[i] = byte(i)
	}
	for n := range 64 {
		var b [4]byte
		if err := pool.read(b[:]); err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(b[:]); got != hex.EncodeToString(want[n*4:n*4+4]) {
			t.Fatalf("id %d = %q, want %q", n, got, hex.EncodeToString(want[n*4:n*4+4]))
		}
	}
	if source.reads != 1 || source.sizes[0] != 256 {
		t.Fatalf("64 ids made %d reads of sizes %v, want one read of 256 bytes", source.reads, source.sizes)
	}
	var b [4]byte
	if err := pool.read(b[:]); err != nil {
		t.Fatal(err)
	}
	// The second read continues the source's counting sequence: byte 256 wraps to 0.
	if got := hex.EncodeToString(b[:]); source.reads != 2 || got != "00010203" {
		t.Fatalf("id 65 = %q after %d reads, want %q after 2", got, source.reads, "00010203")
	}
}

// TestEntryIDPoolSurfacesARefillErrorAndRetries pins the failure path: a failed refill is the ID's error, hands out no stale bytes, and the next
// ID tries the source again.
func TestEntryIDPoolSurfacesARefillErrorAndRetries(t *testing.T) {
	boom := errors.New("entropy exhausted")
	source := &countingRandom{err: boom}
	pool := &entryIDPool{fill: source.Read}
	var b [4]byte
	if err := pool.read(b[:]); !errors.Is(err, boom) {
		t.Fatalf("first read = %v, want %v", err, boom)
	}
	if err := pool.read(b[:]); !errors.Is(err, boom) || source.reads != 2 {
		t.Fatalf("second read = %v after %d reads, want the error and a second attempt", err, source.reads)
	}
	source.err = nil
	if err := pool.read(b[:]); err != nil || hex.EncodeToString(b[:]) != "00010203" {
		t.Fatalf("read after recovery = %q, %v; want the first bytes of the recovered source", hex.EncodeToString(b[:]), err)
	}
}

// TestEntryIDsTakeAnInstalledRandomSourceByteForByte pins that a crypto/rand.Reader installed by a test supplies each ID itself: the pool never
// sits between that source and the ID, so a fixture reader decides the IDs exactly as it did before the pool existed.
func TestEntryIDsTakeAnInstalledRandomSourceByteForByte(t *testing.T) {
	previous := rand.Reader
	rand.Reader = bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	t.Cleanup(func() { rand.Reader = previous })
	for _, want := range []string{"01020304", "05060708"} {
		id, err := generateEntryID()
		if err != nil || id != want {
			t.Fatalf("id = %q, %v; want %q", id, err, want)
		}
	}
}

// TestGenerateEntryIDUsesThePoolWithTheSystemSource pins that, with the system source in place, IDs come from the process pool: 64 IDs take one
// refill.
func TestGenerateEntryIDUsesThePoolWithTheSystemSource(t *testing.T) {
	source := &countingRandom{}
	entryIDRandom.mu.Lock()
	fill, next := entryIDRandom.fill, entryIDRandom.next
	entryIDRandom.fill, entryIDRandom.next = source.Read, 0
	entryIDRandom.mu.Unlock()
	t.Cleanup(func() {
		entryIDRandom.mu.Lock()
		entryIDRandom.fill, entryIDRandom.next = fill, next
		entryIDRandom.mu.Unlock()
	})
	seen := map[string]bool{}
	for range 64 {
		id, err := generateEntryID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 8 || seen[id] {
			t.Fatalf("id %q is not a fresh eight-character ID", id)
		}
		seen[id] = true
	}
	if source.reads != 1 {
		t.Fatalf("64 ids made %d refills, want 1", source.reads)
	}
}
