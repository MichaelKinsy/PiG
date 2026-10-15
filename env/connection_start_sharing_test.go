package env

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Pi's #connect (packages/env/src/connection.ts:216-227) shares one #ready promise among concurrent requests, forgets a failed start so the next request starts again, and #teardown (:307-314) clears #ready so a request after a lost connection starts a new daemon session.
// mutation-checked: keeping a failed start in Connection.runStart, or starting again for every connect, fails it.
func TestConnectionSharesOneStartForgetsAFailedOneAndStartsAgainAfterALoss(t *testing.T) {
	daemon := daemonBinary(t)
	var starts atomic.Int32
	release := make(chan struct{})
	connection := NewConnection(ConnectionOptions{CommandFunc: func(lifetime context.Context) ([]string, error) {
		if starts.Add(1) == 1 {
			return nil, errors.New("not deployed yet")
		}
		select {
		case <-release:
		case <-lifetime.Done():
		}
		return []string{daemon}, nil
	}})
	t.Cleanup(connection.Close)

	if _, err := connection.Session(background); err == nil || !IsConnectionLost(err) {
		t.Fatalf("first start error = %v, want the command failure as a lost connection", err)
	}
	const callers = 8
	ids := make([]int, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for index := range callers {
		wg.Go(func() { ids[index], errs[index] = connection.Session(background) })
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	for index := range callers {
		if errs[index] != nil || ids[index] != ids[0] {
			t.Fatalf("caller %d: session %d, %v; want the shared session %d", index, ids[index], errs[index], ids[0])
		}
	}
	if got := starts.Load(); got != 2 {
		t.Fatalf("command computed %d times, want 2: one failed start, then one start shared by %d callers", got, callers)
	}

	connection.mu.Lock()
	lostSession := connection.session
	connection.mu.Unlock()
	_ = lostSession.cmd.Process.Kill()
	<-lostSession.exited
	for lostSession.isLive() {
		time.Sleep(time.Millisecond)
	}
	next, err := connection.Session(background)
	if err != nil || next == ids[0] {
		t.Fatalf("session after the loss = %d, %v; want a new session after %d", next, err, ids[0])
	}
	if got := starts.Load(); got != 3 {
		t.Fatalf("command computed %d times, want 3", got)
	}
}
