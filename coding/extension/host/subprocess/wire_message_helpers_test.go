package subprocess

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// wireMessage decodes a wire-shaped message into the host AgentMessage, as the
// subprocess host decodes a handler's returned messages.
func wireMessage(fields map[string]any) extension.AgentMessage {
	raw, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	var message extension.AgentMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		panic(err)
	}
	return message
}

// wireContent reads the wire "content" member of a message whose content is a string.
func wireContent(message extension.AgentMessage) string {
	raw, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}
	var fields struct {
		Content any `json:"content"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		panic(err)
	}
	text, _ := fields.Content.(string)
	return text
}
