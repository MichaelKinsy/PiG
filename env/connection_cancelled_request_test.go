package env

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Pi's Connection.request (packages/env/src/connection.ts:190-200) awaits #connect, which takes no signal, and #send (:230-251) then registers the request, calls onStart, writes it and only afterwards sends `cancel` for an aborted signal. A request whose signal is already aborted therefore always reaches the daemon. PiG chose at random between the started connection and the done context, and failed half of such requests with a synthesized `aborted` error without sending them.
// mutation-checked: restoring the `case <-ctx.Done()` branch of Connection.connect fails it.
func TestRequestWithACancelledContextIsStillSentOnAStartedConnection(t *testing.T) {
	t.Parallel()
	connection := daemonConnection(t)
	if _, err := connection.Session(background); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(background)
	cancel()
	target := t.TempDir()
	for attempt := range 200 {
		var started atomic.Bool
		_, _ = connection.Request(cancelled, "lstat", Json{"path": target}, RequestOptions{OnStart: func(uint32, int) { started.Store(true) }})
		if !started.Load() {
			t.Fatalf("attempt %d: the request with a cancelled context was not sent", attempt)
		}
	}
}

// Pi's #connect (packages/env/src/connection.ts:216-227) has no signal: a request whose signal aborts while the daemon starts waits for the start, then is sent and cancelled (:250). Its outcome comes from the daemon, or from the start when the start fails.
// mutation-checked: restoring the `case <-ctx.Done()` branch of Connection.connect fails it.
func TestRequestCancelledWhileTheDaemonStartsWaitsForTheStart(t *testing.T) {
	t.Parallel()
	daemon := daemonBinary(t)
	release := make(chan struct{})
	connection := NewConnection(ConnectionOptions{CommandFunc: func(lifetime context.Context) ([]string, error) {
		select {
		case <-release:
		case <-lifetime.Done():
		}
		return []string{daemon}, nil
	}})
	t.Cleanup(connection.Close)
	ctx, cancel := context.WithCancel(background)
	var started atomic.Bool
	type outcome struct {
		reply Reply
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		reply, err := connection.Request(ctx, "lstat", Json{"path": filepath.Dir(daemon)}, RequestOptions{OnStart: func(uint32, int) { started.Store(true) }})
		done <- outcome{reply, err}
	}()
	cancel()
	select {
	case result := <-done:
		t.Fatalf("the request settled before the daemon started: %+v, %v", result.reply.JSON, result.err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-done
	if !started.Load() {
		t.Fatal("the request was not sent after the daemon started")
	}
}
