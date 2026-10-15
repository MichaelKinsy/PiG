package codingagent

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's first confirmRename finally runs when its cancelled refresh settles, even while a second rename refresh remains pending.
func TestPairReviewCancelledRenameRefreshRunsFinally(t *testing.T) {
	entries := []SessionInfo{{Path: "session.jsonl", ID: "session", Name: "Old"}}
	calls := 0
	latest := make(chan sessionLoadResult, 1)
	loader := func(load *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
		calls++
		switch calls {
		case 1:
			ready := make(chan sessionLoadResult, 1)
			ready <- sessionLoadResult{sessions: entries}
			return ready
		case 2:
			return load.run(func(ctx context.Context) ([]SessionInfo, error) { <-ctx.Done(); return nil, ctx.Err() })
		default:
			return latest
		}
	}
	s := newSessionSelectorWithLoaders(loader, loader, func(_ string, name string) error { entries[0].Name = name; return nil }, nil, "", sessionSelectorInputBindings(t))
	t.Cleanup(s.close)
	s.drainLoadUpdates()
	s.HandleInput("\x1b[114;5u")
	s.HandleInput("\r")
	if !s.renameMode {
		t.Fatal("first pending refresh already left rename")
	}
	s.HandleInput("\r")
	if calls != 3 {
		t.Fatalf("load calls=%d", calls)
	}
	// Await the cancelled loader just as the first upstream confirmRename Promise settles. The newer refresh remains deliberately unresolved.
	s.work.jobs.Wait()
	s.drainLoadUpdates()
	if s.renameMode {
		t.Error("cancelled rename refresh lost its finally callback while replacement refresh stayed pending")
	}
	latest <- sessionLoadResult{sessions: entries}
	s.drainLoadUpdates()
}

// Cancellation requests do not settle the load Promise. The original finally waits until the cancelled load actually returns.
func TestCancelledRenameRefreshWaitsForActualSettlement(t *testing.T) {
	entries := []SessionInfo{{Path: "session.jsonl", ID: "session", Name: "Old"}}
	latest := make(chan sessionLoadResult, 1)
	cancelled, release := make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	calls := 0
	loader := func(load *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
		calls++
		if calls == 1 {
			ready := make(chan sessionLoadResult, 1)
			ready <- sessionLoadResult{sessions: entries}
			return ready
		}
		if calls == 2 {
			return load.run(func(ctx context.Context) ([]SessionInfo, error) {
				<-ctx.Done()
				close(cancelled)
				<-release
				return nil, ctx.Err()
			})
		}
		return latest
	}
	s := newSessionSelectorWithLoaders(loader, loader, func(string, string) error { return nil }, nil, "", sessionSelectorInputBindings(t))
	t.Cleanup(s.close)
	t.Cleanup(releaseOnce)
	s.drainLoadUpdates()
	s.enterRenameMode()
	if err := s.confirmRename("First"); err != nil {
		t.Fatalf("confirmRename(%q): %v", "First", err)
	}
	if err := s.confirmRename("Second"); err != nil {
		t.Fatalf("confirmRename(%q): %v", "Second", err)
	}
	<-cancelled
	s.drainLoadUpdates()
	if !s.renameMode {
		t.Error("abort request was mistaken for first refresh settlement")
	}
	releaseOnce()
	s.work.jobs.Wait()
	s.drainLoadUpdates()
	if s.renameMode || s.currentLoad == nil {
		t.Error("first settlement did not exit rename independently of the pending second refresh")
	}
	latest <- sessionLoadResult{sessions: entries}
	s.drainLoadUpdates()
}

// Both production owners must wake when an inactive refresh settles; inspection tasks do not drain completion queues for them.
func TestCancelledRenameRefreshWakesOwnerLoops(t *testing.T) {
	for _, mode := range []string{"startup", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			entries := []SessionInfo{{Path: "session.jsonl", ID: "session", Name: "Old"}}
			latest := make(chan sessionLoadResult, 1)
			calls := 0
			loader := func(load *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
				calls++
				if calls == 1 {
					ready := make(chan sessionLoadResult, 1)
					ready <- sessionLoadResult{sessions: entries}
					return ready
				}
				if calls == 2 {
					return load.run(func(ctx context.Context) ([]SessionInfo, error) { <-ctx.Done(); return nil, ctx.Err() })
				}
				return latest
			}
			s := newSessionSelectorWithLoaders(loader, loader, func(string, string) error { return nil }, nil, "", sessionSelectorInputBindings(t))
			ui := tui.NewWithOutput(io.Discard, 120, 30)
			done := make(chan error, 1)
			var send func(string)
			if mode == "startup" {
				terminal := &fakeStartupTerminal{}
				send = func(input string) { terminal.send(t, input) }
				go func() {
					_, err := runStartupComponentWith(s, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, ui, terminal)
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
					_, selected := m.runEditorSlotSessionSelector(s, hookSessionSelectorOutcome(s))
					if selected {
						done <- fmt.Errorf("cancel selected a Session")
					} else {
						done <- nil
					}
				}()
			}
			t.Cleanup(func() {
				select {
				case s.work.updates <- func() { s.exitRenameMode(); s.HandleInput("\x1b") }:
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
					return
				case <-time.After(5 * time.Second):
					t.Error("owner did not accept cleanup")
					return
				}
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("owner did not finish cleanup")
				}
			})
			send("\x1b[114;5u")
			send("\r")
			send("\r")
			inspectSessionSelectorUntil(t, s, func() bool { return calls == 3 && !s.renameMode && s.currentLoad != nil })
		})
	}
}

func TestClosedSelectorDropsRenameRefreshContinuation(t *testing.T) {
	calls := 0
	loader := func(load *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
		calls++
		if calls == 1 {
			ready := make(chan sessionLoadResult, 1)
			ready <- sessionLoadResult{sessions: []SessionInfo{{Path: "session.jsonl", ID: "session", Name: "Old"}}}
			return ready
		}
		return load.run(func(ctx context.Context) ([]SessionInfo, error) { <-ctx.Done(); return nil, ctx.Err() })
	}
	s := newSessionSelectorWithLoaders(loader, loader, func(string, string) error { return nil }, nil, "", sessionSelectorInputBindings(t))
	s.drainLoadUpdates()
	s.enterRenameMode()
	if err := s.confirmRename("New"); err != nil {
		t.Fatalf("confirmRename(%q): %v", "New", err)
	}
	s.currentLoad.complete = func() { t.Error("closed selector applied a rename continuation") }
	s.close()
	s.drainLoadUpdates()
}
