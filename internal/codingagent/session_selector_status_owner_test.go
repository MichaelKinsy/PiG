package codingagent

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type selectorStatusOutput struct {
	mu         sync.Mutex
	text       strings.Builder
	errorSeen  bool
	errorReady chan struct{}
	hintsReady chan struct{}
	errorOnce  sync.Once
	hintsOnce  sync.Once
}

func (out *selectorStatusOutput) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	out.text.Write(p)
	if !out.errorSeen && strings.Contains(out.text.String(), "Failed to load sessions: owner-load-error") {
		out.errorSeen = true
		out.text.Reset()
		out.errorOnce.Do(func() { close(out.errorReady) })
	} else if out.errorSeen && strings.Contains(out.text.String(), "regex") {
		out.hintsOnce.Do(func() { close(out.hintsReady) })
	}
	return len(p), nil
}

type selectorStatusTerminal struct {
	input chan func([]byte)
	fail  chan func(error)
}

func (terminal *selectorStatusTerminal) StartWithReadError(input func([]byte), _ func(), fail func(error)) error {
	terminal.input <- input
	if terminal.fail != nil {
		terminal.fail <- fail
	}
	return nil
}
func (*selectorStatusTerminal) Stop()        {}
func (*selectorStatusTerminal) Write(string) {}

func TestSessionSelectorStatusExpiryWakesIdleOwners(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		all        bool
	}{{"startup/current", "startup", false}, {"startup/all", "startup", true}, {"interactive/current", "interactive", false}, {"interactive/all", "interactive", true}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				failed := func() ([]SessionInfo, error) { return nil, errors.New("owner-load-error") }
				current := failed
				if tc.all {
					current = func() ([]SessionInfo, error) { return nil, nil }
				}
				selector := newStatusTestSelector(current, failed)
				out := &selectorStatusOutput{errorReady: make(chan struct{}), hintsReady: make(chan struct{})}
				ui := tui.NewWithOutput(out, 120, 30)
				defer ui.Stop()
				done := make(chan error, 1)
				var send func(string)
				if tc.mode == "startup" {
					terminal := &selectorStatusTerminal{input: make(chan func([]byte), 1)}
					go func() {
						_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, ui, terminal, nil)
						done <- err
					}()
					input := <-terminal.input
					send = func(data string) { input([]byte(data)) }
				} else {
					editor := tui.NewEditor()
					container := tui.NewContainer(editor)
					mode := &InteractiveMode{tuiInst: ui, editor: editor, editorContainer: container, layout: tui.NewContainer(container)}
					ui.Add(mode.layout)
					input := make(chan []byte, 1)
					mode.setModalInputChannel(input)
					send = func(data string) { input <- []byte(data) }
					go func() { mode.runEditorSlotSessionSelector(selector); done <- nil }()
				}
				defer func() {
					send("\x1b")
					if err := <-done; err != nil {
						t.Error(err)
					}
				}()
				if tc.all {
					send("\t")
				}
				<-out.errorReady
				time.Sleep(3999 * time.Millisecond)
				synctest.Wait()
				select {
				case <-out.hintsReady:
					t.Error("owner cleared the load error before 4000ms")
				default:
				}
				time.Sleep(time.Millisecond)
				synctest.Wait()
				select {
				case <-out.hintsReady:
				default:
					t.Error("idle owner did not redraw navigation hints at 4000ms")
				}
			})
		})
	}
}

// Pi's void input callback does not swallow rename rejection. The interactive owner raises it to Run's uncaughtException handler; the startup owner, whose Pi picker has no rename, returns it. Neither leaves a permanent status behind.
func TestSessionSelectorRenameFailureReachesOwners(t *testing.T) {
	for _, kind := range []string{"startup", "interactive"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				denied := errors.New("rename denied")
				selector := newStatusTestSelector(func() ([]SessionInfo, error) {
					return []SessionInfo{{Path: "rename.jsonl", ID: "rename", Name: "Old"}}, nil
				})
				selector.renameSession = func(string, string) error { return denied }
				selector.enterRenameMode()
				selector.renameInput.SetText("New")
				ui := tui.NewWithOutput(io.Discard, 120, 30)
				defer ui.Stop()
				done := make(chan error, 1)
				var send func(string)
				if kind == "startup" {
					terminal := &selectorStatusTerminal{input: make(chan func([]byte), 1)}
					go func() {
						_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, ui, terminal, nil)
						done <- err
					}()
					input := <-terminal.input
					send = func(value string) { input([]byte(value)) }
				} else {
					editor := tui.NewEditor()
					container := tui.NewContainer(editor)
					mode := &InteractiveMode{tuiInst: ui, editor: editor, editorContainer: container, layout: tui.NewContainer(container)}
					ui.Add(mode.layout)
					input := make(chan []byte, 1)
					mode.setModalInputChannel(input)
					send = func(value string) { input <- []byte(value) }
					// Pi's void confirmRename (session-selector.ts:794-796) leaves the rejection unhandled, so it reaches the uncaughtException handler (interactive-mode.ts:4266) that Run's recover mirrors.
					go func() {
						defer func() {
							value := recover()
							if raised, ok := value.(uncaughtError); ok {
								done <- raised
								return
							}
							if value != nil {
								panic(value)
							}
							done <- errors.New("rejection did not reach uncaughtException")
						}()
						mode.runEditorSlotSessionSelector(selector)
					}()
				}
				send("\r")
				synctest.Wait()
				select {
				case err := <-done:
					if kind == "interactive" {
						if _, ok := errors.AsType[uncaughtError](err); !ok {
							t.Fatalf("owner returned %T (%v), want uncaughtError", err, err)
						}
					}
					if !errors.Is(err, denied) {
						t.Fatalf("owner error=%v, want original rejection", err)
					}
				default:
					t.Error("rename rejection did not reach owner")
					send("\x1b")
					<-done
				}
			})
		})
	}
}

func TestSessionSelectorOwnerExitStopsPendingStatus(t *testing.T) {
	for _, kind := range []string{"startup", "interactive"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				selector := newStatusTestSelector(func() ([]SessionInfo, error) { return nil, errors.New("owner-load-error") })
				timer := selector.statusState.timer
				out := &selectorStatusOutput{errorReady: make(chan struct{}), hintsReady: make(chan struct{})}
				ui := tui.NewWithOutput(out, 120, 30)
				defer ui.Stop()
				done := make(chan error, 1)
				var fail func()
				if kind == "startup" {
					terminal := &selectorStatusTerminal{input: make(chan func([]byte), 1), fail: make(chan func(error), 1)}
					go func() {
						_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, ui, terminal, nil)
						done <- err
					}()
					onError := <-terminal.fail
					fail = func() { onError(io.EOF) }
				} else {
					editor := tui.NewEditor()
					container := tui.NewContainer(editor)
					mode := &InteractiveMode{tuiInst: ui, editor: editor, editorContainer: container, layout: tui.NewContainer(container), inputErrCh: make(chan error, 1)}
					ui.Add(mode.layout)
					mode.setModalInputChannel(make(chan []byte))
					fail = func() { mode.inputErrCh <- io.EOF }
					go func() { mode.runEditorSlotSessionSelector(selector); done <- nil }()
				}
				<-out.errorReady
				fail()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if selector.statusState.message != "" || selector.statusTimeout() != nil {
					t.Fatal("owner exit retained status")
				}
				time.Sleep(4 * time.Second)
				select {
				case <-timer.C:
					t.Fatal("owner exit retained a pending timer")
				default:
				}
			})
		})
	}
}
