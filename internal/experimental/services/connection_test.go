package services

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

type localClient struct {
	mu                 sync.Mutex
	state              string
	attachment         *SessionTarget
	connectionListener func(string, error)
	attachmentListener func(*SessionTarget)
	catalogue          func(context.Context, ServiceTarget) ([]ServiceCatalogueEntry, error)
	asyncCatalogue     bool
	catalogueTasks     sync.WaitGroup
}

func (c *localClient) ServerID() string        { return "server" }
func (c *localClient) ConnectionState() string { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func (c *localClient) Attachment() *SessionTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attachment == nil {
		return nil
	}
	return new(*c.attachment)
}
func (c *localClient) ServiceCatalogue(ctx context.Context, target ServiceTarget, complete func([]ServiceCatalogueEntry, error)) {
	request := func() {
		if c.catalogue != nil {
			complete(c.catalogue(ctx, target))
		} else {
			complete([]ServiceCatalogueEntry{{ServiceID: AgentControllerID, Mode: "singleton"}}, nil)
		}
	}
	if c.asyncCatalogue {
		c.catalogueTasks.Go(request)
	} else {
		request()
	}
}
func (c *localClient) OnConnectionStateChange(listener func(string, error)) func() {
	c.connectionListener = listener
	return func() { c.mu.Lock(); defer c.mu.Unlock(); c.connectionListener = nil }
}
func (c *localClient) OnAttachmentChange(listener func(*SessionTarget)) func() {
	c.attachmentListener = listener
	return func() { c.mu.Lock(); defer c.mu.Unlock(); c.attachmentListener = nil }
}
func (c *localClient) connect(state string, err error) {
	c.mu.Lock()
	c.state = state
	listener := c.connectionListener
	c.mu.Unlock()
	if listener != nil {
		listener(state, err)
	}
}
func (c *localClient) attach(target *SessionTarget) {
	c.mu.Lock()
	c.attachment = target
	listener := c.attachmentListener
	c.mu.Unlock()
	if listener != nil {
		listener(target)
	}
}

type localBinding struct {
	mu          sync.Mutex
	opts        RemoteServiceBindingOptions
	bounds      []bool
	rebind      func(context.Context, bool) error
	asyncRebind bool
	rebindTasks sync.WaitGroup
	pending     *localTransition
	ready       func(context.Context, func(error))
	readyTasks  sync.WaitGroup
	closed      int
	closeError  error
}

func (b *localBinding) Use(service Service) (any, error) {
	if err := b.opts.AssertAccess(); err != nil {
		return nil, err
	}
	return service.ID, nil
}
func (b *localBinding) Observe(service Service, handler func(context.Context, any) error) (func(), error) {
	if err := handler(context.Background(), service.ID); err != nil {
		return nil, err
	}
	return func() {}, nil
}

type localTransition struct {
	done chan struct{}
	err  error
}

func (b *localBinding) Ready(ctx context.Context, complete func(error)) {
	b.mu.Lock()
	pending := b.pending
	b.mu.Unlock()
	request := func() {
		if pending != nil {
			if err := waitDone(ctx, pending.done); err != nil {
				complete(err)
				return
			}
			if pending.err != nil {
				complete(pending.err)
				return
			}
		}
		if b.ready != nil {
			b.ready(ctx, complete)
		} else {
			complete(ctx.Err())
		}
	}
	if pending != nil {
		select {
		case <-pending.done:
		default:
			b.readyTasks.Go(request)
			return
		}
	}
	request()
}
func (b *localBinding) Rebind(ctx context.Context, bound bool, complete func(error)) {
	b.mu.Lock()
	b.bounds = append(b.bounds, bound)
	pending := &localTransition{done: make(chan struct{})}
	b.pending = pending
	b.mu.Unlock()
	request := func() {
		if b.rebind != nil {
			pending.err = b.rebind(ctx, bound)
		}
		close(pending.done)
		complete(pending.err)
	}
	if b.asyncRebind {
		b.rebindTasks.Go(request)
	} else {
		request()
	}
}
func (b *localBinding) Dispose(context.Context) error {
	b.rebindTasks.Wait()
	b.readyTasks.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed++
	return b.closeError
}
func (b *localBinding) history() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.bounds...)
}
func localOptions(bindings ...*localBinding) ServiceSourceOptions {
	next := 0
	return ServiceSourceOptions{NewBinding: func(options RemoteServiceBindingOptions) RemoteServiceBinding {
		b := bindings[next]
		next++
		b.opts = options
		return b
	}}
}
func openOptions() ServiceBindingOptions {
	return ServiceBindingOptions{Services: []Service{{ID: AgentControllerID}}, AssertAccess: func() error { return nil }, OnError: func(error) {}}
}

