package tools

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func readFileTool(t *testing.T, name string, data []byte, params map[string]any) agent.AgentToolResult {
	t.Helper()
	dir := t.TempDir()
	if name != "" {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	params["path"] = name
	args, _ := json.Marshal(params)
	res, err := (&ReadTool{CWD: dir}).Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func numberedLines(n int, format string) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf(format, i+1)
	}
	return strings.Join(lines, "\n")
}

// createTinyBmp1x1Red24bpp mirrors the upstream test helper.
func createTinyBmp1x1Red24bpp() []byte {
	b := make([]byte, 58)
	copy(b, "BM")
	binary.LittleEndian.PutUint32(b[2:], 58)
	binary.LittleEndian.PutUint32(b[10:], 54)
	binary.LittleEndian.PutUint32(b[14:], 40)
	binary.LittleEndian.PutUint32(b[18:], 1)
	binary.LittleEndian.PutUint32(b[22:], 1)
	binary.LittleEndian.PutUint16(b[26:], 1)
	binary.LittleEndian.PutUint16(b[28:], 24)
	binary.LittleEndian.PutUint32(b[34:], 4)
	b[56] = 0xff
	return b
}

// Ported from upstream test/tools.test.ts "read tool".
func TestReadToolUpstreamCases(t *testing.T) {
	t.Run("fits within limits", func(t *testing.T) {
		content := "Hello, world!\nLine 2\nLine 3"
		res := readFileTool(t, "test.txt", []byte(content), map[string]any{})
		if res.IsError || res.Content != content {
			t.Fatalf("res = %+v", res)
		}
	})
	t.Run("non-existent file", func(t *testing.T) {
		res := readFileTool(t, "", nil, map[string]any{})
		if !res.IsError {
			t.Fatalf("res = %+v", res)
		}
		args, _ := json.Marshal(map[string]any{"path": "nonexistent.txt"})
		res, _ = (&ReadTool{CWD: t.TempDir()}).Execute(context.Background(), "", args, nil)
		if !res.IsError || !strings.HasPrefix(res.Content, "ENOENT: no such file or directory, access '") {
			t.Fatalf("res = %+v", res)
		}
	})
	t.Run("line limit", func(t *testing.T) {
		res := readFileTool(t, "large.txt", []byte(numberedLines(2500, "Line %d")), map[string]any{})
		if !strings.Contains(res.Content, "Line 2000") || strings.Contains(res.Content, "Line 2001") ||
			!strings.Contains(res.Content, "[Showing lines 1-2000 of 2500. Use offset=2001 to continue.]") {
			t.Fatalf("tail = %q", res.Content[len(res.Content)-120:])
		}
		d := res.Details.(*ReadDetails)
		if d.Truncation == nil || d.Truncation.TruncatedBy != "lines" || d.Truncation.TotalLines != 2500 || d.Truncation.OutputLines != 2000 {
			t.Fatalf("details = %+v", d.Truncation)
		}
	})
	t.Run("byte limit", func(t *testing.T) {
		res := readFileTool(t, "large-bytes.txt", []byte(numberedLines(500, "Line %d: "+strings.Repeat("x", 200))), map[string]any{})
		if !regexp.MustCompile(`\[Showing lines 1-\d+ of 500 \(.* limit\)\. Use offset=\d+ to continue\.\]`).MatchString(res.Content) {
			t.Fatalf("tail = %q", res.Content[len(res.Content)-120:])
		}
	})
	file := []byte(numberedLines(100, "Line %d"))
	t.Run("offset", func(t *testing.T) {
		res := readFileTool(t, "f.txt", file, map[string]any{"offset": 51})
		if strings.Contains(res.Content, "Line 50\n") || !strings.HasPrefix(res.Content, "Line 51") || strings.Contains(res.Content, "Use offset=") {
			t.Fatalf("res = %q", res.Content)
		}
	})
	t.Run("limit", func(t *testing.T) {
		res := readFileTool(t, "f.txt", file, map[string]any{"limit": 10})
		if strings.Contains(res.Content, "Line 11") || !strings.Contains(res.Content, "[90 more lines in file. Use offset=11 to continue.]") {
			t.Fatalf("res = %q", res.Content)
		}
	})
	t.Run("offset and limit", func(t *testing.T) {
		res := readFileTool(t, "f.txt", file, map[string]any{"offset": 41, "limit": 20})
		if !strings.HasPrefix(res.Content, "Line 41\n") || strings.Contains(res.Content, "Line 61") ||
			!strings.Contains(res.Content, "[40 more lines in file. Use offset=61 to continue.]") {
			t.Fatalf("res = %q", res.Content)
		}
	})
	t.Run("offset beyond end", func(t *testing.T) {
		res := readFileTool(t, "short.txt", []byte("Line 1\nLine 2\nLine 3"), map[string]any{"offset": 100})
		if !res.IsError || res.Content != "Offset 100 is beyond end of file (3 lines total)" {
			t.Fatalf("res = %+v", res)
		}
	})
	t.Run("BMP image", func(t *testing.T) {
		res := readFileTool(t, "image.bmp", createTinyBmp1x1Red24bpp(), map[string]any{})
		if !strings.Contains(res.Content, "Read image file [image/png]") || len(res.Images) != 1 || res.Images[0].MimeType != "image/png" {
			t.Fatalf("res = %+v", res)
		}
	})
	t.Run("image extension, text content", func(t *testing.T) {
		res := readFileTool(t, "not-an-image.png", []byte("definitely not a png"), map[string]any{})
		if res.Content != "definitely not a png" || len(res.Images) != 0 {
			t.Fatalf("res = %+v", res)
		}
	})
}

// TOOL-10: an offset past the end is an error, not an empty success.
func TestRead_OffsetBeyondEndIsError(t *testing.T) {
	res := readFileTool(t, "two.txt", []byte("a\nb"), map[string]any{"offset": 50})
	if !res.IsError || !strings.Contains(res.Content, "beyond end of file") {
		t.Fatalf("res = %+v", res)
	}
}

// TOOL-11: a file that is not valid UTF-8 is decoded lossily, not hidden.
func TestRead_Latin1FileIsReadAsText(t *testing.T) {
	res := readFileTool(t, "latin1.txt", []byte("caf\xe9 latin1\nline two\n"), map[string]any{})
	if res.IsError || res.Content != "caf� latin1\nline two\n" {
		t.Fatalf("res = %+v", res)
	}
}

// TOOL-12: a text file starting with "BM" is text, not a BMP image.
func TestRead_BMPrefixedTextIsText(t *testing.T) {
	content := "BMAD method notes\nkeep reading me\n"
	res := readFileTool(t, "notes.md", []byte(content), map[string]any{})
	if res.IsError || res.Content != content || len(res.Images) != 0 {
		t.Fatalf("res = %+v", res)
	}
}
