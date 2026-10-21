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

func TestTurnsRunTicksAtTheTickBoundaryBeforeTimers(t *testing.T) {
	var turns Turns
	var got []string
	end := turns.Chain()
	done := turns.Wait()
	tick := func(label string) {
		turns.Tick(func(written func()) { got = append(got, label); written() })
	}
	// A wait is open, which releases timers but not ticks: they wait for the tool_execution_end that follows.
	turns.Macrotask(func() { got = append(got, "timer-while-waiting") })
	tick("tick-a")
	turns.Macrotask(func() { got = append(got, "timer-b") })
	turns.Ticked()
	if want := []string{"timer-while-waiting", "tick-a", "timer-b"}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	done()
	tick("tick-c")
	turns.Macrotask(func() { got = append(got, "timer-d") })
	end()
	if want := []string{"timer-while-waiting", "tick-a", "timer-b", "tick-c", "timer-d"}; !slices.Equal(got, want) {
		t.Fatalf("after the chain ended: order %v, want %v", got, want)
	}
}

func TestArmWaitsForTheRuntimesToApplyTheHold(t *testing.T) {
	var turns Turns
	var frames []bool
	applied := make(chan struct{})
	release := make(chan struct{})
	turns.ObserveDefer(func(deferring bool) { frames = append(frames, deferring) }, func(ctx context.Context) {
		close(applied)
		select {
		case <-release:
		case <-ctx.Done():
		}
	})
	armed := make(chan func(), 1)
	go func() { armed <- turns.Arm(context.Background()) }()
	<-applied
	if len(frames) != 1 || !frames[0] {
		t.Fatalf("frames sent before the wait = %v, want one hold", frames)
	}
	select {
	case <-armed:
		t.Fatal("Arm returned before the runtimes applied the hold")
	default:
	}
	close(release)
	end := <-armed
	end()
	if len(frames) != 2 || frames[1] {
		t.Fatalf("frames = %v, want hold then release", frames)
	}
}
