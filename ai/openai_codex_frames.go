package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type codexMappedEvent struct {
	data     []byte
	terminal bool
	skip     bool
}

func mapCodexEventFrame(data []byte) (codexMappedEvent, error) {
	return mapCodexEventFrameForTransport(data, "SSE")
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

func newCodexMappedSSEReader(ctx context.Context, source io.Reader) io.Reader {
	reader, writer := io.Pipe()
	go func() {
		err := mapCodexSSE(ctx, source, writer)
		_ = writer.CloseWithError(err)
	}()
	return reader
}

func mapCodexSSE(ctx context.Context, source io.Reader, destination io.Writer) error {
	decoder := newSSEDecoder(source)
	for decoder.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		mapped, err := mapCodexEventFrame([]byte(decoder.Event().Data))
		if err != nil {
			return err
		}
		if mapped.skip {
			continue
		}
		if _, err := fmt.Fprintf(destination, "data: %s\n\n", mapped.data); err != nil {
			return err
		}
		if mapped.terminal {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return decoder.Err()
}
