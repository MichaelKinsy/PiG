package tui

import (
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"
)

// lockedRows is a dirty-flag component whose lines another goroutine may
// replace while a frame renders it.
type lockedRows struct {
	invalidatable
	mu    sync.Mutex
	lines []string
}

func (r *lockedRows) set(lines ...string) {
	r.mu.Lock()
	r.lines = lines
	r.mu.Unlock()
	r.Invalidate()
}

func (r *lockedRows) Render(int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lines
}

// concurrencyChat returns a container whose first row changes, followed by
// 50 fixed rows, so every render rebuilds and copies every line.
func concurrencyChat() (*Container, *lockedRows) {
	head := &lockedRows{lines: []string{"head"}}
	chat := NewContainer(head)
	for i := range 50 {
		chat.Add(&lockedRows{lines: []string{fmt.Sprintf("line %d", i)}})
	}
	return chat, head
}

func checkConcurrencyLines(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) != 51 {
		t.Errorf("render has %d lines, want 51", len(lines))
		return
	}
	for i := range 50 {
		if want := fmt.Sprintf("line %d", i); lines[i+1] != want {
			t.Errorf("line %d = %q, want %q", i+1, lines[i+1], want)
			return
		}
	}
}

// Container.Render returns a fresh slice that its caller owns, as Pi's
// Container.render does, so goroutines that render the same container never
// share an array. A Render that copied the reused window array after
// releasing the lock raced with the next render writing into it (go test
// -race).
func TestContainerRenderIsSafeForConcurrentCallers(t *testing.T) {
	chat, head := concurrencyChat()
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 200 {
				head.set(fmt.Sprintf("head %d %d", g, i))
				lines := chat.Render(40)
				checkConcurrencyLines(t, lines)
				// The caller owns the slice: editing it changes no later render.
				lines[1] = "edited"
			}
		})
	}
	wg.Wait()
	checkConcurrencyLines(t, chat.Render(40))
}

// A frame borrows a nested container's lines while another goroutine renders
// that container with Render. Render must neither write into the array the
// frame reads nor hand that array to its caller (go test -race).
func TestFrameBorrowIsSafeFromConcurrentContainerRender(t *testing.T) {
	ui := NewWithOutput(io.Discard, 40, 10)
	ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	chat, head := concurrencyChat()
	ui.Add(chat)
	ui.Render()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			head.set(fmt.Sprintf("head outside %d", i))
			checkConcurrencyLines(t, chat.Render(40))
		}
	})
	for i := range 1000 {
		head.set(fmt.Sprintf("head frame %d", i))
		ui.Render()
	}
	close(done)
	wg.Wait()
	head.set("head last")
	ui.Render()
	got := ui.RenderSnapshot(40)
	if want := append([]string{"head last"}, chat.Render(40)[1:]...); !slices.Equal(got, want) {
		t.Fatalf("frame lines after concurrent renders = %q, want %q", got, want)
	}
}
