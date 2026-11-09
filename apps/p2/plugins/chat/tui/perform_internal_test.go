package tui

import (
	"sync"
	"testing"
	"time"
)

// Go-only (no Pi test): Pi starts every call at the moment its key is handled, so two actions reach the server in the order they were made. perform keeps that
// order with a turn per action: an action does not begin until the one before it has started its first call (finish) or ended, and its answer is awaited
// apart from the turn.
func TestPerformStartsActionsInTheOrderTheyWereMadeAndAwaitsTheirAnswersApart(t *testing.T) {
	surface := &ChatSurface{}
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	seen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), events...)
	}
	waitFor := func(event string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			for _, got := range seen() {
				if got == event {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%q never happened: %v", event, seen())
			}
			time.Sleep(time.Millisecond)
		}
	}
	prelude, answer, done := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
	// The first action is slow before it starts its call (a conversation that is not live yet); its call's answer is slower still.
	surface.perform(func(turn *turn) error {
		record("first begins")
		<-prelude
		record("first starts its call")
		turn.finish() // what turn.call does once the call is started
		<-answer
		record("first answered")
		done <- struct{}{}
		return nil
	})
	surface.perform(func(turn *turn) error {
		record("second begins")
		turn.finish()
		done <- struct{}{}
		return nil
	})
	waitFor("first begins")
	// The second action is made after the first: it does not begin while the first has not started its call.
	time.Sleep(100 * time.Millisecond)
	if got := seen(); len(got) != 1 {
		t.Fatalf("the second action began before the first started its call: %v", got)
	}
	close(prelude)
	// Once the first has started its call, the second begins although the first's answer is awaited still.
	waitFor("second begins")
	for _, got := range seen() {
		if got == "first answered" {
			t.Fatalf("the second action waited for the first's answer: %v", seen())
		}
	}
	close(answer)
	<-done
	<-done
	got := seen()
	want := []string{"first begins", "first starts its call", "second begins", "first answered"}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if i != 3 && got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

// An action that ends without starting a call lets the next one begin.
func TestPerformLetsTheNextActionBeginWhenAnActionEndsWithoutACall(t *testing.T) {
	surface := &ChatSurface{}
	began := make(chan struct{})
	surface.perform(func(*turn) error { return nil })
	surface.perform(func(*turn) error { close(began); return nil })
	select {
	case <-began:
	case <-time.After(10 * time.Second):
		t.Fatal("the second action never began")
	}
}
