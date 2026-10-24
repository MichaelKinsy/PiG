package childwait

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The grace expires while the pipe's reader is parked in a read of it, which is when a quiet holder is released. The question the grace asks must not
// wait behind that read: a pipe whose reader is blocked and whose holder is quiet answers at once, again and again.
func TestPollPipeAnswersWhileAReaderIsBlockedInReadingIt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })
	var (
		mu    sync.Mutex
		got   int
		taken = make(chan struct{}, 1)
	)
	pipes := Start([]*os.File{r}, func(_ int, chunk []byte) {
		mu.Lock()
		got += len(chunk)
		mu.Unlock()
		select {
		case taken <- struct{}{}:
		default:
		}
	}, Hooks{})
	t.Cleanup(func() { pipes.Wait(yes) })
	if _, err := w.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	<-taken // the reader has the byte and goes back to reading an empty pipe the holder keeps open
	answered := make(chan [3]bool, 1)
	go func() {
		for range 200 {
			hungUp, unread, known := pollPipe(r)
			if hungUp || unread || !known {
				answered <- [3]bool{hungUp, unread, known}
				return
			}
		}
		answered <- [3]bool{false, false, true}
	}()
	select {
	case a := <-answered:
		if a != [3]bool{false, false, true} {
			t.Fatalf("hungUp=%v unread=%v known=%v for an empty pipe a holder keeps open, want false false true", a[0], a[1], a[2])
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("pollPipe did not answer while the reader was blocked in reading the pipe")
	}
}
