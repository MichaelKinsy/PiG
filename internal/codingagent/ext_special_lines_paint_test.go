package codingagent

import (
	"bytes"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
	"github.com/MichaelKinsy/PiG/tui"
)

// renderingGoroutines is a component that records which goroutine renders it.
type renderingGoroutines struct {
	mu  sync.Mutex
	ids []uint64
}

func (r *renderingGoroutines) Render(int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, goroutineid.Current())
	return []string{"transcript"}
}

func (*renderingGoroutines) Invalidate() {}

func (r *renderingGoroutines) take() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ids)
}

// An extension process sets its footer from an extension host goroutine while the owner loop mutates the component tree (Pi runs both on one event loop). Only the owner loop renders the tree: the host goroutine's change reaches the screen through a frame the loop paints, and the owner loop's own change paints at once.
func TestExtensionFooterFromAHostGoroutineIsPaintedByTheOwnerLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
		m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), Model: model})
		var out bytes.Buffer
		m.tuiInst = tui.NewWithOutput(&out, 80, 24)
		transcript := &renderingGoroutines{}
		m.chatContainer = tui.NewContainer(transcript)
		m.extFooter = newSpecialLinesComponent(m.repaintSpecialLines)
		m.tuiInst.Add(m.chatContainer)
		m.tuiInst.Add(m.extFooter)
		m.installRenderDispatcher()
		defer m.enterOwnerLoop()()
		owner := goroutineid.Current()
		m.tuiInst.Render()
		ui := &ExtUIContext{m: m}

		// The set arrives on the host's goroutine, as a call lane runs ui.set_footer.
		out.Reset()
		go ui.SetFooter(extension.FrameFooter([]string{"HOST FOOTER"}, 0))
		synctest.Wait()
		if ids := transcript.take(); slices.ContainsFunc(ids, func(id uint64) bool { return id != owner }) {
			t.Fatalf("the transcript was rendered on goroutines %v, want only the owner loop %d", ids, owner)
		}
		time.Sleep(time.Second) // past the 16ms frame throttle
		synctest.Wait()
		m.drainMainLoopOnce()
		if !strings.Contains(out.String(), "HOST FOOTER") {
			t.Fatalf("the owner loop never painted the footer a host goroutine set: %q", out.String())
		}
		if ids := transcript.take(); slices.ContainsFunc(ids, func(id uint64) bool { return id != owner }) {
			t.Fatalf("the transcript was rendered on goroutines %v, want only the owner loop %d", ids, owner)
		}

		// The owner loop's own set paints before it returns.
		out.Reset()
		ui.SetFooter(extension.FrameFooter([]string{"LOOP FOOTER"}, 0))
		if !strings.Contains(out.String(), "LOOP FOOTER") {
			t.Fatalf("the owner loop's footer change was not painted at once: %q", out.String())
		}
	})
}
