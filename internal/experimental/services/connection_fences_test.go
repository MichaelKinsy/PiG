package services

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func silentlyAttach(client *localClient, target *SessionTarget) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.attachment = target
}

// blockedReadiness opens an activated Session binding whose service readiness blocks until release, and starts WhenAttached for the current attachment.
func blockedReadiness(t *testing.T, client *localClient, local *localBinding) (source *SessionServiceSource, release func(), finished <-chan error) {
	t.Helper()
	source = CreateSessionServiceSource(client, localOptions(local))
	t.Cleanup(func() { requireModelsOK(t, source.Dispose(t.Context())) })
	binding, err := source.Open(openOptions())
	requireModelsOK(t, err)
	requireModelsOK(t, binding.Ready(t.Context()))
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	var calls atomic.Int32
	local.ready = func(ctx context.Context, complete func(error)) {
		if calls.Add(1) == 1 {
			once.Do(func() { close(entered) })
			local.readyTasks.Go(func() { complete(waitDone(ctx, unblock)) })
			return
		}
		complete(nil)
	}
	release = func() { releaseOnce.Do(func() { close(unblock) }) }
	t.Cleanup(release)
	result := make(chan error, 1)
	go func() { result <- source.WhenAttached("session", t.Context()) }()
	<-entered
	return source, release, result
}

// upstream: connection.ts:324-340,384-389. Readiness compares the complete server/Session/attachment identity, not only the local event revision: a client whose attachment identity changed without a listener event still fences the wait.
func TestSessionReadinessRejectsAttachmentIdentityChangedWithoutEvent(t *testing.T) {
	client := &localClient{state: "connected", attachment: &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "old"}}
	_, release, finished := blockedReadiness(t, client, &localBinding{})
	silentlyAttach(client, &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "new"})
	release()
	if err := <-finished; err == nil || err.Error() != "Session session was replaced while attaching" {
		t.Fatalf("stale readiness = %v", err)
	}
}

// upstream: connection.ts:#attachmentRevision. A re-emitted identical attachment is a new generation: the readiness wait that began before it is replaced even though the target compares equal.
func TestSessionReadinessRejectsReemittedIdenticalAttachment(t *testing.T) {
	target := SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}
	client := &localClient{state: "connected", attachment: new(target)}
	_, release, finished := blockedReadiness(t, client, &localBinding{})
	client.attach(new(target))
	release()
	if err := <-finished; err == nil || err.Error() != "Session session was replaced while attaching" {
		t.Fatalf("stale readiness = %v", err)
	}
}

// upstream: connection.ts:212-257. A transition that finished for a replaced generation neither publishes nor reports, even when the later generation's attachment compares equal.
func TestSessionStaleTransitionCompletionNeitherPublishesNorReports(t *testing.T) {
	target := SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}
	client := &localClient{state: "connected"}
	local := &localBinding{asyncRebind: true}
	reported := make(chan error, 4)
	options := localOptions(local)
	options.OnError = func(err error) { reported <- err }
	source := CreateSessionServiceSource(client, options)
	t.Cleanup(func() { requireModelsOK(t, source.Dispose(t.Context())) })
	binding, err := source.Open(openOptions())
	requireModelsOK(t, err)
	requireModelsOK(t, binding.Ready(t.Context()))
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var rebinds atomic.Int32
	local.rebind = func(ctx context.Context, bound bool) error {
		if !bound {
			return nil
		}
		if rebinds.Add(1) == 1 {
			close(entered)
			<-release
			return errors.New("obsolete hydration failed")
		}
		return nil
	}
	client.attach(new(target))
	<-entered
	attached := make(chan struct{})
	stop := source.Attachment.Subscribe(func(state SessionAttachmentState, _ context.Context, delivery ReplicatedStateDelivery) {
		if delivery.Kind == "update" && state.Status == "attached" {
			close(attached)
		}
	})
	client.attach(new(target))
	<-attached
	stop()
	releaseOnce.Do(func() { close(release) })
	source.tasks.Wait()
	if got := source.Attachment.Value(); got != (SessionAttachmentState{Status: "attached", SessionID: "session"}) {
		t.Fatalf("stale completion replaced current state: %#v", got)
	}
	select {
	case err := <-reported:
		t.Fatalf("stale completion reported %v", err)
	default:
	}
}

// upstream: connection.ts:244-256. An attachment transition that fails without a readiness wait still publishes degraded for the current generation and reports the failure.
func TestSessionTransitionFailurePublishesDegradedWithoutReadinessWait(t *testing.T) {
	failure := errors.New("hydrate failed")
	client := &localClient{state: "connected"}
	local := &localBinding{}
	reported := make(chan error, 1)
	options := localOptions(local)
	options.OnError = func(err error) { reported <- err }
	source := CreateSessionServiceSource(client, options)
	t.Cleanup(func() { requireModelsOK(t, source.Dispose(t.Context())) })
	binding, err := source.Open(openOptions())
	requireModelsOK(t, err)
	requireModelsOK(t, binding.Ready(t.Context()))
	local.rebind = func(context.Context, bool) error { return failure }
	client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"})
	if err := <-reported; !errors.Is(err, failure) {
		t.Fatalf("reported = %v", err)
	}
	source.tasks.Wait()
	if got := source.Attachment.Value(); got != (SessionAttachmentState{Status: "degraded", SessionID: "session"}) {
		t.Fatalf("state = %#v", got)
	}
}

