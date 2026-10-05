// Package ollamanative implements a PiG provider that uses Ollama's native
// /api/chat endpoint for tool calling support.
package ollamanative

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	providerName = "ollama-native"
	apiType      = "ollama-native"
	defaultBase  = "http://localhost:11434"
)

// Extension returns a PiG extension that registers the ollama-native provider.
func Extension() *sdk.Extension {
	ext := sdk.New(providerName)

	ext.RegisterProvider(providerName, sdk.ProviderConfig{
		"name":    "Ollama Native",
		"baseUrl": defaultBase,
		"apiKey":  "ollama",
		"api":     apiType,
		"models": []any{
			map[string]any{
				"id":            "granite4.1:3b",
				"name":          "Granite 4.1 3B",
				"reasoning":     false,
				"contextWindow": 32768,
				"maxTokens":     4096,
				"input":         []string{"text"},
				"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			},
			map[string]any{
				"id":            "minicpm5-1b-uncensored:latest",
				"name":          "MiniCPM5 1B Uncensored",
				"reasoning":     false,
				"contextWindow": 32768,
				"maxTokens":     4096,
				"input":         []string{"text"},
				"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			},
			map[string]any{
				"id":            "lfm25-1b-uncensored:latest",
				"name":          "LFM2.5 1B Uncensored",
				"reasoning":     false,
				"contextWindow": 32768,
				"maxTokens":     4096,
				"input":         []string{"text"},
				"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			},
		},
		"streamSimple": streamOllamaNative,
	})

	return ext
}

// ollamaStreamChunk represents a single chunk from Ollama streaming response.
type ollamaStreamChunk struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		Thinking  string `json:"thinking,omitempty"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Index     int                 `json:"index"`
				Name      string              `json:"name"`
				Arguments map[string]any      `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls,omitempty"`
	} `json:"message"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason,omitempty"`
}

// streamOllamaNative implements the streaming provider for Ollama native API.
func streamOllamaNative(ctx sdk.Context, model map[string]any, request map[string]any, _ map[string]any) (*sdk.ModelEventStream, error) {
	stream := sdk.CreateAssistantMessageEventStream()

	// Get tools from host
	tools, err := ctx.GetAllTools()
	if err != nil {
		stream.End(map[string]any{
			"type": "error",
			"error": map[string]any{
				"errorMessage": err.Error(),
				"stopReason":   "error",
			},
		})
		return stream, nil
	}

	// Extract messages from request
	messages := []map[string]any{}
	if msgs, ok := request["messages"].([]any); ok {
		for _, m := range msgs {
			if msg, ok := m.(map[string]any); ok {
				role, _ := msg["role"].(string)
				content := convertContentToString(msg["content"])
				messages = append(messages, map[string]any{
					"role":    role,
					"content": content,
				})
			}
		}
	}

	// Build Ollama request
	ollamaReq := map[string]any{
		"model":    model["id"],
		"messages": messages,
		"stream":   true,
	}

	// Convert PiG tools to Ollama format
	if len(tools) > 0 {
		ollamaTools := []any{}
		for _, tool := range tools {
			var params map[string]any
			if err := json.Unmarshal(tool.Parameters, &params); err != nil {
				params = map[string]any{"type": "object"}
			}
			ollamaTool := map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  params,
				},
			}
			ollamaTools = append(ollamaTools, ollamaTool)
		}
		ollamaReq["tools"] = ollamaTools
	}

	jsonData, err := json.Marshal(ollamaReq)
	if err != nil {
		stream.End(map[string]any{
			"type": "error",
			"error": map[string]any{
				"errorMessage": err.Error(),
				"stopReason":   "error",
			},
		})
		return stream, nil
	}

	// Make HTTP request
	httpReq, err := http.NewRequest("POST", defaultBase+"/api/chat", bytes.NewReader(jsonData))
	if err != nil {
		stream.End(map[string]any{
			"type": "error",
			"error": map[string]any{
				"errorMessage": err.Error(),
				"stopReason":   "error",
			},
		})
		return stream, nil
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		stream.End(map[string]any{
			"type": "error",
			"error": map[string]any{
				"errorMessage": err.Error(),
				"stopReason":   "error",
			},
		})
		return stream, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		stream.End(map[string]any{
			"type": "error",
			"error": map[string]any{
				"errorMessage": fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
				"stopReason":   "error",
			},
		})
		return stream, nil
	}

	// Parse streaming response
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Track state
	var finalStopReason string
	var hasToolCall bool
	var toolCallID, toolCallName string
	var toolCallArgs bytes.Buffer
	contentIndex := 0
	textBuffer := bytes.NewBuffer(nil)

	// Push start event
	partialMsg := map[string]any{
		"role": "assistant",
		"api": apiType,
		"provider": providerName,
		"model": model["id"],
		"stopReason": "pending",
		"timestamp": time.Now().UnixMilli(),
		"content": []any{},
	}
	stream.Push(map[string]any{"type": "start", "partial": partialMsg})

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chunk ollamaStreamChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}

		// Handle tool calls
		if len(chunk.Message.ToolCalls) > 0 {
			hasToolCall = true
			tc := chunk.Message.ToolCalls[0]
			
			if tc.ID != "" {
				toolCallID = tc.ID
			}
			if tc.Function.Name != "" {
				toolCallName = tc.Function.Name
			}
			
			// Accumulate arguments
			if tc.Function.Arguments != nil {
				argsJSON, _ := json.Marshal(tc.Function.Arguments)
				toolCallArgs.Write(argsJSON)
			}
		}

		// Handle text content
		if chunk.Message.Content != "" {
			if textBuffer.Len() == 0 {
				// First text chunk - push text_start
				stream.Push(map[string]any{
					"type": "text_start",
					"contentIndex": contentIndex,
					"partial": partialMsg,
				})
			}
			textBuffer.WriteString(chunk.Message.Content)
			stream.Push(map[string]any{
				"type": "text_delta",
				"contentIndex": contentIndex,
				"delta": chunk.Message.Content,
				"partial": partialMsg,
			})
		}

		// Handle final chunk
		if chunk.Done {
			finalStopReason = chunk.DoneReason
			break
		}
	}

	// Push text_end if we have content
	if textBuffer.Len() > 0 {
		stream.Push(map[string]any{
			"type": "text_end",
			"contentIndex": contentIndex,
			"content": textBuffer.String(),
			"partial": partialMsg,
		})
		contentIndex++
	}

	// Push tool call events if we have a tool call
	if hasToolCall && toolCallID != "" {
		argsJSON := toolCallArgs.Bytes()
		var args map[string]any
		json.Unmarshal(argsJSON, &args)

		toolCallBlock := map[string]any{
			"type": "toolCall",
			"id": toolCallID,
			"name": toolCallName,
			"arguments": args,
		}

		partialMsg["content"] = append(partialMsg["content"].([]any), toolCallBlock)

		stream.Push(map[string]any{
			"type": "toolcall_start",
			"contentIndex": contentIndex,
			"id": toolCallID,
			"toolName": toolCallName,
			"partial": partialMsg,
		})
		stream.Push(map[string]any{
			"type": "toolcall_delta",
			"contentIndex": contentIndex,
			"delta": string(argsJSON),
			"partial": partialMsg,
		})
		stream.Push(map[string]any{
			"type": "toolcall_end",
			"contentIndex": contentIndex,
			"toolCall": toolCallBlock,
			"partial": partialMsg,
		})
	}

	// Determine final stop reason
	stopReason := "stop"
	if finalStopReason == "tool_calls" || hasToolCall {
		stopReason = "toolUse"
	}

	// Update partial with final state
	partialMsg["stopReason"] = stopReason
	if textBuffer.Len() > 0 {
		partialMsg["content"] = append(partialMsg["content"].([]any), map[string]any{
			"type": "text",
			"text": textBuffer.String(),
		})
	}

	// Push done event
	stream.Push(map[string]any{
		"type": "done",
		"reason": stopReason,
		"message": partialMsg,
	})

	return stream, nil
}

// convertContentToString converts PiG's content format to Ollama's string format.
func convertContentToString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var result bytes.Buffer
		for _, block := range v {
			if block, ok := block.(map[string]any); ok {
				if text, ok := block["text"].(string); ok {
					result.WriteString(text)
				}
			}
		}
		return result.String()
	default:
		return ""
	}
}
