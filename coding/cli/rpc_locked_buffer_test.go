package cli

import (
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type bufferWait struct {
	got string
	ok  bool
}

// startBufferWait runs b.waitFor on its own goroutine and returns once it is parked on b, so the next write takes the wake-up path.
func startBufferWait(t *testing.T, b *lockedBuffer, want string, abort <-chan struct{}) <-chan bufferWait {
	t.Helper()
	budget := testbudget.Wait(t)
	done := make(chan bufferWait, 1)
	go func() {
		got, ok := b.waitFor(want, abort, budget)
		done <- bufferWait{got, ok}
	}()
	awaitParkedBufferWait(t, b)
	return done
}

// awaitParkedBufferWait returns once a waitFor of b is parked: a Write clears the mark, and the waiter sets it again when it parks after a write that did not complete its text.
func awaitParkedBufferWait(t *testing.T, b *lockedBuffer) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		b.mu.Lock()
		parked := b.grown != nil
		b.mu.Unlock()
		if parked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("waitFor never parked")
		}
		runtime.Gosched()
	}
}

func receiveBufferWait(t *testing.T, done <-chan bufferWait) bufferWait {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("waitFor did not return")
		return bufferWait{}
	}
}

func TestLockedBufferWaitForWakesOnTheWriteThatCompletesTheText(t *testing.T) {
	var b lockedBuffer
	done := startBufferWait(t, &b, "at cap\n", nil)
	_, _ = b.Write([]byte("Warning: other\n"))
	// A write without the text sends the waiter back to waiting: it parks again instead of returning.
	awaitParkedBufferWait(t, &b)
	_, _ = b.Write([]byte("at cap\n"))
	if r := receiveBufferWait(t, done); !r.ok || r.got != "Warning: other\nat cap\n" {
		t.Fatalf("waitFor = %q, %v; want both lines and true", r.got, r.ok)
	}
}

func TestLockedBufferWaitForReturnsAtOnceWhenTheTextIsThere(t *testing.T) {
	var b lockedBuffer
	_, _ = b.Write([]byte("at cap\n"))
	if got, ok := b.waitFor("at cap", nil, testbudget.Wait(t)); !ok || got != "at cap\n" {
		t.Fatalf("waitFor = %q, %v; want the line and true", got, ok)
	}
}

func TestLockedBufferWaitForReportsTheContentSoFarWhenTheBudgetPasses(t *testing.T) {
	var b lockedBuffer
	_, _ = b.Write([]byte("other\n"))
	if got, ok := b.waitFor("at cap", nil, 10*time.Millisecond); ok || got != "other\n" {
		t.Fatalf("waitFor = %q, %v; want the content so far and false", got, ok)
	}
}

func TestLockedBufferWaitForEndsWhenTheChildEnds(t *testing.T) {
	var b lockedBuffer
	exited := make(chan struct{})
	done := startBufferWait(t, &b, "at cap", exited)
	_, _ = b.Write([]byte("other\n"))
	awaitParkedBufferWait(t, &b)
	close(exited)
	if r := receiveBufferWait(t, done); r.ok || r.got != "other\n" {
		t.Fatalf("waitFor after the end = %q, %v; want the final content and false", r.got, r.ok)
	}
}
