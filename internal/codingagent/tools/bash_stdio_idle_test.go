package tools

import (
	"testing"
	"testing/synctest"
	"time"
)

// Ports suite/regressions/5303-bash-output-truncation.test.ts. Like upstream,
// it uses virtual time, so host load cannot reorder a real subprocess's
// writes and the grace timer.

// Output that keeps arriving after the shell exits re-arms the grace on every
// chunk, so the wait resolves only once the pipe has been idle for the full
// grace after the last chunk.
func TestWaitForStdioIdleRearmsOnEveryChunk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		activity := make(chan struct{})
		result := make(chan bool, 1)
		go func() { result <- waitForStdioIdle(make(chan struct{}), activity) }()
		for range 6 {
			time.Sleep(50 * time.Millisecond)
			select {
			case activity <- struct{}{}:
			case <-result:
				t.Fatal("the wait resolved while output was still arriving every 50ms")
			}
		}
		time.Sleep(99 * time.Millisecond)
		synctest.Wait()
		select {
		case <-result:
			t.Fatal("the wait resolved 99ms after the last chunk, before the grace elapsed")
		default:
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		select {
		case closed := <-result:
			if closed {
				t.Fatal("the wait reported a closed pipe; want release by the idle grace")
			}
		default:
			t.Fatal("the wait did not resolve once the grace elapsed after the last chunk")
		}
	})
}

// A descendant that holds the pipe open but stays quiet releases the wait
// after the grace.
func TestWaitForStdioIdleReleasesAQuietHeldPipe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		result := make(chan bool, 1)
		go func() { result <- waitForStdioIdle(make(chan struct{}), make(chan struct{})) }()
		time.Sleep(99 * time.Millisecond)
		synctest.Wait()
		select {
		case <-result:
			t.Fatal("the wait resolved before the grace elapsed")
		default:
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		select {
		case closed := <-result:
			if closed {
				t.Fatal("the wait reported a closed pipe; want release by the idle grace")
			}
		default:
			t.Fatal("the wait did not resolve after the grace")
		}
	})
}