func TestServerSourceReconnectsOnlyActivatedBindingsAndDrains(t *testing.T) {
	client := &localClient{state: "connecting"}
	active, inactive := &localBinding{}, &localBinding{}
	source := CreateServerServiceSource(client, localOptions(active, inactive))
	if got := source.Connection.Value(); got.Status != "connecting" || got.Attempt != 1 {
		t.Fatalf("initial = %#v", got)
	}
	binding, err := source.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(openOptions()); err != nil {
		t.Fatal(err)
	}
	if len(active.history()) != 0 {
		t.Fatal("open eagerly activated")
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	client.connect("connected", nil)
	client.connect("disconnected", errors.New("wire closed"))
	if got := source.Connection.Value(); got.Status != "disconnected" || got.Reason != "wire closed" || got.RetryAt != nil {
		t.Fatalf("disconnect = %#v", got)
	}
	client.connect("connecting", nil)
	if got := source.Connection.Value(); got.Attempt != 2 {
		t.Fatalf("attempt = %#v", got)
	}
	client.connect("connected", nil)
	if err := source.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := active.history(); !reflect.DeepEqual(got, []bool{false, true, false, false, true}) {
		t.Fatalf("rebind order = %v", got)
	}
	if got := inactive.history(); len(got) != 0 {
		t.Fatalf("inactive rebinds = %v", got)
	}
	if active.closed != 1 || inactive.closed != 1 || client.connectionListener != nil {
		t.Fatal("source did not release bindings/listener")
	}
	if _, err := source.Open(openOptions()); err == nil || err.Error() != "Server service source is disposed" {
		t.Fatalf("open disposed = %v", err)
	}
	if err := source.Dispose(t.Context()); err != nil || active.closed != 1 {
		t.Fatalf("repeat dispose = %v", err)
	}
}

func TestSessionSourceFencesAttachmentGenerationAndRetainsCatalogue(t *testing.T) {
	client := &localClient{state: "connected"}
	local := &localBinding{}
	source := CreateSessionServiceSource(client, localOptions(local))
	defer func() {
		if err := source.Dispose(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	if !source.AcceptsUnavailableServices() {
		t.Fatal("empty source rejects unavailable services")
	}
	binding, err := source.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	oldStarted, releaseOld := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(releaseOld) })
	local.asyncRebind = true
	local.rebind = func(ctx context.Context, bound bool) error {
		target := local.opts.GetTarget()
		if bound && target != nil && target.AttachmentID != nil && *target.AttachmentID == "old" {
			close(oldStarted)
			<-releaseOld
			return errors.New("obsolete hydration failed")
		}
		return nil
	}
	client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "old"})
	<-oldStarted
	// Only the new transition can publish attached while the old hydration is blocked. Observe that publication before calling WhenAttached, which could publish the same state itself and mask a late transition completion in the fence mutant.
	attached := make(chan struct{})
	stop := source.Attachment.Subscribe(func(state SessionAttachmentState, _ context.Context, delivery ReplicatedStateDelivery) {
		if delivery.Kind == "update" && state.Status == "attached" {
			close(attached)
		}
	})
	// The old hydration is still pending when the same Session gets a new attachment identity.
	client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "new"})
	<-attached
	stop()
	if err := source.WhenAttached("session", t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := source.Attachment.Value(); got != (SessionAttachmentState{Status: "attached", SessionID: "session"}) {
		t.Fatalf("attachment = %#v", got)
	}
	release.Do(func() { close(releaseOld) })
	source.tasks.Wait()
	if got := source.Attachment.Value(); got != (SessionAttachmentState{Status: "attached", SessionID: "session"}) {
		t.Fatalf("obsolete completion replaced current state: %#v", got)
	}
	catalogue, err := source.Catalogue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	client.attach(nil)
	if err := source.WhenDetached(t.Context()); err != nil {
		t.Fatal(err)
	}
	cached, err := source.Catalogue(t.Context())
	if err != nil || !reflect.DeepEqual(cached, catalogue) {
		t.Fatalf("cached = %#v, %v", cached, err)
	}
	if source.AcceptsUnavailableServices() {
		t.Fatal("cached source accepts unavailable services")
	}
	if err := source.WhenAttached("session", t.Context()); err == nil {
		t.Fatal("detached source attached")
	}
	// Cancellation must release a waiter, not the shared attachment work.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := source.WhenDetached(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestSessionSourceDegradesAndReportsFailures(t *testing.T) {
	failure := errors.New("hydrate failed")
	client := &localClient{state: "connected"}
	local := &localBinding{}
	reported := make(chan error, 1)
	options := localOptions(local)
	options.OnError = func(err error) { reported <- err }
	source := CreateSessionServiceSource(client, options)
	binding, err := source.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	local.rebind = func(context.Context, bool) error { return failure }
	client.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"})
	if err := source.WhenAttached("session", t.Context()); !errors.Is(err, failure) {
		t.Fatalf("hydrate = %v", err)
	}
	if got := source.Attachment.Value(); got.Status != "degraded" {
		t.Fatalf("state = %#v", got)
	}
	if err := <-reported; !errors.Is(err, failure) {
		t.Fatalf("reported = %v", err)
	}
	if err := source.WhenDetached(t.Context()); err == nil {
		t.Fatal("attached source detached")
	}
	if err := source.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(openOptions()); err == nil || err.Error() != "Session service source is disposed" {
		t.Fatalf("open disposed = %v", err)
	}
}
