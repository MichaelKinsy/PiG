package jsloop

import (
	"context"
	"slices"
	"testing"
)

func TestTurnsHoldCallbacksOfAChainUntilItTurns(t *testing.T) {
	type step struct {
		name string
		do   func(*Turns, *[]string)
	}
	macro := func(label string) func(*Turns, *[]string) {
		return func(turns *Turns, got *[]string) { turns.Macrotask(func() { *got = append(*got, label) }) }
	}
	var end func()
	var done func()
	tests := []struct {
		name  string
		steps []step
		want  []string
	}{
		{"no chain runs a callback at once", []step{{"a", macro("a")}, {"b", macro("b")}}, []string{"a", "b"}},
		{"a chain holds callbacks until it ends", []step{
			{"begin", func(turns *Turns, _ *[]string) { end = turns.Chain() }},
			{"a", macro("a")}, {"b", macro("b")},
			{"mark", func(_ *Turns, got *[]string) { *got = append(*got, "|") }},
			{"end", func(*Turns, *[]string) { end() }},
			{"c", macro("c")},
		}, []string{"|", "a", "b", "c"}},
		{"a turn releases the held callbacks and the chain goes on holding", []step{
			{"begin", func(turns *Turns, _ *[]string) { end = turns.Chain() }},
			{"a", macro("a")},
			{"turn", func(turns *Turns, _ *[]string) { turns.Turn() }},
			{"b", macro("b")},
			{"mark", func(_ *Turns, got *[]string) { *got = append(*got, "|") }},
			{"end", func(*Turns, *[]string) { end() }},
		}, []string{"a", "|", "b"}},
		{"an open wait passes callbacks and opening it releases the held ones", []step{
			{"begin", func(turns *Turns, _ *[]string) { end = turns.Chain() }},
			{"a", macro("a")},
			{"wait", func(turns *Turns, _ *[]string) { done = turns.Wait() }},
			{"b", macro("b")},
			{"done", func(*Turns, *[]string) { done() }},
			{"c", macro("c")},
			{"mark", func(_ *Turns, got *[]string) { *got = append(*got, "|") }},
			{"end", func(*Turns, *[]string) { end() }},
		}, []string{"a", "b", "|", "c"}},
		{"two chains hold until both end", []step{
			{"first", func(turns *Turns, _ *[]string) { end = turns.Chain() }},
			{"second", func(turns *Turns, _ *[]string) { done = turns.Chain() }},
			{"a", macro("a")},
			{"end first", func(*Turns, *[]string) { end() }},
			{"mark", func(_ *Turns, got *[]string) { *got = append(*got, "|") }},
			{"end second", func(*Turns, *[]string) { done() }},
		}, []string{"|", "a"}},
		{"ending a chain twice ends it once", []step{
			{"first", func(turns *Turns, _ *[]string) { end = turns.Chain() }},
			{"second", func(turns *Turns, _ *[]string) { _ = turns.Chain() }},
			{"end", func(*Turns, *[]string) { end(); end() }},
			{"a", macro("a")},
			{"mark", func(_ *Turns, got *[]string) { *got = append(*got, "|") }},
		}, []string{"|"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var turns Turns
			var got []string
			for _, step := range test.steps {
				step.do(&turns, &got)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("order %v, want %v", got, test.want)
			}
		})
	}
}

func TestTurnsKeepArrivalOrderWhenACallbackQueuesBehindAFlush(t *testing.T) {
	var turns Turns
	end := turns.Chain()
	var got []string
	turns.Macrotask(func() {
		got = append(got, "a")
		// Arrives while the flush runs: it queues behind the callbacks already held.
		turns.Macrotask(func() { got = append(got, "c") })
	})
	turns.Macrotask(func() { got = append(got, "b") })
	end()
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("order %v", got)
	}
	if turns.Held() != 0 {
		t.Fatalf("held %d", turns.Held())
	}
}

func TestNilTurnsHoldNothing(t *testing.T) {
	var turns *Turns
	ran := false
	turns.Chain()()
	turns.Wait()()
	turns.Turn()
	turns.Macrotask(func() { ran = true })
	if !ran || turns.Held() != 0 {
		t.Fatal("a nil Turns must run the callback at once")
	}
}

func TestTurnsTravelInTheContext(t *testing.T) {
	var turns Turns
	if TurnsFrom(context.Background()) != nil {
		t.Fatal("a bare context carries no Turns")
	}
	if got := TurnsFrom(WithTurns(context.Background(), &turns)); got != &turns {
		t.Fatalf("got %p, want %p", got, &turns)
	}
	if ctx := WithTurns(context.Background(), nil); TurnsFrom(ctx) != nil {
		t.Fatal("a nil Turns is not stored")
	}
}
