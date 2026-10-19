package node

// pi: packages/durable/src/images/node-worker.ts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math/rand/v2"
	"reflect"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/durable/images"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// recorder is an ImageProcessor that counts how many Prepare calls run at once and what bytes each saw.
type recorder struct {
	running, peak atomic.Int32
	release       chan struct{}
	mutate        bool
	err           error
	panicWith     any
	calls         atomic.Int32
	started       chan struct{}
	onWorker      atomic.Bool
}

func (r *recorder) Prepare(ctx context.Context, data []byte, mimeType string, limits tools.ImageLimits) (*tools.PreparedImage, error) {
	r.calls.Add(1)
	frames := make([]byte, 8192)
	r.onWorker.Store(strings.Contains(string(frames[:runtime.Stack(frames, false)]), "node.(*workerImages).run"))
	if r.started != nil {
		r.started <- struct{}{}
	}
	n := r.running.Add(1)
	defer r.running.Add(-1)
	for {
		p := r.peak.Load()
		if n <= p || r.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if r.release != nil {
		<-r.release
	}
	if r.mutate && len(data) > 0 {
		data[0] ^= 0xff
	}
	if r.panicWith != nil {
		panic(r.panicWith)
	}
	return &tools.PreparedImage{MimeType: mimeType}, r.err
}

// workerGoroutines counts the live worker goroutines.
func workerGoroutines() int {
	var out strings.Builder
	_ = pprof.Lookup("goroutine").WriteTo(&out, 1)
	return strings.Count(out.String(), "node.(*workerImages).run")
}

// pi: packages/durable/src/images/node.ts createNodePhotonImages: the worker thread prepares one image at a time, however
// many are requested at once.
func TestTheWorkerPreparesOneImageAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const callers = 4
		drainWorkers(t)
		r := &recorder{release: make(chan struct{}, callers)}
		p := newWorkerImages(r, time.Minute)
		var wg sync.WaitGroup
		for range callers {
			wg.Go(func() {
				if _, err := p.Prepare(context.Background(), []byte{1}, "image/png", tools.DEFAULT_IMAGE_LIMITS); err != nil {
					t.Error(err)
				}
			})
		}
		// Every caller is waiting on the worker or on the processor: the peak is what ran together.
		synctest.Wait()
		if got := r.peak.Load(); got != 1 {
			t.Errorf("peak concurrent = %d, want 1", got)
		}
		for range callers {
			r.release <- struct{}{}
		}
		wg.Wait()
		if r.calls.Load() != callers {
			t.Errorf("calls = %d, want %d", r.calls.Load(), callers)
		}
	})
}

// pi: packages/durable/src/images/node.ts createNodePhotonImages: worker: false is createPhotonImages on the calling thread,
// whose order (one image at a time, in call order) durable/images pins.
func TestWorkerFalsePreparesWithThePlainProcessor(t *testing.T) {
	off := false
	p, err := CreateNodePhotonImages(&NodePhotonImagesOptions{Worker: &off})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.TypeOf(p) != reflect.TypeOf(images.CreatePhotonImages()) {
		t.Errorf("worker: false returned %T, want the plain Photon processor", p)
	}
}

// sequenceRecorder is an ImageProcessor that logs when each image, named by its first byte, starts and ends, and holds
// each one until it is released or its context is cancelled.
type sequenceRecorder struct {
	mu      sync.Mutex
	log     []string
	release chan struct{}
}

func (r *sequenceRecorder) Prepare(ctx context.Context, data []byte, mimeType string, _ tools.ImageLimits) (*tools.PreparedImage, error) {
	r.record(fmt.Sprintf("start %d", data[0]))
	select {
	case <-r.release:
	case <-ctx.Done():
		r.record(fmt.Sprintf("cancelled %d", data[0]))
		return nil, ctx.Err()
	}
	r.record(fmt.Sprintf("end %d", data[0]))
	return &tools.PreparedImage{MimeType: mimeType}, nil
}

func (r *sequenceRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, event)
}

