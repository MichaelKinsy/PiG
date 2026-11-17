package codingagent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// heldHookTerminal is a terminal that the renderer writes from one goroutine and the held hook reads from another.
type heldHookTerminal struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *heldHookTerminal) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *heldHookTerminal) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// longForkMode is a saved session of 16 pairs, far longer than the 30-row terminal, painted once on a recording terminal as the first frame of a TUI that started below
// the header row. It returns the mode, the terminal, and the id of the last user message, which /fork forks from.
func longForkMode(t *testing.T) (*InteractiveMode, *heldHookTerminal, string) {
	t.Helper()
	m := sessionChromeMode(t)
	var lastUser string
	for i := 1; i <= 16; i++ {
		id, err := m.currentSession().AppendMessage(userMsg(fmt.Sprintf("LONG-Q%d question", i)))
		if err != nil {
			t.Fatal(err)
		}
		lastUser = id
		if _, err := m.currentSession().AppendMessage(assistantMsg("", ai.TextContent{Text: fmt.Sprintf("LONG-A%d answer", i)})); err != nil {
			t.Fatal(err)
		}
	}
	terminal := &heldHookTerminal{}
	ui := tui.NewWithOutput(terminal, 80, 30)
	ui.SetRenderDeadline(func(time.Time) {}) // the test owns the frames
	m.tuiInst = ui
	m.editorContainer = tui.NewContainer(m.editor)
	ui.Add(tui.NewText(scrollbackHeaderRow))
	ui.Add(m.chatContainer)
	ui.Add(m.statusLine)
	ui.Add(m.editorContainer)
	m.renderSessionEntries()
	ui.Render()
	if !strings.Contains(terminal.String(), "LONG-A16 answer") {
		t.Fatalf("the first frame lacks the saved transcript: %q", terminal.String())
	}
	return m, terminal, lastUser
}

