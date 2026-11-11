package codingagent

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// lockedOutput is a terminal output the test reads while a render goroutine writes it.
type lockedOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *lockedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// With a layout the model picker is the editor slot's child (showModelSelector: m.editorContainer.SetChildren(ms)), a cached child of the layout. Pi's Container
// renders every child on every frame, so the frame after a timed-out catalog refresh shows the timeout message in place of "Refreshing model catalogs…".
// TestModelPickerRefreshTimeoutAndErrors covers the layout-less overlay, which no container caches.
func TestModelPickerRefreshTimeoutInTheEditorSlotIsDrawnByTheNextFrame(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
		registry.SetModelsStore(blocked)
		var output lockedOutput
		m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir(), ModelRegistry: registry})
		m.tuiInst = tui.NewWithOutput(&output, 100, 40)
		m.installRenderDispatcher()
		m.statusLine = NewFooterComponent(nil, "", nil)
		m.chatContainer = tui.NewContainer()
		m.editor = tui.NewEditor()
		m.editorContainer = tui.NewContainer(m.editor)
		m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
		m.tuiInst.AddChild(m.layout)
		done := make(chan bool, 1)
		go func() { _, ok := m.buildSlashContext(t.Context()).PickModel(""); done <- ok }()
		<-blocked.started
		synctest.Wait()
		if !strings.Contains(output.String(), "Refreshing model catalogs") {
			t.Fatalf("the picker in the editor slot never drew its refresh status: %q", output.String())
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		if !strings.Contains(output.String(), "Model refresh timed out; showing cached models.") {
			t.Errorf("the frame after the timeout lacks its message: %q", output.String())
		}
		input, _ := m.modalRoute()
		input <- []byte("\x1b")
		<-done
	})
}
