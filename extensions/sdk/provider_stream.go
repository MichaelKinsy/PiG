package sdk

import (
	"encoding/json"
	"fmt"
)

// ProviderStreamSimpleFunc produces a legacy registerProvider streamSimple result. Context owns cancellation; model, request and options have Pi's wire shapes.
type ProviderStreamSimpleFunc = func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error)

func (e *Extension) dispatchProviderStream(ctx Context, id string, request *requestMsg) {
	e.providerMu.RLock()
	handler := e.providerStreams[request.Tool]
	e.providerMu.RUnlock()
	if handler == nil {
		_ = e.conn.respond(id, nil, fmt.Errorf("unknown provider stream: %s", request.Tool))
		return
	}
	var args struct {
		Model   map[string]any `json:"model"`
		Context map[string]any `json:"context"`
		Options map[string]any `json:"options"`
	}
	if err := json.Unmarshal(request.Args, &args); err != nil {
		_ = e.conn.respond(id, nil, err)
		return
	}
	stream, err := handler(ctx, args.Model, args.Context, args.Options)
	if err != nil {
		_ = e.conn.respond(id, nil, err)
		return
	}
	if stream == nil {
		_ = e.conn.respond(id, nil, fmt.Errorf("provider returned nil stream"))
		return
	}
	if err := stream.forward(ctx.ctx, func(event any) error {
		return e.conn.notify("provider_stream_event", map[string]any{"request_id": id, "result": event})
	}); err != nil {
		_ = e.conn.respond(id, nil, err)
		return
	}
	select {
	case <-ctx.Done():
		_ = e.conn.respond(id, nil, ctx.ctx.Err())
	case <-stream.done:
		result, err := stream.resultPayload()
		_ = e.conn.respond(id, result, err)
	}
}
