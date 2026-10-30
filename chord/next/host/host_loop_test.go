package host

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
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

// lendLog is the order of what a lent handler and its section do.
type lendLog struct {
	mu    sync.Mutex
	order []string
}

func (l *lendLog) add(text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.order = append(l.order, text)
}

func (l *lendLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.order...)
}

// A section lent to a handler runs the handler's synchronous part before the rest of the section: the handler's calls into
// the loop run inline, the microtasks it queues run after the section, and once it parks (the signal is raised) its code is
// outside the section: its next Run waits for the section's end. Pi runs an async handler to its first `await` inside the
// section that calls it.
func TestLoopLendRunsAHandlersFirstPartInsideTheSectionAndGivesItBackWhenItParks(t *testing.T) {
	loop := NewLoop()
	var log lendLog
	resumed := make(chan struct{})
	inline := make(chan [2]bool, 1)
	loop.Run(func() {
		log.add("section")
		signal := parking.New()
		loop.Lend(signal, func() {
			log.add("handler")
			var during bool
			loop.Run(func() {
				during = loop.onLoop()
				log.add("handler's section")
			})
			loop.Queue(func() { log.add("microtask") })
			signal.Raise()
			// Parked: outside the section.
			inline <- [2]bool{during, loop.onLoop()}
			loop.Run(func() {
				log.add("handler resumed")
				close(resumed)
			})
		})
		log.add("rest of the section")
	})
	<-resumed
	want := []string{"section", "handler", "handler's section", "rest of the section", "microtask", "handler resumed"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := <-inline; got != [2]bool{true, false} {
		t.Fatalf("handler on the loop (before, after parking) = %v, want (true, false)", got)
	}
	loop.Wait()
}

// A handler that returns without parking gives the section back as it returns.
func TestLoopLendGivesTheSectionBackWhenTheHandlerReturns(t *testing.T) {
	loop := NewLoop()
	var log lendLog
	loop.Run(func() {
		signal := parking.New()
		loop.Lend(signal, func() {
			log.add("handler")
			signal.Raise()
		})
		log.add("rest of the section")
		if !loop.onLoop() {
			t.Error("the section is not the loop's owner after the handler returned")
		}
	})
	if got, want := log.all(), []string{"handler", "rest of the section"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	loop.Wait()
}

// Outside a section nothing is lent: the handler's sections wait for the loop as any goroutine's do, and Lend returns when
// the signal is raised.
func TestLoopLendOutsideASectionLendsNothing(t *testing.T) {
	loop := NewLoop()
	var log lendLog
	signal := parking.New()
	loop.Lend(signal, func() {
		loop.Run(func() { log.add("handler's section") })
		signal.Raise()
	})
	if got, want := log.all(), []string{"handler's section"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if loop.onLoop() {
		t.Fatal("the caller owns the loop outside a section")
	}
	loop.Wait()
}
