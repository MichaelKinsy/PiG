package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// RoutedServiceBinding activates a source-owned host binding on first Ready.
type RoutedServiceBinding struct {
	services           RemoteServiceBinding
	getBound           func() bool
	onActivate         func(context.Context) error
	remove             func()
	mu                 sync.Mutex
	activated          bool
	activationDone     chan struct{}
	activationComplete bool
}

func (binding *RoutedServiceBinding) Use(service Service) (any, error) {
	return binding.services.Use(service)
}
func (binding *RoutedServiceBinding) Observe(service Service, handler func(context.Context, any) error) (func(), error) {
	return binding.services.Observe(service, handler)
}

func (binding *RoutedServiceBinding) Ready(ctx context.Context) error {
	binding.mu.Lock()
	first := !binding.activated
	if first {
		binding.activated = true
		binding.activationDone = make(chan struct{})
	}
	done := binding.activationDone
	binding.mu.Unlock()
	if first {
		err := <-binding.beginRebind(ctx, binding.getBound())
		close(done)
		if err != nil {
			return err
		}
	} else if err := waitDone(ctx, done); err != nil {
		return err
	}
	ready := make(chan error, 1)
	binding.services.Ready(ctx, func(err error) { ready <- err })
	if err := <-ready; err != nil {
		return err
	}
	binding.mu.Lock()
	complete := binding.activationComplete
	binding.mu.Unlock()
	if !complete && binding.onActivate != nil {
		if err := binding.onActivate(ctx); err != nil {
			return err
		}
	}
	binding.mu.Lock()
	binding.activationComplete = true
	binding.mu.Unlock()
	return nil
}

func (binding *RoutedServiceBinding) beginRebind(ctx context.Context, bound bool) <-chan error {
	done := make(chan error, 1)
	binding.services.Rebind(ctx, bound, func(err error) { done <- err })
	return done
}

func (binding *RoutedServiceBinding) beginUpdateBound(ctx context.Context, bound bool) <-chan error {
	binding.mu.Lock()
	activated := binding.activated
	binding.mu.Unlock()
	if !activated {
		return nil
	}
	return binding.beginRebind(ctx, bound)
}

func (binding *RoutedServiceBinding) Dispose(ctx context.Context) error {
	binding.remove()
	return binding.services.Dispose(ctx)
}

type sourceBindings struct {
	mu       sync.Mutex
	bindings []*RoutedServiceBinding
	disposed bool
	options  ServiceSourceOptions
	tasks    sync.WaitGroup
}

func (source *sourceBindings) snapshot() []*RoutedServiceBinding {
	source.mu.Lock()
	defer source.mu.Unlock()
	return slices.Clone(source.bindings)
}

// open is called with the source mutex held, so disposal cannot miss a new binding.
func (source *sourceBindings) open(options ServiceBindingOptions, target func() *ServiceTarget, bound func() bool, activate func(context.Context) error) *RoutedServiceBinding {
	if options.AssertAccess == nil {
		options.AssertAccess = func() error { return nil }
	}
	if options.OnError == nil {
		options.OnError = func(error) {}
	}
	binding := &RoutedServiceBinding{getBound: bound, onActivate: activate}
	binding.services = source.options.NewBinding(RemoteServiceBindingOptions{ServiceBindingOptions: options, GetTarget: target})
	binding.remove = func() {
		source.mu.Lock()
		defer source.mu.Unlock()
		source.bindings = slices.DeleteFunc(source.bindings, func(candidate *RoutedServiceBinding) bool { return candidate == binding })
	}
	source.bindings = append(source.bindings, binding)
	return binding
}

func (source *sourceBindings) report(err error) {
	if err != nil && source.options.OnError != nil {
		source.options.OnError(err)
	}
}

func (source *sourceBindings) dispose(ctx context.Context, removeListener func(), message string) error {
	source.mu.Lock()
	if source.disposed {
		source.mu.Unlock()
		return nil
	}
	source.disposed = true
	source.mu.Unlock()
	removeListener()
	source.tasks.Wait()
	source.mu.Lock()
	bindings := source.bindings
	source.bindings = nil
	source.mu.Unlock()
	return settleBindings(bindings, func(binding *RoutedServiceBinding) error { return binding.Dispose(ctx) }, message)
}

