package client

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// BeginInvoke performs request validation, correlation registration and ordered send admission before returning. The operation keeps ctx unchanged. Cancelling its Wait only abandons that observer; ctx still controls the actual request.
func (client *Client) BeginInvoke(ctx context.Context, target protocol.RpcTarget, call chord.ServiceCall) (*chord.ServiceInvocation, error) {
	pending, err := client.beginRequest(ctx, target, call, nil)
	if err != nil {
		return nil, err
	}
	return chord.NewServiceInvocation(func(waitContext context.Context) (json.RawMessage, error) {
		if waitContext.Err() != nil {
			return nil, context.Cause(waitContext)
		}
		select {
		case <-pending.done:
			if pending.err != nil {
				return nil, pending.err
			}
			if pending.value == nil {
				return nil, nil
			}
			return pending.value.(json.RawMessage), nil
		case <-waitContext.Done():
			return nil, context.Cause(waitContext)
		}
	}), nil
}

func (transport *clientServiceTransport) BeginInvoke(ctx context.Context, call chord.ServiceCall) (*chord.ServiceInvocation, error) {
	target := transport.getTarget()
	if target == nil {
		return nil, errors.New("Remote service target is unavailable")
	}
	return transport.client.BeginInvoke(ctx, target, call)
}
