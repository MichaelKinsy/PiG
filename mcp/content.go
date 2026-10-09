package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
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
	// Raw is the entry of resources/read as the server sent it; empty for contents built in Go.
	Raw json.RawMessage `json:"-"`
}

// ContentBlockType is the "type" of a ContentBlock: the closed union of protocol/content.ts ContentBlock. A block decoded from JSON keeps whatever type the server sent.
type ContentBlockType string

// The ContentBlock types.
const (
	ContentText         ContentBlockType = "text"
	ContentImage        ContentBlockType = "image"
	ContentAudio        ContentBlockType = "audio"
	ContentResourceLink ContentBlockType = "resource_link"
	ContentResource     ContentBlockType = "resource"
)

// ContentBlock is one block of a tool result. Type selects the variant:
// "text" uses Text; "image" and "audio" use Data and MimeType; "resource_link"
// uses URI, Name, Title, Description, MimeType, and Size; "resource" uses
// Resource. A block decoded from JSON keeps its original bytes, so a server's
// block reaches a script exactly as sent.
type ContentBlock struct {
	Type     ContentBlockType
	Text     string
	Data     string
	MimeType string
	URI      string
	Name     string
	Title    string
	// HasTitle is set when the server sent a non-null `title`, possibly empty. A title of a block built in Go is
	// present when it is not empty.
	HasTitle    bool
	Description string
	Size        *float64
	Resource    *ResourceContents
	Annotations *ContentAnnotations
	Meta        json.RawMessage

	raw json.RawMessage
}

