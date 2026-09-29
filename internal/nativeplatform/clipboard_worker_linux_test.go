//go:build linux

package nativeplatform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"weak"
)

func TestUpstreamX11IncrementalMetadata(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:113; exact metadata vectors from fixtures/clipboard-x11-test.c.
	chunk := x11Property{data: []byte{31, 0, 0, 0}, items: 1, format: 32, typeID: 4}
	var result x11Property
	for range 2 {
		if !result.appendProperty(chunk) {
			t.Fatal("valid atom chunk rejected")
		}
	}
	if len(result.data) != 2*len(chunk.data) || result.items != 2 {
		t.Fatal(result)
	}
	chunk.format, chunk.items = 8, 4
	if result.appendProperty(chunk) {
		t.Fatal("format change accepted")
	}
	chunk.format, chunk.items, chunk.typeID = 32, 1, 31
	if result.appendProperty(chunk) {
		t.Fatal("type change accepted")
	}
	chunk.typeID = 4
	chunk.data = chunk.data[:len(chunk.data)-1]
	if result.appendProperty(chunk) {
		t.Fatal("inconsistent byte count accepted")
	}
	if len(result.data) != 8 || result.items != 2 {
		t.Fatal("invalid chunks mutated accumulation")
	}
	chunk.data = nil
	chunk.items = 0
	chunk.format = 8
	if result.appendProperty(chunk) {
		t.Fatal("mismatched terminator accepted")
	}
	chunk.format = 32
	if !result.appendProperty(chunk) {
		t.Fatal("matching terminator rejected")
	}
}

func TestUpstreamClipboardPrivateWorker(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:118, concurrent/process-exit/worker-exit table rows.
	t.Run("bounds private clipboard work and preserves cleanup: concurrent", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var reads atomic.Int32
			worker := clipboardWorker{read: func(context.Context, bool) clipboardReadResult {
				data := []byte("text")
				if reads.Add(1) == 1 {
					close(started)
				}
				<-release
				return clipboardReadResult{data: data, available: true}
			}}
			pending := make(chan clipboardReadResult, 1)
			begin := time.Now()
			go func() { pending <- worker.run(t.Context(), false) }()
			<-started
			concurrentStart := time.Now()
			concurrent := make(chan clipboardReadResult, 20)
			for index := range 20 {
				go func() { concurrent <- worker.run(t.Context(), index%2 == 0) }()
			}
			if _, err := os.ReadFile("clipboard_worker_linux_test.go"); err != nil {
				t.Fatal(err)
			}
			for range 20 {
				if got := <-concurrent; got.available || got.err != nil {
					t.Fatal(got)
				}
			}
			if time.Since(concurrentStart) >= 1500*time.Millisecond {
				t.Fatal("concurrent reads delayed filesystem I/O")
			}
			if got := <-pending; got.available || got.err != nil {
				t.Fatal(got)
			}
			if time.Since(begin) >= 4500*time.Millisecond {
				t.Fatal("private caller exceeded bounded wait")
			}
			worker.mu.Lock()
			busy := worker.task != nil
			worker.mu.Unlock()
			if reads.Load() != 1 || !busy {
				t.Fatal("timeout did not preserve the single blocked reader")
			}
			if got := worker.run(t.Context(), true); got.available || got.err != nil {
				t.Fatal(got)
			}
			if reads.Load() != 1 {
				t.Fatal("a second private reader started")
			}
			close(release)
			synctest.Wait()
			worker.mu.Lock()
			busy = worker.task != nil
			worker.mu.Unlock()
			if busy {
				t.Fatal("late result retained the private slot")
			}
			for _, image := range []bool{false, true} {
				got := worker.run(t.Context(), image)
				if got.err != nil || !got.available || !slices.Equal(got.data, []byte("text")) {
					t.Fatal(got)
				}
			}
			if reads.Load() != 3 {
				t.Fatal("expected the original read followed by text and image reads")
			}
			worker.mu.Lock()
			busy = worker.task != nil
			worker.mu.Unlock()
			if busy {
				t.Fatal("successful result retained")
			}
		})
	})
	t.Run("bounds private clipboard work and preserves cleanup: worker-exit", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var reads atomic.Int32
			worker := clipboardWorker{read: func(context.Context, bool) clipboardReadResult {
				if reads.Add(1) == 1 {
					close(started)
				}
				<-release
				return clipboardReadResult{data: []byte("text"), available: true}
			}}
			ctx, cancel := context.WithCancel(t.Context())
			exited := make(chan struct{})
			go func() { _ = worker.run(ctx, false); close(exited) }()
			<-started
			cancel()
			synctest.Wait()
			select {
			case <-exited:
				close(release)
				t.Fatal("environment teardown returned before the bounded native wait ended")
			default:
			}
			time.Sleep(3 * time.Second)
			<-exited
			worker.mu.Lock()
			busy := worker.task != nil
			worker.mu.Unlock()
			if reads.Load() != 1 || !busy {
				t.Fatal("originating environment teardown discarded the private operation")
			}
			close(release)
			synctest.Wait()
			if got := worker.run(t.Context(), false); !got.available || string(got.data) != "text" {
				t.Fatal(got)
			}
		})
	})
	t.Run("bounds private clipboard work and preserves cleanup: process-exit", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClipboardPrivateWorkerProcessFixture$")
		command.Env = append(os.Environ(), "PIG_CLIPBOARD_PROCESS_FIXTURE=1")
		output, err := command.CombinedOutput()
		if err != nil || string(output) != "passed\nPASS\n" {
			t.Fatalf("process exit: %v %q", err, output)
		}
	})
}

