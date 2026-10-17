package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Ports packages/coding-agent/test/tools.test.ts (1.0.4) "should read file contents that fit within limits" and "should
// detect image MIME type from file magic": programmatic callers get the text for text files and an image block for images
// (https://github.com/earendil-works/pi/issues/10251). A failed read is a thrown error and has no structured content.
func TestReadStructuredContent(t *testing.T) {
	dir := t.TempDir()
	const content = "Line 1\nLine 2\nLine 3"
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// A 1x1 PNG.
	const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
	pngBytes, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &ReadTool{CWD: dir}

	text, err := tool.Execute(context.Background(), "read", json.RawMessage(`{"path":"test.txt"}`), nil)
	if err != nil || text.IsError {
		t.Fatalf("read text: %+v, %v", text, err)
	}
	if string(text.StructuredContent) != `"Line 1\nLine 2\nLine 3"` {
		t.Errorf("text structuredContent = %s", text.StructuredContent)
	}

	image, err := tool.Execute(context.Background(), "read", json.RawMessage(`{"path":"image.png"}`), nil)
	if err != nil || image.IsError || len(image.Images()) != 1 {
		t.Fatalf("read image: %+v, %v", image, err)
	}
	want, _ := json.Marshal(map[string]string{"type": "image", "data": image.Images()[0].Data, "mimeType": "image/png", "note": image.Text()})
	var gotValue, wantValue map[string]string
	if err := json.Unmarshal(image.StructuredContent, &gotValue); err != nil {
		t.Fatalf("image structuredContent = %s: %v", image.StructuredContent, err)
	}
	_ = json.Unmarshal(want, &wantValue)
	if gotValue["type"] != "image" || gotValue["mimeType"] != "image/png" || gotValue["data"] != image.Images()[0].Data || gotValue["note"] != image.Text() || len(gotValue) != 4 {
		t.Errorf("image structuredContent = %s, want %s", image.StructuredContent, want)
	}
	// The key order is the object literal's: type, data, mimeType, note.
	if prefix := `{"type":"image","data":"`; len(image.StructuredContent) < len(prefix) || string(image.StructuredContent[:len(prefix)]) != prefix {
		t.Errorf("image structuredContent key order = %.60s", image.StructuredContent)
	}

	missing, err := tool.Execute(context.Background(), "read", json.RawMessage(`{"path":"missing.txt"}`), nil)
	if err != nil || !missing.IsError || missing.StructuredContent != nil {
		t.Errorf("read of a missing file = %+v, %v; want a thrown error without structured content", missing, err)
	}

	if string(tool.OutputSchema()) != readOutputSchema {
		t.Errorf("OutputSchema = %s", tool.OutputSchema())
	}
}