type contentBlockWire struct {
	Type        ContentBlockType    `json:"type"`
	Text        *string             `json:"text,omitempty"`
	Data        *string             `json:"data,omitempty"`
	MimeType    string              `json:"mimeType,omitempty"`
	URI         string              `json:"uri,omitempty"`
	Name        string              `json:"name,omitempty"`
	Title       *string             `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Size        *float64            `json:"size,omitempty"`
	Resource    *ResourceContents   `json:"resource,omitempty"`
	Annotations *ContentAnnotations `json:"annotations,omitempty"`
	Meta        json.RawMessage     `json:"_meta,omitempty"`
}

// UnmarshalJSON reads a block and keeps its bytes. Upstream's client passes a
// server's block through unchecked (client.ts validateCallToolResult), so a
// member of another JSON type, an absent member and a block that is not an
// object are not decode errors: the typed fields take the members that have
// the declared type, and the kept bytes carry every member for ToLLMContent.
func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	*b = ContentBlock{raw: append(json.RawMessage(nil), data...)}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil
	}
	str := func(name string) (string, bool) {
		var v string
		raw, ok := members[name]
		if !ok || json.Unmarshal(raw, &v) != nil || len(raw) == 0 || raw[0] != '"' {
			return "", false
		}
		return v, true
	}
	if v, ok := str("type"); ok {
		b.Type = ContentBlockType(v)
	}
	b.Text, _ = str("text")
	b.Data, _ = str("data")
	b.MimeType, _ = str("mimeType")
	b.URI, _ = str("uri")
	b.Name, _ = str("name")
	b.Title, b.HasTitle = str("title")
	b.Description, _ = str("description")
	if raw, ok := members["size"]; ok && len(raw) > 0 && raw[0] != '"' {
		var n float64
		if json.Unmarshal(raw, &n) == nil {
			b.Size = &n
		}
	}
	if raw, ok := members["resource"]; ok {
		b.Resource = lenientResource(raw)
	}
	if raw, ok := members["annotations"]; ok {
		var a ContentAnnotations
		if json.Unmarshal(raw, &a) == nil {
			b.Annotations = &a
		}
	}
	b.Meta = members["_meta"]
	return nil
}

// lenientResource decodes the embedded resource of a block, keeping the members that have their declared type.
func lenientResource(raw json.RawMessage) *ResourceContents {
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || members == nil {
		return nil
	}
	str := func(name string) *string {
		var v string
		m, ok := members[name]
		if !ok || len(m) == 0 || m[0] != '"' || json.Unmarshal(m, &v) != nil {
			return nil
		}
		return &v
	}
	r := &ResourceContents{Text: str("text"), Blob: str("blob"), Meta: members["_meta"]}
	if v := str("uri"); v != nil {
		r.URI = *v
	}
	if v := str("mimeType"); v != nil {
		r.MimeType = *v
	}
	return r
}

// MarshalJSON writes the bytes the block was decoded from, or builds the
// block from its fields.
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	if b.raw != nil {
		return b.raw, nil
	}
	var title *string
	if b.HasTitle || b.Title != "" {
		title = &b.Title
	}
	w := contentBlockWire{
		Type: b.Type, MimeType: b.MimeType, URI: b.URI, Name: b.Name, Title: title,
		Description: b.Description, Size: b.Size, Resource: b.Resource, Annotations: b.Annotations, Meta: b.Meta,
	}
	switch b.Type {
	case ContentText:
		w.Text = &b.Text
	case ContentImage, ContentAudio:
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

// UnmarshalJSON reads a tools/call result as upstream's client does: `content` is a list of blocks, `isError` is read for its JavaScript truthiness
// (client.ts passes the server's value through and tools.ts tests `result.isError`), and no member is rejected for its type.
func (r *CallToolResult) UnmarshalJSON(data []byte) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	*r = CallToolResult{StructuredContent: members["structuredContent"], Meta: members["_meta"]}
	if raw, ok := members["content"]; ok {
		var blocks []ContentBlock
		if err := json.Unmarshal(raw, &blocks); err == nil {
			r.Content = blocks
		}
	}
	if raw, ok := members["isError"]; ok && string(raw) != "null" {
		truthy := jsTruthy(raw)
		r.IsError = &truthy
	}
	return nil
}

// jsTruthy is JavaScript's truthiness of a JSON value: false, null, 0, -0 and "" are falsy.
func jsTruthy(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return true
	}
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	}
	return true
}

// stringOrUndefined is a member copied into a text or image result: an absent member is `undefined`, which Go spells as the empty string.
func stringOrUndefined(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	return jsString(raw, true)
}

// jsString is the string a JavaScript template literal makes of a JSON value; an absent member is `undefined`.
func jsString(raw json.RawMessage, present bool) string {
	if !present {
		return "undefined"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "undefined"
	}
	return jsValueString(v)
}

func jsValueString(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		canonical, err := jsonstringify.Canonicalize([]byte(strconv.FormatFloat(v, 'g', -1, 64)))
		if err != nil {
			return strconv.FormatFloat(v, 'g', -1, 64)
		}
		return string(canonical)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item != nil {
				parts[i] = jsValueString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// LLMContentType is the discriminator of LLMContent: upstream's `"text" | "image"`.
type LLMContentType string

const (
	LLMContentTypeText  LLMContentType = "text"
	LLMContentTypeImage LLMContentType = "image"
)

// LLMContent is tool result content in the shape LLM APIs accept: text and
// base64 images. It matches the text and image content of package ai.
type LLMContent struct {
	Type     LLMContentType
	Text     string
	Data     string
	MimeType string
}

func blockToLLMContent(block ContentBlock) LLMContent {
	if block.raw != nil {
		return decodedBlockToLLMContent(block)
	}
	switch block.Type {
	case ContentText:
		return LLMContent{Type: LLMContentTypeText, Text: block.Text}
	case ContentImage:
		return LLMContent{Type: LLMContentTypeImage, Data: block.Data, MimeType: block.MimeType}
	case ContentAudio:
		return LLMContent{Type: LLMContentTypeText, Text: "[audio " + block.MimeType + " omitted]"}
	case ContentResourceLink:
		return LLMContent{Type: LLMContentTypeText, Text: block.Name + ": " + block.URI}
	case ContentResource:
		resource := block.Resource
		if resource == nil {
			return LLMContent{Type: LLMContentTypeText, Text: "[unsupported MCP content resource]"}
		}
		if resource.Text != nil {
			return LLMContent{Type: LLMContentTypeText, Text: *resource.Text}
		}
		blob := ""
		if resource.Blob != nil {
			blob = *resource.Blob
		}
		if strings.HasPrefix(resource.MimeType, "image/") {
			return LLMContent{Type: LLMContentTypeImage, Data: blob, MimeType: resource.MimeType}
		}
		mime := resource.MimeType
		if mime == "" {
			mime = "unknown type"
		}
		return LLMContent{Type: LLMContentTypeText, Text: fmt.Sprintf("[binary resource %s (%s) omitted]", resource.URI, mime)}
	default:
		return LLMContent{Type: LLMContentTypeText, Text: "[unsupported MCP content " + string(block.Type) + "]"}
	}
}

// decodedBlockToLLMContent is blockToLLMContent for a block a server sent: a member the server left out reads as JavaScript's `undefined` and
// one of another JSON type as its value, since upstream copies and interpolates the members unchecked. A block whose `resource` is not an object
// is a TypeError upstream, which has no Go result, so it renders the unsupported placeholder.
func decodedBlockToLLMContent(block ContentBlock) LLMContent {
	var members map[string]json.RawMessage
	_ = json.Unmarshal(block.raw, &members)
	text := func(name string) string { return stringOrUndefined(members[name]) }
	member := func(name string) string {
		raw, ok := members[name]
		return jsString(raw, ok)
	}
	typeName := member("type")
	switch typeName {
	case "text":
		return LLMContent{Type: LLMContentTypeText, Text: text("text")}
	case "image":
		return LLMContent{Type: LLMContentTypeImage, Data: text("data"), MimeType: text("mimeType")}
	case "audio":
		return LLMContent{Type: LLMContentTypeText, Text: "[audio " + member("mimeType") + " omitted]"}
	case "resource_link":
		return LLMContent{Type: LLMContentTypeText, Text: member("name") + ": " + member("uri")}
	case "resource":
		var resource map[string]json.RawMessage
		if json.Unmarshal(members["resource"], &resource) != nil || resource == nil {
			return LLMContent{Type: LLMContentTypeText, Text: "[unsupported MCP content resource]"}
		}
		if raw, ok := resource["text"]; ok {
			return LLMContent{Type: LLMContentTypeText, Text: jsString(raw, true)}
		}
		var mime string
		if raw, ok := resource["mimeType"]; ok && string(raw) != "null" {
			mime = jsString(raw, true)
		} else {
			mime = "unknown type"
		}
		if strings.HasPrefix(mime, "image/") {
			return LLMContent{Type: LLMContentTypeImage, Data: stringOrUndefined(resource["blob"]), MimeType: mime}
		}
		uri := resource["uri"]
		return LLMContent{Type: LLMContentTypeText, Text: fmt.Sprintf("[binary resource %s (%s) omitted]", jsString(uri, uri != nil), mime)}
	}
	return LLMContent{Type: LLMContentTypeText, Text: "[unsupported MCP content " + typeName + "]"}
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
		content = append(content, LLMContent{Type: LLMContentTypeText, Text: indentJSON(result.StructuredContent)})
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
