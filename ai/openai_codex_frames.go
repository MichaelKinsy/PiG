package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

type codexMappedEvent struct {
	data     []byte
	terminal bool
	skip     bool
}

// mapCodexWebSocketEventFrame is parseWebSocket's onMessage (openai-codex-responses.ts:1325-1348) and mapCodexEvents for one text frame: an empty frame is skipped, any other text goes to JSON.parse as it is (so "[DONE]" is invalid), a null value fails reading its `type`, and a value that is not an object has no type and is skipped. The mapped data is compact, so a frame written over several lines stays one SSE data line.
func mapCodexWebSocketEventFrame(data []byte) (codexMappedEvent, error) {
	if len(data) == 0 {
		return codexMappedEvent{skip: true}, nil
	}
	if err := jsonparse.Validate(data); err != nil {
		return codexMappedEvent{}, fmt.Errorf("Invalid Codex WebSocket JSON: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return codexMappedEvent{}, fmt.Errorf("Invalid Codex WebSocket JSON: %w", err)
	}
	if value == nil {
		return codexMappedEvent{}, errors.New("Invalid Codex WebSocket JSON: Cannot read properties of null (reading 'type')")
	}
	event, ok := value.(map[string]any)
	if !ok {
		return codexMappedEvent{skip: true}, nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return codexMappedEvent{}, fmt.Errorf("Invalid Codex WebSocket JSON: %w", err)
	}
	return mapCodexEvent(event, compact.Bytes())
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
