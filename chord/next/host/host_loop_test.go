package host

import (
	"sync"
	"testing"
	"time"
)

func TestLoopRunsSectionsOneAtATimeAndNestsInline(t *testing.T) {
	loop := NewLoop()
	var inside, peak int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			loop.Run(func() {
				mu.Lock()
				inside++
				peak = max(peak, inside)
				mu.Unlock()
				loop.Run(func() { time.Sleep(time.Millisecond) })
				mu.Lock()
				inside--
				mu.Unlock()
			})
		})
	}
	wg.Wait()
	if peak != 1 {
		t.Fatalf("sections overlapped: %d at once", peak)
	}
}

func TestLoopQueueRunsAfterTheSectionInOrder(t *testing.T) {
	loop := NewLoop()
	var order []string
	loop.Run(func() {
		loop.Queue(func() {
			order = append(order, "a")
			loop.Queue(func() { order = append(order, "c") })
		})
		loop.Queue(func() { order = append(order, "b") })
		order = append(order, "section")
	})
	want := []string{"section", "a", "b", "c"}
	if len(order) != len(want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
}

func TestLoopQueueFromOutsideRunsOnASection(t *testing.T) {
	loop := NewLoop()
	done := make(chan bool, 1)
	loop.Queue(func() { done <- loop.onLoop() })
	select {
	case onLoop := <-done:
		if !onLoop {
			t.Fatal("a queued job ran outside a section")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued job never ran")
	}
	loop.Wait()
}

func TestLoopTimerRunsAsASectionAndStopPreventsIt(t *testing.T) {
	loop := NewLoop()
	ran := make(chan bool, 2)
	loop.AfterFunc(time.Millisecond, func() { ran <- loop.onLoop() })
	stopped := loop.AfterFunc(time.Millisecond, func() { ran <- false })
	if !stopped.Stop() {
		t.Fatal("Stop of a pending timer reported false")
	}
	if stopped.Stop() {
		t.Fatal("a second Stop reported true")
	}
	select {
	case onLoop := <-ran:
		if !onLoop {
			t.Fatal("a timer job ran outside a section")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the timer never fired")
	}
	time.Sleep(20 * time.Millisecond)
	if len(ran) != 0 {
		t.Fatal("a stopped timer ran")
	}
}

func TestNodeDelay(t *testing.T) {
	for _, c := range []struct{ in, want time.Duration }{
		{-time.Second, time.Millisecond}, {0, time.Millisecond}, {time.Millisecond / 2, time.Millisecond},
		{1500 * time.Microsecond, time.Millisecond}, {nodeTimeoutMax, nodeTimeoutMax},
		{nodeTimeoutMax + time.Millisecond, time.Millisecond}, {2 * time.Second, 2 * time.Second},
	} {
		if got := nodeDelay(c.in); got != c.want {
			t.Errorf("nodeDelay(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFakeLoopRunsTimersInTimeOrderWithTheClockAtTheirTime(t *testing.T) {
	loop := newFakeLoop()
	var seen []int64
	loop.AfterFunc(300*time.Millisecond, func() { seen = append(seen, loop.Now()) })
	loop.AfterFunc(100*time.Millisecond, func() {
		seen = append(seen, loop.Now())
		loop.AfterFunc(100*time.Millisecond, func() { seen = append(seen, loop.Now()) })
	})
	loop.Advance(250)
	if len(seen) != 2 || seen[0] != 100 || seen[1] != 200 || loop.Now() != 250 || loop.TimerCount() != 1 {
		t.Fatalf("seen %v now %d timers %d", seen, loop.Now(), loop.TimerCount())
	}
}
