package services

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionReadinessRejectsWithoutWaitingForOtherBindings(t *testing.T) {
	failure := errors.New("hydrate failed")
	client := &localClient{state: "connected", attachment: &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}}
	first := &localBinding{ready: func(_ context.Context, complete func(error)) { complete(failure) }}
	released := make(chan struct{})
	second := &localBinding{}
	second.ready = func(ctx context.Context, complete func(error)) {
		second.readyTasks.Go(func() { <-ctx.Done(); close(released); complete(ctx.Err()) })
	}
	source := CreateSessionServiceSource(client, localOptions(first, second))
	for range 2 {
		if _, err := source.Open(openOptions()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := source.WhenAttached("session", ctx)
	if err == nil || err.Error() != failure.Error() || ctx.Err() != nil {
		t.Fatalf("Promise.all rejection waited for other bindings: %v (context=%v)", err, ctx.Err())
	}
	<-released
	if err := source.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionTransitionsBeginInAttachmentEventOrder(t *testing.T) {
	client := &localClient{state: "connected"}
	local := &localBinding{}
	source := CreateSessionServiceSource(client, localOptions(local))
	binding, err := source.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []bool{false}
	for range 100 {
		client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"})
		client.attach(nil)
		want = append(want, true, false)
	}
	if err := source.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := local.history(); !reflect.DeepEqual(got, want) {
		t.Fatalf("attachment event order lost: %v", got)
	}
}

func TestSessionDisposalDoesNotWaitForClientOwnedCatalogue(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	defer released.Do(func() { close(release) })
	client := &localClient{state: "connected", asyncCatalogue: true, catalogue: func(ctx context.Context, target ServiceTarget) ([]ServiceCatalogueEntry, error) {
		close(started)
		<-release
		return []ServiceCatalogueEntry{{ServiceID: AgentControllerID, Mode: "singleton"}}, nil
	}}
	defer func() { released.Do(func() { close(release) }); client.catalogueTasks.Wait() }()
	source := CreateSessionServiceSource(client, ServiceSourceOptions{})
	client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"})
	<-started
	done := make(chan error, 1)
	go func() { done <- source.Dispose(t.Context()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		released.Do(func() { close(release) })
		<-done
		t.Fatal("source disposal waited for the client-owned catalogue request")
	}
}

func TestSessionReadinessRejectsReplacedGeneration(t *testing.T) {
	client := &localClient{state: "connected", attachment: &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "old"}}
	local := &localBinding{}
	source := CreateSessionServiceSource(client, localOptions(local))
	defer func() {
		if err := source.Dispose(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	binding, err := source.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int32
	local.ready = func(ctx context.Context, complete func(error)) {
		if calls.Add(1) == 1 {
			close(entered)
			local.readyTasks.Go(func() { complete(waitDone(ctx, release)) })
			return
		}
		complete(nil)
	}
	finished := make(chan error, 1)
	go func() { finished <- source.WhenAttached("session", t.Context()) }()
	<-entered
	client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "new"})
	if err := source.WhenAttached("session", t.Context()); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := <-finished; err == nil || err.Error() != "Session session was replaced while attaching" {
		t.Fatalf("stale readiness = %v", err)
	}
}

func TestSessionDetachWaitsForReleaseAndCanBeCancelled(t *testing.T) {
	client := &localClient{state: "connected", attachment: &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}}
	local := &localBinding{}
	source := CreateSessionServiceSource(client, localOptions(local))
	defer func() {
		if err := source.Dispose(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	binding, err := source.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	local.asyncRebind = true
	local.rebind = func(ctx context.Context, bound bool) error {
		if !bound {
			close(entered)
			return waitDone(ctx, release)
		}
		return nil
	}
	client.attach(nil)
	<-entered
	if got := source.Attachment.Value().Status; got == "detached" {
		t.Fatal("detached before old bindings released")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := source.WhenDetached(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel wait = %v", err)
	}
	once.Do(func() { close(release) })
	if err := source.WhenDetached(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := source.Attachment.Value().Status; got != "detached" {
		t.Fatalf("after release = %q", got)
	}
}

func TestServerTransitionWrapsEvenOneFailure(t *testing.T) {
	failure := errors.New("rebind failed")
	client := &localClient{state: "connected"}
	local := &localBinding{}
	reported := make(chan error, 1)
	options := localOptions(local)
	options.OnError = func(err error) { reported <- err }
	source := CreateServerServiceSource(client, options)
	binding, err := source.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	local.rebind = func(context.Context, bool) error { return failure }
	client.connect("disconnected", nil)
	err = <-reported
	if !errors.Is(err, failure) || err.Error() != "" {
		t.Fatalf("server AggregateError = %v", err)
	}
	if err := source.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSourceDisposalJoinsFailuresAndRemovedBindingsStayRemoved(t *testing.T) {
	one, two := errors.New("first"), errors.New("second")
	client := &localClient{state: "connected"}
	removed, first, second := &localBinding{}, &localBinding{closeError: one}, &localBinding{closeError: two}
	source := CreateServerServiceSource(client, localOptions(removed, first, second))
	binding, err := source.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := binding.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := source.Open(openOptions()); err != nil {
			t.Fatal(err)
		}
	}
	client.connect("disconnected", nil)
	err = source.Dispose(t.Context())
	if !errors.Is(err, one) || !errors.Is(err, two) || err.Error() != "Failed to dispose server service source" {
		t.Fatalf("joined failure = %v", err)
	}
	if first.closed != 1 || second.closed != 1 || removed.closed != 1 {
		t.Fatalf("dispose counts = %d, %d, %d", first.closed, second.closed, removed.closed)
	}
	if got := removed.history(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("removed binding rebound: %v", got)
	}
}

func TestSourceDelegatesCatalogueRoutingAndAccess(t *testing.T) {
	ctx := t.Context()
	var targets []ServiceTarget
	client := &localClient{state: "connected", catalogue: func(got context.Context, target ServiceTarget) ([]ServiceCatalogueEntry, error) {
		if got != ctx {
			t.Fatal("catalogue context changed")
		}
		targets = append(targets, target)
		return []ServiceCatalogueEntry{}, nil
	}}
	local := &localBinding{}
	server := CreateServerServiceSource(client, localOptions(local))
	if _, err := server.Catalogue(ctx); err != nil {
		t.Fatal(err)
	}
	denied := errors.New("access denied")
	options := openOptions()
	options.AssertAccess = func() error { return denied }
	binding, err := server.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Use(Service{ID: AgentControllerID}); !errors.Is(err, denied) {
		t.Fatalf("access = %v", err)
	}
	if got := local.opts.GetTarget(); got == nil || *got != (ServiceTarget{ServerID: "server"}) || local.opts.Bound {
		t.Fatalf("server routing = %#v, bound=%v", got, local.opts.Bound)
	}
	if err := server.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	client.attachment = &SessionTarget{ServerID: "server", SessionID: "s", AttachmentID: "a"}
	session := CreateSessionServiceSource(client, localOptions(local))
	if _, err := session.Catalogue(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(targets, []ServiceTarget{{ServerID: "server"}, {ServerID: "server", SessionID: new("s"), AttachmentID: new("a")}}) {
		t.Fatalf("routes = %#v", targets)
	}
}

func BenchmarkSourceStatePublication(b *testing.B) {
	state := &SourceState[SessionAttachmentState]{}
	stop := state.Subscribe(func(SessionAttachmentState, context.Context, ReplicatedStateDelivery) {})
	defer stop()
	value := SessionAttachmentState{Status: "attached", SessionID: "representative-session"}
	b.ReportAllocs()
	for b.Loop() {
		state.replace(b.Context(), value)()
		if value.Status == "attached" {
			value.Status = "degraded"
		} else {
			value.Status = "attached"
		}
	}
}

func BenchmarkConnectionLifecycle(b *testing.B) {
	for b.Loop() {
		client := &localClient{state: "connected"}
		locals := make([]*localBinding, 8)
		for i := range locals {
			locals[i] = &localBinding{}
		}
		source := CreateSessionServiceSource(client, localOptions(locals...))
		for range locals {
			binding, err := source.Open(openOptions())
			if err != nil {
				b.Fatal(err)
			}
			if err := binding.Ready(b.Context()); err != nil {
				b.Fatal(err)
			}
		}
		client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"})
		if err := source.WhenAttached("session", b.Context()); err != nil {
			b.Fatal(err)
		}
		client.attach(nil)
		if err := source.WhenDetached(b.Context()); err != nil {
			b.Fatal(err)
		}
		if err := source.Dispose(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}
