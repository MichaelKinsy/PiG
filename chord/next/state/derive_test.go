package state_test

import (
	"reflect"
	"runtime"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/state/derive.test.ts, the describe "mapShared". Every other block runs derives on a
// ChordHost through self.derive, defineState and references; derive_own_test.go runs the same scenarios on Derive
// itself, and the host variants wait for the host, contracts and documents packages.

type element struct{ n int }

type note struct{ text string }

type noteWithTime struct {
	text string
	at   int
}

func TestMapShared(t *testing.T) {
	t.Run("maps an object element once while it stays the same object; primitives every time", func(t *testing.T) {
		var calls []any
		f := func(x any) int {
			calls = append(calls, x)
			if n, ok := x.(int); ok {
				return n
			}
			return x.(*element).n
		}
		mapped := state.MapShared(f)
		a, b := &element{n: 1}, &element{n: 2}
		if got, want := mapped([]any{a, b, 3}), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
			t.Fatalf("first map = %v, want %v", got, want)
		}
		c := &element{n: 2}
		if got, want := mapped([]any{a, c, 3}), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
			t.Fatalf("second map = %v, want %v", got, want)
		}
		if len(calls) != 5 || calls[0] != any(a) || calls[1] != any(b) || calls[2] != 3 || calls[3] != any(c) || calls[4] != 3 {
			t.Fatalf("calls = %v, want [a b 3 c 3]", calls)
		}
	})

	t.Run("attaches its cleanups to tiny elements allocated right after the cache", func(t *testing.T) {
		// runtime.AddCleanup refuses a cleanup whose closure points into the allocation block of its object; a small
		// pointer-free element shares a 16-byte tiny block with whatever was allocated just before it.
		for n := range 256 {
			mapped := state.MapShared(func(e *element) int { return e.n })
			e := &element{n: n}
			if got := mapped([]*element{e}); len(got) != 1 || got[0] != n {
				t.Fatalf("map = %v, want [%d]", got, n)
			}
		}
	})

	t.Run("keeps no element alive in its cache", func(t *testing.T) {
		mapped := state.MapShared(func(n *note) *note { return &note{text: n.text} })
		refs := func() (weak.Pointer[note], weak.Pointer[note]) {
			n := &note{text: "t"}
			result := mapped([]*note{n})[0]
			return weak.Make(n), weak.Make(result)
		}
		element, result := refs()
		nexttest.ExpectCollected(t, element, result)
		runtime.GC()
		if got := mapped([]*note{{text: "u"}}); len(got) != 1 || got[0].text != "u" {
			t.Fatalf("map after collection = %v", got)
		}
	})

	t.Run("returns the previous result object for an unchanged element", func(t *testing.T) {
		strip := state.MapShared(func(n *noteWithTime) *note { return &note{text: n.text} })
		n := &noteWithTime{text: "t", at: 1}
		first := strip([]*noteWithTime{n})[0]
		second := strip([]*noteWithTime{n, {text: "u", at: 2}})[0]
		if first != second {
			t.Fatal("the unchanged element was mapped again")
		}
	})
}
