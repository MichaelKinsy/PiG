//go:build unix

package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's InteractiveMode.setupKeyHandlers (interactive-mode.ts:3047-3115) registers sixteen app actions on the default editor. Each case here presses the
// action's default key, then its remapped key, then the default key after the remap, through the editor input path (dispatchKey) of a mounted mode.
// keyWorld is every effect the handlers have, so a key that reaches another action's handler, two handlers, or none shows as a different world.
type keyWorld struct {
	Editor          string
	Status          string
	SigintArmed     bool
	Exit            bool
	ModelSelector   bool
	EscapeArmed     bool
	Suspends        int
	ToolsExpanded   bool
	HideThinking    bool
	HideSetting     bool
	ThinkingLevel   string
	CycleDirections []string
	Slash           []string
	Copied          []string
	EditorLaunches  int
	QueuedFollowUps int
	QueuedSteering  int
}

type keyRig struct {
	m        *InteractiveMode
	world    keyWorld
	launched string
	// bashCancels counts the cancellations of the running `!` command.
	bashCancels int
	// editorLaunchable is true for the rig that expects the external editor to start; a key that starts it in any other rig fails the test.
	editorLaunchable bool
	// summaryCancels counts the cancellations of the running branch summary.
	summaryCancels int
}

// remapKeyID is the unused key each action is remapped to: ctrl+alt+<letter>, which the terminal sends as a Kitty CSI-u sequence.
func remapKey(letter byte) (id, wire string) {
	return "ctrl+alt+" + string(letter), fmt.Sprintf("\x1b[%d;7u", letter)
}

