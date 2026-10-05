package childwait

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func yes() bool { return true }

// .upstream/v1.0.0/packages/coding-agent/test/suite/regressions/5303-bash-output-truncation.test.ts:39
// "captures output emitted after exit while a descendant holds stdout open":
// every chunk re-arms the grace, and the wait ends one grace after the last.
func TestWaitIdleRearmsOnEveryChunk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		activity := make(chan struct{})
		result := make(chan bool, 1)
		go func() { result <- waitIdle(make(chan struct{}), activity, yes, nil) }()
		for range 6 {
			time.Sleep(50 * time.Millisecond)
			select {
			case activity <- struct{}{}:
			case <-result:
				t.Fatal("the wait resolved while output was still arriving every 50ms")
			}
		}
		time.Sleep(Grace - time.Millisecond)
		synctest.Wait()
		select {
		case <-result:
			t.Fatal("the wait resolved before the grace elapsed after the last chunk")
		default:
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		select {
		case closed := <-result:
			if closed {
				t.Fatal("the wait reported closed pipes; want release by the idle grace")
			}
		default:
			t.Fatal("the wait did not resolve once the grace elapsed after the last chunk")
		}
	})
}

// .upstream/v1.0.0/packages/coding-agent/test/suite/regressions/5303-bash-output-truncation.test.ts:66
// "resolves after the grace when a descendant holds stdout open but stays quiet".
func TestWaitIdleReleasesAQuietHeldPipe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		result := make(chan bool, 1)
		go func() { result <- waitIdle(make(chan struct{}), make(chan struct{}), yes, nil) }()
		time.Sleep(Grace - time.Millisecond)
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
				t.Fatal("the wait reported closed pipes; want release by the idle grace")
			}
		default:
			t.Fatal("the wait did not resolve after the grace")
		}
	})
}

// When no process can hold a pipe, the grace gives way to EOF.
func TestWaitIdleReadsToEOFWhenNothingHoldsThePipe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		result := make(chan bool, 1)
		go func() { result <- waitIdle(done, make(chan struct{}), func() bool { return false }, nil) }()
		time.Sleep(10 * Grace)
		synctest.Wait()
		select {
		case <-result:
			t.Fatal("the wait gave up on a pipe that no process holds")
		default:
		}
		close(done)
		synctest.Wait()
		if closed := <-result; !closed {
			t.Fatal("the wait did not report the ended pipes")
		}
	})
}

// A holder that stays quiet releases Wait after the grace, and a chunk that
// arrives afterwards is discarded.
func TestPipesWaitReleasesAHeldPipeAndDiscardsLateData(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	var (
		mu  sync.Mutex
		got strings.Builder
	)
	delivered := make(chan struct{}, 1)
	pipes := Start([]*os.File{r}, func(_ int, chunk []byte) {
		mu.Lock()
		got.Write(chunk)
		mu.Unlock()
		select {
		case delivered <- struct{}{}:
		default:
		}
	}, Hooks{})
	if _, err := io.WriteString(w, "kept"); err != nil {
		t.Fatal(err)
	}
	<-delivered
	if pipes.Wait(yes) {
		t.Fatal("Wait reported ended pipes while the write end is open")
	}
	_, _ = io.WriteString(w, "late")
	mu.Lock()
	defer mu.Unlock()
	if got.String() != "kept" {
		t.Fatalf("output = %q, want %q", got.String(), "kept")
	}
}

// Wait returns as soon as every pipe ends, without waiting for the grace.
func TestPipesWaitReturnsOnEOFOfEveryPipe(t *testing.T) {
	var files []*os.File
	var writers []*os.File
	for range 2 {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		files, writers = append(files, r), append(writers, w)
	}
	var (
		mu  sync.Mutex
		got [2]string
	)
	pipes := Start(files, func(index int, chunk []byte) {
		mu.Lock()
		got[index] += string(chunk)
		mu.Unlock()
	}, Hooks{})
	_, _ = io.WriteString(writers[0], "out")
	_, _ = io.WriteString(writers[1], "err")
	for _, w := range writers {
		_ = w.Close()
	}
	if !pipes.Wait(func() bool { t.Error("grace expired"); return true }) {
		t.Fatal("Wait did not report ended pipes")
	}
	mu.Lock()
	defer mu.Unlock()
	if got != [2]string{"out", "err"} {
		t.Fatalf("output = %q", got)
	}
}