func TestClipboardWorkerReleasesPrivateResults(t *testing.T) {
	// The C fixture counts private allocations, not the returned JS value. Go checks the equivalent absence of retained private buffers after late completion and after callers release their returned data.
	synctest.Test(t, func(t *testing.T) {
		type buffer struct{ data [32]byte } // Avoid the GC's shared tiny-allocation block.
		allocated := make(chan weak.Pointer[buffer], 3)
		release := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		worker := clipboardWorker{read: func(context.Context, bool) clipboardReadResult {
			value := &buffer{}
			copy(value.data[:], "text")
			allocated <- weak.Make(value)
			<-release
			return clipboardReadResult{data: value.data[:4], available: true}
		}}
		pending := make(chan clipboardReadResult, 1)
		go func() { pending <- worker.run(t.Context(), false) }()
		first := <-allocated
		runtime.GC()
		if first.Value() == nil {
			t.Fatal("private operation lost its live buffer")
		}
		if got := <-pending; got.available {
			t.Fatal("blocked operation did not time out")
		}
		close(release)
		released = true
		synctest.Wait()
		runtime.GC()
		if first.Value() != nil {
			t.Fatal("late private result retained")
		}
		readAndDrop := func(image bool) {
			result := worker.run(t.Context(), image)
			if !result.available || !slices.Equal(result.data, []byte("text")) {
				t.Fatal(result)
			}
		}
		for _, image := range []bool{false, true} {
			readAndDrop(image)
			pointer := <-allocated
			synctest.Wait()
			runtime.GC()
			if pointer.Value() != nil {
				t.Fatal("completed private result retained")
			}
		}
	})
}

func TestClipboardPrivateWorkerProcessFixture(t *testing.T) {
	if os.Getenv("PIG_CLIPBOARD_PROCESS_FIXTURE") != "1" {
		return
	}
	linuxClipboardWorker.read = func(context.Context, bool) clipboardReadResult { select {} }
	defer ShutdownClipboard()
	if got := linuxClipboardWorker.run(t.Context(), false); got.available || got.err != nil {
		t.Fatal(got)
	}
	fmt.Println("passed")
}
