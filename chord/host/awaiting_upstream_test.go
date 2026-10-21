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

// getAsync is `host.get(...)` without its await: the call has run up to its first await, as upstream's synchronous part
// does, when it returns. A bare `go host.Get(...)` has no such order: the next call, or an abort that follows, can run
// before it has taken its reference.
func getAsync(host *HostImpl, options *GetOptions) <-chan getResult {
	done := make(chan getResult, 1)
	<-host.goEntered(func() {
		ref, err := host.Get(awaitingSlow, map[string]any{}, options)
		done <- getResult{ref, err}
	})
	return done
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