// A /fork of a long transcript whose session_before_fork handler is held open, the owner loop live as in the program (the loop serves terminal input while the swap is
// awaited, agent-session-runtime.ts fork): (i) nothing typed, (ii) a key typed while the hook is held, (iii) the hook returning at once. In none of them may the terminal be
// sent the old transcript twice before the fork's own frames, or the new transcript twice: the transcript rows did not change between the frame the closed picker asked for and the
// swap, so the only frame that carries them is the one Pi paints inside its await.
func TestForkOfALongTranscriptWithAHeldBeforeHook(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typed string
		hold  bool
	}{
		{"nothing typed", "", true},
		{"a key typed while held", "Z7", true},
		{"released at once", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, terminal, lastUser := longForkMode(t)
			const selectorRow = "SELECTOR ROW OF THE FORK PICKER"
			// Pi's picker is as tall as the pane: it scrolls the viewport, so closing it puts the first changed row above the viewport top and Pi paints a clearing frame.
			selector := tui.NewContainer()
			for range 32 {
				selector.Add(tui.NewText(selectorRow))
			}
			m.editorContainer.SetChildren(selector)
			m.tuiInst.Render()
			m.editorContainer.SetChildren(m.editor)
			m.tuiInst.RequestRender() // the closed picker's request: the test's terminal does not throttle-paint it
			before := terminal.String()

			ctx, cancel := context.WithCancel(context.Background())
			m.runCtx = ctx
			reader, writer := io.Pipe()
			m.startTerminalInput(ctx, reader)
			t.Cleanup(func() {
				_ = writer.Close()
				cancel()
				_ = m.stopTerminalInput()
				m.backgroundTasks.Wait()
			})

			entered, release := make(chan struct{}), make(chan struct{})
			if !tc.hold {
				close(release)
			}
			var atHook string
			handle := m.opts.SessionHandle.(*beforeUIHandle)
			handle.runner = inproc.NewRunner([]extension.Extension{{Path: "held", Handlers: map[string][]extension.HandlerFn{
				EventSessionBeforeFork: {func(...any) (any, error) {
					atHook = terminal.String()
					close(entered)
					<-release
					return nil, nil
				}},
			}}}, m.opts.CWD)

			// The key and then a barrier key the loop consumes itself: when the listener sees the barrier, the editor has handled the key and the loop is idle, so a frame can be painted here.
			const barrier = "\x1d"
			barrierSeen := make(chan struct{}, 1)
			m.addKeyPressListener(func(data string) bool {
				if data != barrier {
					return false
				}
				barrierSeen <- struct{}{}
				return true
			})
			var heldWrites string
			var typedSeen bool
			driven := make(chan struct{})
			go func() {
				defer close(driven)
				if !tc.hold {
					return
				}
				<-entered
				if tc.typed != "" {
					_, _ = io.WriteString(writer, tc.typed)
					_, _ = io.WriteString(writer, barrier)
					select {
					case <-barrierSeen:
						typedSeen = true
						m.tuiInst.Render()
					case <-time.After(10 * time.Second):
					}
				}
				heldWrites = terminal.String()
				close(release)
			}()

			sc := m.buildSlashContext(ctx)
			sc.Args = lastUser
			if err := forkHandler(sc); err != nil {
				t.Fatal(err)
			}
			<-driven
			m.tuiInst.Render()
			if tc.hold && tc.typed != "" && !typedSeen {
				t.Fatal("the loop did not see the keys typed while the hook was held")
			}
			if atHook == "" {
				t.Fatal("no frame was written before the session_before_fork handler ran")
			}
			all := terminal.String()
			if !strings.HasPrefix(all, before) || !strings.HasPrefix(all, atHook) {
				t.Fatalf("the terminal's bytes are not an append-only record")
			}
			frameA := strings.TrimPrefix(atHook, before)
			held := ""
			if heldWrites != "" {
				held = strings.TrimPrefix(heldWrites, atHook)
			}
			swap := strings.TrimPrefix(all, atHook+held)
			t.Logf("frame A: %d bytes, 2J=%d 3J=%d, rows of the old transcript=%d, selector row=%v\nwhile held: %d bytes, 2J=%d 3J=%d\nswap: %d bytes, 2J=%d 3J=%d, LONG-Q1 rows=%d, status=%v",
				len(frameA), strings.Count(frameA, "\x1b[2J"), strings.Count(frameA, "\x1b[3J"), strings.Count(frameA, "LONG-A"), strings.Contains(frameA, selectorRow),
				len(held), strings.Count(held, "\x1b[2J"), strings.Count(held, "\x1b[3J"),
				len(swap), strings.Count(swap, "\x1b[2J"), strings.Count(swap, "\x1b[3J"), strings.Count(swap, "LONG-Q1 question"), strings.Contains(swap, "Forked to new session"))
			// Pi's frames for this fork (the comparator's raw stream, tmux 100x35): the frame the closed picker asked for is a clearing frame holding the old transcript once, nothing is
			// written while the hook is held, and the swap's frame is a clearing frame holding the new transcript once.
			if strings.Contains(frameA, selectorRow) || strings.Contains(frameA, "Forked to new session") {
				t.Errorf("the frame before the swap is not the old transcript with the picker gone:\n%q", frameA)
			}
			if strings.Count(frameA, "\x1b[2J") != 1 || strings.Count(frameA, "\x1b[3J") != 1 || strings.Count(frameA, "LONG-Q1 question") != 1 || strings.Count(frameA, "LONG-A16 answer") != 1 {
				t.Errorf("frame A is not one clearing frame that holds the old transcript once:\n%q", frameA)
			}
			if tc.typed == "" && held != "" {
				t.Errorf("bytes were written while the hook was held and nothing was typed:\n%q", held)
			}
			if tc.typed != "" {
				if !strings.Contains(held, tc.typed) {
					t.Errorf("the frame after the typed key lacks it:\n%q", held)
				}
				if strings.Contains(held, "\x1b[2J") || strings.Contains(held, "\x1b[3J") || strings.Contains(held, "LONG-Q1 question") {
					t.Errorf("a typed key repainted the transcript while the hook was held:\n%q", held)
				}
			}
			if !strings.Contains(swap, "Forked to new session") {
				t.Errorf("the swap's frames lack the status:\n%q", swap)
			}
			if strings.Count(swap, "\x1b[2J") != 1 || strings.Count(swap, "\x1b[3J") != 1 || strings.Count(swap, "LONG-Q1 question") != 1 {
				t.Errorf("the swap did not paint the new transcript once in one clearing frame:\n%q", swap)
			}
		})
	}
}
