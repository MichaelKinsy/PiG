// SPDX-License-Identifier: MIT

package history

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestAppendRecordRejectsOversizedRecord(t *testing.T) {
	prev := maxRecordLimit
	maxRecordLimit = 128
	t.Cleanup(func() { maxRecordLimit = prev })

	st := NewStore()
	st.BeginLoad(1, 0, unbounded)
	small := []byte(`{"id":1,"kind":"note","text":"ok"}`)
	if err := st.AddRow(1, 1, 1, small); err != nil {
		t.Fatalf("small AddRow failed: %v", err)
	}
	if err := st.EndLoad(1); err != nil {
		t.Fatalf("EndLoad failed: %v", err)
	}

	oversized := []byte(`{"id":2,"kind":"note","text":"` + strings.Repeat("x", 200) + `"}`)
	if err := st.AddRow(1, 2, 2, oversized); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("AddRow(oversized) = %v, want %v", err, ErrRecordTooLarge)
	}
	if err := st.Append(1, 2, 2, oversized); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("Append(oversized) = %v, want %v", err, ErrRecordTooLarge)
	}
	if ch := st.chainOf(1, false); ch == nil || len(ch.ents) != 1 {
		t.Fatalf("chain entries after rejected oversized appends = %v, want 1", ch)
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
