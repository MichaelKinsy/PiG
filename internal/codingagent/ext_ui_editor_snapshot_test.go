package codingagent

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Node state snapshots read this getter on the request worker while the owner
// can accept the next key. The current editor value must remain race-free.
func TestExtensionEditorSnapshotConcurrentInput(t *testing.T) {
	m := newRunOnMainProbe(t)
	m.layout = m.chatContainer
	m.newRunner = inproc.NewRunner(nil, m.opts.CWD)
	m.wireInprocContextActions()
	ui := m.newRunner.GetUIContext()
	start := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		<-start
		for range 10000 {
			_ = ui.GetEditorText()
		}
	})
	close(start)
	for i := range 10000 {
		m.editor.SetText(strconv.Itoa(i))
	}
	readers.Wait()
	m.editor.SetText("latest editor value")
	if got := ui.GetEditorText(); got != "latest editor value" {
		t.Fatalf("snapshot=%q", got)
	}
	m.editor.Clear()
	if got := ui.GetEditorText(); got != "" {
		t.Fatalf("clear retained snapshot=%q", got)
	}
	for _, data := range []string{"abc", "\x7f", "\x1b[200~one\ntwo\x1b[201~", "\x1b[Z", "z"} {
		m.editor.HandleInput(data)
		if got, want := ui.GetEditorText(), m.editor.Text(); got != want {
			t.Fatalf("input %q: snapshot=%q editor=%q", data, got, want)
		}
	}
}

func BenchmarkExtensionEditorSnapshot(b *testing.B) {
	for _, size := range []int{0, 1024, 64 * 1024} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			m := &InteractiveMode{editor: tui.NewEditor()}
			m.bindEditorSnapshot()
			ui := &ExtUIContext{m: m}
			text := strings.Repeat("x", size)
			b.ReportAllocs()
			for b.Loop() {
				m.editor.SetText(text)
				if ui.GetEditorText() != text {
					b.Fatal("stale snapshot")
				}
			}
		})
	}
}

func TestExtensionEditorSnapshotPreservesObserverAndReplacement(t *testing.T) {
	m := newRunOnMainProbe(t)
	var changes []string
	m.editor.OnChange = func(text string) { changes = append(changes, text) }
	m.bindEditorSnapshot()
	m.bindEditorSnapshot()
	m.editor.SetText("first")
	if len(changes) != 1 || changes[0] != "first" {
		t.Fatalf("observer=%v", changes)
	}
	m.editor = tui.NewEditor()
	m.editor.SetText("replacement")
	m.bindEditorSnapshot()
	if got := (&ExtUIContext{m: m}).GetEditorText(); got != "replacement" {
		t.Fatalf("replacement snapshot=%q", got)
	}
}
