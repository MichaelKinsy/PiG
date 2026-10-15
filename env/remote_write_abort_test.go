package env

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// countedAbort is a context whose Err turns into context.Canceled after a set number of reads and whose Done never closes: it aborts at an exact check of an operation and never makes the connection send a cancel.
type countedAbort struct {
	context.Context
	reads atomic.Int32
	after int32
}

func (c *countedAbort) Err() error {
	if c.reads.Add(1) > c.after {
		return context.Canceled
	}
	return nil
}

// chunkedContent is chunks transfer chunks and one more byte, each chunk filled with its own index.
func chunkedContent(chunks int) []byte {
	content := make([]byte, 0, chunks*writeChunk+1)
	for index := range chunks {
		content = append(content, bytes.Repeat([]byte{byte(index + 1)}, writeChunk)...)
	}
	return append(content, 0xff)
}

// Pi's #write (packages/env/src/remote-env.ts:587-626) checks writeFile's signal before each chunk after the first; when it is aborted it waits for every chunk in flight (Promise.allSettled), fails with "The operation was aborted", and closes the handle. The file then holds exactly the chunks sent before the check, in order.
// mutation-checked: dropping the signal check before a chunk fails it. Settling no chunk before the handle is closed survives: the local daemon writes the few chunks in flight before the test reads the file.
func TestRemoteWriteFileAbortedBetweenChunksSettlesTheChunksInFlightAndStops(t *testing.T) {
	t.Parallel()
	env, _ := remoteEnvironment(t)
	// The checks before the transfer: a single-chunk write makes only those.
	probe := &countedAbort{Context: context.Background(), after: 1 << 30}
	if err := env.WriteFile(probe, "probe.bin", []byte("x")); err != nil {
		t.Fatal(err)
	}
	const sentBeforeAbort = 3
	content := chunkedContent(10)
	ctx := &countedAbort{Context: context.Background(), after: probe.reads.Load() + sentBeforeAbort}
	err := env.WriteFile(ctx, "big.bin", content)
	var fileError *durableenv.FileError
	if !errors.As(err, &fileError) || fileError.Code != durableenv.FileErrorAborted || fileError.Message != "The operation was aborted" {
		t.Fatalf("WriteFile = %v; want the aborted FileError", err)
	}
	target := filepath.Join(env.Cwd(), "big.bin")
	written, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	want := (1 + sentBeforeAbort) * writeChunk
	if !bytes.Equal(written, content[:want]) {
		t.Fatalf("file holds %d bytes; want the first %d bytes (the first request and %d chunks) in order", len(written), want, sentBeforeAbort)
	}
}

// Pi's appendFile has no signal for the transfer (remote-env.ts:589) and checks it before and after (:625 "both"): an append whose context is aborted during the transfer appends everything and then fails as aborted.
// mutation-checked: checking the append's context between chunks, or dropping the check after the transfer, fails it.
func TestRemoteAppendFileAbortedDuringTheTransferAppendsEverythingThenFails(t *testing.T) {
	t.Parallel()
	env, _ := remoteEnvironment(t)
	probe := &countedAbort{Context: context.Background(), after: 1 << 30}
	if err := env.AppendFile(probe, "probe.bin", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// The probe also counts the check after the transfer.
	before := probe.reads.Load() - 1
	content := chunkedContent(4)
	ctx := &countedAbort{Context: context.Background(), after: before}
	err := env.AppendFile(ctx, "log.bin", content)
	var fileError *durableenv.FileError
	if !errors.As(err, &fileError) || fileError.Code != durableenv.FileErrorAborted {
		t.Fatalf("AppendFile = %v; want the aborted FileError", err)
	}
	written, readErr := os.ReadFile(filepath.Join(env.Cwd(), "log.bin"))
	if readErr != nil || !bytes.Equal(written, content) {
		t.Fatalf("file holds %d bytes, %v; want all %d appended", len(written), readErr, len(content))
	}
}

// Pi's exec (packages/env/src/remote-env.ts:887-906) turns a throwing onOutput into callback_error, aborts the command through its own controller, and settles with the callback's error rather than the daemon's aborted reply.
// mutation-checked: not cancelling the request after the callback fails, or returning the daemon's error instead of the callback's, fails it.
func TestRemoteExecThrowingOnOutputCancelsTheCommandAndFailsWithTheCallbackError(t *testing.T) {
	t.Parallel()
	env, _ := remoteEnvironment(t)
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	began := time.Now()
	_, err := env.Exec(background, []string{"sh", "-c", "echo first; sleep 20"}, &durableenv.ShellExecOptions{
		OnOutput: func(context.Context, string, durableenv.ShellOutputInfo) { panic(errors.New("listener failed")) },
	})
	var failure *durableenv.ExecutionError
	if !errors.As(err, &failure) || failure.Code != durableenv.ExecutionErrorCallbackError || failure.Message != "listener failed" {
		t.Fatalf("Exec = %v; want the callback_error with the callback's message", err)
	}
	if elapsed := time.Since(began); elapsed > 10*time.Second {
		t.Fatalf("Exec settled after %v; the failing callback did not abort the command", elapsed)
	}
}
