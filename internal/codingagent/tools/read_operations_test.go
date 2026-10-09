package tools

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi core/tools/read.ts:11-20,125-170: execute calls ops.access, then ops.detectImageMimeType (when present), then ops.readFile; a throw from access is the tool error;
// an absent detectImageMimeType makes every file text.
func TestReadOperationsReplaceTheLocalFilesystem(t *testing.T) {
	var calls []string
	tool := &ReadTool{CWD: "/virtual", Operations: &ReadOperations{
		Access:              func(p string) error { calls = append(calls, "access "+p); return nil },
		DetectImageMimeType: func(p string) (string, error) { calls = append(calls, "detect "+p); return "", nil },
		ReadFile:            func(p string) ([]byte, error) { calls = append(calls, "read "+p); return []byte("a\nb\nc"), nil },
	}}
	result := runFileTool(t, tool, t.Context(), map[string]any{"path": "dir/file.txt", "offset": 2})
	if result.IsError || result.Text() != "b\nc" {
		t.Fatalf("result = %+v", result)
	}
	want := []string{"access /virtual/dir/file.txt", "detect /virtual/dir/file.txt", "read /virtual/dir/file.txt"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}

func TestReadOperationsWithoutImageDetectionReadsPNGBytesAsText(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n"
	tool := &ReadTool{CWD: "/virtual", Operations: &ReadOperations{
		Access:   func(string) error { return nil },
		ReadFile: func(string) ([]byte, error) { return []byte(png + "rest"), nil },
	}}
	result := runFileTool(t, tool, t.Context(), map[string]any{"path": "x.png"})
	if result.IsError || !strings.HasSuffix(result.Text(), "rest") {
		t.Fatalf("a missing detectImageMimeType must leave the file as text, got %+v", result)
	}
}

func TestReadOperationsAccessAndReadErrorsAreToolErrors(t *testing.T) {
	denied := &ReadTool{CWD: "/virtual", Operations: &ReadOperations{
		Access:   func(string) error { return errors.New("denied") },
		ReadFile: func(string) ([]byte, error) { t.Fatal("readFile must not run after a failed access"); return nil, nil },
	}}
	if result := runFileTool(t, denied, t.Context(), map[string]any{"path": "x"}); !result.IsError || result.Text() != "denied" {
		t.Fatalf("access error = %+v", result)
	}
}

// Pi read.ts:92,134-139: ReadToolOptions.resizeOptions is the fallback resize profile handed to processImage when the execution context has no model metadata.
func TestReadToolOptionsResizeOptionsReachImageProcessing(t *testing.T) {
	original := processReadImage
	t.Cleanup(func() { processReadImage = original })
	var got *ai.ModelImageResizeOptions
	processReadImage = func(data []byte, mime string, autoResize bool, options *ai.ModelImageResizeOptions) ([]byte, string, string, error) {
		got = options
		return data, mime, "", nil
	}
	want := &ai.ModelImageResizeOptions{}
	tool := &ReadTool{CWD: "/virtual", ResizeOptions: want, Operations: &ReadOperations{
		Access:              func(string) error { return nil },
		DetectImageMimeType: func(string) (string, error) { return "image/png", nil },
		ReadFile:            func(string) ([]byte, error) { return []byte("png"), nil },
	}}
	result := runFileTool(t, tool, t.Context(), map[string]any{"path": "x.png"})
	if result.IsError || got != want {
		t.Fatalf("resize options = %p, want %p (result %+v)", got, want, result)
	}
}
