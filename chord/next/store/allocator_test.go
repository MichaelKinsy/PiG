package store

import "testing"

// Ports packages/chord/test/next/store/allocator.test.ts: tests of the id allocator (slice 6b D3, §1.2): from 2,
// raised by explicit ids, saturating at the safe range.

func mintOne(t *testing.T, ids *IdAllocator) int64 {
	t.Helper()
	id, err := ids.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return id
}

func TestAllocator(t *testing.T) {
	t.Run("mints from its value, and an explicit id raises it above itself, never lowering it", func(t *testing.T) {
		ids := NewIdAllocator(FirstID)
		if first, second := mintOne(t, ids), mintOne(t, ids); first != 2 || second != 3 {
			t.Fatalf("minted %d, %d; want 2, 3", first, second)
		}
		ids.Raise(100)
		if id := mintOne(t, ids); id != 101 {
			t.Fatalf("minted %d after Raise(100), want 101", id)
		}
		ids.Raise(50)
		if ids.Value() != 102 {
			t.Fatalf("Value = %d, want 102", ids.Value())
		}
	})

	t.Run("saturates at Number.MAX_SAFE_INTEGER: that id may be written, every later mint is refused", func(t *testing.T) {
		ids := NewIdAllocator(MaxSafeInteger - 1)
		if id := mintOne(t, ids); id != MaxSafeInteger-1 {
			t.Fatalf("minted %d, want MaxSafeInteger-1", id)
		}
		if _, err := ids.Mint(); err == nil || err.Error() != "ID space is exhausted" {
			t.Fatalf("Mint = %v, want ID space is exhausted", err)
		}
		ids.Raise(MaxSafeInteger)
		if ids.Value() != MaxSafeInteger {
			t.Fatalf("Value = %d, want MaxSafeInteger", ids.Value())
		}
		if _, err := ids.Mint(); err == nil || err.Error() != "ID space is exhausted" {
			t.Fatalf("Mint = %v, want ID space is exhausted", err)
		}
	})
}
