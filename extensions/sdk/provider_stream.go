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
		return e.conn.notifyResult("provider_stream_event", id, event)
	}); err != nil {
		_ = e.conn.respond(id, nil, err)
		return
	}
	// forward returned without an error: the request's context is done, or the stream has ended and every event of it was sent. The context is read first, and once. A select
	// on the context and the stream's end picks at random when both are ready, and both are whenever an author ends its stream because it saw the cancel: the cancelled request
	// was then answered with a result now and then. A cancel that arrives after this read finds the stream ended and forwarded, and the request keeps its result.
	if err := ctx.ctx.Err(); err != nil {
		_ = e.conn.respond(id, nil, err)
		return
	}
	result, err := stream.resultPayload()
	_ = e.conn.respond(id, result, err)
}
