package subprocess

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// promptOptionsEventArgs preserves an untyped selection between handlers without changing the native event's value-typed public API.
func promptOptionsEventArgs(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	selected := extension.BeforeAgentStartSelectedTools(ctx)
	if selected == nil {
		return raw, nil
	}
	var event map[string]json.RawMessage
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(event["systemPromptOptions"], &options); err != nil {
		return nil, err
	}
	options["selectedTools"] = selected
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	event["systemPromptOptions"] = encoded
	return json.Marshal(event)
}
