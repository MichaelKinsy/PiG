// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package jv

import (
	"testing"
	"unsafe"
)

// A task record and every small object of a large task graph (U-harness-scheduler-retention's 20,000-task chain) cost the object's
// header: the two counters share a word, so it is keys, values, the index and that word, in words on any word size.
func TestAnObjectHeaderIsEightWords(t *testing.T) {
	const word = unsafe.Sizeof(uintptr(0))
	if got := unsafe.Sizeof(Object{}) / word; got != 8 {
		t.Fatalf("an Object is %d words, want 8: keep nIndex and dead in one word", got)
	}
	o := NewObject()
	for _, k := range []string{"0", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"} {
		o.Set(k, k)
	}
	o.Delete("a")
	if o.Len() != 13 || o.nIndex != 1 || o.dead != 1 {
		t.Fatalf("len %d, index keys %d, retired %d after one delete of 14", o.Len(), o.nIndex, o.dead)
	}
}

// SameOrdered is Equal that also demands the same member order, so a shared value serializes as the one it stands for.
func TestSameOrderedDemandsTheMemberOrderAtEveryDepth(t *testing.T) {
	build := func(keys ...string) *Object {
		o := NewObject()
		for _, k := range keys {
			o.Set(k, k)
		}
		return o
	}
	nested := func(inner *Object) *Object {
		o := NewObject()
		o.Set("list", []any{inner, 1.0})
		return o
	}
	a, same, swapped := build("x", "y"), build("x", "y"), build("y", "x")
	if !SameOrdered(a, same) || SameOrdered(a, swapped) || !Equal(a, swapped) {
		t.Fatal("order: SameOrdered must say yes to the same order, no to another, where Equal says yes to both")
	}
	if !SameOrdered(nested(a), nested(same)) || SameOrdered(nested(a), nested(swapped)) {
		t.Fatal("a member order inside an array is part of the value")
	}
	if SameOrdered(a, build("x")) || SameOrdered(a, "x") || !SameOrdered(1.0, 1.0) || SameOrdered(1.0, 2.0) {
		t.Fatal("different sizes, kinds and scalars")
	}
}
