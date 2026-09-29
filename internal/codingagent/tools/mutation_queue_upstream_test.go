package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestFileMutationQueueUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/file-mutation-queue.test.ts:38
	t.Run("serializes operations for the same file", func(t *testing.T) {
		testMutationOrder(t, false)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/file-mutation-queue.test.ts:56
	t.Run("allows different files to proceed in parallel", func(t *testing.T) {
		dir := t.TempDir()
		synctest.Test(t, func(t *testing.T) {
			q := NewFileMutationQueue()
			var order []string
			var mu sync.Mutex
			var wg sync.WaitGroup
			for _, name := range []string{"a", "b"} {
				wg.Go(func() {
					if err := q.With(filepath.Join(dir, name), func() error {
						mu.Lock()
						order = append(order, name+":start")
						mu.Unlock()
						time.Sleep(30 * time.Millisecond)
						mu.Lock()
						order = append(order, name+":end")
						mu.Unlock()
						return nil
					}); err != nil {
						t.Error(err)
					}
				})
				synctest.Wait()
			}
			wg.Wait()
			for _, name := range []string{"a", "b"} {
				if slices.Index(order, name+":start") >= slices.Index(order, name+":end") {
					t.Fatal(order)
				}
			}
			if slices.Index(order, "b:start") >= slices.Index(order, "a:end") {
				t.Fatal(order)
			}
		})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/file-mutation-queue.test.ts:77
	t.Run("uses the same queue for symlink aliases", func(t *testing.T) {
		testMutationOrder(t, true)
	})
}

func testMutationOrder(t *testing.T, alias bool) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := target
	labels := []string{"first", "second"}
	if alias {
		second = filepath.Join(dir, "alias.txt")
		testenv.Symlink(t, target, second)
		labels = []string{"target", "alias"}
	}
	synctest.Test(t, func(t *testing.T) {
		q := NewFileMutationQueue()
		var order []string
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i, path := range []string{target, second} {
			wg.Go(func() {
				if err := q.With(path, func() error {
					mu.Lock()
					order = append(order, labels[i]+":start")
					mu.Unlock()
					if i == 0 {
						time.Sleep(30 * time.Millisecond)
					}
					mu.Lock()
					order = append(order, labels[i]+":end")
					mu.Unlock()
					return nil
				}); err != nil {
					t.Error(err)
				}
			})
			synctest.Wait()
		}
		wg.Wait()
		want := []string{labels[0] + ":start", labels[0] + ":end", labels[1] + ":start", labels[1] + ":end"}
		if !slices.Equal(order, want) {
			t.Fatalf("order = %q, want %q", order, want)
		}
		if len(q.chains) != 0 {
			t.Fatal("queue retained drained entries")
		}
	})
}

func TestFileMutationToolsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/file-mutation-queue.test.ts:102
	t.Run("preserves both parallel edits on the same file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "parallel-edit.txt")
		if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		synctest.Test(t, func(t *testing.T) {
			tool := &EditTool{CWD: dir, Queue: NewFileMutationQueue(), Operations: &EditOperations{
				Access: accessReadWrite,
				ReadFile: func(path string) ([]byte, error) {
					b, err := os.ReadFile(path)
					time.Sleep(30 * time.Millisecond)
					return b, err
				},
				WriteFile: func(path, content string) error {
					time.Sleep(30 * time.Millisecond)
					return os.WriteFile(path, []byte(content), 0o600)
				},
			}}
			var wg sync.WaitGroup
			for _, edit := range []editEntry{{"alpha", "ALPHA"}, {"beta", "BETA"}} {
				args, err := json.Marshal(editParams{Path: path, Edits: []editEntry{edit}})
				if err != nil {
					t.Fatal(err)
				}
				wg.Go(func() { assertMutationResult(t, tool, t.Context(), args, false) })
				// Await the first suspended operation rather than relying on scheduler timing.
				synctest.Wait()
			}
			wg.Wait()
		})
		assertMutationFile(t, path, "ALPHA\nBETA\ngamma\n")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/file-mutation-queue.test.ts:131
	t.Run("shares the queue between edit and write", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "mixed.txt")
		if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		synctest.Test(t, func(t *testing.T) {
			q := NewFileMutationQueue()
			edit := &EditTool{CWD: dir, Queue: q, Operations: &EditOperations{
				Access: accessReadWrite,
				ReadFile: func(path string) ([]byte, error) {
					b, err := os.ReadFile(path)
					time.Sleep(30 * time.Millisecond)
					return b, err
				},
				WriteFile: func(path, content string) error {
					time.Sleep(30 * time.Millisecond)
					return os.WriteFile(path, []byte(content), 0o600)
				},
			}}
			write := &WriteTool{CWD: dir, Queue: q, Operations: &WriteOperations{
				Mkdir: func(string) error { return nil },
				WriteFile: func(path, content string) error {
					time.Sleep(10 * time.Millisecond)
					return os.WriteFile(path, []byte(content), 0o600)
				},
			}}
			var wg sync.WaitGroup
			wg.Go(func() {
				assertMutationResult(t, edit, t.Context(), json.RawMessage(`{"path":"mixed.txt","edits":[{"oldText":"original","newText":"edited"}]}`), false)
			})
			synctest.Wait()
			time.Sleep(5 * time.Millisecond)
			wg.Go(func() {
				assertMutationResult(t, write, t.Context(), json.RawMessage(`{"path":"mixed.txt","content":"replacement\n"}`), false)
			})
			wg.Wait()
		})
		assertMutationFile(t, path, "replacement\n")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/file-mutation-queue.test.ts:176
	t.Run("keeps write queue locked while an aborted write is still in flight", func(t *testing.T) { testAbortedMutation(t, false) })
	// .upstream/v0.87.1/packages/coding-agent/test/file-mutation-queue.test.ts:221
	t.Run("keeps edit queue locked while an aborted edit write is still in flight", func(t *testing.T) { testAbortedMutation(t, true) })
}

func testAbortedMutation(t *testing.T, edit bool) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "abort.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		q := NewFileMutationQueue()
		firstStarted, finishFirst, secondStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var mu sync.Mutex
		firstSettled := false
		writeFile := func(path, content string) error {
			if content == "first\n" || content == "ALPHA\nbeta\n" {
				close(firstStarted)
				<-finishFirst
				err := os.WriteFile(path, []byte(content), 0o600)
				mu.Lock()
				firstSettled = true
				mu.Unlock()
				return err
			}
			mu.Lock()
			settled := firstSettled
			mu.Unlock()
			if !settled {
				t.Error("second write began before first settled")
			}
			close(secondStarted)
			return os.WriteFile(path, []byte(content), 0o600)
		}
		var tool agent.AgentTool = &WriteTool{CWD: dir, Queue: q, Operations: &WriteOperations{Mkdir: func(string) error { return nil }, WriteFile: writeFile}}
		firstArgs := json.RawMessage(`{"path":"abort.txt","content":"first\n"}`)
		secondArgs := json.RawMessage(`{"path":"abort.txt","content":"second\n"}`)
		if edit {
			tool = &EditTool{CWD: dir, Queue: q, Operations: &EditOperations{Access: accessReadWrite, ReadFile: os.ReadFile, WriteFile: writeFile}}
			firstArgs = json.RawMessage(`{"path":"abort.txt","edits":[{"oldText":"alpha","newText":"ALPHA"}]}`)
			secondArgs = json.RawMessage(`{"path":"abort.txt","edits":[{"oldText":"beta","newText":"BETA"}]}`)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var wg sync.WaitGroup
		wg.Go(func() { assertMutationResult(t, tool, ctx, firstArgs, true) })
		<-firstStarted
		cancel()
		wg.Go(func() { assertMutationResult(t, tool, t.Context(), secondArgs, false) })
		synctest.Wait()
		time.Sleep(20 * time.Millisecond)
		select {
		case <-secondStarted:
			t.Error("queue released before in-flight write completed")
		default:
		}
		close(finishFirst)
		wg.Wait()
	})
	want := "second\n"
	if edit {
		want = "ALPHA\nBETA\n"
	}
	assertMutationFile(t, path, want)
}

