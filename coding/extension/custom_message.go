package extension

import "encoding/json"

// CustomMessage is the custom message an extension's message renderer receives (core/messages.ts:46 CustomMessage<T>).
type CustomMessage struct {
	CustomType string
	// Content is a string or an array of text and image content blocks.
	Content any
	Display bool
	// Details is the extension's own data for the renderer; nil leaves it unset.
	Details any
	// Timestamp is the message's time in milliseconds since the Unix epoch.
	Timestamp int64
}

// MessageRole is "custom": a CustomMessage is a SessionMessage, a member of the union session-manager.ts appendMessage takes.
func (CustomMessage) MessageRole() string { return "custom" }

// MarshalJSON writes the object Pi's renderer reads: role is always "custom" and details is left out when unset.
func (m CustomMessage) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Role       string `json:"role"`
		CustomType string `json:"customType"`
		Content    any    `json:"content"`
		Display    bool   `json:"display"`
		Details    any    `json:"details,omitempty"`
		Timestamp  int64  `json:"timestamp"`
	}{"custom", m.CustomType, m.Content, m.Display, m.Details, m.Timestamp})
}
