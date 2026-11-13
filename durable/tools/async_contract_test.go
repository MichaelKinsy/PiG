package tools

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/env"
)

// image.ts detectSupportedImageMimeTypeOf / isAnimatedPngOf await each positional read, so a read that rejects ends the detection with that error, and
// a PNG's chunk walk reads one block, not one read per chunk.
func pngWithChunks(chunks int) []byte {
	data := []byte("\x89PNG\r\n\x1a\n")
	data = append(data, 0, 0, 0, 13)
	data = append(data, "IHDR"...)
	data = append(data, make([]byte, 13+4)...)
	for range chunks {
		data = append(data, 0, 0, 0, 0)
		data = append(data, "tEXt"...)
		data = append(data, 0, 0, 0, 0)
	}
	data = append(data, 0, 0, 0, 0)
	data = append(data, "IDAT"...)
	data = append(data, 0, 0, 0, 0)
	return data
}

func TestImageDetectionPropagatesAFailedReadAndReadsOneBlockForAPNGChunkWalk(t *testing.T) {
	data := pngWithChunks(20)
	source := func(failAt int, reads *int) byteSource {
		return byteSource{size: int64(len(data)), read: func(offset, length int64) ([]byte, error) {
			*reads++
			if *reads == failAt {
				return nil, errors.New("read failed")
			}
			return data[min(offset, int64(len(data))):min(offset+length, int64(len(data)))], nil
		}}
	}
	for failAt, name := range map[int]string{1: "the header read", 2: "the chunk block read"} {
		var reads int
		if _, err := detectSupportedImageMimeTypeOf(source(failAt, &reads)); err == nil || err.Error() != "read failed" {
			t.Fatalf("a failure of %s returned %v", name, err)
		}
	}
	var reads int
	mime, err := detectSupportedImageMimeTypeOf(source(0, &reads))
	if err != nil || mime != "image/png" || reads != 2 {
		t.Fatalf("mime %q, error %v after %d reads; the header read and one block cover 20 chunks", mime, err, reads)
	}
}

// path-utils.ts resolveReadToolPath awaits env.exists over the de-duplicated variants in order: the first existing one wins, a rejection ends the walk
// with its error before any later variant is asked, and the caller's context reaches every call.
type recordingEnv struct {
	env.ExecutionEnv
	exists func(ctx context.Context, path string) (bool, error)
	asked  []string
	ctxs   []context.Context
}

func (e *recordingEnv) AbsolutePath(_ context.Context, path string) (string, error) { return path, nil }

func (e *recordingEnv) Exists(ctx context.Context, path string) (bool, error) {
	e.asked = append(e.asked, path)
	e.ctxs = append(e.ctxs, ctx)
	return e.exists(ctx, path)
}

type pathCtxKey struct{}

func TestResolveReadToolPathAsksTheVariantsInOrderAndStopsAtTheFirstRejection(t *testing.T) {
	ctx := context.WithValue(context.Background(), pathCtxKey{}, "caller")
	const name = "Screenshot 2024-01-02 at 10.05.00 AM.png"
	var first, second string
	{
		probe := &recordingEnv{exists: func(context.Context, string) (bool, error) { return false, nil }}
		if _, err := resolveReadToolPath(ctx, probe, name); err != nil {
			t.Fatal(err)
		}
		if len(probe.asked) < 2 {
			t.Fatalf("variants asked: %q", probe.asked)
		}
		first, second = probe.asked[0], probe.asked[1]
	}
	if first != name {
		t.Fatalf("the requested path is asked first, got %q", first)
	}

	both := &recordingEnv{exists: func(context.Context, string) (bool, error) { return true, nil }}
	got, err := resolveReadToolPath(ctx, both, name)
	if err != nil || got != first || len(both.asked) != 1 {
		t.Fatalf("with every variant present: %q, %v after %q", got, err, both.asked)
	}

	onlySecond := &recordingEnv{exists: func(_ context.Context, path string) (bool, error) { return path == second, nil }}
	if got, err = resolveReadToolPath(ctx, onlySecond, name); err != nil || got != second || !slices.Equal(onlySecond.asked, []string{first, second}) {
		t.Fatalf("second variant only: %q, %v after %q", got, err, onlySecond.asked)
	}

	failure := errors.New("exists failed")
	rejecting := &recordingEnv{exists: func(context.Context, string) (bool, error) { return false, failure }}
	if _, err = resolveReadToolPath(ctx, rejecting, name); !errors.Is(err, failure) || len(rejecting.asked) != 1 {
		t.Fatalf("a rejection returned %v after %q; no later variant is asked", err, rejecting.asked)
	}
	for _, c := range append(append(both.ctxs, onlySecond.ctxs...), rejecting.ctxs...) {
		if c.Value(pathCtxKey{}) != "caller" {
			t.Fatal("the caller's context does not reach env.exists")
		}
	}
}

// file-mutation-queue.ts: a callback that fails releases the slot (the finally), the error is the caller's result, and the next caller then runs.
func TestWithFileMutationQueueReleasesTheSlotWhenTheCallbackFails(t *testing.T) {
	executionEnv := createEnv(t)
	key, err := mutationKey(background, executionEnv, "failing.txt")
	mustDo(t, err)
	failure := errors.New("callback failed")
	if _, err := withFileMutationQueue(background, executionEnv, "failing.txt", func() (struct{}, error) { return struct{}{}, failure }); !errors.Is(err, failure) {
		t.Fatalf("error = %v, want the callback's", err)
	}
	mutationQueues.mu.Lock()
	_, left := mutationQueues.tails[key]
	mutationQueues.mu.Unlock()
	if left {
		t.Fatal("a failed callback left its slot in the queue")
	}
	ran := false
	if _, err := withFileMutationQueue(background, executionEnv, "failing.txt", func() (struct{}, error) { ran = true; return struct{}{}, nil }); err != nil || !ran {
		t.Fatalf("the next caller ran=%v error=%v", ran, err)
	}
}
