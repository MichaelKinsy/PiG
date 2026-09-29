package ai

// Ports packages/ai/src/types.ts (SimpleStreamOptions.deferred).

import (
	"encoding/json"
	"fmt"
)

// DeferredOption is a deferred-generation boolean or an object with an optional window.
type DeferredOption struct {
	Object  bool
	Enabled bool
	Window  string
}

// MarshalJSON emits the upstream boolean or object form.
func (option DeferredOption) MarshalJSON() ([]byte, error) {
	if !option.Object {
		return json.Marshal(option.Enabled)
	}
	return json.Marshal(struct {
		Window string `json:"window,omitempty"`
	}{Window: option.Window})
}

// UnmarshalJSON decodes either deferred-generation option form.
func (option *DeferredOption) UnmarshalJSON(data []byte) error {
	var enabled bool
	if err := json.Unmarshal(data, &enabled); err == nil {
		*option = DeferredOption{Enabled: enabled}
		return nil
	}
	var object struct {
		Window string `json:"window"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("deferred option: %w", err)
	}
	*option = DeferredOption{Object: true, Window: object.Window}
	return nil
}
