package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// pi: packages/coding-agent/src/core/tools/read.ts — the image detection reads the path before the text is read, and a directory fails there with
// Node's fs error, which names no path.
func TestReadDirectoryReportsNodeEISDIR(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"sub", "."} {
		result := runFileTool(t, CreateReadTool(dir, nil), context.Background(), map[string]any{"path": path})
		if !result.IsError || result.Text() != "EISDIR: illegal operation on a directory, read" {
			t.Errorf("read %q: %+v", path, result)
		}
	}
}
