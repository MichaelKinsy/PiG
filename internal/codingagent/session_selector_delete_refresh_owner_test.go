package codingagent

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestSessionSelectorDeleteRefreshOwnerDoesNotResumeDeletedPath(t *testing.T) {
	for _, owner := range []string{"startup", "interactive"} {
		t.Run(owner, func(t *testing.T) {
			victim, survivor := scopeSession("deleted"), scopeSession("survivor")
			victim.Name, survivor.Name = "delete-target", "survivor"
			rows := []SessionInfo{victim, survivor}
			pending := make(chan sessionLoadResult, 1)
			refreshStarted := make(chan struct{})
			deleted := false
			loader := func(_ *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
				if deleted {
					close(refreshStarted)
					return pending
				}
				ready := make(chan sessionLoadResult, 1)
				ready <- sessionLoadResult{sessions: rows}
				return ready
			}
			s := newSessionSelectorWithLoaders(loader, loader, nil, unlinkDeleter(func(path string) error {
				if path != victim.Path {
					return fmt.Errorf("unexpected deletion %q", path)
				}
				deleted = true
				return nil
			}), "", sessionSelectorInputBindings(t))
			ui := tui.NewWithOutput(io.Discard, 120, 30)
			type result struct {
				path string
				err  error
			}
			done := make(chan result, 1)
			var send func(string)
			if owner == "startup" {
				terminal := &fakeStartupTerminal{}
				send = func(input string) { terminal.send(t, input) }
				go func() {
					completed, err := runStartupComponentWith(s, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, ui, terminal)
					if err == nil && !completed {
						err = fmt.Errorf("startup owner did not complete")
					}
					done <- result{s.SelectedPath(), err}
				}()
			} else {
				editor := tui.NewEditor()
				container := tui.NewContainer(editor)
				m := &InteractiveMode{tuiInst: ui, editor: editor, editorContainer: container, layout: tui.NewContainer(container)}
				ui.Add(m.layout)
				input := make(chan []byte, 4)
				m.setModalInputChannel(input)
				send = func(data string) { input <- []byte(data) }
				go func() {
					path, ok := m.runEditorSlotSessionSelector(s)
					var err error
					if !ok {
						err = fmt.Errorf("interactive owner did not select")
					}
					done <- result{path, err}
				}()
			}
			joined := false
			t.Cleanup(func() {
				if joined {
					return
				}
				pending <- sessionLoadResult{sessions: []SessionInfo{survivor}}
				send("\x1b")
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("selector owner did not stop")
				}
			})
			send("\x04")
			send("\r")
			select {
			case <-refreshStarted:
			case r := <-done:
				joined = true
				t.Fatalf("selector ended before refresh: %+v", r)
			case <-time.After(5 * time.Second):
				t.Fatal("deletion did not start refresh")
			}
			// The promise deliberately stays unresolved while the real input owner handles Enter.
			send("\r")
			select {
			case r := <-done:
				joined = true
				if r.err != nil || r.path != survivor.Path {
					t.Fatalf("resumed path=%q, error=%v; want %q", r.path, r.err, survivor.Path)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Enter waited for refresh instead of selecting the surviving row")
			}
		})
	}
}
