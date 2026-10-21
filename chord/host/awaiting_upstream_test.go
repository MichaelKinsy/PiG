package host

// Ports packages/chord/test/awaiting.test.ts (Pi b45e0ebc): awaited gets. Host.Get and Ctx.Get return once the
// reference is ready; abort, the ctx closing and the host's ReadyTimeout fail them and release only that reference.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var awaitingSlow = Remote("t.e8-slow", RemoteDef{
	Version: 1, Input: tObject(), Host: NewHostNamed("server"),
	Variants: Fields[VariantDef]{{"main", VariantDef{State: Fields[Schema]{{"n", tNumber()}}}}},
})

// awaitingSlowFactory serves the Slow object: it goes live once gate opens, or ends when its object is disposed.
func awaitingSlowFactory(gate *requestsLatch) Factory {
	return func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.Resource("gate", ResourceDeclaration[any]{Version: 1, Open: func(options ResourceOpenOptions) (any, error) {
			// Pi's open returns a promise nothing waits on once its object is disposed; a Go open that blocks must also
			// end with its abort signal, or disposing the loading object would wait for it.
			select {
			case <-gate.ch:
			case <-options.Signal.Done():
			}
			return nil, nil
		}}); err != nil {
			return nil, err
		}
		n, err := self.State("n", tNumber(), float64(1), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main", "n", n), nil
	}
}

// awaitingSetup is a server whose Slow object goes live when open is called, and a browser whose link counts frames.
func awaitingSetup(t *testing.T, readyTimeout *time.Duration) (server, browser *HostImpl, link *countingLink, open func()) {
	t.Helper()
	gate := newRequestsLatch()
	t.Cleanup(gate.open)
	server = requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, awaitingSlow, awaitingSlowFactory(gate), ServeOptions{})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
		t.Fatal(err)
	}
	link = newCountingLink(up)
	browser = requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, ReadyTimeout: readyTimeout, Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	return server, browser, link, gate.open
}

type getResult struct {
	ref Ref
	err error
}

// getAsync is `host.get(...)` without its await. Upstream's get takes its reference synchronously (host.ts get: acquire,
// then awaitReady), so the reference is held when get returns its promise, whatever the caller does next. Hold is that
// synchronous part and the task started here is the await. goEntered around a whole Get is not that order: a Get whose
// first step waits for a busy host loop counts as entered before it has acquired, and an abort that follows can release
// the binding the get was about to share.
func getAsync(host *HostImpl, options *GetOptions) <-chan getResult {
	done := make(chan getResult, 1)
	ref, err := host.Hold(awaitingSlow, map[string]any{}, options)
	if err != nil {
		done <- getResult{err: err}
		return done
	}
	var signals []context.Context
	if options != nil && options.Signal != nil {
		signals = append(signals, options.Signal)
	}
	<-host.goEntered(func() {
		if err := awaitReady(host, ref.(RefCore), signals, host.readyTimeout); err != nil {
			done <- getResult{err: err}
			return
		}
		done <- getResult{ref: ref}
	})
	return done
}

// getAsync holds its reference when it returns, even while the host loop is busy: the loop is blocked, getAsync must
// not return before its acquire ran, and the hold frame goes out only after the loop is free again. (Before, a Get
// that waited for the loop counted as entered at once: the abort test in this file lost 8 of 24000 runs to the reference
// not being held yet when the first get was aborted.)
func TestAwaitingGetAsyncHoldsItsReferenceWhenItReturns(t *testing.T) {
	_, browser, link, open := awaitingSetup(t, nil)
	inLoop, freeLoop := make(chan struct{}), make(chan struct{})
	go browser.run(func() {
		close(inLoop)
		<-freeLoop
	})
	<-inLoop
	returned := make(chan (<-chan getResult), 1)
	go func() { returned <- getAsync(browser, nil) }()
	select {
	case <-returned:
		close(freeLoop)
		open() // the get it started would otherwise wait for its ready timeout before the cleanup can close the host
		t.Fatal("getAsync returned while the host loop was busy: its reference was not held yet")
	case <-time.After(100 * time.Millisecond):
	}
	close(freeLoop)
	second := <-returned
	waitFor(t, "the hold", func() bool { return link.countSent("hold") == 1 })
	open()
	if got := <-second; got.err != nil {
		t.Fatal(got.err)
	}
}