// ServerServiceSource owns server-scoped bindings and serializes reconnect transitions. It does not connect or close the client.
type ServerServiceSource struct {
	sourceBindings
	Connection     *SourceState[ServerConnectionState]
	client         ServiceClient
	removeListener func()
	attempt        int
	transition     <-chan struct{}
}

func CreateServerServiceSource(client ServiceClient, options ServiceSourceOptions) *ServerServiceSource {
	source := &ServerServiceSource{client: client, sourceBindings: sourceBindings{options: options}}
	if client.ConnectionState() == "connecting" {
		source.attempt = 1
	}
	source.Connection = &SourceState[ServerConnectionState]{value: toServerConnectionState(client.ConnectionState(), source.attempt, nil)}
	source.removeListener = client.OnConnectionStateChange(source.connectionChanged)
	return source
}

func (*ServerServiceSource) AcceptsUnavailableServices() bool { return false }

func (source *ServerServiceSource) Catalogue(ctx context.Context) ([]ServiceCatalogueEntry, error) {
	return readCatalogue(ctx, source.client, ServiceTarget{ServerID: source.client.ServerID()})
}

func (source *ServerServiceSource) Open(options ServiceBindingOptions) (*RoutedServiceBinding, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.disposed {
		return nil, errors.New("Server service source is disposed")
	}
	return source.open(options,
		func() *ServiceTarget { return &ServiceTarget{ServerID: source.client.ServerID()} },
		func() bool { return source.client.ConnectionState() == "connected" }, nil), nil
}

func (source *ServerServiceSource) connectionChanged(state string, failure error) {
	source.mu.Lock()
	if source.disposed {
		source.mu.Unlock()
		return
	}
	if state == "connecting" {
		source.attempt++
	}
	notify := source.Connection.replace(context.Background(), toServerConnectionState(source.client.ConnectionState(), source.attempt, failure))
	source.mu.Unlock()
	notify()
	source.mu.Lock()
	if source.disposed {
		source.mu.Unlock()
		return
	}
	previous := source.transition
	done := make(chan struct{})
	source.transition = done
	source.tasks.Add(1)
	source.mu.Unlock()
	go func() {
		defer source.tasks.Done()
		defer close(done)
		if previous != nil {
			<-previous
		}
		failures := rebindAll(source.snapshot(), context.Background(), state == "connected")()
		if len(failures) > 0 {
			source.report(newAggregateError("", failures))
		}
	}()
}

func (source *ServerServiceSource) Dispose(ctx context.Context) error {
	return source.dispose(ctx, source.removeListener, "Failed to dispose server service source")
}

// SessionServiceSource owns bindings for the selected attachment. Readiness is fenced by both the local revision and the server/Session/attachment identity.
type SessionServiceSource struct {
	sourceBindings
	Attachment     *SourceState[SessionAttachmentState]
	client         ServiceClient
	removeListener func()
	catalogue      []ServiceCatalogueEntry
	catalogueKnown bool
	revision       uint64
}

func CreateSessionServiceSource(client ServiceClient, options ServiceSourceOptions) *SessionServiceSource {
	source := &SessionServiceSource{client: client, sourceBindings: sourceBindings{options: options}}
	state := SessionAttachmentState{Status: "detached"}
	if attachment := client.Attachment(); attachment != nil {
		state = SessionAttachmentState{Status: "attaching", SessionID: attachment.SessionID}
	}
	source.Attachment = &SourceState[SessionAttachmentState]{value: state}
	source.removeListener = client.OnAttachmentChange(source.attachmentChanged)
	return source
}

func (source *SessionServiceSource) AcceptsUnavailableServices() bool {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.client.Attachment() == nil && !source.catalogueKnown
}

func (source *SessionServiceSource) Catalogue(ctx context.Context) ([]ServiceCatalogueEntry, error) {
	target := sessionServiceTarget(source.client.Attachment())
	if target == nil {
		source.mu.Lock()
		defer source.mu.Unlock()
		return append([]ServiceCatalogueEntry{}, source.catalogue...), nil
	}
	catalogue, err := readCatalogue(ctx, source.client, *target)
	if err != nil {
		return nil, err
	}
	source.mu.Lock()
	source.catalogue = slices.Clone(catalogue)
	source.catalogueKnown = true
	source.mu.Unlock()
	return catalogue, nil
}

