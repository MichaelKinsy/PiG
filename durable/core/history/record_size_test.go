// SPDX-License-Identifier: MIT

package history

import (
	"errors"
	"math"
	"testing"
)

func TestRecordSizeBounds(t *testing.T) {
	if err := checkRecordSize(0); err != nil {
		t.Fatalf("checkRecordSize(0) = %v, want nil", err)
	}
	if err := checkRecordSize(math.MaxInt32); err != nil {
		t.Fatalf("checkRecordSize(MaxInt32) = %v, want nil", err)
	}
	if err := checkRecordSize(-1); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("checkRecordSize(-1) = %v, want %v", err, ErrRecordTooLarge)
	}
	if math.MaxInt > math.MaxInt32 {
		if err := checkRecordSize(math.MaxInt32 + 1); !errors.Is(err, ErrRecordTooLarge) {
			t.Fatalf("checkRecordSize(MaxInt32+1) = %v, want %v", err, ErrRecordTooLarge)
		}
		if err := checkRecordSize(math.MaxUint32); !errors.Is(err, ErrRecordTooLarge) {
			t.Fatalf("checkRecordSize(MaxUint32) = %v, want %v", err, ErrRecordTooLarge)
		}
	}

	if !chunkFits(100, 1024, 500) {
		t.Fatal("chunkFits(100, 1024, 500) = false, want true")
	}
	if chunkFits(900, 1024, 200) {
		t.Fatal("chunkFits(900, 1024, 200) = true, want false when exceeding capacity")
	}
	if !chunkFits(math.MaxInt32-10, math.MaxInt32, 10) {
		t.Fatal("chunkFits(MaxInt32-10, MaxInt32, 10) = false, want true at exact MaxInt32 bound")
	}
	if math.MaxInt > math.MaxInt32 && chunkFits(math.MaxInt32-10, math.MaxInt32+100, 11) {
		t.Fatal("chunkFits(MaxInt32-10, MaxInt32+100, 11) = true, want false past MaxInt32")
	}

	if got, err := checkChunkSize(100, 200); err != nil || got != 300 {
		t.Fatalf("checkChunkSize(100, 200) = (%d, %v), want (300, nil)", got, err)
	}
	if got, err := checkChunkSize(math.MaxInt32-10, 10); err != nil || got != math.MaxInt32 {
		t.Fatalf("checkChunkSize(MaxInt32-10, 10) = (%d, %v), want (%d, nil)", got, err, math.MaxInt32)
	}
	if _, err := checkChunkSize(math.MaxInt32, 1); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("checkChunkSize(MaxInt32, 1) = %v, want %v", err, ErrRecordTooLarge)
	}
	if _, err := checkChunkSize(math.MaxUint32, 16); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("checkChunkSize(MaxUint32, 16) = %v, want %v", err, ErrRecordTooLarge)
	}
}

func TestReloadOversizedChunkSetsLoadErrWithoutPanic(t *testing.T) {
	st := NewStore()
	st.BeginLoad(1, 0, unbounded)
	if err := st.AddRow(1, 1, 1, []byte(`{"id":1,"kind":"note","text":"ok"}`)); err != nil {
		t.Fatalf("AddRow failed: %v", err)
	}
	if err := st.EndLoad(1); err != nil {
		t.Fatalf("EndLoad failed: %v", err)
	}
	ch := st.chainOf(1, false)
	ch.arena[0] = nil
	ch.ents[0].off = math.MaxInt32
	ch.ents[0].n = 16

	if got := ch.read(0, 0, 16); got != nil {
		t.Fatalf("read() on oversized evicted chunk = %q, want nil", got)
	}
	if err := st.Err(); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("st.Err() = %v, want %v", err, ErrRecordTooLarge)
	}
}

func TestReloadRecordLenMatchesRejectsNarrowingWraparound(t *testing.T) {
	if !reloadRecordLenMatches(32, 32) {
		t.Fatal("reloadRecordLenMatches(32, 32) = false, want true")
	}
	if reloadRecordLenMatches(32, 31) {
		t.Fatal("reloadRecordLenMatches(32, 31) = true, want false")
	}
	if reloadRecordLenMatches(0, -1) {
		t.Fatal("reloadRecordLenMatches(0, -1) = true, want false")
	}
	if math.MaxInt > math.MaxUint32 {
		wrapped := int(uint64(32) + (uint64(1) << 32))
		if reloadRecordLenMatches(32, wrapped) {
			t.Fatalf("reloadRecordLenMatches(32, %d) = true, want false for uint32 wraparound", wrapped)
		}
		if reloadRecordLenMatches(0, 1<<32) {
			t.Fatalf("reloadRecordLenMatches(0, %d) = true, want false for 4 GiB record", int(1<<32))
		}
	}
	if math.MaxInt > math.MaxInt32 {
		overInt32 := math.MaxInt32 + 1
		if reloadRecordLenMatches(uint32(overInt32), overInt32) {
			t.Fatalf("reloadRecordLenMatches(%d, %d) = true, want false above MaxInt32", overInt32, overInt32)
		}
	}
}
