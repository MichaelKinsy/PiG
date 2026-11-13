//go:build unix

package codingagent

import (
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's onEscape (interactive-mode.ts:3050-3076) takes the first of five branches: a streaming run, a running `!` command, bash mode, a blank editor
// with a double-Escape action configured. handleCtrlC (4229-4236) clears, then exits on a second press within 500 ms.
type escapeWorld struct {
	Editor           string
	RunAborted       bool
	RetryAborted     bool
	CompactionAbort  bool
	BashCancelled    bool
	SummaryCancelled bool
	Slash            []string
	EscapeArmed      bool
	Exit             bool
	SigintArmed      bool
	QueuedMessages   int
}

func (r *keyRig) escapeWorld(abortCtx context.Context) escapeWorld {
	m := r.m
	handle := m.opts.SessionHandle.(*recordingCompactHandle)
	return escapeWorld{
		Editor:           m.editor.Text(),
		RunAborted:       abortCtx.Err() != nil,
		RetryAborted:     handle.abortRetryCount > 0,
		CompactionAbort:  handle.abortCompactionCount > 0,
		BashCancelled:    r.bashCancels > 0,
		SummaryCancelled: r.summaryCancels > 0,
		Slash:            r.world.Slash,
		EscapeArmed:      !m.lastEscapeTime.IsZero(),
		Exit:             m.requestExit.Load(),
		SigintArmed:      !m.lastSigintTime.IsZero(),
		QueuedMessages:   len(m.compactionQueue),
	}
}

func TestSetupKeyHandlersEscapeAndClearBranches(t *testing.T) {
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
				r.m.retryEscapeHandler = true
				r.m.compactionQueue = []compactionQueuedMessage{{text: "queued", mode: compactionQueueSteer}}
			},
			want: escapeWorld{RetryAborted: true, QueuedMessages: 1}},
		{name: "a running bash command waits for the retry countdown's own Escape", keys: []string{"\x1b"},
			setup: func(r *keyRig) {
				r.m.isIdle = false
				r.m.retryCountdownStop = func() {}
				r.m.retryEscapeHandler = true
				r.m.bashCancel = func() { r.bashCancels++ }
			},
			want: escapeWorld{RetryAborted: true}},
		{name: "compaction aborts the compaction", keys: []string{"\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.isCompacting = true },
			want:  escapeWorld{CompactionAbort: true}},
		{name: "a branch summary cancels the summary instead of the compaction", keys: []string{"\x1b"},
			setup: func(r *keyRig) {
				r.m.isIdle = true
				r.m.isCompacting = true
				r.m.branchSummaryCancel = func() { r.summaryCancels++ }
			},
			want: escapeWorld{SummaryCancelled: true}},
		{name: "without a settings manager the double Escape opens the tree", keys: []string{"\x1b", "\x1b"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.opts.SettingsManager = nil },
			want:  escapeWorld{Slash: []string{"/tree"}}},
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
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.lastEscapeTime = time.Now().Add(-600 * time.Millisecond) },
			want:  escapeWorld{EscapeArmed: true}},
		{name: "a second Ctrl+C within the window exits", keys: []string{"\x03", "\x03"},
			setup: func(r *keyRig) { r.m.isIdle = true; r.m.editor.SetText("draft") },
			want:  escapeWorld{Exit: true, SigintArmed: false}},
		{name: "a Ctrl+C 400 ms after the first exits", keys: []string{"\x03"},
			setup: func(r *keyRig) { r.m.lastSigintTime = time.Now().Add(-400 * time.Millisecond) },
			want:  escapeWorld{Exit: true}},
		{name: "a Ctrl+C 600 ms after the first clears and arms again", keys: []string{"\x03"},
			setup: func(r *keyRig) {
				r.m.lastSigintTime = time.Now().Add(-600 * time.Millisecond)
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

// Pi's CustomEditor.handleInput runs the first registered handler whose binding matches (custom-editor.ts:139-144, a Map in registration order), and
// setupKeyHandlers registers the sixteen actions in the order of keyHandlerCases, so a key bound to two actions runs the earlier one, and a registered
// action beats an action the handlers do not register (app.thinking.save, app.session.toggleNamedFilter).
func TestSetupKeyHandlersRegistrationOrderDecidesOverlappingBindings(t *testing.T) {
	var registered []keyHandlerCase
	for _, c := range keyHandlerCases {
		switch c.action {
		case "app.interrupt", "app.exit", "app.clipboard.pasteImage":
			// CustomEditor checks these three before the registered actions.
		default:
			registered = append(registered, c)
		}
	}
	_, wire := remapKey('z')
	for i, first := range registered {
		later := []string{"app.thinking.save", "app.session.toggleNamedFilter"}
		for _, c := range registered[i+1:] {
			later = append(later, c.action)
		}
		for _, other := range later {
			t.Run(first.action+" over "+other, func(t *testing.T) {
				id, _ := remapKey('z')
				before, after := pressCase(t, first, map[string]string{first.action: id, other: id}, wire)
				expected := before
				first.want(&expected)
				if !reflect.DeepEqual(after, expected) {
					t.Errorf("world %+v, want the earlier action's %+v", after, expected)
				}
			})
		}
	}
}

// The model selector refreshes the model catalogs on goroutines the mode does not join, and the refresh outlives the key that opened the selector. A registry
// backed by the rig's agent directory creates auth.json and models-store.json there, and the refresh still running recreates them while the test's cleanup removes
// the directory: "TempDir RemoveAll cleanup: unlinkat ...: directory not empty" in 3 of 120 runs of
// TestSetupKeyHandlersRegistrationOrderDecidesOverlappingBindings on a loaded machine, always in a subtest of app.model.select. The rig keeps the registry in
// memory, so no refresh can write a directory a cleanup removes.
func TestKeyRigModelSelectorTouchesNoFileInTheAgentDirectory(t *testing.T) {
	r := newKeyRig(t, nil)
	r.press(t, "\x0c")
	if !r.world.ModelSelector {
		t.Fatal("the default model selector key did not open the selector")
	}
	entries, err := os.ReadDir(r.m.opts.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Errorf("opening the model selector left %q in the rig's agent directory", entry.Name())
	}
}

// handleDequeue (interactive-mode.ts:4467-4474) restores every queued message to the editor and says how many, or that none were queued.
func TestSetupKeyHandlersDequeueReportsTheRestoredCount(t *testing.T) {
	steer, followUp := compactionQueueSteer, compactionQueueFollowUp
	type queued struct {
		text string
		mode compactionQueueMode
	}
	for _, c := range []struct {
		queued     []queued
		editor     string
		wantEditor string
		wantStatus string
	}{
		{nil, "", "", "No queued messages to restore"},
		{nil, "keep", "keep", "No queued messages to restore"},
		{[]queued{{"one", steer}}, "", "one", "Restored 1 queued message to editor"},
		{[]queued{{"one", steer}, {"two", steer}}, "", "one\n\ntwo", "Restored 2 queued messages to editor"},
		{[]queued{{"b", followUp}, {"a", steer}, {"c", steer}}, "", "a\n\nc\n\nb", "Restored 3 queued messages to editor"},
		{[]queued{{"one", steer}}, "draft", "one\n\ndraft", "Restored 1 queued message to editor"},
		{[]queued{{"one", steer}}, "  ", "one", "Restored 1 queued message to editor"},
	} {
		r := newKeyRig(t, nil)
		for _, q := range c.queued {
			r.m.compactionQueue = append(r.m.compactionQueue, compactionQueuedMessage{text: q.text, mode: q.mode})
		}
		r.m.editor.SetText(c.editor)
		r.press(t, HostDequeueInputs()[0])
		if w := r.snapshot(); w.Editor != c.wantEditor || w.Status != c.wantStatus || len(r.m.compactionQueue) != 0 {
			t.Errorf("%+v: editor %q, status %q, queue %d; want %q, %q, 0", c.queued, w.Editor, w.Status, len(r.m.compactionQueue), c.wantEditor, c.wantStatus)
		}
	}
}

// handleFollowUp (interactive-mode.ts:4435-4464) ignores blank text, queues text typed during compaction with a history entry, and otherwise acts as Enter when idle.
func TestSetupKeyHandlersFollowUpBranches(t *testing.T) {
	t.Run("blank text keeps the editor", func(t *testing.T) {
		r := newKeyRig(t, nil)
		r.m.isCompacting = true
		r.m.editor.SetText("  ")
		r.press(t, hostKittyFollowUpInput())
		if w := r.snapshot(); w.Editor != "  " || w.QueuedFollowUps != 0 || w.Status != "" {
			t.Errorf("blank follow-up: editor %q, queued %d, status %q", w.Editor, w.QueuedFollowUps, w.Status)
		}
	})
	t.Run("compaction queues the trimmed text and keeps it in the editor history", func(t *testing.T) {
		r := newKeyRig(t, nil)
		r.m.isCompacting = true
		r.m.editor.SetText(" later ")
		r.press(t, hostKittyFollowUpInput())
		if len(r.m.compactionQueue) != 1 || r.m.compactionQueue[0].text != "later" || r.m.compactionQueue[0].mode != compactionQueueFollowUp {
			t.Fatalf("queue = %+v, want one follow-up %q", r.m.compactionQueue, "later")
		}
		r.press(t, "\x1b[A")
		if got := r.m.editor.Text(); got != "later" {
			t.Errorf("Up after the queued follow-up recalls %q, want %q", got, "later")
		}
	})
	t.Run("idle submits like Enter", func(t *testing.T) {
		r := newKeyRig(t, nil)
		var submitted []string
		r.m.editor.OnSubmit = func(text string) { submitted = append(submitted, text) }
		r.m.editor.SetText(" go ")
		r.press(t, hostKittyFollowUpInput())
		if !reflect.DeepEqual(submitted, []string{"go"}) || r.m.editor.Text() != "" {
			t.Errorf("submitted %q, editor %q; want [go] and an empty editor", submitted, r.m.editor.Text())
		}
	})
}

// CustomEditor runs onCtrlD only for an empty editor (custom-editor.ts:121-129); otherwise app.exit reaches the editor, whose Ctrl+D deletes forward.
func TestSetupKeyHandlersExitRunsOnlyForAnEmptyEditor(t *testing.T) {
	for _, c := range []struct {
		text     string
		wantText string
		wantExit bool
	}{
		{"", "", true},
		{"ab", "b", false},
		{" ", "", false},
		{"\n", "\n", false},
	} {
		r := newKeyRig(t, nil)
		r.m.editor.SetText(c.text)
		r.press(t, "\x1b[H") // Home
		r.press(t, "\x04")
		if w := r.snapshot(); w.Exit != c.wantExit || w.Editor != c.wantText {
			t.Errorf("Ctrl+D over %q: exit %v, editor %q; want %v, %q", c.text, w.Exit, w.Editor, c.wantExit, c.wantText)
		}
	}
}

// onChange (interactive-mode.ts:3103-3109) sets bash mode from `text.trimStart().startsWith("!")` and recolors the editor border when it changes.
func TestSetupKeyHandlersBashModeFollowsTheEditorText(t *testing.T) {
	r := newKeyRig(t, nil)
	border := func() string { return r.m.editor.Render(40)[0] }
	idle := border()
	bash := tui.ActiveTheme().GetBashModeBorderColor()(strings.Repeat("─", 40))
	if idle == bash {
		t.Fatal("the idle and bash borders are the same")
	}
	for _, step := range []struct {
		key  string
		text string
		want string
	}{
		{"!", "!", bash},
		{"x", "!x", bash},
		{"\x7f", "!", bash},
		{"\x7f", "", idle},
		{"a", "a", idle},
		{"!", "a!", idle},
	} {
		r.press(t, step.key)
		if got := r.m.editor.Text(); got != step.text || border() != step.want {
			t.Fatalf("after %q: text %q, border %q; want %q, border %q", step.key, got, border(), step.text, step.want)
		}
	}
	for _, c := range []struct{ text, want string }{{" \n\t!ls", bash}, {"\n\n!", bash}, {"x\n!", idle}, {"", idle}} {
		r.m.editor.SetText(c.text)
		if border() != c.want {
			t.Errorf("text %q: border %q, want %q", c.text, border(), c.want)
		}
	}
}

// app.message.copy runs handleCopyCommand({ flashConfirmation: true, preferSelection: true }) (interactive-mode.ts:3090-3093): the fullscreen renderer flashes
// "Copied!" and the status line stays empty, and every other renderer shows the status line.
func TestSetupKeyHandlersCopyConfirmsWithAFlashOnlyInFullscreen(t *testing.T) {
	for _, fullscreen := range []bool{false, true} {
		mode := &InteractiveMode{
			editor:        tui.NewEditor(),
			keybindings:   DefaultKeybindingsManager(),
			isIdle:        true,
			opts:          InteractiveModeOptions{SessionHandle: &recordingCompactHandle{lastAssistant: new("answer")}},
			chatContainer: tui.NewContainer(),
			copyClipboard: func(string) error { return nil },
		}
		if fullscreen {
			renderer := tui.NewTuiAltScreenWithOutput(io.Discard, 40, 6, tui.TuiAltScreenOptions{})
			renderer.SetCopyOnSelect(false)
			mode.tuiInst, mode.altScreen = renderer, renderer
		} else {
			mode.tuiInst = tui.NewWithOutput(io.Discard, 40, 6)
		}
		if err := mode.dispatchKey(context.Background(), "\x18"); err != nil {
			t.Fatal(err)
		}
		want := "Copied last agent message to clipboard"
		if fullscreen {
			want = ""
		}
		if mode.lastStatusMessage != want {
			t.Errorf("fullscreen %v: status %q, want %q", fullscreen, mode.lastStatusMessage, want)
		}
	}
}

// An extension's editor component (ctx.ui.setEditorComponent) receives the keys through classifyKeyWithBindings, which names the app action each
// binding has; the base CustomEditor behavior of that action then runs through handleEditorAction.
func TestSetupKeyHandlersClassifyNamesEachActionForItsDefaultAndRemappedKey(t *testing.T) {
	restoreTUIKeybindings(t)
	actions := map[string]keyAction{
		"app.clear": actionClearEditor, "app.suspend": actionSuspend, "app.thinking.cycle": actionCycleThinking,
		"app.interrupt": actionInterrupt, "app.exit": actionExit, "app.clipboard.pasteImage": actionPasteImage,
		"app.model.cycleForward": actionCycleModelForward, "app.model.cycleBackward": actionCycleModelBackward,
		"app.model.select": actionModelPicker, "app.tools.expand": actionToggleTools, "app.thinking.toggle": actionToggleThinking,
		"app.editor.external": actionExternalEditor, "app.message.copy": actionMessageCopy, "app.message.followUp": actionFollowUp,
		"app.message.dequeue": actionDequeue, "app.session.new": actionSessionNew, "app.session.tree": actionSessionTree,
		"app.session.fork": actionSessionFork, "app.session.resume": actionSessionResume,
	}
	for _, c := range keyHandlerCases {
		want, ok := actions[c.action]
		if !ok {
			t.Fatalf("no key action for %s", c.action)
		}
		id, wire := remapKey(c.letter)
		remapped := NewKeybindingsManagerFromBindings(map[string][]KeyID{c.action: {KeyID(id)}}, "")
		tui.SetKeybindings(remapped.merged)
		if got := classifyKeyWithBindings(wire, remapped); got != want {
			t.Errorf("%s remapped to %s: classified %d, want %d", c.action, id, got, want)
		}
		if c.key != "" {
			if got := classifyKeyWithBindings(c.key, remapped); got == want {
				t.Errorf("%s: its default key still classifies as the action after the remap", c.action)
			}
			defaults := NewKeybindingsManagerFromBindings(nil, "")
			tui.SetKeybindings(defaults.merged)
			if got := classifyKeyWithBindings(c.key, defaults); got != want {
				t.Errorf("%s default key %q: classified %d, want %d", c.action, c.key, got, want)
			}
		}
	}
}