func (source *SessionServiceSource) Open(options ServiceBindingOptions) (*RoutedServiceBinding, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.disposed {
		return nil, errors.New("Session service source is disposed")
	}
	return source.open(options,
		func() *ServiceTarget { return sessionServiceTarget(source.client.Attachment()) },
		func() bool { return source.client.Attachment() != nil },
		func(ctx context.Context) error {
			if attachment := source.client.Attachment(); attachment != nil {
				return source.WhenAttached(attachment.SessionID, ctx)
			}
			return source.WhenDetached(ctx)
		}), nil
}

func (source *SessionServiceSource) attachmentChanged(attachment *SessionTarget) {
	if attachment != nil {
		attachment = new(*attachment)
	}
	source.mu.Lock()
	if source.disposed {
		source.mu.Unlock()
		return
	}
	source.revision++
	revision := source.revision
	var notify func()
	if attachment != nil {
		notify = source.Attachment.replace(context.Background(), SessionAttachmentState{Status: "attaching", SessionID: attachment.SessionID})
	}
	source.mu.Unlock()
	if notify != nil {
		notify()
	}
	source.mu.Lock()
	if source.disposed {
		source.mu.Unlock()
		return
	}
	bindings := slices.Clone(source.bindings)
	source.tasks.Add(1)
	source.mu.Unlock()
	if attachment != nil {
		source.client.ServiceCatalogue(context.Background(), *sessionServiceTarget(attachment), func(catalogue []ServiceCatalogueEntry, err error) {
			if err != nil {
				source.report(err)
				return
			}
			source.mu.Lock()
			defer source.mu.Unlock()
			if source.revision == revision && sameAttachment(source.client.Attachment(), attachment) {
				source.catalogue = slices.Clone(catalogue)
				source.catalogueKnown = true
			}
		})
	}
	complete := rebindAll(bindings, context.Background(), attachment != nil)
	go func() {
		defer source.tasks.Done()
		err := throwFailures(complete(), "Failed to rebind Session services")
		source.mu.Lock()
		current := source.revision == revision && sameAttachment(source.client.Attachment(), attachment)
		var notify func()
		if current {
			state := SessionAttachmentState{Status: "detached"}
			if attachment != nil {
				state = SessionAttachmentState{Status: "attached", SessionID: attachment.SessionID}
				if err != nil {
					state.Status = "degraded"
				}
			}
			notify = source.Attachment.replace(context.Background(), state)
		}
		source.mu.Unlock()
		if notify != nil {
			notify()
		}
		if current {
			source.report(err)
		}
	}()
}

// WhenAttached waits for the exact current attachment generation to finish hydrating. Cancelling the wait does not cancel shared binding work.
func (source *SessionServiceSource) WhenAttached(sessionID string, ctx context.Context) error {
	source.mu.Lock()
	attachment := source.client.Attachment()
	revision := source.revision
	source.mu.Unlock()
	if attachment == nil || attachment.SessionID != sessionID {
		return fmt.Errorf("Session %s is not the current attachment", sessionID)
	}
	err := source.ready(ctx)
	source.mu.Lock()
	current := source.revision == revision && sameAttachment(source.client.Attachment(), attachment)
	var notify func()
	if err != nil && current {
		notify = source.Attachment.replace(ctx, SessionAttachmentState{Status: "degraded", SessionID: sessionID})
	} else if err == nil && current && source.Attachment.Value() != (SessionAttachmentState{Status: "attached", SessionID: sessionID}) {
		notify = source.Attachment.replace(ctx, SessionAttachmentState{Status: "attached", SessionID: sessionID})
	}
	source.mu.Unlock()
	if notify != nil {
		notify()
	}
	if err != nil {
		return err
	}
	if !current {
		return fmt.Errorf("Session %s was replaced while attaching", sessionID)
	}
	return nil
}

