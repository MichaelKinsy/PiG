package codingagent_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type idleSnapshotContext struct {
	context.Context
	entered chan struct{}
	waiting chan struct{}
	calls   atomic.Int32
}

// Done marks the wait's select boundary, after it must have captured its Session.
func (ctx *idleSnapshotContext) Done() <-chan struct{} {
	switch ctx.calls.Add(1) {
	case 1:
		close(ctx.entered)
	case 2:
		close(ctx.waiting)
	}
	return ctx.Context.Done()
}

// Pi's teardownCurrent awaits the outgoing Session even if host UI state changes during the await.
func TestIdleWaitRetainsOutgoingSessionAcrossUIRebind(t *testing.T) {
	started, resume := make(chan struct{}), make(chan struct{})
	outgoing := newSessionPair(t, `{"retry":{"enabled":false}}`, 128000, nil, func() *ai.AssistantMessage {
		close(started)
		<-resume
		return reply("finished", ai.Usage{})()
	})
	incoming := newSessionPair(t, `{"retry":{"enabled":false}}`, 128000, nil)
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	t.Cleanup(func() { release(); _ = outgoing.session.Close() })
	if err := outgoing.session.Services().Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	if err := outgoing.session.SendExtensionUserMessage("captured work", nil); err != nil {
		t.Fatal(err)
	}
	<-started
	modeSettled := make(chan struct{})
	outgoing.harness.Do(func() { outgoing.harness.SetIdleWaitState(outgoing.session, modeSettled) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := &idleSnapshotContext{Context: ctx, entered: make(chan struct{}), waiting: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- outgoing.harness.WaitForSessionIdle(observed) }()
	<-observed.entered
	outgoing.harness.Do(func() { outgoing.harness.SetIdleWaitState(incoming.session, nil) })
	close(modeSettled)
	select {
	case <-observed.waiting:
	case err := <-done:
		t.Fatalf("idle wait followed the newly rebound idle Session: %v", err)
	}
	// The second select belongs to the captured, still-active Session. Cancellation must reach that waiter.
	cancel()
	if err := <-done; err == nil {
		t.Fatal("idle wait followed the newly rebound idle Session instead of its outgoing owner")
	}
	release()
	if err := outgoing.session.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
}
