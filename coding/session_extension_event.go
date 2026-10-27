package coding

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// EmitExtensionEvent is `hasHandlers(event.type)` then `extensionRunner.emit(event)` on the Session's runner, as agent-session-runtime.ts does around a session replacement. event is one of the events the runner's generic emit takes, in Pi's JSON shape. emitted is false, and no handler ran, when no extension has a handler for the type; result is what emit answered, nil for nothing.
func (s *Session) EmitExtensionEvent(ctx context.Context, raw json.RawMessage) (result json.RawMessage, emitted bool, err error) {
	runner := s.currentRunner()
	if runner == nil {
		return nil, false, nil
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, false, fmt.Errorf("event: %w", err)
	}
	event, err := decodeRunnerEvent(raw)
	if err != nil {
		return nil, false, err
	}
	if !runner.HasHandlers(probe.Type) {
		return nil, false, nil
	}
	answer, err := runner.Emit(ctx, event)
	if err != nil {
		return nil, true, err
	}
	if answer == nil {
		return nil, true, nil
	}
	encoded, err := json.Marshal(answer)
	return encoded, true, err
}