// WhenDetached waits for every binding to finish releasing the previous attachment.
func (source *SessionServiceSource) WhenDetached(ctx context.Context) error {
	source.mu.Lock()
	attachment := source.client.Attachment()
	revision := source.revision
	source.mu.Unlock()
	if attachment != nil {
		return errors.New("A Session is still attached")
	}
	if err := source.ready(ctx); err != nil {
		return err
	}
	source.mu.Lock()
	current := source.revision == revision && source.client.Attachment() == nil
	var notify func()
	if current && source.Attachment.Value().Status != "detached" {
		notify = source.Attachment.replace(ctx, SessionAttachmentState{Status: "detached"})
	}
	source.mu.Unlock()
	if notify != nil {
		notify()
	}
	if !current {
		return errors.New("The Session attachment changed while detaching")
	}
	return nil
}

func (source *SessionServiceSource) ready(ctx context.Context) error {
	bindings := source.snapshot()
	waitContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(bindings))
	for _, binding := range bindings {
		binding.services.Ready(waitContext, func(err error) { results <- err })
	}
	var failure error
	for range bindings {
		if err := <-results; err != nil && failure == nil {
			failure = err
			cancel()
		}
	}
	return failure
}

func (source *SessionServiceSource) Dispose(ctx context.Context) error {
	return source.dispose(ctx, source.removeListener, "Failed to dispose Session service source")
}

type catalogueResult struct {
	entries []ServiceCatalogueEntry
	err     error
}

func readCatalogue(ctx context.Context, client ServiceClient, target ServiceTarget) ([]ServiceCatalogueEntry, error) {
	result := make(chan catalogueResult, 1)
	client.ServiceCatalogue(ctx, target, func(entries []ServiceCatalogueEntry, err error) {
		result <- catalogueResult{entries: entries, err: err}
	})
	select {
	case value := <-result:
		return value.entries, value.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func sessionServiceTarget(attachment *SessionTarget) *ServiceTarget {
	if attachment == nil {
		return nil
	}
	return &ServiceTarget{ServerID: attachment.ServerID, SessionID: new(attachment.SessionID), AttachmentID: new(attachment.AttachmentID)}
}

func sameAttachment(left, right *SessionTarget) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func toServerConnectionState(state string, attempt int, failure error) ServerConnectionState {
	if state == "connecting" {
		return ServerConnectionState{Status: state, Attempt: attempt}
	}
	result := ServerConnectionState{Status: state, Since: time.Now().UTC().Format("2006-01-02T15:04:05.000Z")}
	if state == "disconnected" {
		result.Reason = "Client is disconnected"
		if failure != nil {
			result.Reason = failure.Error()
		}
	}
	return result
}

func waitDone(ctx context.Context, done <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func rebindAll(bindings []*RoutedServiceBinding, ctx context.Context, bound bool) func() []error {
	transitions := make([]<-chan error, 0, len(bindings))
	for _, binding := range bindings {
		if done := binding.beginUpdateBound(ctx, bound); done != nil {
			transitions = append(transitions, done)
		}
	}
	return func() []error {
		failures := make([]error, 0, len(transitions))
		for _, done := range transitions {
			if err := <-done; err != nil {
				failures = append(failures, err)
			}
		}
		return failures
	}
}

func settleBindings(bindings []*RoutedServiceBinding, call func(*RoutedServiceBinding) error, message string) error {
	failures := make([]error, len(bindings))
	var tasks sync.WaitGroup
	for i, binding := range bindings {
		tasks.Go(func() { failures[i] = call(binding) })
	}
	tasks.Wait()
	failures = slices.DeleteFunc(failures, func(err error) bool { return err == nil })
	return throwFailures(failures, message)
}

func throwFailures(failures []error, message string) error {
	if len(failures) == 0 {
		return nil
	}
	if len(failures) == 1 {
		return failures[0]
	}
	return newAggregateError(message, failures)
}

// AggregateError retains every failure while exposing the upstream aggregate message.
type AggregateError struct {
	Message string
	Errors  []any
}

func (err *AggregateError) Error() string { return err.Message }
func (err *AggregateError) Unwrap() []error {
	var causes []error
	for _, value := range err.Errors {
		if cause, ok := value.(error); ok {
			causes = append(causes, cause)
		}
	}
	return causes
}

func newAggregateError(message string, failures []error) *AggregateError {
	values := make([]any, len(failures))
	for i, failure := range failures {
		values[i] = failure
	}
	return &AggregateError{Message: message, Errors: values}
}
