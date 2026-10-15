package images

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/durable/tools"
)

// orderRecorder is an ImageProcessor that logs when each image, named by its first byte, starts and ends, and holds each one
// until it is released or its context is cancelled.
type orderRecorder struct {
	mu      sync.Mutex
	log     []string
	running int
	peak    int
	release chan struct{}
}

func (r *orderRecorder) Prepare(ctx context.Context, data []byte, mimeType string, _ tools.ImageLimits) (*tools.PreparedImage, error) {
	r.record(fmt.Sprintf("start %d", data[0]), 1)
	select {
	case <-r.release:
	case <-ctx.Done():
		r.record(fmt.Sprintf("cancelled %d", data[0]), -1)
		return nil, ctx.Err()
	}
	r.record(fmt.Sprintf("end %d", data[0]), -1)
	return &tools.PreparedImage{MimeType: mimeType}, nil
}

func (r *orderRecorder) record(event string, running int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, event)
	r.running += running
	r.peak = max(r.peak, r.running)
}

// pi: packages/durable/src/images/index.ts createPhotonImages: prepare is an async wrapper around synchronous work on the
// calling thread, so however many images are requested at once, each is prepared to the end before the next one starts.
func TestCreatePhotonImagesPreparesOneImageAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const callers = 4
		r := &orderRecorder{release: make(chan struct{}, callers)}
		p := newSequential(r)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				if _, err := p.Prepare(context.Background(), []byte{byte(i)}, "image/png", tools.DEFAULT_IMAGE_LIMITS); err != nil {
					t.Error(err)
				}
			})
		}
		// Every caller is waiting its turn or on the processor: the peak is what ran together.
		synctest.Wait()
		for range callers {
			r.release <- struct{}{}
		}
		wg.Wait()
		if r.peak != 1 {
			t.Errorf("peak concurrent = %d, want 1 (log %q)", r.peak, r.log)
		}
		if len(r.log) != 2*callers {
			t.Errorf("log = %q, want a start and an end for each of %d images", r.log, callers)
		}
	})
}

// pi: packages/durable/src/images/index.ts createPhotonImages: images are prepared one after another in call order. A caller
// whose context is cancelled while it waits gives up without taking a turn, and the callers after it keep their order.
func TestCreatePhotonImagesPreparesImagesInCallOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &orderRecorder{release: make(chan struct{})}
		p := newSequential(r)
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
