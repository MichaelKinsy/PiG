//go:build !pig_strip_mistral_conversations

package ai

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// mistralWireMessages are the messages of a request in the SDK's wire member names: toMistralWireMessage keeps role, prefix, name and content, then
// appends tool_calls and tool_call_id (mistral-conversations.ts:394-406), and toMistralWireContentChunk renames imageUrl to image_url (:408-418).
//
// MarshalJSONTo writes each message and content chunk straight from the mistralMessage the converter built. Lowering the messages into a second set of
// structs first boxed every chunk slice into an `any`, and encoding/json then copied each `any` again to reach it: two allocations per message.
type mistralWireMessages []mistralMessage

// MarshalJSONTo writes the messages as an array, which an empty or nil list is too. A member is left out as the struct tags it replaces left it out:
// prefix when nil, name and tool_call_id when empty, content, tool_calls, thinking when nil (an `any` holding an empty string or slice is written).
func (messages mistralWireMessages) MarshalJSONTo(encoder *jsontext.Encoder) error {
	if err := encoder.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for i := range messages {
		if err := messages[i].writeWire(encoder); err != nil {
			return err
		}
	}
	return encoder.WriteToken(jsontext.EndArray)
}

func (message *mistralMessage) writeWire(encoder *jsontext.Encoder) error {
	if err := encoder.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeStringMember(encoder, "role", message.Role); err != nil {
		return err
	}
	if message.Prefix != nil {
		if err := encoder.WriteToken(jsontext.String("prefix")); err != nil {
			return err
		}
		if err := encoder.WriteToken(jsontext.Bool(*message.Prefix)); err != nil {
			return err
		}
	}
	if message.Name != "" {
		if err := writeStringMember(encoder, "name", message.Name); err != nil {
			return err
		}
	}
	if message.Content != nil {
		if err := encoder.WriteToken(jsontext.String("content")); err != nil {
			return err
		}
		if err := writeMistralContent(encoder, message.Content); err != nil {
			return err
		}
	}
	if message.ToolCalls != nil {
		if err := encoder.WriteToken(jsontext.String("tool_calls")); err != nil {
			return err
		}
		if err := jsonv2.MarshalEncode(encoder, message.ToolCalls); err != nil {
			return err
		}
	}
	if message.ToolCallID != "" {
		if err := writeStringMember(encoder, "tool_call_id", message.ToolCallID); err != nil {
			return err
		}
	}
	return encoder.WriteToken(jsontext.EndObject)
}

func writeMistralContent(encoder *jsontext.Encoder, content any) error {
	switch content := content.(type) {
	case string:
		return encoder.WriteToken(jsontext.String(content))
	case []mistralContentChunk:
		if err := encoder.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for i := range content {
			if err := content[i].writeWire(encoder); err != nil {
				return err
			}
		}
		return encoder.WriteToken(jsontext.EndArray)
	}
	return jsonv2.MarshalEncode(encoder, content)
}

// writeWire writes a chunk as mistralWireChunk did: type, text, thinking, image_url; a chunk has one of text, thinking and image_url.
func (chunk *mistralContentChunk) writeWire(encoder *jsontext.Encoder) error {
	if err := encoder.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeStringMember(encoder, "type", chunk.Type); err != nil {
		return err
	}
	if chunk.Text != nil {
		if err := writeStringMember(encoder, "text", *chunk.Text); err != nil {
			return err
		}
	}
	if chunk.Thinking != nil {
		if err := encoder.WriteToken(jsontext.String("thinking")); err != nil {
			return err
		}
		if err := writeMistralThinking(encoder, chunk.Thinking); err != nil {
			return err
		}
	}
	if chunk.ImageURL != "" {
		if err := writeStringMember(encoder, "image_url", chunk.ImageURL); err != nil {
			return err
		}
	}
	return encoder.WriteToken(jsontext.EndObject)
}

func writeMistralThinking(encoder *jsontext.Encoder, thinking any) error {
	parts, ok := thinking.([]mistralTextChunk)
	if !ok {
		return jsonv2.MarshalEncode(encoder, thinking)
	}
	if parts == nil {
		// The encoder this replaces wrote a nil slice as null, as encoding/json does.
		return encoder.WriteToken(jsontext.Null)
	}
	if err := encoder.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for i := range parts {
		if err := encoder.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeStringMember(encoder, "type", parts[i].Type); err != nil {
			return err
		}
		if err := writeStringMember(encoder, "text", parts[i].Text); err != nil {
			return err
		}
		if err := encoder.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
	}
	return encoder.WriteToken(jsontext.EndArray)
}

func writeStringMember(encoder *jsontext.Encoder, name, value string) error {
	if err := encoder.WriteToken(jsontext.String(name)); err != nil {
		return err
	}
	return encoder.WriteToken(jsontext.String(value))
}

// MarshalJSON is the JSON.stringify text of the messages for an encoder that does not call MarshalJSONTo: the one that keeps a lone UTF-16 unit as an
// escape (encodeRequestBody falls back to it for such a request). It writes the messages from the structs the encoder lowers them to.
func (messages mistralWireMessages) MarshalJSON() ([]byte, error) {
	//perf:ok cold path: runs only for a request with a lone UTF-16 surrogate, which the typed MarshalJSONTo writes as U+FFFD; the lowered copy is what JSON.stringify's escape needs
	return jsstring.MarshalJSON(messages.lowered())
}

// mistralWireMessage is a mistralMessage after toMistralWireMessage, which appends tool_calls and tool_call_id after the members it keeps.
type mistralWireMessage struct {
	Role       string `json:"role"`
	Prefix     *bool  `json:"prefix,omitempty"`
	Name       string `json:"name,omitempty"`
	Content    any    `json:"content,omitempty"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// mistralWireChunk is a mistralContentChunk after toMistralWireContentChunk; a chunk has one of text, thinking and image_url.
type mistralWireChunk struct {
	Type     string  `json:"type"`
	Text     *string `json:"text,omitempty"`
	Thinking any     `json:"thinking,omitempty"`
	ImageURL string  `json:"image_url,omitempty"`
}

func (messages mistralWireMessages) lowered() []mistralWireMessage {
	lowered := make([]mistralWireMessage, len(messages))
	for i, message := range messages {
		content := message.Content
		if chunks, ok := content.([]mistralContentChunk); ok {
			wire := make([]mistralWireChunk, len(chunks))
			for j, chunk := range chunks {
				wire[j] = mistralWireChunk{Type: chunk.Type, Text: chunk.Text, Thinking: chunk.Thinking, ImageURL: chunk.ImageURL}
			}
			content = wire
		}
		lowered[i] = mistralWireMessage{Role: message.Role, Prefix: message.Prefix, Name: message.Name, Content: content, ToolCalls: message.ToolCalls, ToolCallID: message.ToolCallID}
	}
	return lowered
}
