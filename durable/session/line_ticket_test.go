package session_test

import (
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// A line ticket is a position taken now and used later, as upstream's queueMicrotask-scheduled commit takes its place
// in the publishing turn: a commit started afterwards queues behind it, however late the ticket's own job runs.
func TestLineTicketKeepsItsPositionAheadOfLaterCommits(t *testing.T) {
	harness := open()
	var order []string
	ticket := harness.Session.TakeLineTicket()
	if ticket == nil {
		t.Fatal("no ticket before close")
	}
	later := make(chan error, 1)
	go func() {
		later <- tryCommit(harness.Session, func(durable.Tx) error { order = append(order, "later"); return nil })
	}()
	waitLineJobs(harness, 2)
	if _, err := harness.Session.CommitWithTicket(ticket, ctx, func(*session.Transaction) (any, error) { order = append(order, "ticket"); return nil, nil }, session.TransactionScope{}); err != nil {
		t.Fatal(err)
	}
	if err := <-later; err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "ticket" || order[1] != "later" {
		t.Fatalf("commit order %v, want ticket then later", order)
	}
}

// A ticket given up unused keeps the jobs queued behind it waiting for the jobs queued before it.
func TestReleasedLineTicketStillWaitsForTheJobAheadOfIt(t *testing.T) {
	harness := open()
	var released atomic.Bool
	hold := make(chan struct{})
	inside := make(chan struct{})
	ahead := make(chan error, 1)
	go func() {
		_, err := harness.Session.ReadOnLine(func() (any, error) {
			close(inside)
			<-hold
			released.Store(true)
			return nil, nil
		})
		ahead <- err
	}()
	<-inside
	ticket := harness.Session.TakeLineTicket()
	behind := make(chan bool, 1)
	go func() {
		_ = tryCommit(harness.Session, func(durable.Tx) error { behind <- released.Load(); return nil })
	}()
	waitLineJobs(harness, 3)
	go ticket.Release()
	// While the job ahead is held, nothing behind the ticket may run.
	for range 20000 {
		select {
		case <-behind:
			t.Fatal("a job behind a released ticket ran before the job ahead of the ticket finished")
		default:
			runtime.Gosched()
		}
	}
	close(hold)
	<-behind
	if err := <-ahead; err != nil {
		t.Fatal(err)
	}
}

func TestLineTicketIsNotGrantedOnceCloseBegan(t *testing.T) {
	harness := open()
	if err := harness.Session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if ticket := harness.Session.TakeLineTicket(); ticket != nil {
		t.Fatal("a ticket was granted after close")
	}
}
