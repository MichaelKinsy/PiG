package codingagent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Both real owner loops must process input, progress, and completion independently. Inspection is itself marshalled to that loop; no worker reads or mutates UI state.
func TestSessionSelectorAsyncOwnerLoops(t *testing.T) {
	for _, mode := range []string{"startup", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			started, publish, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
			current, all := []SessionInfo{scopeSession("current")}, []SessionInfo{scopeSession("all")}
			s := newSessionSelectorWithLoaders(resolvedSessionLoader(func() ([]SessionInfo, error) { return current, nil }), func(load *sessionLoad, progress SessionListProgress) <-chan sessionLoadResult {
				return load.run(func(ctx context.Context) ([]SessionInfo, error) {
					close(started)
					select {
					case <-publish:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					load.post(func() { progress(1, 2, all) })
					select {
					case <-finish:
						return all, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				})
			}, nil, nil, "", sessionSelectorInputBindings(t))
			ui := tui.NewWithOutput(io.Discard, 120, 30)
			done := make(chan error, 1)
			var send func(string)
			if mode == "startup" {
				terminal := &fakeStartupTerminal{}
				send = func(input string) { terminal.send(t, input) }
				go func() {
					completed, err := runStartupComponentWith(s, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, ui, terminal)
					if err == nil && !completed {
						err = fmt.Errorf("startup loop did not complete")
					}
					done <- err
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
					_, selected := m.runEditorSlotSessionSelector(s)
					if selected {
						done <- fmt.Errorf("cancel selected a session")
					} else {
						done <- nil
					}
				}()
			}
			t.Cleanup(func() {
				send("\x1b")
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("owner loop did not cancel and join its work")
				}
			})
			send("\t")
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("All load did not start")
			}
			send("x")
			inspectSessionSelectorUntil(t, s, func() bool { return s.searchInput.Text() == "x" && s.loading })
			// The query is cleared before progress so the partial item is observable.
			send("\x7f")
			close(publish)
			inspectSessionSelectorUntil(t, s, func() bool {
				return s.searchInput.Text() == "" && len(s.filtered) == 1 && s.filtered[0].Session.ID == "all" && strings.Contains(strings.Join(s.Render(120), "\n"), "Loading 1/2")
			})
			send("\t")
			inspectSessionSelectorUntil(t, s, func() bool {
				return s.scope == sessionScopeCurrent && len(s.filtered) == 1 && s.filtered[0].Session.ID == "current"
			})
			close(finish)
			inspectSessionSelectorUntil(t, s, func() bool {
				return s.allLoad == nil && len(s.all) == 1 && s.scope == sessionScopeCurrent && s.filtered[0].Session.ID == "current"
			})
		})
	}
}

func inspectSessionSelectorUntil(t *testing.T, s *sessionSelector, condition func() bool) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		result := make(chan bool, 1)
		select {
		case s.work.updates <- func() { result <- condition() }:
		case <-timer.C:
			t.Fatal("owner loop stopped processing updates")
		}
		select {
		case ok := <-result:
			if ok {
				return
			}
		case <-timer.C:
			t.Fatal("owner loop did not reach expected state")
		}
	}
}
