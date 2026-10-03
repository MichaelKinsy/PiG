package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// Ports packages/mcp/src/protocol/content.ts.

// ContentAnnotations are the annotations of a content block.
type ContentAnnotations struct {
	Audience     []string `json:"audience,omitempty"`
	Priority     *float64 `json:"priority,omitempty"`
	LastModified string   `json:"lastModified,omitempty"`
}

// ResourceContents is the contents of a resource: text when Text is non-nil,
// otherwise a base64 blob.
type ResourceContents struct {
	URI      string          `json:"uri"`
	MimeType string          `json:"mimeType,omitempty"`
	Text     *string         `json:"text,omitempty"`
	Blob     *string         `json:"blob,omitempty"`
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

// ContentBlock is one block of a tool result. Type selects the variant:
// "text" uses Text; "image" and "audio" use Data and MimeType; "resource_link"
// uses URI, Name, Title, Description, MimeType, and Size; "resource" uses
// Resource. A block decoded from JSON keeps its original bytes, so a server's
// block reaches a script exactly as sent.
type ContentBlock struct {
	Type        string
	Text        string
	Data        string
	MimeType    string
	URI         string
	Name        string
	Title       string
	Description string
	Size        *float64
	Resource    *ResourceContents
	Annotations *ContentAnnotations
	Meta        json.RawMessage

	raw json.RawMessage
}

type contentBlockWire struct {
	Type        string              `json:"type"`
	Text        *string             `json:"text,omitempty"`
	Data        *string             `json:"data,omitempty"`
	MimeType    string              `json:"mimeType,omitempty"`
	URI         string              `json:"uri,omitempty"`
	Name        string              `json:"name,omitempty"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Size        *float64            `json:"size,omitempty"`
	Resource    *ResourceContents   `json:"resource,omitempty"`
	Annotations *ContentAnnotations `json:"annotations,omitempty"`
	Meta        json.RawMessage     `json:"_meta,omitempty"`
}

// UnmarshalJSON reads a block and keeps its bytes.
func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	var w contentBlockWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*b = ContentBlock{
		Type: w.Type, MimeType: w.MimeType, URI: w.URI, Name: w.Name, Title: w.Title,
		Description: w.Description, Size: w.Size, Resource: w.Resource, Annotations: w.Annotations,
		Meta: w.Meta, raw: append(json.RawMessage(nil), data...),
	}
	if w.Text != nil {
		b.Text = *w.Text
	}
	if w.Data != nil {
		b.Data = *w.Data
	}
	return nil
}

// MarshalJSON writes the bytes the block was decoded from, or builds the
// block from its fields.
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	if b.raw != nil {
		return b.raw, nil
	}
	w := contentBlockWire{
		Type: b.Type, MimeType: b.MimeType, URI: b.URI, Name: b.Name, Title: b.Title,
		Description: b.Description, Size: b.Size, Resource: b.Resource, Annotations: b.Annotations, Meta: b.Meta,
	}
	switch b.Type {
	case "text":
		w.Text = &b.Text
	case "image", "audio":
		w.Data = &b.Data
	}
	return json.Marshal(w)
}

// CallToolResult is the result of tools/call.
type CallToolResult struct {
	Content           []ContentBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           *bool           `json:"isError,omitempty"`
	Meta              json.RawMessage `json:"_meta,omitempty"`

	// Raw is the result object as the server sent it, with `content` defaulted
	// to an empty list. A host that passes the whole result on, as upstream's
	// codemode bridge does, uses it to keep the server's members and their order.
	Raw json.RawMessage `json:"-"`
}

// LLMContent is tool result content in the shape LLM APIs accept: text and
// base64 images. It matches the text and image content of package ai.
type LLMContent struct {
	Type     string // "text" or "image"
	Text     string
	Data     string
	MimeType string
}

func blockToLLMContent(block ContentBlock) LLMContent {
	switch block.Type {
	case "text":
		return LLMContent{Type: "text", Text: block.Text}
	case "image":
		return LLMContent{Type: "image", Data: block.Data, MimeType: block.MimeType}
	case "audio":
		return LLMContent{Type: "text", Text: "[audio " + block.MimeType + " omitted]"}
	case "resource_link":
		return LLMContent{Type: "text", Text: block.Name + ": " + block.URI}
	case "resource":
		resource := block.Resource
		if resource == nil {
			return LLMContent{Type: "text", Text: "[unsupported MCP content resource]"}
		}
		if resource.Text != nil {
			return LLMContent{Type: "text", Text: *resource.Text}
		}
		blob := ""
		if resource.Blob != nil {
			blob = *resource.Blob
		}
		if strings.HasPrefix(resource.MimeType, "image/") {
			return LLMContent{Type: "image", Data: blob, MimeType: resource.MimeType}
		}
		mime := resource.MimeType
		if mime == "" {
			mime = "unknown type"
		}
		return LLMContent{Type: "text", Text: fmt.Sprintf("[binary resource %s (%s) omitted]", resource.URI, mime)}
	default:
		return LLMContent{Type: "text", Text: "[unsupported MCP content " + block.Type + "]"}
	}
}

// ToLLMContent converts a tool result to text and image content for a model.
// Text and images pass through, embedded text resources become text, embedded
// image resources become images, and other blocks (audio, resource links,
// binary resources) become a short text placeholder. A result without content
// blocks but with structuredContent becomes its indented JSON, since servers
// should, but do not always, mirror structured results as text.
func ToLLMContent(result CallToolResult) []LLMContent {
	content := make([]LLMContent, 0, len(result.Content))
	for _, block := range result.Content {
		content = append(content, blockToLLMContent(block))
	}
	if len(content) == 0 && len(result.StructuredContent) > 0 {
		content = append(content, LLMContent{Type: "text", Text: indentJSON(result.StructuredContent)})
	}
	return content
}

// indentJSON is JSON.stringify(value, null, 2).
func indentJSON(raw json.RawMessage) string {
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return string(raw)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, canonical, "", "  "); err != nil {
		return string(raw)
	}
	// JSON.stringify keeps empty containers on one line, as json.Indent does.
	return buf.String()
}
