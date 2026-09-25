package pico3

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// applyFrame applies one encoded frame to the stored turn message. It is the
// same switch as ai.ReduceAssistantMessageFrames, over the stored JSON form.
func applyFrame(turn JsonObject, frame ai.AssistantMessageFrame) error {
	if start, ok := frame.(ai.StartFrame); ok {
		turn["message"] = storedMessage(start.Partial)
		return nil
	}
	message, ok := turn["message"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s before start", frame.FrameType())
	}
	content := arr(message, "content")
	switch value := frame.(type) {
	case ai.TextStartFrame:
		content = setBlock(content, value.ContentIndex, mustStored(value.Content))
	case ai.TextDeltaFrame:
		block := blockAt(content, value.ContentIndex)
		block["text"] = str(block, "text") + value.Delta
	case ai.TextEndFrame:
		block := blockAt(content, value.ContentIndex)
		block["text"] = value.Content
		delete(block, "textSignature")
		if value.TextSignature != "" {
			block["textSignature"] = value.TextSignature
		}
	case ai.ThinkingStartFrame:
		content = setBlock(content, value.ContentIndex, mustStored(value.Content))
	case ai.ThinkingDeltaFrame:
		block := blockAt(content, value.ContentIndex)
		block["thinking"] = str(block, "thinking") + value.Delta
	case ai.ThinkingEndFrame:
		applyThinkingEnd(blockAt(content, value.ContentIndex), value)
	case ai.ToolCallStartFrame:
		content = setBlock(content, value.ContentIndex, mustStored(value.ToolCall))
	case ai.ToolCallCheckpointFrame:
		blockAt(content, value.ContentIndex)["arguments"] = safeParse(value.JSON)
	case ai.ToolCallDeltaFrame:
		// Arguments materialise at checkpoint or end; deltas only feed the
		// encoder's own JSON buffer.
	case ai.ToolCallEndFrame:
		applyToolCallEnd(blockAt(content, value.ContentIndex), value)
	}
	message["content"] = content
	return nil
}

func setBlock(content []any, index int, block JsonValue) []any {
	for len(content) <= index {
		content = append(content, nil)
	}
	content[index] = block
	return content
}

func blockAt(content []any, index int) JsonObject {
	if index < 0 || index >= len(content) {
		return JsonObject{}
	}
	block, _ := content[index].(map[string]any)
	if block == nil {
		return JsonObject{}
	}
	return block
}

func applyThinkingEnd(block JsonObject, frame ai.ThinkingEndFrame) {
	block["thinking"] = frame.Content
	delete(block, "thinkingSignature")
	delete(block, "redacted")
	if frame.ThinkingSignature != "" {
		block["thinkingSignature"] = frame.ThinkingSignature
	}
	if frame.Redacted {
		block["redacted"] = frame.Redacted
	}
}

func applyToolCallEnd(block JsonObject, frame ai.ToolCallEndFrame) {
	block["id"] = frame.ID
	block["name"] = frame.Name
	block["arguments"] = mustStored(frame.Arguments)
	if frame.ThoughtSignature != "" {
		block["thoughtSignature"] = frame.ThoughtSignature
	}
	if frame.Namespace != "" {
		block["namespace"] = frame.Namespace
	}
}

func safeParse(text string) JsonValue {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return JsonObject{}
	}
	return value
}
