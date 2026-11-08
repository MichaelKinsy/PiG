package desktop_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/desktop"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell/fakeshell"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// fakeContent is native content that records what the app does to it.
type fakeContent struct {
	mu        sync.Mutex
	attached  bool
	detached  bool
	host      *surfacehost.Host
	opened    []string
	redraws   atomic.Int32
	startedCh chan struct{}
}

func newFakeContent() *fakeContent { return &fakeContent{startedCh: make(chan struct{})} }

func (f *fakeContent) Attach(func()) { f.mu.Lock(); f.attached = true; f.mu.Unlock() }
func (f *fakeContent) Detach()       { f.mu.Lock(); f.detached = true; f.mu.Unlock() }
func (f *fakeContent) Invalidate()   { f.redraws.Add(1) }
func (f *fakeContent) Start(h *surfacehost.Host) error {
	f.mu.Lock()
	f.host = h
	f.mu.Unlock()
	close(f.startedCh)
	return nil
}
func (f *fakeContent) OpenSession(id string) {
	f.mu.Lock()
	f.opened = append(f.opened, id)
	f.mu.Unlock()
}

func TestNativeWindowShowsContentOnASurfaceHostOfTheServer(t *testing.T) {
	content := newFakeContent()
	r := start(t, func(cfg *desktop.Config, _ *fakeshell.Platform) { cfg.Native = content })
	select {
	case <-content.startedCh:
	case <-time.After(30 * time.Second):
		t.Fatal("the content was never started on a surface host")
	}
	win := r.window(t)
	if win.Spec.Content == nil || win.Spec.URL != "" {
		t.Fatalf("a native window has content and no page: %+v", win.Spec)
	}
	content.mu.Lock()
	attached, h := content.attached, content.host
	content.mu.Unlock()
	if !attached {
		t.Fatal("the content was not attached to the window")
	}
	// The host is the third surface's: connected to the server, its theme PiG's.
	deadline := time.Now().Add(20 * time.Second)
	for h.Connection() != surfaces.Connected || h.Theme().ID != "pig.dark" {
		if time.Now().After(deadline) {
			t.Fatalf("connection %v theme %q", h.Connection(), h.Theme().ID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A deep link selects the session in the native content, and loads no page.
	r.shell.Handlers.OpenURL("pig://session/0b1c2d3e-4f50-4162-8394-a5b6c7d8e9f0")
	deadline = time.Now().Add(10 * time.Second)
	for {
		content.mu.Lock()
		opened := append([]string(nil), content.opened...)
		content.mu.Unlock()
		if len(opened) == 1 && opened[0] == "0b1c2d3e-4f50-4162-8394-a5b6c7d8e9f0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("opened %v", opened)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(win.Loads) != 0 {
		t.Fatalf("the native window loaded pages: %v", win.Loads)
	}
	r.stop(t)
	content.mu.Lock()
	defer content.mu.Unlock()
	if !content.detached {
		t.Fatal("the content was not detached at quit")
	}
}
