package agentharness

// Ports packages/agent/src/harness/agent-harness.ts

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
)

// UnmarshalJSON restores the streaming assistant through the AgentMessage codec instead of asking encoding/json to instantiate interface-valued content blocks.
func (operation *LaneSnapshotOperation) UnmarshalJSON(data []byte) error {
	type plain LaneSnapshotOperation
	var fields struct {
		plain
		StreamingMessage json.RawMessage `json:"streamingMessage"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields.StreamingMessage) != 0 && string(fields.StreamingMessage) != "null" {
		var message agent.AgentMessage
		if err := json.Unmarshal(fields.StreamingMessage, &message); err != nil {
			return err
		}
		if message.Assistant == nil {
			return fmt.Errorf("lane streaming message must be an assistant")
		}
		fields.plain.StreamingMessage = message.Assistant
	}
	*operation = LaneSnapshotOperation(fields.plain)
	return nil
}

// UnmarshalJSON restores the message/custom union and preserves absent versus null custom data when a replicated lane snapshot crosses JSON.
func (item *LaneQueuedItem) UnmarshalJSON(data []byte) error {
	var fields struct {
		EntryID    string          `json:"entryId"`
		Kind       string          `json:"kind"`
		Type       string          `json:"type"`
		Message    json.RawMessage `json:"message"`
		CustomType string          `json:"customType"`
		Data       json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	next := LaneQueuedItem{EntryID: fields.EntryID, Kind: fields.Kind, Type: fields.Type}
	switch fields.Type {
	case "message":
		if err := json.Unmarshal(fields.Message, &next.Message); err != nil {
			return err
		}
	case "custom":
		next.CustomType = fields.CustomType
		next.HasData = len(fields.Data) != 0
		if next.HasData {
			if err := json.Unmarshal(fields.Data, &next.Data); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown lane queued item type: %q", fields.Type)
	}
	*item = next
	return nil
}
