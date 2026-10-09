package routing

// Ports packages/server/src/session-router.ts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// SessionRouterOptions binds application Session acquisition to connection identity and attachment publications. C is a stable comparable client identity; Server uses connection pointers.
type SessionRouterOptions[C comparable] struct {
	Host              ServerHost
	ServerId          string
	IsClosing         func() bool
	PublishAttachment func(context.Context, C, *protocol.SessionTarget) error
	ReportError       func(error)
}
type routerResult[T any] struct {
	done  chan struct{}
	value T
	err   error
}

func newRouterResult[T any]() *routerResult[T] { return &routerResult[T]{done: make(chan struct{})} }

type routerAttachment[C comparable] struct {
	id         string
	client     C
	session    *hostedSession[C]
	operations map[*routerResult[json.RawMessage]]bool
	acquiring  *routerResult[RoutedSessionAttachment]
	lease      RoutedSessionAttachment
	releasing  *routerResult[struct{}]
}
type hostedSession[C comparable] struct {
	id          string
	handle      RoutedSessionHandle
	attachments []*routerAttachment[C]
}
type sessionCleanupError struct{ aggregateError }

// SessionRouter serializes each client's attachment transitions and service admission, but not service execution. Accepted operations retain their attachment lease until settlement.
type SessionRouter[C comparable] struct {
	options          SessionRouterOptions[C]
	mu               sync.Mutex
	hosted           map[string]*hostedSession[C]
	hostedOrder      []string
	opening          map[string]*routerResult[*hostedSession[C]]
	openingOrder     []string
	attachments      map[C]*routerAttachment[C]
	disconnected     map[C]bool
	clientOperations map[C]*routerResult[struct{}]
	closing          *routerResult[struct{}]
	closed           bool
	done             chan struct{}
	work             sync.WaitGroup
}

func NewSessionRouter[C comparable](options SessionRouterOptions[C]) *SessionRouter[C] {
	return &SessionRouter[C]{options: options, hosted: make(map[string]*hostedSession[C]), opening: make(map[string]*routerResult[*hostedSession[C]]), attachments: make(map[C]*routerAttachment[C]), disconnected: make(map[C]bool), clientOperations: make(map[C]*routerResult[struct{}]), done: make(chan struct{})}
}
func runRouterClient[C comparable, T any](r *SessionRouter[C], client C, operation func() (T, error)) *routerResult[T] {
	result, tail := newRouterResult[T](), newRouterResult[struct{}]()
	r.mu.Lock()
	if r.closed {
		result.err = NewServerDrainingError()
		close(result.done)
		r.mu.Unlock()
		return result
	}
	previous := r.clientOperations[client]
	r.clientOperations[client] = tail
	r.work.Go(func() {
		if previous != nil {
			<-previous.done
		}
		result.value, result.err = operation()
		close(result.done)
		close(tail.done)
		r.mu.Lock()
		if r.clientOperations[client] == tail {
			delete(r.clientOperations, client)
		}
		r.mu.Unlock()
	})
	r.mu.Unlock()
	return result
}

// ExecuteServiceCall admits one service call and waits for its result.
func (r *SessionRouter[C]) ExecuteServiceCall(ctx context.Context, call chord.ServiceCall, target protocol.RpcTarget, client C, publish chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return r.QueueServiceCall(ctx, call, target, client, publish).Wait()
}

// QueuedServiceCall is a service call whose position in its client's admission order is fixed.
type QueuedServiceCall struct {
	admitted *routerResult[*routerResult[json.RawMessage]]
}

// QueueServiceCall fixes the call's position among its client's admissions before it returns, as upstream's
// executeServiceCall does by calling runForClient synchronously (packages/server/src/session-router.ts:54).
// Calls queued in sequence reach the Session endpoint in that sequence.
func (r *SessionRouter[C]) QueueServiceCall(ctx context.Context, call chord.ServiceCall, target protocol.RpcTarget, client C, publish chord.ServiceUpdatePublisher) *QueuedServiceCall {
	return &QueuedServiceCall{admitted: runRouterClient(r, client, func() (*routerResult[json.RawMessage], error) {
		return r.startServiceCall(ctx, client, target, call, publish)
	})}
}

// Wait returns the admitted call's result or the admission error.
func (q *QueuedServiceCall) Wait() (json.RawMessage, error) {
	<-q.admitted.done
	if q.admitted.err != nil {
		return nil, q.admitted.err
	}
	result := q.admitted.value
	<-result.done
	return result.value, result.err
}

