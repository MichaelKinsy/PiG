package chord

import (
	"context"
	"encoding/json"
	"errors"
)

// ServiceInvocation is a service operation whose synchronous admission has completed. Cancelling Wait cancels only that observer, not the admitted operation or its original Context.
type ServiceInvocation struct {
	wait func(context.Context) (json.RawMessage, error)
}

// NewServiceInvocation binds an admitted operation to its owned completion. The producer retains cancellation, response correlation, and cleanup ownership.
func NewServiceInvocation(wait func(context.Context) (json.RawMessage, error)) *ServiceInvocation {
	return &ServiceInvocation{wait: wait}
}
func (operation *ServiceInvocation) Wait(ctx context.Context) (json.RawMessage, error) {
	return operation.wait(ctx)
}

// ServiceResultInvocation is the typed completion of an admitted operation. Its observer does not own or cancel the producer. BeginCallResult applies the same decoding rules as CallResult.
type ServiceResultInvocation[R any] struct {
	wait func(context.Context) (R, error)
}

// NewServiceResultInvocation binds an already admitted producer to its typed completion. The producer retains ownership of work, cancellation and shutdown.
func NewServiceResultInvocation[R any](wait func(context.Context) (R, error)) *ServiceResultInvocation[R] {
	return &ServiceResultInvocation[R]{wait: wait}
}

func (operation *ServiceResultInvocation[R]) Wait(ctx context.Context) (R, error) {
	return operation.wait(ctx)
}

// BeginCallResult admits a typed call through the selected facade. Admission errors return before an operation exists; response and decoding errors belong to Wait.
func BeginCallResult[R any](ctx context.Context, service *RemoteService, member string, args ...any) (*ServiceResultInvocation[R], error) {
	operation, err := service.BeginCall(ctx, member, args...)
	if err != nil {
		return nil, err
	}
	return NewServiceResultInvocation(func(waitCtx context.Context) (R, error) {
		raw, err := operation.Wait(waitCtx)
		return decodeCallResult[R](raw, err, service.facade.serviceId, member)
	}), nil
}

// ErrInvocationAdmissionUnavailable reports a transport or implementation without an explicit Promise admission boundary.
var ErrInvocationAdmissionUnavailable = errors.New("Remote service transport does not expose invocation admission")

// ServiceMemberInitiator is a provided implementation's admission boundary. provider.ts:234 applies a JavaScript member synchronously during invoke, so its synchronous prefix is admitted before the Promise settles; a Go implementation exposes that prefix explicitly.
type ServiceMemberInitiator interface {
	BeginServiceMember(ctx context.Context, member string, args []json.RawMessage) (*ServiceInvocation, error)
}

// InitiatingServiceTransport exposes the Promise-returning invocation boundary without inferring it from a blocking Go method.
type InitiatingServiceTransport interface {
	RemoteServiceTransport
	BeginInvoke(context.Context, ServiceCall) (*ServiceInvocation, error)
}

// BeginCall applies the same access/member/value checks as Call, then returns only after transport admission. Transports without an explicit admission boundary are rejected, never emulated by a prematurely signalled goroutine.
func (service *RemoteService) BeginCall(ctx context.Context, member string, args ...any) (*ServiceInvocation, error) {
	if err := service.access(); err != nil {
		return nil, err
	}
	call, err := service.facade.prepareCall(member, args)
	if err != nil {
		return nil, err
	}
	transport, ok := service.facade.transport.(InitiatingServiceTransport)
	if !ok {
		return nil, ErrInvocationAdmissionUnavailable
	}
	return transport.BeginInvoke(ctx, call)
}
