package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/coding-agent/test/file-mutation-queue.test.ts through the exported withFileMutationQueue function.

// :39 "serializes operations for the same file".
func TestWithFileMutationQueueSerializesSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.txt")
	var mu sync.Mutex
	var order []string
	record := func(entry string) { mu.Lock(); order = append(order, entry); mu.Unlock() }
	var wg sync.WaitGroup
	started := make(chan struct{})
	wg.Go(func() {
		_, _ = WithFileMutationQueue(path, func() (struct{}, error) {
			record("first:start")
			close(started)
			time.Sleep(30 * time.Millisecond)
			record("first:end")
			return struct{}{}, nil
		})
	})
	<-started
	wg.Go(func() {
		_, _ = WithFileMutationQueue(path, func() (struct{}, error) {
			record("second:start")
			record("second:end")
			return struct{}{}, nil
		})
	})
	wg.Wait()
	want := []string{"first:start", "first:end", "second:start", "second:end"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// :57 "allows different files to proceed in parallel": b starts while a is still running.
func TestWithFileMutationQueueRunsDifferentFilesInParallel(t *testing.T) {
	dir := t.TempDir()
	aRunning := make(chan struct{})
	bStarted := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = WithFileMutationQueue(filepath.Join(dir, "a"), func() (struct{}, error) {
			close(aRunning)
			select {
			case <-bStarted:
			case <-time.After(5 * time.Second):
				t.Error("b never started while a held its own file")
			}
			return struct{}{}, nil
		})
	})
	<-aRunning
	_, _ = WithFileMutationQueue(filepath.Join(dir, "b"), func() (struct{}, error) { close(bStarted); return struct{}{}, nil })
	wg.Wait()
}

// :78 "uses the same queue for symlink aliases".
func TestWithFileMutationQueueSharesOneSlotForSymlinkAliases(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	alias := filepath.Join(dir, "alias.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, target, alias)
	holding := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = WithFileMutationQueue(target, func() (struct{}, error) { close(holding); <-release; return struct{}{}, nil })
	})
	<-holding
	ran := make(chan struct{})
	wg.Go(func() {
		_, _ = WithFileMutationQueue(alias, func() (struct{}, error) { close(ran); return struct{}{}, nil })
	})
	select {
	case <-ran:
		t.Fatal("an alias of the held file ran before the holder finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	select {
	case <-ran:
	default:
		t.Fatal("the alias never ran after the holder finished")
	}
}

// :102,:131 the edit and write tools share the queue with the function: a held function blocks a created tool's mutation, and a held tool reservation blocks the function.
func TestWithFileMutationQueueSharesTheQueueWithEditAndWriteTools(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "shared.txt")
	writeParams, _ := json.Marshal(map[string]any{"path": file, "content": "x"})

	holding := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = WithFileMutationQueue(file, func() (struct{}, error) { close(holding); <-release; return struct{}{}, nil })
	})
	<-holding
	ticket, ok := CreateWriteTool(dir, nil).ReserveMutationOrder(writeParams)
	if !ok {
		t.Fatal("the write tool did not reserve a queue position")
	}
	waited := make(chan struct{})
	go func() { ticket.Wait(); close(waited); ticket.Release() }()
	select {
	case <-waited:
		t.Fatal("the write tool's mutation ran while WithFileMutationQueue held the file")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the write tool never ran after the function finished")
	}
	wg.Wait()

	held, ok := CreateEditTool(dir, nil).ReserveMutationOrder(mustJSON(t, map[string]any{"path": file, "edits": []map[string]string{{"oldText": "a", "newText": "b"}}}))
	if !ok {
		t.Fatal("the edit tool did not reserve a queue position")
	}
	ran := make(chan struct{})
	go func() {
		_, _ = WithFileMutationQueue(file, func() (struct{}, error) { close(ran); return struct{}{}, nil })
	}()
	select {
	case <-ran:
		t.Fatal("WithFileMutationQueue ran while the edit tool held the file")
	case <-time.After(50 * time.Millisecond):
	}
	held.Wait()
	held.Release()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("WithFileMutationQueue never ran after the edit tool released the file")
	}
}

// file-mutation-queue.ts:53-60 try/finally: fn's value and error come back, and a failing or panicking fn releases the file for the next caller.
func TestWithFileMutationQueueReturnsResultsAndReleasesOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	got, err := WithFileMutationQueue(path, func() (int, error) { return 7, nil })
	if got != 7 || err != nil {
		t.Fatalf("got %d, %v", got, err)
	}
	boom := errors.New("boom")
	if _, err := WithFileMutationQueue(path, func() (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the function's error", err)
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = WithFileMutationQueue(path, func() (int, error) { panic("thrown") })
	}()
	done := make(chan struct{})
	go func() { _, _ = WithFileMutationQueue(path, func() (int, error) { close(done); return 1, nil }) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a panicking function left the file locked")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
