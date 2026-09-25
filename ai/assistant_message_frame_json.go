package ai

import (
	"encoding/json"
	"fmt"
)

// UnmarshalAssistantMessageFrame decodes one frame by its `type` discriminator.
func UnmarshalAssistantMessageFrame(data []byte) (AssistantMessageFrame, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("assistant message frame: %w", err)
	}
	switch probe.Type {
	case "start":
		return decodeFrame[StartFrame](data)
	case "text_start":
		return decodeFrame[TextStartFrame](data)
	case "text_delta":
		return decodeFrame[TextDeltaFrame](data)
	case "text_end":
		return decodeFrame[TextEndFrame](data)
	case "thinking_start":
		return decodeFrame[ThinkingStartFrame](data)
	case "thinking_delta":
		return decodeFrame[ThinkingDeltaFrame](data)
	case "thinking_end":
		return decodeFrame[ThinkingEndFrame](data)
	case "toolcall_start":
		return decodeFrame[ToolCallStartFrame](data)
	case "toolcall_checkpoint":
		return decodeFrame[ToolCallCheckpointFrame](data)
	case "toolcall_delta":
		return decodeFrame[ToolCallDeltaFrame](data)
	case "toolcall_end":
		return decodeFrame[ToolCallEndFrame](data)
	default:
		return nil, fmt.Errorf("unknown assistant message frame type %q", probe.Type)
	}
}

func decodeFrame[T AssistantMessageFrame](data []byte) (AssistantMessageFrame, error) {
	var frame T
	if err := json.Unmarshal(data, &frame); err != nil {
		return nil, fmt.Errorf("assistant message frame: %w", err)
	}
	return frame, nil
}

// UnmarshalJSON decodes the upstream AssistantMessage wire shape, including
// its closed content-block union.
func (message *AssistantMessage) UnmarshalJSON(data []byte) error {
	type plain AssistantMessage
	var wire struct {
		plain
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	decoded := AssistantMessage(wire.plain)
	if wire.Content != nil {
		decoded.Content = make([]AssistantContentBlock, 0, len(wire.Content))
		for index, raw := range wire.Content {
			block, err := UnmarshalContentBlock(raw)
			if err != nil {
				return fmt.Errorf("assistant content[%d]: %w", index, err)
			}
			assistantBlock, ok := block.(AssistantContentBlock)
			if !ok {
				return fmt.Errorf("assistant content[%d]: %s is not an assistant content block", index, block.contentType())
			}
			decoded.Content = append(decoded.Content, assistantBlock)
		}
	}
	*message = decoded
	return nil
}
