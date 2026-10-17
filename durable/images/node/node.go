// Package node is the image processor for hosts that load Photon's WebAssembly from an installed package.
//
// Ports packages/durable/src/images/node.ts and node-worker.ts.
package node

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/durable/images"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// defaultIdle is how long the worker stays after its last image (node.ts: idleMs ?? 30_000).
const defaultIdle = 30 * time.Second

// NodePhotonImagesOptions configures CreateNodePhotonImages.
type NodePhotonImagesOptions struct {
	// Worker prepares images on one worker goroutine, one at a time, so a large photo does not run on the caller's
	// goroutine. Default true; false prepares them with images.CreatePhotonImages on the caller's goroutine, one at a time in
	// call order.
	Worker *bool
	// IdleMs is how long the worker stays after its last image before it exits. The next image starts a new one.
	// Default 30000.
	IdleMs *int
}

// CreateNodePhotonImages is images.CreatePhotonImages for Node and Bun, by default behind a worker that keeps its goroutine
// only while it prepares an image and for IdleMs after. The Go processor decodes and encodes natively, so there is no
// WebAssembly to read and the worker always starts (Pi falls back to the calling thread when it cannot); the error is the one
// Pi's promise carries. A nil options value is the defaults.
func CreateNodePhotonImages(options *NodePhotonImagesOptions) (tools.ImageProcessor, error) {
	if options == nil {
		options = &NodePhotonImagesOptions{}
	}
	inner := images.CreatePhotonImages()
	if options.Worker != nil && !*options.Worker {
		return inner, nil
	}
	idle := defaultIdle
	if options.IdleMs != nil {
		idle = time.Duration(*options.IdleMs) * time.Millisecond
	}
	return newWorkerImages(inner, idle), nil
}

// workerImages prepares on one worker goroutine at a time (node.ts PhotonThread), started by the first request and gone
// idle after the last.
type workerImages struct {
	inner tools.ImageProcessor
	idle  time.Duration

	mu     sync.Mutex
	thread *thread
}

func newWorkerImages(inner tools.ImageProcessor, idle time.Duration) *workerImages {
	return &workerImages{inner: inner, idle: idle}
}

// job is one request and the channel its reply comes back on (node-worker.ts PrepareRequest).
type job struct {
	ctx      context.Context
	bytes    []byte
	mimeType string
	limits   tools.ImageLimits
	reply    chan reply
}

// reply is a request's result, or why it failed (node-worker.ts PrepareReply). A panic in the processor goes back to the
// caller, which raises it on its own goroutine as it would have without the worker.
type reply struct {
	result *tools.PreparedImage
	err    error
	panic  any
}

type thread struct {
	jobs chan *job
	done chan struct{}
}

// Prepare hands a copy of the bytes to the worker, so the caller keeps its own, and waits for the answer.
func (w *workerImages) Prepare(ctx context.Context, data []byte, mimeType string, limits tools.ImageLimits) (*tools.PreparedImage, error) {
	j := &job{ctx: ctx, bytes: bytes.Clone(data), mimeType: mimeType, limits: limits, reply: make(chan reply, 1)}
	for {
		t := w.current()
		select {
		case t.jobs <- j:
			select {
			case r := <-j.reply:
				if r.panic != nil {
					panic(r.panic)
				}
				return r.result, r.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case <-t.done:
			// The worker went idle between current and the send: the next current starts a new one.
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (w *workerImages) current() *thread {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.thread == nil {
		w.thread = &thread{jobs: make(chan *job), done: make(chan struct{})}
		go w.run(w.thread) //portlint:allow golifetime node-worker.ts's idle worker: run exits after w.idle without a job and clears w.thread
	}
	return w.thread
}

// run prepares each job it is given, then exits after idle without one.
func (w *workerImages) run(t *thread) {
	timer := time.NewTimer(w.idle)
	defer timer.Stop()
	for {
		select { //portlint:allow golifetime the idle timer case is the stop: the worker exits once no job came within w.idle
		case j := <-t.jobs:
			j.reply <- w.prepare(j)
			timer.Reset(w.idle)
		case <-timer.C:
			w.mu.Lock()
			w.thread = nil
			w.mu.Unlock()
			close(t.done)
			return
		}
	}
}

func (w *workerImages) prepare(j *job) (r reply) {
	defer func() {
		if p := recover(); p != nil {
			r = reply{panic: p}
		}
	}()
	result, err := w.inner.Prepare(j.ctx, j.bytes, j.mimeType, j.limits)
	return reply{result: result, err: err}
}