// awaiting.test.ts:44-62 "an aborted get rejects and releases only its reference; the binding stays for the others"
func TestAwaitingAnAbortedGetRejectsAndReleasesOnlyItsReferenceTheBindingStaysForTheOthers(t *testing.T) {
	_, browser, link, open := awaitingSetup(t, nil)
	abort, cancel := context.WithCancel(context.Background())
	first := getAsync(browser, &GetOptions{Signal: abort})
	second := getAsync(browser, nil)
	cancel()
	if got := <-first; !IsChordError(got.err, CodeAborted) {
		t.Fatalf("aborted get: %v", got.err)
	}
	browser.run(func() {})
	if got := link.countSent("release"); got != 0 {
		t.Fatalf("releases = %d: the other awaiting get keeps the hold", got)
	}
	open()
	got := <-second
	if got.err != nil {
		t.Fatal(got.err)
	}
	if value := requestsReadable(t, got.ref, "n").Value(); value != float64(1) {
		t.Fatalf("n = %v", value)
	}
	if got := link.countSent("hold"); got != 1 {
		t.Fatalf("holds = %d, want 1", got)
	}
	got.ref.Release()
	waitFor(t, "the release", func() bool { return link.countSent("release") == 1 })
}

// awaiting.test.ts:64-73 "a ctx.get still waiting when its call settles rejects aborted and releases its reference"
func TestAwaitingACtxGetStillWaitingWhenItsCallSettlesRejectsAbortedAndReleasesItsReference(t *testing.T) {
	_, browser, link, _ := awaitingSetup(t, nil)
	pending := make(chan error, 1)
	if _, err := browser.Root(func(ctx Ctx) (any, error) {
		go func() {
			_, err := ctx.Get(awaitingSlow, map[string]any{}, nil)
			pending <- err
		}()
		// The call settles while the get is waiting.
		waitFor(t, "the hold", func() bool { return link.countSent("hold") == 1 })
		return nil, nil
	}, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := <-pending; !IsChordError(err, CodeAborted) {
		t.Fatalf("ctx.Get after its call settled: %v", err)
	}
	waitFor(t, "the release", func() bool { return link.countSent("release") == 1 })
}

// awaiting.test.ts:75-83 "a get times out after readyTimeout with ready_timeout and releases its hold"
func TestAwaitingAGetTimesOutAfterReadyTimeoutWithReadyTimeoutAndReleasesItsHold(t *testing.T) {
	timeout := 30 * time.Millisecond
	_, browser, link, _ := awaitingSetup(t, &timeout)
	got := <-getAsync(browser, nil)
	var chordError *ChordError
	if !IsChordError(got.err, CodeUnavailable) || !errors.As(got.err, &chordError) || chordError.Reason != "ready_timeout" {
		t.Fatalf("timed out get: %v", got.err)
	}
	waitFor(t, "the release", func() bool { return link.countSent("release") == 1 })
}

// awaiting.test.ts:85-95 "ctx.get resolves hydrated; the call's reference is released when the call ends"
func TestAwaitingCtxGetResolvesHydratedTheCallsReferenceIsReleasedWhenTheCallEnds(t *testing.T) {
	_, browser, link, open := awaitingSetup(t, nil)
	open()
	value, err := browser.Root(func(ctx Ctx) (any, error) {
		slow, err := ctx.Get(awaitingSlow, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		return requestsReadable(t, slow, "n").Value(), nil
	}, RootOptions{})
	if err != nil || value != float64(1) {
		t.Fatalf("root = %v, %v", value, err)
	}
	waitFor(t, "the release", func() bool { return link.countSent("release") == 1 })
}
