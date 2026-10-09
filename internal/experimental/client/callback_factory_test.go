package client

import "context"

// callbackFactory turns a test's scripted callback-form attempt into Pi's ByteTransportFactory, so every scripted test reaches Connection
// through the production adapter (adaptFactory), as a real transport factory does. The factory returns when the script completes the
// attempt, whatever the attempt context says, as Pi awaits a factory Promise that ignores its caller; a transport that arrives after the
// attempt ended is the Connection's to close.
func callbackFactory(start callbackByteTransportFactory) ByteTransportFactory {
	return func(ctx context.Context, handlers ByteTransportHandlers) (ByteTransport, error) {
		type opened struct {
			transport callbackByteTransport
			err       error
		}
		result := make(chan opened, 1)
		start(ctx, handlers, func(transport callbackByteTransport, err error) { result <- opened{transport, err} })
		attempt := <-result
		if attempt.err != nil || attempt.transport == nil {
			return nil, attempt.err
		}
		return blockingTransport{attempt.transport}, nil
	}
}

// blockingTransport is a scripted callback-form transport as Pi's ByteTransport: Send returns when the script completes the chunk.
// It keeps the script's Submit, which Connection admits frames through directly, as it does for the unix transport.
type blockingTransport struct{ callbackByteTransport }

func (transport blockingTransport) Send(ctx context.Context, chunk []byte) error {
	done := make(chan error, 1)
	transport.Submit(chunk, func(err error) { done <- err })
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Done is the script's own lifetime, when it has one.
func (transport blockingTransport) Done() <-chan struct{} {
	if lifetime, ok := transport.callbackByteTransport.(interface{ Done() <-chan struct{} }); ok {
		return lifetime.Done()
	}
	return nil
}