// newKeyRig builds a mounted mode whose keybindings carry bindings, a Session handle that records cycles and a settings manager that persists.
func newKeyRig(t *testing.T, bindings map[string]string) *keyRig {
	t.Helper()
	restoreTUIKeybindings(t)
	user := map[string][]KeyID{}
	for action, key := range bindings {
		user[action] = []KeyID{KeyID(key)}
	}
	km := NewKeybindingsManagerFromBindings(user, "")
	tui.SetKeybindings(km.merged)

	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000, MaxThinking: ai.ThinkingLevelHigh}}
	other := &ai.Model{ID: "other", DisplayName: "other", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	last := "last assistant text"
	handle := &recordingCompactHandle{
		agent:         mustNewAgent(agent.AgentOptions{Model: model}),
		scopedModels:  []extension.ScopedModel{{Model: model}, {Model: other}},
		cycleResults:  []*ModelCycleResult{{Model: other}},
		lastAssistant: &last,
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", fakeEditor(t, "capture")+" "+filepath.Join(t.TempDir(), "capture.json"))
	agentDir := t.TempDir()
	sm := NewSettingsManager(t.TempDir(), agentDir)
	// The model selector starts a catalog refresh that outlives the key press and is not joined. A registry of its own without an agent directory keeps that refresh in memory: the default one is backed by agentDir, and the refresh creates auth.json and models-store.json there while the cleanup removes the directory.
	m := newSwitchTuiProbeWithOptions(t, InteractiveModeOptions{CWD: t.TempDir(), Model: model, AgentDir: agentDir, Settings: Settings{TuiMode: "regular"}, SettingsManager: sm, SessionHandle: handle, ModelRegistry: NewModelRegistryWithModelsPath("")})
	t.Cleanup(m.teardownCurrentTui)
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	t.Cleanup(func() { cancel(); m.backgroundTasks.Wait() })
	m.keybindings = km
	m.agent = handle.agent
	rig := &keyRig{m: m}
	m.slashRegistry = NewSlashRegistry()
	for _, name := range []string{"new", "tree", "fork", "resume"} {
		m.slashRegistry.builtins[name].Handler = func(*SlashContext) error {
			rig.world.Slash = append(rig.world.Slash, "/"+name)
			return nil
		}
	}
	m.copyClipboard = func(text string) error {
		rig.world.Copied = append(rig.world.Copied, text)
		return nil
	}
	previous := suspendKill
	suspendKill = func(pid int) error {
		if pid != 0 {
			t.Errorf("suspend signalled pid %d, want the process group 0", pid)
		}
		rig.world.Suspends++
		return nil
	}
	t.Cleanup(func() { suspendKill = previous })
	return rig
}

// settle joins the background work a key started and runs the owner-loop tasks it posted.
func (r *keyRig) settle() {
	if r.world.Suspends > 0 {
		// The suspend continue waiter ends only with the mode's context: drain what is queued and leave it to the cleanup.
		r.drain()
		return
	}
	done := make(chan struct{})
	go func() {
		r.m.backgroundTasks.Wait()
		r.m.clipboardReads.Wait()
		close(done)
	}()
	for {
		select {
		case task := <-r.m.uiTaskCh:
			task()
		case <-done:
			r.drain()
			return
		}
	}
}

// drain runs the owner-loop tasks that are queued now.
func (r *keyRig) drain() {
	for {
		select {
		case task := <-r.m.uiTaskCh:
			task()
		default:
			return
		}
	}
}

// snapshot reads the world after the keys pressed so far.
func (r *keyRig) snapshot() keyWorld {
	m := r.m
	w := r.world
	w.Editor = m.editor.Text()
	w.Status = m.lastStatusMessage
	w.SigintArmed = !m.lastSigintTime.IsZero()
	w.Exit = m.requestExit.Load()
	w.EscapeArmed = !m.lastEscapeTime.IsZero()
	w.ToolsExpanded = m.toolsExpanded
	w.HideThinking = m.hideThinking
	w.HideSetting = m.opts.SettingsManager.GetHideThinkingBlock()
	if m.externalEditorActive {
		w.EditorLaunches = 1
	}
	w.ThinkingLevel = string(m.agent.ThinkingLevel())
	handle := m.opts.SessionHandle.(*recordingCompactHandle)
	w.CycleDirections = append([]string(nil), handle.cycleDirections...)
	for _, queued := range m.compactionQueue {
		if queued.mode == compactionQueueFollowUp {
			w.QueuedFollowUps++
		} else {
			w.QueuedSteering++
		}
	}
	return w
}

// dispatch presses key through the editor input path and returns when the mode has handled it.
func (r *keyRig) dispatch(t *testing.T, key string) {
	t.Helper()
	// A key that opens the model selector blocks the owner loop in the selector's input loop, which also runs queued tasks: the probe task shows that
	// the loop is running, and Escape closes the selector.
	done := make(chan error, 1)
	go func() { done <- r.m.dispatchKey(r.m.runCtx, key) }()
	type probe struct{ selector, input bool }
	probed := make(chan probe, 1)
	r.m.uiTaskCh <- func() {
		_, selector := r.m.tuiInst.GetFocusedComponent().(*tui.ModelSelectorComponent)
		input, _ := r.m.modalRoute()
		probed <- probe{selector, input != nil}
	}
	var err error
	select {
	case err = <-done:
	case p := <-probed:
		if p.input {
			input, _ := r.m.modalRoute()
			input <- []byte("\x1b")
			r.world.ModelSelector = p.selector
		}
		err = <-done
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (r *keyRig) press(t *testing.T, key string) {
	t.Helper()
	r.dispatch(t, key)
	if !r.editorLaunchable && r.m.externalEditorActive {
		// The editor's completion callback enters raw mode, which a test has no terminal for: join the editor and leave the callback queued.
		r.m.backgroundTasks.Wait()
		t.Fatalf("key %q started the external editor", key)
	}
	r.settle()
}

type keyHandlerCase struct {
	action   string
	letter   byte
	key      string // the default key as the terminal sends it; empty when the action has no default key
	setup    func(t *testing.T, r *keyRig)
	want     func(w *keyWorld)
	oldKey   func(w *keyWorld) // what the default key does once another key has the action, when it is not nothing
	noSettle bool              // the key leaves background work that ends only with the mode (the suspend continue waiter) or a callback the test never runs (the external editor's)
}

var keyHandlerCases = []keyHandlerCase{
	{action: "app.clear", letter: 'a', key: "\x03",
		setup: func(t *testing.T, r *keyRig) { r.m.editor.SetText("draft") },
		want:  func(w *keyWorld) { w.Editor = ""; w.SigintArmed = true }},
	{action: "app.suspend", letter: 'b', key: "\x1a", noSettle: true, want: func(w *keyWorld) { w.Suspends = 1 }},
	{action: "app.thinking.cycle", letter: 'c', key: "\x1b[Z", want: func(w *keyWorld) { w.ThinkingLevel = "minimal"; w.Status = "Thinking level: minimal" }},
	{action: "app.interrupt", letter: 'q', key: "\x1b", want: func(w *keyWorld) { w.EscapeArmed = true }},
	{action: "app.exit", letter: 'r', key: "\x04", want: func(w *keyWorld) { w.Exit = true }},
	{action: "app.clipboard.pasteImage", letter: 's', key: "\x16",
		setup: func(t *testing.T, r *keyRig) {
			withEnv(t, map[string]string{})
			useClipboardTextTestSeams(t, "darwin", map[string]string{},
				func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no image") },
				func() *tui.NativeClipboard {
					return &tui.NativeClipboard{
						GetImage: func(context.Context) ([]byte, bool, error) { return nil, true, nil },
						GetText:  func(context.Context) (*string, bool, error) { return new("pasted text"), true, nil },
					}
				})
		},
		want: func(w *keyWorld) { w.Editor = "pasted text" }},
	{action: "app.model.cycleForward", letter: 'd', key: "\x10", want: func(w *keyWorld) { w.CycleDirections = []string{"forward"}; w.Status = "Switched to other" }},
	{action: "app.model.cycleBackward", letter: 'e', key: hostCycleBackwardInput(), want: func(w *keyWorld) { w.CycleDirections = []string{"backward"}; w.Status = "Switched to other" }},
	{action: "app.model.select", letter: 'p', key: "\x0c", want: func(w *keyWorld) { w.ModelSelector = true }},
	{action: "app.tools.expand", letter: 'f', key: "\x0f", want: func(w *keyWorld) { w.ToolsExpanded = true; w.Status = "Tool output: expanded" }},
	{action: "app.thinking.toggle", letter: 'g', key: "\x14", want: func(w *keyWorld) { w.HideThinking = true; w.HideSetting = true; w.Status = "Thinking blocks: hidden" }},
	{action: "app.editor.external", letter: 'h', key: "\x07", noSettle: true,
		setup: func(t *testing.T, r *keyRig) {
			t.Setenv("VISUAL", "")
			capture := filepath.Join(t.TempDir(), "capture.json")
			t.Setenv("EDITOR", fakeEditor(t, "capture")+" "+capture)
			r.launched = capture
			r.editorLaunchable = true
			r.m.editor.SetText("seed")
		},
		want: func(w *keyWorld) { w.Editor = "seed"; w.EditorLaunches = 1 }},
	{action: "app.message.copy", letter: 'i', key: "\x18", want: func(w *keyWorld) {
		w.Copied = []string{"last assistant text"}
		w.Status = "Copied last agent message to clipboard"
	}},
	{action: "app.message.followUp", letter: 'j', key: hostFollowUpInput(),
		setup: func(t *testing.T, r *keyRig) { r.m.isCompacting = true; r.m.editor.SetText("later") },
		want: func(w *keyWorld) {
			w.Editor = ""
			w.QueuedFollowUps = 1
			w.Status = "Queued message for after compaction"
		},
		// Alt+Enter without an app action is the legacy Shift+Enter: the editor inserts a line break. Ctrl+Q, the key with Windows keybindings, does nothing.
		oldKey: func(w *keyWorld) {
			if hostFollowUpInput() == "\x1b\r" {
				w.Editor = "later\n"
			}
		}},
	{action: "app.message.dequeue", letter: 'k', key: HostDequeueInputs()[0],
		setup: func(t *testing.T, r *keyRig) {
			r.m.compactionQueue = []compactionQueuedMessage{{text: "queued", mode: compactionQueueSteer}}
		},
		want: func(w *keyWorld) {
			w.Editor = "queued"
			w.QueuedSteering = 0
			w.Status = "Restored 1 queued message to editor"
		}},
	{action: "app.session.new", letter: 'l', want: func(w *keyWorld) { w.Slash = []string{"/new"} }},
	{action: "app.session.tree", letter: 'm', want: func(w *keyWorld) { w.Slash = []string{"/tree"} }},
	{action: "app.session.fork", letter: 'n', want: func(w *keyWorld) { w.Slash = []string{"/fork"} }},
	{action: "app.session.resume", letter: 'o', want: func(w *keyWorld) { w.Slash = []string{"/resume"} }},
}

// pressCase presses key on a fresh rig that carries bindings and reports the world before and after, and for the external editor whether it launched.
func pressCase(t *testing.T, c keyHandlerCase, bindings map[string]string, key string) (before, after keyWorld) {
	t.Helper()
	r := newKeyRig(t, bindings)
	if c.setup != nil {
		c.setup(t, r)
	}
	before = r.snapshot()
	if c.noSettle {
		// The external editor's completion callback enters raw mode, which a test has no terminal for: it stays queued on the owner loop and never runs.
		r.dispatch(t, key)
		after = r.snapshot()
		if r.launched != "" && after.EditorLaunches == 1 {
			r.m.backgroundTasks.Wait()
			data, err := os.ReadFile(r.launched)
			var got struct{ Content string }
			if err != nil || json.Unmarshal(data, &got) != nil || got.Content != "seed" {
				t.Errorf("the external editor was not given the editor text: capture %q, %v", data, err)
			}
		}
		return before, after
	}
	r.press(t, key)
	return before, r.snapshot()
}

func TestSetupKeyHandlersRunsEachAppActionForItsDefaultAndRemappedKey(t *testing.T) {
	for _, c := range keyHandlerCases {
		t.Run(c.action, func(t *testing.T) {
			id, wire := remapKey(c.letter)
			want := func(before keyWorld) keyWorld {
				w := before
				c.want(&w)
				return w
			}
			if c.key != "" {
				before, after := pressCase(t, c, nil, c.key)
				if expected := want(before); !reflect.DeepEqual(after, expected) {
					t.Errorf("default key %q: world %+v, want %+v", c.key, after, expected)
				}
			}
			before, after := pressCase(t, c, map[string]string{c.action: id}, wire)
			if expected := want(before); !reflect.DeepEqual(after, expected) {
				t.Errorf("remapped key %q: world %+v, want %+v", wire, after, expected)
			}
			if c.key != "" {
				before, after := pressCase(t, c, map[string]string{c.action: id}, c.key)
				expected := before
				if c.oldKey != nil {
					c.oldKey(&expected)
				}
				if !reflect.DeepEqual(after, expected) {
					t.Errorf("default key %q after the remap: world %+v, want %+v", c.key, after, expected)
				}
			}
		})
	}
}
