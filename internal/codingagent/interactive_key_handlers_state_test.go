//go:build unix

package codingagent

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's onEscape (interactive-mode.ts:3050-3076) takes the first of five branches: a streaming run, a running `!` command, bash mode, a blank editor
// with a double-Escape action configured. handleCtrlC (4229-4236) clears, then exits on a second press within 500 ms.
type escapeWorld struct {
	Editor          string
	RunAborted      bool
	RetryAborted    bool
	CompactionAbort bool
	BashCancelled   bool
	Slash           []string
	EscapeArmed     bool
	Exit            bool
	SigintArmed     bool
	QueuedMessages  int
}

func (r *keyRig) escapeWorld(abortCtx context.Context) escapeWorld {
	m := r.m
	handle := m.opts.SessionHandle.(*recordingCompactHandle)
	return escapeWorld{
		Editor:          m.editor.Text(),
		RunAborted:      abortCtx.Err() != nil,
		RetryAborted:    handle.abortRetryCount > 0,
		CompactionAbort: handle.abortCompactionCount > 0,
		BashCancelled:   r.bashCancels > 0,
		Slash:           r.world.Slash,
		EscapeArmed:     !m.lastEscapeTime.IsZero(),
		Exit:            m.requestExit.Load(),
		SigintArmed:     !m.lastSigintTime.IsZero(),
		QueuedMessages:  len(m.compactionQueue),
	}
}

func TestSetupKeyHandlersEscapeAndClearBranches(t *testing.T) {
	const window = 500 * time.Millisecond
	cases := []struct {
		name   string
		action string // the double-Escape setting; empty keeps the default
		setup  func(r *keyRig)
		keys   []string
		want   escapeWorld
	}{
		{name: "a streaming run restores the queue and aborts before anything else", keys: []string{"\x1b"},
			setup: func(r *keyRig) {
				r.m.isIdle = false
				r.m.turnActive.Store(true)
				r.m.compactionQueue = []compactionQueuedMessage{{text: "queued", mode: compactionQueueSteer}}
				r.m.bashCancel = func() { r.bashCancels++ }
				r.m.editor.SetText("!draft")
			},
			want: escapeWorld{Editor: "queued\n\n!draft", RunAborted: true}},
		{name: "a retry countdown cancels only the retry", keys: []string{"\x1b"},
			setup: func(r *keyRig) {
				r.m.isIdle = false
				r.m.turnActive.Store(true)
				r.m.retryCountdownStop = func() {}
				r.m.compactionQueue = []compactionQueuedMessage{{text: "queued", mode: compactionQueueSteer}}
			},
			want: escapeWorld{RetryAborted: true, QueuedMessages: 1}},
		{name: "a running bash command is aborted and the editor kept", keys: []string{"\x1b"},
			setup: func(r *keyRig) {
				r.m.isIdle = true
				r.m.bashCancel = func() { r.bashCancels++ }
				r.m.editor.SetText("!sleep 9")
			},
			want: escapeWorld{Editor: "!sleep 9", BashCancelled: true}},
		{name: "bash mode clears the editor and does not arm the double Escape", keys: []string{"\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.editor.SetText(" \n!ls") },
			want:  escapeWorld{}},
		{name: "text keeps its editor and does not arm the double Escape", keys: []string{"\x1b", "\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.editor.SetText("draft") },
			want:  escapeWorld{Editor: "draft"}},
		{name: "a blank editor arms on the first Escape", keys: []string{"\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.editor.SetText("   ") },
			want:  escapeWorld{Editor: "   ", EscapeArmed: true}},
		{name: "the second Escape opens the tree", keys: []string{"\x1b", "\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true },
			want:  escapeWorld{Slash: []string{"/tree"}}},
		{name: "the second Escape opens the fork selector when configured", action: "fork", keys: []string{"\x1b", "\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true },
			want:  escapeWorld{Slash: []string{"/fork"}}},
		{name: "none neither arms nor opens anything", action: "none", keys: []string{"\x1b", "\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true },
			want:  escapeWorld{}},
		{name: "an Escape 400 ms after the first still opens the tree", keys: []string{"\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.lastEscapeTime = time.Now().Add(-400 * time.Millisecond) },
			want:  escapeWorld{Slash: []string{"/tree"}}},
		{name: "an Escape 600 ms after the first only arms again", keys: []string{"\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.lastEscapeTime = time.Now().Add(-2 * window) },
			want:  escapeWorld{EscapeArmed: true}},
		{name: "a second Ctrl+C within the window exits", keys: []string{"\x03", "\x03"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.editor.SetText("draft") },
			want:  escapeWorld{Exit: true, SigintArmed: false}},
		{name: "a Ctrl+C 400 ms after the first exits", keys: []string{"\x03"},
			setup: func(r *keyRig) { r.m.lastSigintTime = time.Now().Add(-400 * time.Millisecond) },
			want:  escapeWorld{Exit: true}},
		{name: "a Ctrl+C 600 ms after the first clears and arms again", keys: []string{"\x03"},
			setup: func(r *keyRig) {
				r.m.lastSigintTime = time.Now().Add(-2 * window)
				r.m.editor.SetText("draft")
			},
			want: escapeWorld{SigintArmed: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newKeyRig(t, nil)
			if c.action != "" {
				if err := r.m.opts.SettingsManager.SetDoubleEscapeAction(c.action); err != nil {
					t.Fatal(err)
				}
			}
			c.setup(r)
			abortCtx := r.m.abortCtx
			for _, key := range c.keys {
				r.press(t, key)
			}
			got := r.escapeWorld(abortCtx)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("world %+v, want %+v", got, c.want)
			}
		})
	}
}

// app.model.select (interactive-mode.ts:3085) opens the model selector, which owns input until it closes.
func TestSetupKeyHandlersModelSelectOpensTheSelectorForItsDefaultAndRemappedKey(t *testing.T) {
	id, wire := remapKey('t')
	for _, c := range []struct {
		name     string
		bindings map[string]string
		key      string
		opens    bool
	}{
		{"default key", nil, "\x0c", true},
		{"remapped key", map[string]string{"app.model.select": id}, wire, true},
		{"default key after the remap", map[string]string{"app.model.select": id}, "\x0c", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newKeyRig(t, c.bindings)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = r.m.dispatchKey(r.m.runCtx, c.key)
			}()
			// The selector's input loop runs this task, so it has the selector shown and focused, and owns the modal input channel.
			type shown struct{ selector, input bool }
			started := make(chan shown, 1)
			r.m.uiTaskCh <- func() {
				_, selector := r.m.tuiInst.GetFocusedComponent().(*tui.ModelSelectorComponent)
				input, _ := r.m.modalRoute()
				started <- shown{selector, input != nil}
			}
			select {
			case got := <-started:
				if !c.opens {
					t.Fatalf("the key opened the selector: %+v", got)
				}
				if !got.selector || !got.input {
					t.Errorf("selector focused %v, owns input %v; want both", got.selector, got.input)
				}
				input, _ := r.m.modalRoute()
				input <- []byte("\x1b")
				<-done
			case <-done:
				if c.opens {
					t.Fatal("the key did not open the model selector")
				}
			}
			if w := r.snapshot(); !reflect.DeepEqual(w, keyWorld{ThinkingLevel: "off"}) {
				t.Errorf("world %+v, want nothing else changed", w)
			}
		})
	}
}