// pi: packages/durable/src/images/node.ts: the worker answers requests in the order they were sent. A caller whose context
// is cancelled while it waits gives up without taking a turn, and the callers after it keep their order.
func TestTheWorkerPreparesImagesInCallOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		r := &sequenceRecorder{release: make(chan struct{})}
		p := newWorkerImages(r, time.Minute)
		cancelled, cancel := context.WithCancel(context.Background())
		errs := make([]chan error, 5)
		for i := range errs {
			ctx := context.Background()
			if i == 2 {
				ctx = cancelled
			}
			errs[i] = make(chan error, 1)
			go func() {
				_, err := p.Prepare(ctx, []byte{byte(i)}, "image/png", tools.DEFAULT_IMAGE_LIMITS)
				errs[i] <- err
			}()
			// Each call is made once the one before it is preparing or waiting, so the calls have an order.
			synctest.Wait()
		}
		cancel()
		if err := <-errs[2]; !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled caller = %v, want context.Canceled", err)
		}
		for _, i := range []int{0, 1, 3, 4} {
			r.release <- struct{}{}
			if err := <-errs[i]; err != nil {
				t.Errorf("caller %d: %v", i, err)
			}
		}
		want := []string{"start 0", "end 0", "start 1", "end 1", "start 3", "end 3", "start 4", "end 4"}
		if !reflect.DeepEqual(r.log, want) {
			t.Errorf("log = %q, want %q", r.log, want)
		}
	})
}

// pi: packages/durable/test/tools-images.test.ts:496 "prepares in a worker thread, leaving the event loop free": Go has no
// event loop to block, so the case's claim is that the image is prepared on the worker, not on the caller's goroutine.
func TestTheWorkerPreparesOnItsOwnGoroutine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		r := &recorder{}
		if _, err := newWorkerImages(r, time.Minute).Prepare(context.Background(), []byte{1}, "image/png", tools.DEFAULT_IMAGE_LIMITS); err != nil {
			t.Fatal(err)
		}
		if !r.onWorker.Load() {
			t.Error("the image was prepared on the caller's goroutine, not the worker's")
		}
	})
}

// pi: packages/durable/test/tools-images.test.ts "gives the same result as the calling thread, and keeps the caller's
// bytes": a worker that changes the bytes it is given leaves the caller's as they were (Pi transfers a copy).
func TestTheWorkerKeepsTheCallersBytes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		r := &recorder{mutate: true}
		data := []byte{1, 2, 3}
		if _, err := newWorkerImages(r, time.Minute).Prepare(context.Background(), data, "image/png", tools.DEFAULT_IMAGE_LIMITS); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, []byte{1, 2, 3}) {
			t.Errorf("caller's bytes = %v, want [1 2 3]", data)
		}
	})
}

func noisyPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.UintN(256))
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// pi: packages/durable/test/tools-images.test.ts "gives the same result as the calling thread": the worker and the plain
// processor prepare a photo to the same image.
func TestTheWorkerGivesTheSameResultAsTheCallingGoroutine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		photo := noisyPNG(t, 1100, 1100)
		worker, err := CreateNodePhotonImages(nil)
		if err != nil {
			t.Fatal(err)
		}
		threaded, err := worker.Prepare(context.Background(), photo, "image/png", tools.DEFAULT_IMAGE_LIMITS)
		if err != nil || threaded == nil {
			t.Fatalf("worker = %+v, %v", threaded, err)
		}
		inline, err := images.CreatePhotonImages().Prepare(context.Background(), photo, "image/png", tools.DEFAULT_IMAGE_LIMITS)
		if err != nil || !reflect.DeepEqual(threaded, inline) {
			t.Errorf("inline = %+v, %v; want the worker's %+v", inline, err, threaded)
		}
	})
}

