package node

// pi: packages/durable/src/images/node.ts PhotonThread: #settled starts the idle timer once no request is pending, and prepare clears it

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/durable/tools"
)

func prepareOne(t *testing.T, p tools.ImageProcessor) {
	t.Helper()
	if got, err := p.Prepare(context.Background(), []byte{1}, "image/bmp", tools.DEFAULT_IMAGE_LIMITS); err != nil || got == nil {
		t.Fatalf("prepare = %+v, %v", got, err)
	}
}

// node.ts: `const idleMs = options.idleMs ?? 30_000`. The worker is there 29.999 s after its last image and gone after 30 s.
func TestTheWorkerStaysThirtySecondsByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		p, err := CreateNodePhotonImages(nil)
		if err != nil {
			t.Fatal(err)
		}
		p.(*workerImages).inner = &recorder{}
		base := workerGoroutines()
		prepareOne(t, p)
		time.Sleep(30*time.Second - time.Millisecond)
		synctest.Wait()
		if got := workerGoroutines() - base; got != 1 {
			t.Errorf("29.999 s after the image: %d workers, want 1", got)
		}
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		if got := workerGoroutines() - base; got != 0 {
			t.Errorf("30.001 s after the image: %d workers, want none", got)
		}
	})
}

// node.ts #settled arms the idle timer when a reply leaves no request pending and prepare clears it: the worker leaves
// idleMs after its last image, not idleMs after it started.
func TestTheWorkerLeavesIdleMsAfterItsLastImage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		idle := 10
		p, err := CreateNodePhotonImages(&NodePhotonImagesOptions{IdleMs: &idle})
		if err != nil {
			t.Fatal(err)
		}
		p.(*workerImages).inner = &recorder{}
		base := workerGoroutines()
		prepareOne(t, p)
		time.Sleep(6 * time.Millisecond)
		prepareOne(t, p)
		time.Sleep(6 * time.Millisecond)
		synctest.Wait()
		if got := workerGoroutines() - base; got != 1 {
			t.Errorf("12 ms after the first image, 6 after the second: %d workers, want 1 still", got)
		}
		time.Sleep(5 * time.Millisecond)
		synctest.Wait()
		if got := workerGoroutines() - base; got != 0 {
			t.Errorf("11 ms after the second image: %d workers, want none", got)
		}
	})
}

// A request that finds the worker as its idle time ends is served by a new worker, however the two orders fall: the
// request's send never waits for a worker that has gone.
func TestARequestAsTheWorkerGoesIdleIsServedByANewWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drainWorkers(t)
		idle := 10 * time.Millisecond
		w := newWorkerImages(&recorder{}, idle)
		for range 400 {
			prepareOne(t, w)
			// The idle timer fires at the instant this sleep ends.
			time.Sleep(idle)
		}
		time.Sleep(time.Second)
		synctest.Wait()
	})
}
