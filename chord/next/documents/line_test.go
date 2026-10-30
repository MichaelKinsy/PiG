package documents

import (
	"testing"
	"time"
)

// Ports packages/chord/test/next/documents/line.test.ts

// waitingIs waits until the line shows n holders that asked and wait: Pi's holders take their place at the call, which
// a goroutine does once it is scheduled.
func waitingIs(t *testing.T, line *Line, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for line.waitingHolders() != n {
		if time.Now().After(deadline) {
			t.Fatalf("waiting = %d, want %d", line.waitingHolders(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTheLineHandsItToEachHolderInTheOrderTheyAskedOnceThePreviousOneReleased(t *testing.T) {
	line := NewLine()
	var order []string
	first := line.Acquire()
	released := make(chan func(), 2)
	go func() {
		release := line.Acquire()
		order = append(order, "second")
		released <- release
	}()
	waitingIs(t, line, 1)
	third := make(chan struct{})
	go func() {
		release := line.Acquire()
		order = append(order, "third")
		release()
		close(third)
	}()
	waitingIs(t, line, 2)
	time.Sleep(10 * time.Millisecond)
	if len(order) != 0 {
		t.Fatalf("order before the first released = %v", order)
	}
	first()
	(<-released)()
	<-third
	if len(order) != 2 || order[0] != "second" || order[1] != "third" {
		t.Fatalf("order = %v, want [second third]", order)
	}
}

func TestTheLineIgnoresASecondReleaseOfTheSameHolder(t *testing.T) {
	line := NewLine()
	first := line.Acquire()
	secondHeld := make(chan func(), 1)
	go func() { secondHeld <- line.Acquire() }()
	waitingIs(t, line, 1)
	thirdHeld := make(chan struct{})
	go func() {
		line.Acquire()
		close(thirdHeld)
	}()
	waitingIs(t, line, 2)
	first()
	first()
	second := <-secondHeld
	select {
	case <-thirdHeld:
		t.Fatal("the third holder took the line while the second held it")
	case <-time.After(10 * time.Millisecond):
	}
	second()
	select {
	case <-thirdHeld:
	case <-time.After(5 * time.Second):
		t.Fatal("the third holder never took the line")
	}
}