func assertMutationResult(t *testing.T, tool agent.AgentTool, ctx context.Context, args json.RawMessage, aborted bool) {
	t.Helper()
	result, err := tool.Execute(ctx, "call", args, nil)
	if err != nil || result.IsError != aborted || (aborted && result.Text() != "Operation aborted") {
		t.Errorf("execute = %+v, %v", result, err)
	}
}
func assertMutationFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file = %q, %v; want %q", got, err, want)
	}
}

func TestEditAbortAfterAccessDoesNotRead(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/tools/edit.ts:188 checks abort after access resolves.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	read := false
	tool := &EditTool{CWD: t.TempDir(), Queue: NewFileMutationQueue(), Operations: &EditOperations{
		Access: func(string) error { cancel(); return nil },
		ReadFile: func(string) ([]byte, error) {
			read = true
			t.Error("read after access was cancelled")
			return []byte("hello"), nil
		},
		WriteFile: func(string, string) error { t.Error("write after access was cancelled"); return nil },
	}}
	result, err := tool.Execute(ctx, "call", json.RawMessage(`{"path":"f","edits":[{"oldText":"hello","newText":"world"}]}`), nil)
	if err != nil || !result.IsError || result.Text() != "Operation aborted" {
		t.Fatalf("execute = %+v, %v", result, err)
	}
	data, err := json.Marshal([]any{result.Text(), read})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("EDIT_ACCESS %s\n", data)
}
