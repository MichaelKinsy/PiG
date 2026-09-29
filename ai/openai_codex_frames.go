package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

type codexMappedEvent struct {
	data     []byte
	terminal bool
	skip     bool
}

func mapCodexWebSocketEventFrame(data []byte) (codexMappedEvent, error) {
	return mapCodexEventFrameForTransport(data, "WebSocket")
}

func mapCodexEventFrameForTransport(data []byte, transport string) (codexMappedEvent, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return codexMappedEvent{skip: true}, nil
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		return codexMappedEvent{}, fmt.Errorf("Invalid Codex %s JSON: %w", transport, err)
	}
	return mapCodexEvent(event, data)
}

// mapCodexEvent is mapCodexEvents' per-event step (openai-codex-responses.ts:729-760) for a parsed JSON object.
func mapCodexEvent(event map[string]any, data []byte) (codexMappedEvent, error) {
	typeName, _ := event["type"].(string)
	if typeName == "" {
		return codexMappedEvent{skip: true}, nil
	}
	switch typeName {
	case "error":
		code, message := codexErrorCodeAndMessage(event)
		detail := message
		if detail == "" {
			detail = code
		}
		if detail == "" {
			detail = string(data)
		}
		return codexMappedEvent{}, errors.New("Codex error: " + detail)
	case "response.failed":
		message := "Codex response failed"
		if response, _ := event["response"].(map[string]any); response != nil {
			if failure, _ := response["error"].(map[string]any); failure != nil {
				if value, _ := failure["message"].(string); value != "" {
					message = value
				}
			}
		}
		return codexMappedEvent{}, errors.New(message)
	case "response.done", "response.completed", "response.incomplete":
		if response, _ := event["response"].(map[string]any); response != nil {
			if status, _ := response["status"].(string); !codexResponseStatuses[status] {
				delete(response, "status")
			}
		}
		event["type"] = "response.completed"
		mapped, err := json.Marshal(event)
		return codexMappedEvent{data: mapped, terminal: true}, err
	default:
		return codexMappedEvent{data: data}, nil
	}
}

func codexErrorCodeAndMessage(event map[string]any) (code, message string) {
	code, _ = event["code"].(string)
	message, _ = event["message"].(string)
	if nested, _ := event["error"].(map[string]any); nested != nil {
		if code == "" {
			code, _ = nested["code"].(string)
		}
		if message == "" {
			message, _ = nested["message"].(string)
		}
	}
	return code, message
}
