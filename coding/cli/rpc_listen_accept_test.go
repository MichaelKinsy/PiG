package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// scriptedListener answers Accept from a list of errors, then returns conn.
type scriptedListener struct {
	net.Listener
	errs []error
	conn net.Conn
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	if len(l.errs) == 0 {
		return l.conn, nil
	}
	err := l.errs[0]
	l.errs = l.errs[1:]
	return nil, err
}

type temporaryError struct{}

func (temporaryError) Error() string   { return "temporary" }
func (temporaryError) Temporary() bool { return true }

func TestNextAcceptDelayDoublesFromFiveMillisecondsToOneSecond(t *testing.T) {
	var got []time.Duration
	var d time.Duration
	for range 11 {
		d = nextAcceptDelay(d)
		got = append(got, d)
	}
	want := []time.Duration{5, 10, 20, 40, 80, 160, 320, 640}
	for i, w := range want {
		if got[i] != w*time.Millisecond {
			t.Fatalf("delay %d = %v, want %v (net/http Server.Serve)", i, got[i], w*time.Millisecond)
		}
	}
	for _, d := range got[len(want):] {
		if d != time.Second {
			t.Fatalf("delay %v after the doubling, want the 1 s ceiling: %v", d, got)
		}
	}
}

func TestAcceptWithBackoffRetriesTransientErrors(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	errs := []error{&os.SyscallError{Syscall: "accept", Err: syscall.EMFILE}, syscall.ENFILE, syscall.ECONNABORTED, temporaryError{}}
	var delays []time.Duration
	conn, err := acceptWithBackoff(t.Context(), &scriptedListener{errs: errs, conn: server}, func(_ error, d time.Duration) { delays = append(delays, d) })
	if err != nil || conn != server {
		t.Fatalf("accept = %v, %v; want the connection after the transient errors", conn, err)
	}
	if want := []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}; !slices.Equal(delays, want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
}

func TestAcceptWithBackoffReturnsAPermanentError(t *testing.T) {
	boom := errors.New("accept: bad descriptor")
	_, err := acceptWithBackoff(t.Context(), &scriptedListener{errs: []error{syscall.EMFILE, boom}}, func(error, time.Duration) {})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the non-transient error", err)
	}
}

func TestAcceptWithBackoffStopsWhenTheServerStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	retried := make(chan struct{})
	listener := &scriptedListener{errs: []error{syscall.EMFILE, syscall.EMFILE, syscall.EMFILE}}
	done := make(chan error, 1)
	go func() {
		_, err := acceptWithBackoff(ctx, listener, func(error, time.Duration) { close(retried) })
		done <- err
	}()
	<-retried
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, syscall.EMFILE) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("acceptWithBackoff kept waiting after its context ended")
	}
}

func TestHoldForFurtherSignalsForwardsASignalAfterTheFanOut(t *testing.T) {
	second := make(chan os.Signal, 1)
	fanout, serverDone := make(chan struct{}), make(chan struct{})
	defer close(serverDone)
	forced := make(chan os.Signal, 1)
	close(fanout)
	holdForFurtherSignals(second, fanout, serverDone, func(s os.Signal) { forced <- s })
	second <- syscall.SIGTERM
	select {
	case s := <-forced:
		if s != syscall.SIGTERM {
			t.Fatalf("forced %v", s)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("a signal after the fan-out finished was not forwarded")
	}
}

func TestHoldForFurtherSignalsForwardsASignalDuringTheFanOut(t *testing.T) {
	second := make(chan os.Signal, 1)
	fanout, serverDone := make(chan struct{}), make(chan struct{})
	defer close(serverDone)
	forced := make(chan os.Signal, 1)
	second <- syscall.SIGHUP
	holdForFurtherSignals(second, fanout, serverDone, func(s os.Signal) { forced <- s })
	select {
	case s := <-forced:
		if s != syscall.SIGHUP {
			t.Fatalf("forced %v", s)
		}
	default:
		t.Fatal("a signal during the fan-out was not forwarded before the call returned")
	}
}