func (r *SessionRouter[C]) AttachClient(ctx context.Context, client C, sessionID string) error {
	if r.options.IsClosing() {
		return NewServerDrainingError()
	}
	result := runRouterClient(r, client, func() (struct{}, error) { return struct{}{}, r.attachClientNow(ctx, client, sessionID) })
	<-result.done
	return result.err
}
func (r *SessionRouter[C]) DetachClient(ctx context.Context, client C) error {
	result := runRouterClient(r, client, func() (struct{}, error) {
		r.mu.Lock()
		attachment := r.attachments[client]
		r.mu.Unlock()
		if attachment == nil {
			return struct{}{}, nil
		}
		return struct{}{}, r.releaseAttachment(ctx, attachment, true)
	})
	<-result.done
	return result.err
}
func (r *SessionRouter[C]) Disconnect(ctx context.Context, client C) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.disconnected[client] = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.disconnected, client); r.mu.Unlock() }()
	result := runRouterClient(r, client, func() (struct{}, error) {
		r.mu.Lock()
		attachment := r.attachments[client]
		r.mu.Unlock()
		if attachment == nil {
			return struct{}{}, nil
		}
		return struct{}{}, r.releaseAttachment(ctx, attachment, false)
	})
	<-result.done
	return result.err
}
func (r *SessionRouter[C]) RemoveSession(ctx context.Context, sessionID string) error {
	if r.options.IsClosing() {
		return NewServerDrainingError()
	}
	r.mu.Lock()
	hosted := r.hosted[sessionID]
	r.mu.Unlock()
	if hosted != nil {
		r.applyTermination(hosted)
	}
	r.mu.Lock()
	hosted = r.hosted[sessionID]
	if hosted == nil {
		r.mu.Unlock()
		return nil
	}
	attachments := slices.Clone(hosted.attachments)
	r.mu.Unlock()
	failures := r.releaseAttachments(ctx, attachments)
	failures = append(failures, hosted.handle.Close(ctx))
	r.mu.Lock()
	r.removeHosted(hosted)
	r.mu.Unlock()
	return routingErrors("Failed to close Session "+sessionID, failures, true)
}
func (r *SessionRouter[C]) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closing == nil {
		closing := newRouterResult[struct{}]()
		r.closing = closing
		// session-router.ts:109-110 snapshots the client operations and opening Sessions before closeInternal first suspends. openingSessions is an insertion-ordered Map.
		operations := make([]*routerResult[struct{}], 0, len(r.clientOperations))
		for _, operation := range r.clientOperations {
			operations = append(operations, operation)
		}
		opening := make([]*routerResult[*hostedSession[C]], 0, len(r.openingOrder))
		for _, id := range r.openingOrder {
			opening = append(opening, r.opening[id])
		}
		go func() { closing.err = r.closeInternal(ctx, operations, opening); close(closing.done) }()
	}
	result := r.closing
	r.mu.Unlock()
	<-result.done
	return result.err
}
func (r *SessionRouter[C]) closeInternal(ctx context.Context, operations []*routerResult[struct{}], opening []*routerResult[*hostedSession[C]]) error {
	var failures []error
	for _, operation := range operations {
		<-operation.done
	}
	for _, operation := range opening {
		<-operation.done
	}
	r.invalidateTerminated()
	// Promise.allSettled resolves before any rejection is reported, and results keep the snapshot order.
	for _, operation := range opening {
		if operation.err != nil {
			r.options.ReportError(operation.err)
			if _, ok := errors.AsType[*sessionCleanupError](operation.err); ok {
				failures = append(failures, operation.err)
			}
		}
	}
	r.mu.Lock()
	var attachments []*routerAttachment[C]
	for _, id := range r.hostedOrder {
		if hosted := r.hosted[id]; hosted != nil {
			attachments = append(attachments, hosted.attachments...)
		}
	}
	r.mu.Unlock()
	failures = append(failures, r.releaseAttachments(ctx, attachments)...)
	r.mu.Lock()
	hosted := make([]*hostedSession[C], 0, len(r.hostedOrder))
	for _, id := range r.hostedOrder {
		if value := r.hosted[id]; value != nil {
			hosted = append(hosted, value)
		}
	}
	r.mu.Unlock()
	closed := make([]error, len(hosted))
	var group sync.WaitGroup
	for i, session := range hosted {
		group.Go(func() { closed[i] = session.handle.Close(ctx) })
	}
	group.Wait()
	for i, err := range closed {
		if err != nil {
			r.options.ReportError(err)
			failures = append(failures, err)
		} else {
			r.mu.Lock()
			r.removeHosted(hosted[i])
			r.mu.Unlock()
		}
	}
	r.mu.Lock()
	clear(r.attachments)
	clear(r.clientOperations)
	r.closed = true
	close(r.done)
	r.mu.Unlock()
	r.work.Wait()
	return routingErrors("Failed to close routed Sessions", failures, false)
}
func (r *SessionRouter[C]) attachClientNow(ctx context.Context, client C, sessionID string) error {
	r.mu.Lock()
	disconnected, current := r.disconnected[client], r.attachments[client]
	r.mu.Unlock()
	if r.options.IsClosing() || disconnected {
		return NewServerDrainingError()
	}
	if current != nil && r.retireTerminatedAttachment(current) {
		r.mu.Lock()
		current = r.attachments[client]
		r.mu.Unlock()
	}
	if current != nil && current.session.id == sessionID {
		return nil
	}
	hosted, err := r.acquire(ctx, sessionID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	disconnected = r.disconnected[client]
	r.mu.Unlock()
	if r.options.IsClosing() || disconnected {
		return NewServerDrainingError()
	}
	if current != nil {
		if err := r.releaseAttachment(ctx, current, false); err != nil {
			return err
		}
	}
	acquiring := newRouterResult[RoutedSessionAttachment]()
	attachment := &routerAttachment[C]{id: uuid.NewString(), client: client, session: hosted, operations: make(map[*routerResult[json.RawMessage]]bool), acquiring: acquiring}
	r.mu.Lock()
	hosted.attachments = append(hosted.attachments, attachment)
	r.mu.Unlock()
	acquiring.value, acquiring.err = hosted.handle.AttachClient(ctx)
	r.mu.Lock()
	attachment.lease = acquiring.value
	close(acquiring.done)
	if acquiring.err != nil {
		hosted.attachments = slices.DeleteFunc(hosted.attachments, func(value *routerAttachment[C]) bool { return value == attachment })
		r.mu.Unlock()
		return acquiring.err
	}
	invalid := r.hosted[hosted.id] != hosted || !slices.Contains(hosted.attachments, attachment) || r.disconnected[client]
	r.mu.Unlock()
	if invalid || r.options.IsClosing() {
		if err := r.releaseAttachment(ctx, attachment, true); err != nil {
			return err
		}
		return NewServerDrainingError()
	}
	r.mu.Lock()
	r.attachments[client] = attachment
	r.mu.Unlock()
	return r.options.PublishAttachment(ctx, client, &protocol.SessionTarget{ServerId: r.options.ServerId, SessionId: sessionID, AttachmentId: attachment.id})
}
func (r *SessionRouter[C]) startServiceCall(ctx context.Context, client C, target protocol.RpcTarget, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (*routerResult[json.RawMessage], error) {
	result, err := r.admitServiceCall(ctx, client, target, call, publish)
	return result, err
}

// ServiceInitiator is an endpoint's admission boundary. Upstream invokes an endpoint synchronously, so the call's synchronous
// prefix runs before the next call is admitted (packages/server/src/session-router.ts:207). A Go endpoint exposes that
// prefix by implementing BeginInvokeService: it returns once the call is admitted, and the invocation delivers the outcome. It
// reports chord.ErrInvocationAdmissionUnavailable for a call it cannot admit; the router then starts that call itself. The router
// holds its lock during BeginInvokeService, so the endpoint must not block or call back into the router before it returns.
type ServiceInitiator interface {
	BeginInvokeService(context.Context, chord.ServiceCall, chord.ServiceUpdatePublisher) (*chord.ServiceInvocation, error)
}

// beginServiceCall admits one endpoint call. An endpoint without ServiceInitiator is started on a goroutine that has begun
// running before this returns: consecutive calls then start in admission order, but the endpoint cannot observe its
// position before an earlier call's goroutine reaches its first instruction.
func beginServiceCall(endpoint RoutedServerServiceAttachment, ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher, spawn func(func())) (*chord.ServiceInvocation, error) {
	if initiator, ok := endpoint.(ServiceInitiator); ok {
		invocation, err := initiator.BeginInvokeService(ctx, call, publish)
		if !errors.Is(err, chord.ErrInvocationAdmissionUnavailable) {
			return invocation, err
		}
	}
	result := newRouterResult[json.RawMessage]()
	begun := make(chan struct{})
	spawn(func() {
		close(begun)
		result.value, result.err = endpoint.InvokeService(ctx, call, publish)
		close(result.done)
	})
	<-begun
	return chord.NewServiceInvocation(func(waitContext context.Context) (json.RawMessage, error) {
		select {
		case <-result.done:
			return result.value, result.err
		case <-waitContext.Done():
			return nil, context.Cause(waitContext)
		}
	}), nil
}

// admitServiceCall registers the operation and begins its endpoint call.
func (r *SessionRouter[C]) admitServiceCall(ctx context.Context, client C, target protocol.RpcTarget, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (*routerResult[json.RawMessage], error) {
	if r.options.IsClosing() {
		return nil, NewServerDrainingError()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disconnected[client] {
		return nil, NewServerDrainingError()
	}
	route, ok := target.(protocol.SessionTarget)
	if !ok {
		return nil, NewSessionNotAttachedError()
	}
	attachment := r.attachments[client]
	if attachment == nil || attachment.session.id != route.SessionId || attachment.id != route.AttachmentId {
		return nil, NewSessionNotAttachedError()
	}
	result := newRouterResult[json.RawMessage]()
	attachment.operations[result] = true
	invocation, err := beginServiceCall(attachment.lease, ctx, call, publish, r.work.Go)
	if err != nil {
		delete(attachment.operations, result)
		return nil, err
	}
	r.work.Go(func() {
		result.value, result.err = invocation.Wait(context.Background())
		close(result.done)
		r.mu.Lock()
		delete(attachment.operations, result)
		r.mu.Unlock()
	})
	return result, nil
}
func (r *SessionRouter[C]) releaseAttachment(ctx context.Context, attachment *routerAttachment[C], publish bool) error {
	r.mu.Lock()
	if attachment.releasing == nil {
		result := newRouterResult[struct{}]()
		attachment.releasing = result
		operations := make([]*routerResult[json.RawMessage], 0, len(attachment.operations))
		for operation := range attachment.operations {
			operations = append(operations, operation)
		}
		r.work.Go(func() {
			for _, operation := range operations {
				<-operation.done
			}
			<-attachment.acquiring.done
			if attachment.acquiring.err != nil {
				result.err = attachment.acquiring.err
			} else if attachment.acquiring.value != nil {
				result.err = attachment.acquiring.value.Release(ctx)
			}
			if err := r.clearAttachment(ctx, attachment, publish); err != nil {
				result.err = err
			}
			close(result.done)
		})
	}
	result := attachment.releasing
	r.mu.Unlock()
	<-result.done
	return result.err
}
func (r *SessionRouter[C]) clearAttachment(ctx context.Context, attachment *routerAttachment[C], publish bool) error {
	r.mu.Lock()
	attachment.session.attachments = slices.DeleteFunc(attachment.session.attachments, func(value *routerAttachment[C]) bool { return value == attachment })
	current := r.attachments[attachment.client] == attachment
	if current {
		delete(r.attachments, attachment.client)
	}
	r.mu.Unlock()
	if current && publish {
		return r.options.PublishAttachment(ctx, attachment.client, nil)
	}
	return nil
}
func (r *SessionRouter[C]) releaseAttachments(ctx context.Context, attachments []*routerAttachment[C]) []error {
	failures := make([]error, len(attachments))
	var group sync.WaitGroup
	for i, attachment := range attachments {
		group.Go(func() { failures[i] = r.releaseAttachment(ctx, attachment, true) })
	}
	group.Wait()
	return failures
}
func (r *SessionRouter[C]) acquire(ctx context.Context, sessionID string) (*hostedSession[C], error) {
	for {
		r.mu.Lock()
		hosted := r.hosted[sessionID]
		if hosted == nil {
			break
		}
		r.mu.Unlock()
		if !r.applyTermination(hosted) {
			return hosted, nil
		}
	}
	pending := r.opening[sessionID]
	if pending == nil {
		pending = newRouterResult[*hostedSession[C]]()
		r.opening[sessionID] = pending
		r.openingOrder = append(r.openingOrder, sessionID)
		r.work.Go(func() {
			pending.value, pending.err = r.open(ctx, sessionID)
			close(pending.done)
			r.mu.Lock()
			if r.opening[sessionID] == pending {
				delete(r.opening, sessionID)
				r.openingOrder = slices.DeleteFunc(r.openingOrder, func(id string) bool { return id == sessionID })
			}
			r.mu.Unlock()
		})
	}
	r.mu.Unlock()
	<-pending.done
	return pending.value, pending.err
}
func (r *SessionRouter[C]) open(ctx context.Context, sessionID string) (*hostedSession[C], error) {
	metadata, err := r.options.Host.ResolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	handle, err := r.options.Host.OpenSession(ctx, metadata)
	if err != nil {
		return nil, err
	}
	if r.options.IsClosing() {
		if err := handle.Close(ctx); err != nil {
			r.options.ReportError(err)
			return nil, &sessionCleanupError{aggregateError{"Failed to close routed Session acquired while draining", []error{NewServerDrainingError(), err}}}
		}
		return nil, NewServerDrainingError()
	}
	hosted := &hostedSession[C]{id: metadata.SessionID(), handle: handle}
	r.mu.Lock()
	if r.hosted[hosted.id] == nil {
		r.hostedOrder = append(r.hostedOrder, hosted.id)
	}
	r.hosted[hosted.id] = hosted
	if terminated := handle.Terminated(); terminated != nil {
		r.work.Go(func() {
			select {
			case <-terminated:
				r.invalidate(hosted, handle.TerminalError())
			case <-r.done:
			}
		})
	}
	r.mu.Unlock()
	return hosted, nil
}

// invalidateTerminated applies every termination already signalled. session-router.ts:#open registers `handle.terminated?.then(invalidate)`, a microtask that runs before any later event, so no later step (a close, an acquisition or a Session removal) observes a terminated handle as live. The Go watcher goroutine may not have run yet, and a release or attach through the retired handle fails with the worker no longer registered. invalidate is idempotent.
func (r *SessionRouter[C]) invalidateTerminated() {
	r.mu.Lock()
	hosted := make([]*hostedSession[C], 0, len(r.hostedOrder))
	for _, id := range r.hostedOrder {
		if value := r.hosted[id]; value != nil {
			hosted = append(hosted, value)
		}
	}
	r.mu.Unlock()
	for _, value := range hosted {
		r.applyTermination(value)
	}
}

// retireTerminatedAttachment completes the retirement of attachment when its Harness termination is already signalled and reports whether it did. session-router.ts:#invalidate starts the attachment release from the termination microtask, and that release settles in the same microtask drain, before a later request can read attachmentsByClient (:162). The Go watcher and its release run on other goroutines, so the attach joins the release itself. The release failure is the invalidation's to report (session-router.ts:307-309); it does not fail the attach.
func (r *SessionRouter[C]) retireTerminatedAttachment(attachment *routerAttachment[C]) bool {
	if !r.applyTermination(attachment.session) {
		return false
	}
	_ = r.releaseAttachment(context.Background(), attachment, true)
	return true
}

// applyTermination invalidates hosted when its Harness termination is already signalled and reports whether it did.
func (r *SessionRouter[C]) applyTermination(hosted *hostedSession[C]) bool {
	channel := hosted.handle.Terminated()
	if channel == nil {
		return false
	}
	select {
	case <-channel:
		r.invalidate(hosted, hosted.handle.TerminalError())
		return true
	default:
		return false
	}
}
func (r *SessionRouter[C]) invalidate(hosted *hostedSession[C], failure error) {
	r.mu.Lock()
	if r.hosted[hosted.id] != hosted {
		r.mu.Unlock()
		return
	}
	r.removeHosted(hosted)
	attachments := slices.Clone(hosted.attachments)
	for _, attachment := range attachments {
		r.work.Go(func() {
			if err := r.releaseAttachment(context.Background(), attachment, true); err != nil {
				r.options.ReportError(err)
			}
		})
	}
	r.mu.Unlock()
	if failure != nil {
		r.options.ReportError(failure)
	}
}
func (r *SessionRouter[C]) removeHosted(hosted *hostedSession[C]) {
	if r.hosted[hosted.id] == hosted {
		delete(r.hosted, hosted.id)
		r.hostedOrder = slices.DeleteFunc(r.hostedOrder, func(id string) bool { return id == hosted.id })
	}
}
