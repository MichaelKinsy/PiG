package agent

import (
	"encoding/json/jsontext"

	"github.com/MichaelKinsy/PiG/internal/jsonwire"
)

// The MarshalJSONTo methods are MarshalJSON written straight into the encoder of the encoding/json that is marshaling the message, from the
// appender of the JSON.stringify text each message already has (jsonwire.WriteStringify). The sdk encoder, which does not know them, still
// calls MarshalJSON; message_json_to_test.go holds the two to the same bytes.

// MarshalJSONTo writes the message the way MarshalJSON does.
func (m AgentMessage) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, m.AppendStringify)
}

// MarshalJSONTo writes the message the way MarshalJSON does.
func (m AssistantMessage) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, m.AppendStringify)
}

// MarshalJSONTo writes the message the way MarshalJSON does.
func (m ToolResultMessage) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, m.AppendStringify)
}
