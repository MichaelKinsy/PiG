package inproc

import (
	"context"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/src/core/extensions/runner.ts

// EmitCacheWarmingDecision awaits handlers in registration order and returns the last supplied action, or the event's action when no handler overrides it. Handler errors are reported without stopping subsequent handlers.
func (r *Runner) EmitCacheWarmingDecision(ctx context.Context, event extension.CacheWarmingDecisionEvent) (extension.CacheWarmingAction, error) {
	if err := r.assertActive(); err != nil {
		return event.Action, err
	}
	dispatchCtx := r.dispatchContext(ctx)
	action := event.Action
	type handlers struct {
		path  string
		calls []extension.HandlerFn
	}
	snapshot := make([]handlers, 0, len(r.extensions))
	for _, ext := range r.extensions {
		snapshot = append(snapshot, handlers{ext.Path, append([]extension.HandlerFn(nil), ext.EventHandlers(event.Type)...)})
	}
	for _, ext := range snapshot {
		for _, handler := range ext.calls {
			result, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.path, event.Type, err)
				continue
			}
			typed, ok := coerceResult[*extension.CacheWarmingDecisionEventResult](result)
			if ok && typed != nil && typed.Action != nil {
				action = *typed.Action
			}
		}
	}
	return action, nil
}