// pi: packages/durable/test/tools-images.test.ts "prepares concurrent images, and starts a new worker after the idle one
// exited" (idleMs: 10): the worker exits after IdleMs without a request, and the next image starts another.
func TestTheWorkerExitsWhenIdleAndTheNextImageStartsAnother(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		idle := 10
		p, err := CreateNodePhotonImages(&NodePhotonImagesOptions{IdleMs: &idle})
		if err != nil {
			t.Fatal(err)
		}
		w, ok := p.(*workerImages)
		if !ok {
			t.Fatalf("processor = %T, want the worker", p)
		}
		w.inner = &recorder{}
		base := workerGoroutines()
		prepare := func() {
			t.Helper()
			if got, err := p.Prepare(context.Background(), []byte{1}, "image/bmp", tools.DEFAULT_IMAGE_LIMITS); err != nil || got == nil {
				t.Fatalf("prepare = %+v, %v", got, err)
			}
		}
		prepare()
		if got := workerGoroutines() - base; got != 1 {
			t.Errorf("after an image: %d workers, want 1", got)
		}
		time.Sleep(9 * time.Millisecond)
		synctest.Wait()
		if got := workerGoroutines() - base; got != 1 {
			t.Errorf("9 ms idle: %d workers, want 1 still", got)
		}
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		if got := workerGoroutines() - base; got != 0 {
			t.Errorf("past IdleMs: %d workers, want the idle one gone", got)
		}
		prepare()
		if got := workerGoroutines() - base; got != 1 {
			t.Errorf("next image: %d workers, want a new one", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
	})
}

// pi: node.ts PhotonThread: an image's failure rejects that request only; the worker serves the next one.
func TestTheWorkersFailuresReachTheirCallerAndTheWorkerGoesOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		boom := errors.New("boom")
		r := &recorder{err: boom}
		w := newWorkerImages(r, time.Minute)
		if _, err := w.Prepare(context.Background(), []byte{1}, "image/png", tools.DEFAULT_IMAGE_LIMITS); !errors.Is(err, boom) {
			t.Errorf("error = %v, want boom", err)
		}
		r.err = nil
		if got, err := w.Prepare(context.Background(), []byte{1}, "image/png", tools.DEFAULT_IMAGE_LIMITS); err != nil || got == nil {
			t.Errorf("after a failure: %+v, %v", got, err)
		}
		r.panicWith = "bad image"
		func() {
			defer func() {
				if p := recover(); p != "bad image" {
					t.Errorf("recovered %v, want the processor's panic raised on the caller's goroutine", p)
				}
			}()
			_, _ = w.Prepare(context.Background(), []byte{1}, "image/png", tools.DEFAULT_IMAGE_LIMITS)
		}()
		r.panicWith = nil
		if got, err := w.Prepare(context.Background(), []byte{1}, "image/png", tools.DEFAULT_IMAGE_LIMITS); err != nil || got == nil {
			t.Errorf("after a panic: %+v, %v", got, err)
		}
	})
}

// A request whose context is cancelled returns the context's error whether it waits for the busy worker or for its own
// image, and the worker does not take it up.
func TestTheWorkerHonoursACancelledContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		r := &recorder{release: make(chan struct{}), started: make(chan struct{}, 2)}
		w := newWorkerImages(r, time.Minute)
		running, stop := context.WithCancel(context.Background())
		first := make(chan error, 1)
		go func() {
			_, err := w.Prepare(running, []byte{1}, "image/png", tools.DEFAULT_IMAGE_LIMITS)
			first <- err
		}()
		<-r.started
		ctx, cancel := context.WithCancel(context.Background())
		queued := make(chan error, 1)
		go func() {
			_, err := w.Prepare(ctx, []byte{2}, "image/png", tools.DEFAULT_IMAGE_LIMITS)
			queued <- err
		}()
		cancel()
		if err := <-queued; !errors.Is(err, context.Canceled) {
			t.Errorf("queued = %v, want context.Canceled", err)
		}
		stop()
		if err := <-first; !errors.Is(err, context.Canceled) {
			t.Errorf("running = %v, want context.Canceled while the image is still being prepared", err)
		}
		r.release <- struct{}{}
		if r.calls.Load() != 1 {
			t.Errorf("worker prepared %d images, want only the first", r.calls.Load())
		}
	})
}

// drainWorkers lets the fake clock run past every idle worker's exit when the bubble ends, so no worker outlives its test.
func drainWorkers(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		time.Sleep(time.Hour)
		synctest.Wait()
	})
}
