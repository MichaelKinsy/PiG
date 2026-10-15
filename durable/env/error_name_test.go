package env

import "testing"

// TestErrorNames mirrors packages/durable/src/env/index.ts: FileError and ExecutionError set `name` to the class name.
func TestErrorNames(t *testing.T) {
	if got := NewFileError(FileErrorNotFound, "gone", "/x", nil).Name(); got != "FileError" {
		t.Errorf("FileError name = %q", got)
	}
	if got := NewExecutionError(ExecutionErrorTimeout, "slow", nil).Name(); got != "ExecutionError" {
		t.Errorf("ExecutionError name = %q", got)
	}
}