// upstream: connection.ts:#whenAttached/#whenDetached/toServerConnectionState diagnostics.
func TestSourceDiagnosticsMatchUpstreamMessages(t *testing.T) {
	client := &localClient{state: "connected"}
	server := CreateServerServiceSource(client, localOptions())
	t.Cleanup(func() { requireModelsOK(t, server.Dispose(t.Context())) })
	client.connect("disconnected", nil)
	if got := server.Connection.Value(); got.Status != "disconnected" || got.Reason != "Client is disconnected" {
		t.Fatalf("disconnect without error = %#v", got)
	}
	session := CreateSessionServiceSource(&localClient{state: "connected", attachment: &SessionTarget{ServerID: "server", SessionID: "other", AttachmentID: "a"}}, localOptions())
	t.Cleanup(func() { requireModelsOK(t, session.Dispose(t.Context())) })
	if err := session.WhenAttached("session", t.Context()); err == nil || err.Error() != "Session session is not the current attachment" {
		t.Fatalf("WhenAttached mismatch = %v", err)
	}
	if err := session.WhenDetached(t.Context()); err == nil || err.Error() != "A Session is still attached" {
		t.Fatalf("WhenDetached while attached = %v", err)
	}
}

// upstream: connection.ts RoutedServiceBinding.ready. Activation completes once: later readiness waits do not repeat the source's attachment wait.
func TestSessionBindingActivationRunsOnce(t *testing.T) {
	client := &localClient{state: "connected", attachment: &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}}
	local := &localBinding{}
	var ready atomic.Int32
	local.ready = func(_ context.Context, complete func(error)) { ready.Add(1); complete(nil) }
	source := CreateSessionServiceSource(client, localOptions(local))
	t.Cleanup(func() { requireModelsOK(t, source.Dispose(t.Context())) })
	binding, err := source.Open(openOptions())
	requireModelsOK(t, err)
	requireModelsOK(t, binding.Ready(t.Context()))
	// First readiness: the binding's own wait plus the source's attachment wait.
	if got := ready.Load(); got != 2 {
		t.Fatalf("first readiness waits = %d, want 2", got)
	}
	requireModelsOK(t, binding.Ready(t.Context()))
	if got := ready.Load(); got != 3 {
		t.Fatalf("second readiness waits = %d, want 3 (activation not repeated)", got)
	}
}

// The source owns its copy of the attachment announced to the listener: a caller that mutates its value afterwards cannot change which generation completes.
func TestSessionAttachmentChangeSnapshotsAnnouncedTarget(t *testing.T) {
	client := &localClient{state: "connected"}
	silentlyAttach(client, &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"})
	local := &localBinding{asyncRebind: true}
	source := CreateSessionServiceSource(client, localOptions(local))
	t.Cleanup(func() { requireModelsOK(t, source.Dispose(t.Context())) })
	binding, err := source.Open(openOptions())
	requireModelsOK(t, err)
	requireModelsOK(t, binding.Ready(t.Context()))
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	local.rebind = func(context.Context, bool) error {
		close(entered)
		<-release
		return nil
	}
	announced := &SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}
	source.attachmentChanged(announced)
	<-entered
	announced.AttachmentID = "mutated"
	releaseOnce.Do(func() { close(release) })
	source.tasks.Wait()
	if got := source.Attachment.Value(); got != (SessionAttachmentState{Status: "attached", SessionID: "session"}) {
		t.Fatalf("state = %#v", got)
	}
}

// upstream: connection.ts:220-227. A catalogue response that arrives for a replaced generation does not replace the retained catalogue, even when the later generation's attachment compares equal.
func TestSessionStaleCatalogueResponseDoesNotReplaceRetainedCatalogue(t *testing.T) {
	target := SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"}
	stale, current := []ServiceCatalogueEntry{{ServiceID: "stale", Mode: "singleton"}}, []ServiceCatalogueEntry{{ServiceID: "current", Mode: "singleton"}}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	client := &localClient{state: "connected", asyncCatalogue: true, catalogue: func(context.Context, ServiceTarget) ([]ServiceCatalogueEntry, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return stale, nil
		}
		return current, nil
	}}
	source := CreateSessionServiceSource(client, localOptions())
	t.Cleanup(func() { requireModelsOK(t, source.Dispose(t.Context())) })
	client.attach(new(target))
	<-entered
	client.attach(new(target))
	retained := func() []ServiceCatalogueEntry {
		source.mu.Lock()
		defer source.mu.Unlock()
		return source.catalogue
	}
	for len(retained()) == 0 {
		runtime.Gosched()
	}
	releaseOnce.Do(func() { close(release) })
	client.catalogueTasks.Wait()
	if got := retained(); len(got) != 1 || got[0].ServiceID != "current" {
		t.Fatalf("retained catalogue = %+v, want the current generation's", got)
	}
}
