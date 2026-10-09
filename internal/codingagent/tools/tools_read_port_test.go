package tools

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func requireTextParts(t *testing.T, result agent.AgentToolResult, present, absent []string) {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool failed: %s", result.Text())
	}
	for _, part := range present {
		if !strings.Contains(result.Text(), part) {
			t.Errorf("output %q lacks %q", result.Text(), part)
		}
	}
	for _, part := range absent {
		if strings.Contains(result.Text(), part) {
			t.Errorf("output %q contains %q", result.Text(), part)
		}
	}
}

// Pi: packages/coding-agent/src/core/tools/read.ts:41 (ReadToolDetails.truncation).
func TestToolsReadPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:81
	t.Run("should read file contents that fit within limits", func(t *testing.T) {
		content := "Hello, world!\nLine 2\nLine 3"
		result := readFileTool(t, "test.txt", []byte(content), map[string]any{})
		if result.IsError || result.Text() != content {
			t.Fatalf("result = %+v", result)
		}
		requireTextParts(t, result, nil, []string{"Use offset="})
		// ToolResultDetailsFor is the built-in result boundary used by extension tools.
		// The raw ReadDetails value is renderer state, not upstream SDK details.
		if got := extension.ToolResultDetailsFor(result.Details); got != nil {
			t.Fatalf("details = %#v, want undefined", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:94
	t.Run("should handle non-existent files", func(t *testing.T) {
		result := runFileTool(t, &ReadTool{CWD: t.TempDir()}, t.Context(), map[string]any{"path": "nonexistent.txt"})
		if !result.IsError || !regexp.MustCompile(`(?i)ENOENT|not found`).MatchString(result.Text()) {
			t.Fatalf("result = %+v", result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:100
	t.Run("should truncate files exceeding line limit", func(t *testing.T) {
		result := readFileTool(t, "large.txt", []byte(numberedLines(2500, "Line %d")), map[string]any{})
		requireTextParts(t, result, []string{"Line 1", "Line 2000", "[Showing lines 1-2000 of 2500. Use offset=2001 to continue.]"}, []string{"Line 2001"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:114
	t.Run("should truncate when byte limit exceeded", func(t *testing.T) {
		result := readFileTool(t, "large-bytes.txt", []byte(numberedLines(500, "Line %d: "+strings.Repeat("x", 200))), map[string]any{})
		requireTextParts(t, result, []string{"Line 1:"}, nil)
		if !regexp.MustCompile(`\[Showing lines 1-\d+ of 500 \(.* limit\)\. Use offset=\d+ to continue\.\]`).MatchString(result.Text()) {
			t.Fatal(result.Text())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:128
	t.Run("should handle offset parameter", func(t *testing.T) {
		result := readFileTool(t, "offset-test.txt", []byte(numberedLines(100, "Line %d")), map[string]any{"offset": 51})
		requireTextParts(t, result, []string{"Line 51", "Line 100"}, []string{"Line 50", "Use offset="})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:143
	t.Run("should handle limit parameter", func(t *testing.T) {
		result := readFileTool(t, "limit-test.txt", []byte(numberedLines(100, "Line %d")), map[string]any{"limit": 10})
		requireTextParts(t, result, []string{"Line 1", "Line 10", "[90 more lines in file. Use offset=11 to continue.]"}, []string{"Line 11"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:157
	t.Run("should handle offset + limit together", func(t *testing.T) {
		result := readFileTool(t, "offset-limit-test.txt", []byte(numberedLines(100, "Line %d")), map[string]any{"offset": 41, "limit": 20})
		requireTextParts(t, result, []string{"Line 41", "Line 60", "[40 more lines in file. Use offset=61 to continue.]"}, []string{"Line 40", "Line 61"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:176
	t.Run("should show error when offset is beyond file length", func(t *testing.T) {
		result := readFileTool(t, "short.txt", []byte("Line 1\nLine 2\nLine 3"), map[string]any{"offset": 100})
		if !result.IsError || !strings.Contains(result.Text(), "Offset 100 is beyond end of file (3 lines total)") {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:185
	t.Run("should include truncation details when truncated", func(t *testing.T) {
		result := readFileTool(t, "large-file.txt", []byte(numberedLines(2500, "Line %d")), map[string]any{})
		details, ok := extension.ToolResultDetailsFor(result.Details).(*extension.ReadToolDetails)
		if !ok || details.Truncation == nil {
			t.Fatalf("details = %#v", result.Details)
		}
		tr := details.Truncation
		if !tr.Truncated || tr.TruncatedBy != "lines" || tr.TotalLines != 2500 || tr.OutputLines != 2000 {
			t.Fatalf("truncation = %+v", tr)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:200
	t.Run("should detect image MIME type from file magic (not extension)", func(t *testing.T) {
		data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg==")
		if err != nil {
			t.Fatal(err)
		}
		result := readFileTool(t, "image.txt", data, map[string]any{})
		requireTextParts(t, result, []string{"Read image file [image/png]"}, nil)
		if len(result.Images()) == 0 || result.Images()[0].MimeType != "image/png" || result.Images()[0].Data == "" {
			t.Fatalf("images = %+v", result.Images())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:222
	t.Run("should read BMP files from disk as PNG image attachments", func(t *testing.T) {
		result := readFileTool(t, "image.bmp", createTinyBmp1x1Red24bpp(), map[string]any{})
		requireTextParts(t, result, []string{"Read image file [image/png]", "[Image converted from image/bmp to image/png.]"}, nil)
		if len(result.Images()) == 0 || result.Images()[0].MimeType != "image/png" {
			t.Fatalf("images = %+v", result.Images())
		}
		data, err := base64.StdEncoding.DecodeString(result.Images()[0].Data)
		if err != nil || len(data) == 0 || data[0] != 0x89 {
			t.Fatalf("image data = %x, %v", data, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:240
	t.Run("should treat files with image extension but non-image content as text", func(t *testing.T) {
		result := readFileTool(t, "not-an-image.png", []byte("definitely not a png"), map[string]any{})
		requireTextParts(t, result, []string{"definitely not a png"}, nil)
		if len(result.Images()) != 0 {
			t.Fatalf("unexpected images: %+v", result.Images())
		}
	})
}
