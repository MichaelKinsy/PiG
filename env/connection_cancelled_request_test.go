package env

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Pi main c96e368e2 (D304): Connection.request awaits untilAborted(#connect(), signal), so a request whose signal is already aborted rejects with code `aborted` and is never sent, also on a started connection.
func TestRequestWithACancelledContextIsNotSentAndIsAborted(t *testing.T) {
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
		_, err := connection.Request(cancelled, "lstat", Json{"path": target}, RequestOptions{OnStart: func(uint32, int) { started.Store(true) }})
		if started.Load() {
			t.Fatalf("attempt %d: the request with a cancelled context was sent", attempt)
		}
		if remote, ok := errors.AsType[*RemoteError](err); !ok || remote.Code != "aborted" {
			t.Fatalf("attempt %d: err = %v, want code aborted", attempt, err)
		}
	}
}

// Pi main c96e368e2 (D304): a request whose signal aborts while the daemon starts rejects at once with code `aborted`; the start goes on and the request is never sent.
func TestRequestCancelledWhileTheDaemonStartsIsAbortedAtOnce(t *testing.T) {
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
		if remote, ok := errors.AsType[*RemoteError](result.err); !ok || remote.Code != "aborted" {
			t.Fatalf("err = %v, want code aborted", result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not settle at once while the daemon started")
	}
	close(release)
	// The start went on: another request on the connection works.
	if _, err := connection.Info(background); err != nil {
		t.Fatalf("Info after the aborted request: %v", err)
	}
	if started.Load() {
		t.Fatal("the aborted request was sent")
	}
}
