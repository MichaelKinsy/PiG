package session

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/storage"
)

// A commit admitted while close listeners run waits for them; it must not hold the line mutex while it waits. The
// Harness scheduler takes a line ticket while holding its own mutex (kick, scheduleReconcileLocked), and its close
// listener (seal) takes that mutex, so a waiting admission that held the line mutex would close a cycle: the listener
// waits for the scheduler mutex, the scheduler waits for the line mutex, and the admission waits for the listener.
func TestAdmissionWaitingForCloseListenersDoesNotHoldTheLine(t *testing.T) {
	session := CreateSession(storage.NewMemoryStorage())
	var schedulerMu sync.Mutex
	started, proceed := make(chan struct{}), make(chan struct{})
	session.SubscribeClose(func() {
		close(started)
		<-proceed
	})
	session.SubscribeClose(func() {
		// The scheduler's seal.
		schedulerMu.Lock()
		schedulerMu.Unlock() //nolint:staticcheck // SA2001: the empty critical section is the lock acquisition seal performs.
	})
	closed := make(chan error, 1)
	go func() { closed <- session.Close(context.Background()) }()
	<-started

	// A commit that passed Commit's own check before close began reaches the line's admission while the listeners run.
	admitted := make(chan error, 1)
	go func() {
		_, err := enqueue(session, true, nil, func() (struct{}, error) { return struct{}{}, nil })
		admitted <- err
	}()
	// Wait until the admission is waiting; with the defect it holds the line mutex here.
	deadline := time.Now().Add(5 * time.Second)
	stacks := make([]byte, 1<<20)
	for !strings.Contains(string(stacks[:runtime.Stack(stacks, true)]), "session.enqueue[") {
		if time.Now().After(deadline) {
			t.Fatal("the admission never waited for the close listeners")
		}
		time.Sleep(time.Millisecond)
	}

	// The scheduler, holding its mutex, takes a line ticket (kick).
	ticketed := make(chan *LineTicket, 1)
	holding := make(chan struct{})
	go func() {
		schedulerMu.Lock()
		close(holding)
		ticket := session.TakeLineTicket()
		schedulerMu.Unlock()
		ticketed <- ticket
	}()
	<-holding
	close(proceed)

	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close deadlocked: an admission waiting for the close listeners held the line mutex")
	}
	if ticket := <-ticketed; ticket != nil {
		t.Fatal("a ticket taken once close began must be nil")
	}
	if err := <-admitted; err == nil {
		t.Fatal("a commit admitted after close began must be rejected")
	}
}
