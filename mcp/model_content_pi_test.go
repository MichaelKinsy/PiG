package mcp_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/model-content.test.ts (Pi 1acc72acd).

var (
	modelContentLink = mcp.ContentBlock{
		Type:        mcp.ContentResourceLink,
		URI:         "docs://guide",
		Name:        "guide",
		Title:       "The Guide",
		MimeType:    "text/markdown",
		Size:        new(float64(2048)),
		Description: "How to use it",
	}
	modelContentPDF = mcp.ContentBlock{
		Type: mcp.ContentResource,
		Resource: &mcp.ResourceContents{
			URI:      "file:///r/report.pdf",
			MimeType: "application/pdf",
			Blob:     new("JVBERg=="),
		},
	}
)

func TestToMcpModelContentNamesTheReadToolInResourceLinksWhenThereIsOne(t *testing.T) {
	got := mcp.ToMcpModelContent("docs", mcp.CallToolResult{Content: []mcp.ContentBlock{modelContentLink}}, mcp.McpModelContentOptions{ReadResourceTool: "read_mcp_resource"})
	want := []mcp.LLMContent{{
		Type: mcp.LLMContentTypeText,
		Text: `[Resource docs://guide "The Guide" (text/markdown, 2.0KB): How to use it. Read it with read_mcp_resource (server "docs")]`,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("with readResourceTool:\n got %#v\nwant %#v", got, want)
	}

	gotBare := mcp.ToMcpModelContent("docs", mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: mcp.ContentResourceLink, URI: "file:///a", Name: "a"}}}, mcp.McpModelContentOptions{})
	wantBare := []mcp.LLMContent{{Type: mcp.LLMContentTypeText, Text: `[Resource file:///a "a"]`}}
	if !reflect.DeepEqual(gotBare, wantBare) {
		t.Fatalf("without readResourceTool:\n got %#v\nwant %#v", gotBare, wantBare)
	}
}

func TestToMcpModelContentSavesBinaryResourcesShowsTextBlobsAsTextAndKeepsImages(t *testing.T) {
	type savedEntry struct {
		data      []byte
		extension string
	}
	var saved []savedEntry
	saveOutput := func(data []byte, extension string) (string, error) {
		saved = append(saved, savedEntry{data: append([]byte(nil), data...), extension: extension})
		return "/tmp/saved" + extension, nil
	}
	jsonBlob := base64.StdEncoding.EncodeToString([]byte(`{"a":1}`))
	content := []mcp.ContentBlock{
		modelContentPDF,
		{
			Type: mcp.ContentResource,
			Resource: &mcp.ResourceContents{
				URI:      "docs://data",
				MimeType: "application/json; charset=utf-8",
				Blob:     &jsonBlob,
			},
		},
		{
			Type: mcp.ContentResource,
			Resource: &mcp.ResourceContents{
				URI:      "docs://logo",
				MimeType: "image/png",
				Blob:     new("AAAA"),
			},
		},
		{
			Type: mcp.ContentResource,
			Resource: &mcp.ResourceContents{
				URI:  "docs://raw",
				Blob: new("AAAA"),
			},
		},
	}
	got := mcp.ToMcpModelContent("docs", mcp.CallToolResult{Content: content}, mcp.McpModelContentOptions{SaveOutput: saveOutput})
	want := []mcp.LLMContent{
		{Type: mcp.LLMContentTypeText, Text: "[Binary resource file:///r/report.pdf (application/pdf, 4B) saved to /tmp/saved.pdf]"},
		{Type: mcp.LLMContentTypeText, Text: `{"a":1}`},
		{Type: mcp.LLMContentTypeImage, Data: "AAAA", MimeType: "image/png"},
		{Type: mcp.LLMContentTypeText, Text: "[Binary resource docs://raw (unknown type, 3B) saved to /tmp/saved.bin]"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("content:\n got %#v\nwant %#v", got, want)
	}
	rawAAAA, _ := base64.StdEncoding.DecodeString("AAAA")
	if len(saved) != 2 || !bytes.Equal(saved[0].data, []byte("%PDF")) || saved[0].extension != ".pdf" || !bytes.Equal(saved[1].data, rawAAAA) || saved[1].extension != ".bin" {
		t.Fatalf("saved = %#v", saved)
	}
}

func TestToMcpModelContentLeavesBinaryResourcesOutWithoutASaverAndSaysWhyWhenSavingFails(t *testing.T) {
	gotOmitted := mcp.ToMcpModelContent("docs", mcp.CallToolResult{Content: []mcp.ContentBlock{modelContentPDF}}, mcp.McpModelContentOptions{})
	wantOmitted := []mcp.LLMContent{{Type: mcp.LLMContentTypeText, Text: "[binary resource file:///r/report.pdf (application/pdf) omitted]"}}
	if !reflect.DeepEqual(gotOmitted, wantOmitted) {
		t.Fatalf("without saver:\n got %#v\nwant %#v", gotOmitted, wantOmitted)
	}

	failing := func([]byte, string) (string, error) {
		return "", errors.New("disk full")
	}
	gotFailed := mcp.ToMcpModelContent("docs", mcp.CallToolResult{Content: []mcp.ContentBlock{modelContentPDF}}, mcp.McpModelContentOptions{SaveOutput: failing})
	wantFailed := []mcp.LLMContent{{Type: mcp.LLMContentTypeText, Text: "[Binary resource file:///r/report.pdf (application/pdf, 4B) could not be saved: disk full]"}}
	if !reflect.DeepEqual(gotFailed, wantFailed) {
		t.Fatalf("failing saver:\n got %#v\nwant %#v", gotFailed, wantFailed)
	}
}

func TestToMcpModelContentShowsStructuredContentAsJSONWhenThereAreNoContentBlocks(t *testing.T) {
	got := mcp.ToMcpModelContent("docs", mcp.CallToolResult{
		Content:           []mcp.ContentBlock{},
		StructuredContent: json.RawMessage(`[{"id":1}]`),
	}, mcp.McpModelContentOptions{})
	want := []mcp.LLMContent{{Type: mcp.LLMContentTypeText, Text: "[\n  {\n    \"id\": 1\n  }\n]"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("structured fallback:\n got %#v\nwant %#v", got, want)
	}
}
