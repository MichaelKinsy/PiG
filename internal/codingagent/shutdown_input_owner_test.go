package codingagent

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

type shutdownReadCloser struct {
	entered        chan struct{}
	closing        chan struct{}
	release        chan struct{}
	once           sync.Once
	order          *[]string
	releaseOnClose bool
	closeErr       error
}

func (r *shutdownReadCloser) Read([]byte) (int, error) {
	close(r.entered)
	<-r.release
	*r.order = append(*r.order, "read-returned")
	return 0, io.EOF
}

func (r *shutdownReadCloser) Close() error {
	r.once.Do(func() {
		close(r.closing)
		if r.releaseOnClose {
			close(r.release)
		}
	})
	return r.closeErr
}

type countedInputCloser struct {
	*strings.Reader
	closes int
}

func (r *countedInputCloser) Close() error { r.closes++; return nil }

func TestInteractiveInputEOFJoinsWithoutClosingCompletedSource(t *testing.T) {
	var order []string
	mode := upstreamShutdownMode(t, &order, false)
	source := &countedInputCloser{Reader: strings.NewReader("")}
	err := mode.inputLoop(t.Context(), source)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("input EOF was lost: %v", err)
	}
	if source.closes != 0 || mode.inputOwner != nil {
		t.Fatalf("natural EOF: closes=%d owner=%v", source.closes, mode.inputOwner)
	}
}

func TestInteractiveShutdownReturnsReaderCloseErrorAfterJoining(t *testing.T) {
	var order []string
	mode := upstreamShutdownMode(t, &order, false)
	failure := errors.New("reader close failed")
	source := &shutdownReadCloser{entered: make(chan struct{}), closing: make(chan struct{}), release: make(chan struct{}), order: &order, releaseOnClose: true, closeErr: failure}
	mode.startTerminalInput(t.Context(), source)
	<-source.entered
	mode.requestShutdown()
	if err := mode.inputLoop(t.Context(), source); !errors.Is(err, failure) {
		t.Fatalf("close failure was lost: %v", err)
	}
	if want := []string{"read-returned", "drainInput", "stop", "dispose"}; !slices.Equal(order, want) {
		t.Fatalf("close failure skipped cleanup: %v", order)
	}
}

func TestInteractiveShutdownLeavesTerminalFileForNextReader(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	var order []string
	mode := upstreamShutdownMode(t, &order, false)
	mode.startTerminalInput(t.Context(), input)
	mode.requestShutdown()
	if err := mode.inputLoop(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if _, err := io.ReadFull(input, data[:]); err != nil {
		t.Fatal(err)
	}
	if string(data[:]) != "x" {
		t.Fatalf("next reader lost input: %q", data)
	}
}

func BenchmarkInteractiveInputLifecycle(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		mode := &InteractiveMode{}
		if err := mode.inputLoop(b.Context(), strings.NewReader("")); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
	}
}

// Pi ProcessTerminal.stop removes its input listener before returning. The Go owner must wait for its reader and decoder before terminal teardown or caller-owned output can be released.
func TestInteractiveShutdownJoinsInputBeforeTerminalTeardown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var order []string
		mode := upstreamShutdownMode(t, &order, false)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		source := &shutdownReadCloser{entered: make(chan struct{}), closing: make(chan struct{}), release: make(chan struct{}), order: &order}
		mode.startTerminalInput(ctx, source)
		<-source.entered
		// A temporary startup/UI loop must leave the shared owner alive.
		until := make(chan struct{})
		close(until)
		if err := mode.inputLoopUntil(ctx, source, until); err != nil {
			t.Fatal(err)
		}
		select {
		case <-source.closing:
			t.Error("temporary loop closed shared input")
		default:
		}
		mode.requestShutdown()
		done := make(chan error, 1)
		go func() { done <- mode.inputLoop(ctx, source) }()
		synctest.Wait()
		select {
		case <-source.closing:
		default:
			t.Error("shutdown did not cancel the owned read")
		}
		returnedEarly := false
		select {
		case err := <-done:
			returnedEarly = true
			t.Errorf("shutdown returned before read joined: %v", err)
		default:
		}
		if len(order) != 0 {
			t.Errorf("terminal teardown overtook input: %v", order)
		}
		close(source.release)
		if !returnedEarly {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		synctest.Wait()
		if want := []string{"read-returned", "drainInput", "stop", "dispose"}; !slices.Equal(order, want) {
			t.Fatalf("order=%v; want %v", order, want)
		}
	})
}
