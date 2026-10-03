package durable

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// The pi-ai Message union in its JSON form, as entries, context edits, and documents store it. ai's message types
// encode their role; DecodeMessage restores the concrete type from it.

// DecodeMessage decodes one JSON message by its role.
func DecodeMessage(data []byte) (ai.Message, error) {
	var probe struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("message: %w", err)
	}
	switch probe.Role {
	case "user":
		type plain ai.UserMessage
		var wire struct {
			plain
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("user message: %w", err)
		}
		message := ai.UserMessage(wire.plain)
		content, err := decodeUserContent(wire.Content)
		if err != nil {
			return nil, err
		}
		message.Content = content
		return message, nil
	case "assistant":
		var message ai.AssistantMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, fmt.Errorf("assistant message: %w", err)
		}
		return message, nil
	case "toolResult":
		var message ai.ToolResultMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, fmt.Errorf("tool result message: %w", err)
		}
		return message, nil
	case "system":
		type plain ai.SystemMessage
		var wire struct {
			plain
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("system message: %w", err)
		}
		message := ai.SystemMessage(wire.plain)
		content, err := decodeSystemContent(wire.Content)
		if err != nil {
			return nil, err
		}
		message.Content = content
		return message, nil
	}
	return nil, fmt.Errorf("message has unknown role %q", probe.Role)
}

// DecodeUserContent decodes user message content held as a JSON value: a string or an array of text and image
// blocks.
func DecodeUserContent(value JsonValue) (ai.UserContent, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return decodeUserContent(raw)
}

func decodeUserContent(raw json.RawMessage) (ai.UserContent, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return ai.UserText(text), nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, fmt.Errorf("user content: %w", err)
	}
	blocks := make(ai.UserContentBlocks, 0, len(raws))
	for index, item := range raws {
		block, err := ai.UnmarshalContentBlock(item)
		if err != nil {
			return nil, fmt.Errorf("user content[%d]: %w", index, err)
		}
		typed, ok := block.(ai.UserContentBlock)
		if !ok {
			return nil, fmt.Errorf("user content[%d] is %T, not user content", index, block)
		}
		blocks = append(blocks, typed)
	}
	return blocks, nil
}

func decodeSystemContent(raw json.RawMessage) (ai.SystemContent, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return ai.SystemText(text), nil
	}
	var blocks ai.SystemTextBlocks
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("system content: %w", err)
	}
	return blocks, nil
}

// DecodeMessages decodes a JSON message array; JSON null stays nil.
func DecodeMessages(data []byte) ([]ai.Message, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	if raws == nil {
		return nil, nil
	}
	messages := make([]ai.Message, 0, len(raws))
	for index, raw := range raws {
		message, err := DecodeMessage(raw)
		if err != nil {
			return nil, fmt.Errorf("message[%d]: %w", index, err)
		}
		messages = append(messages, message)
	}
	return messages, nil
}
