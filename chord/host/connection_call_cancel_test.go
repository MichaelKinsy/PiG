package host

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// connection.ts:337-358 Connection.call: a caller whose signal aborts while its call is in flight rejects at once with
// "aborted: call aborted" carrying the signal's reason, sends cancel so the provider's call is aborted (connection.ts
// :409 incoming abort, "cancelled by caller"), and a result that arrives afterwards is ignored.
func TestConnectionCallCancelSendsCancelRejectsAndIgnoresTheLateResult(t *testing.T) {
	slow := Remote("t.slowcall", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
		Methods: Fields[MethodSpec]{{"slow", Method(tNull(), tString())}, {"echo", Method(tString(), tString())}},
	}}}})
	entered := make(chan struct{}, 1)
	calleeAborted := make(chan error, 1)
	gate := newRequestsLatch()
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, slow, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf(
			"variant", "main",
			"slow", methodFunc(func(_ any, ctx Ctx) (any, error) {
				context.AfterFunc(ctx.Signal(), func() { calleeAborted <- context.Cause(ctx.Signal()) })
				entered <- struct{}{}
				gate.wait()
				return "late", nil
			}),
			"echo", methodFunc(func(text any, _ Ctx) (any, error) { return text, nil }),
		), nil
	}, ServeOptions{})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	// Opened before the hosts close (cleanups run last-in, first-out): a provider call still waiting must finish.
	t.Cleanup(gate.open)
	ref := requestsGet(t, browser, slow, requestsEmpty(), nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	signal, abort := context.WithCancelCause(context.Background())
	reason := errors.New("user gave up")
	result := make(chan error, 1)
	go func() {
		_, err := browser.Root(func(ctx Ctx) (any, error) { return callMember(ref, "slow", nil, ctx) }, RootOptions{Signal: signal})
		result <- err
	}()
	<-entered
	abort(reason)
	select {
	case err := <-result:
		var chordError *ChordError
		if !errors.As(err, &chordError) || chordError.Code != CodeAborted || chordError.Message != "aborted: call aborted" || !errors.Is(err, reason) {
			t.Fatalf("call = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled call did not reject")
	}
	select {
	case cause := <-calleeAborted:
		if !IsChordError(cause, CodeAborted) || cause.Error() != "aborted: cancelled by caller" {
			t.Fatalf("callee cause = %v", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the provider's call was not cancelled")
	}
	// The late result reaches a call that already settled: it is ignored, and the link keeps working.
	gate.open()
	if got := mustCall(t, ref, "echo", "still here", nil); got != "still here" {
		t.Fatalf("echo = %v", got)
	}
}
