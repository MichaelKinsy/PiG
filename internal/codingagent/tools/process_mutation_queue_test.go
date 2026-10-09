package tools

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Upstream keeps one module-level fileMutationQueues map (file-mutation-queue.ts:4), so an edit tool and a write tool made by
// separate createEditTool and createWriteTool calls, even in different sessions, serialise mutations of one file.
func TestSeparatelyCreatedEditAndWriteToolsShareOneMutationQueue(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "shared.txt")
	edit := CreateEditTool(dir, nil)
	write := CreateWriteTool(dir, nil)
	otherSessionWrite := CreateWriteTool(dir, nil)

	editParams, _ := json.Marshal(map[string]any{"path": file, "edits": []map[string]string{{"oldText": "a", "newText": "b"}}})
	writeParams, _ := json.Marshal(map[string]any{"path": file, "content": "x"})
	first, ok := edit.ReserveMutationOrder(editParams)
	if !ok {
		t.Fatal("the edit tool did not reserve a queue position")
	}
	second, ok := write.ReserveMutationOrder(writeParams)
	if !ok {
		t.Fatal("the write tool did not reserve a queue position")
	}
	third, ok := otherSessionWrite.ReserveMutationOrder(writeParams)
	if !ok {
		t.Fatal("the second write tool did not reserve a queue position")
	}

	done := make(chan string, 2)
	go func() { second.Wait(); done <- "write"; second.Release() }()
	go func() { third.Wait(); done <- "other write"; third.Release() }()
	select {
	case got := <-done:
		t.Fatalf("%s ran while the edit tool still held the file's queue position", got)
	case <-time.After(50 * time.Millisecond):
	}
	first.Release()
	for _, want := range []string{"write", "other write"} {
		select {
		case got := <-done:
			if got != want {
				t.Fatalf("ran %q, want %q: reservations are served in reservation order", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%q never ran after the edit released the file", want)
		}
	}
}

// file-mutation-queue.ts:32-57: withFileMutationQueue returns fn's value and rethrows fn's rejection, and either way releases the file's queue slot so the next caller runs.
func TestWithFileMutationQueueReturnsTheResultAndReleasesOnFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "result.txt")
	got, err := WithFileMutationQueue(file, func() (string, error) { return "value", nil })
	if err != nil || got != "value" {
		t.Fatalf("WithFileMutationQueue = %q, %v, want value, nil", got, err)
	}
	want := errors.New("boom")
	if _, err := WithFileMutationQueue(file, func() (int, error) { return 0, want }); !errors.Is(err, want) {
		t.Fatalf("a failing fn's error = %v, want %v", err, want)
	}
	ran := make(chan struct{})
	go func() {
		_, _ = WithFileMutationQueue(file, func() (struct{}, error) { close(ran); return struct{}{}, nil })
	}()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the file's queue slot was not released after fn failed")
	}
}
