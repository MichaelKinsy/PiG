package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The ls tool runs in parallel mode (ExecutionMode), so two calls may sort at the same time.
func TestLsParallelCallsSortIndependently(t *testing.T) {
	dir := t.TempDir()
	for i := range 64 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("Ünder-%02d é", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool := CreateLsTool(dir, nil)
	var wg sync.WaitGroup
	outs := make([]string, 8)
	for i := range outs {
		wg.Go(func() { outs[i] = runFileTool(t, tool, context.Background(), map[string]any{}).Text() })
	}
	wg.Wait()
	for i := range outs {
		if outs[i] != outs[0] {
			t.Fatalf("call %d listed\n%s\nwant\n%s", i, outs[i], outs[0])
		}
	}
}
