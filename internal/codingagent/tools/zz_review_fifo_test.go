package tools

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestReviewWriteQueueErrorVisible(t *testing.T) {
	d := t.TempDir()
	a := filepath.Join(d, "a")
	b := filepath.Join(d, "b")
	testenv.Symlink(t, b, a)
	testenv.Symlink(t, a, b)
	tool := &WriteTool{CWD: d, Queue: NewFileMutationQueue()}
	args, _ := json.Marshal(map[string]string{"path": "a", "content": "never written"})
	result, err := tool.Execute(t.Context(), "test", args, nil)
	if err == nil && !result.IsError {
		t.Fatalf("write falsely succeeded after canonical-key error: %v", result.Content)
	}
}
