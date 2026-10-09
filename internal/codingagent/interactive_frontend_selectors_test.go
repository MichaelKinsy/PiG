package codingagent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// frontendSelectorLoop is a frontend probe whose input loop runs on its own
// goroutine, as Run's does, with the session's actions enabled. Keys reach
// it the way the terminal reader's pump routes them.
type frontendSelectorLoop struct {
	m       *InteractiveMode
	fe      *fakeFrontend
	session *fakeFrontendSession
	ctx     context.Context
}

func newFrontendSelectorLoop(t *testing.T) *frontendSelectorLoop {
	t.Helper()
	m, fe, session := newFrontendEditorProbe(t)
	ctx, cancel := context.WithCancel(t.Context())
	m.backgroundCtx = ctx
	m.inputReadCh = make(chan inputChunk)
	m.inputErrCh = make(chan error, 1)
	m.frontendInputReady()
	until, done := make(chan struct{}), make(chan error, 1)
	go func() { done <- m.inputLoopUntil(ctx, strings.NewReader(""), until) }()
	t.Cleanup(func() {
		close(until)
		if err := <-done; err != nil {
			t.Errorf("input loop: %v", err)
		}
		cancel()
	})
	return &frontendSelectorLoop{m: m, fe: fe, session: session, ctx: ctx}
}

// onOwner runs fn on the owner loop and waits for it.
func (l *frontendSelectorLoop) onOwner(t *testing.T, fn func()) {
	t.Helper()
	ran := make(chan struct{})
	if err := l.m.postToMain(l.ctx, func() { fn(); close(ran) }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("owner loop did not run the task")
	}
}

// key routes data as the terminal reader does and waits until it is handled
// when the main loop takes it.
func (l *frontendSelectorLoop) key(t *testing.T, data string) {
	t.Helper()
	if ticket := l.m.routeInputSequence(l.ctx, []byte(data), nil, l.m.inputReadCh); ticket != nil {
		select {
		case <-ticket.done:
		case <-time.After(5 * time.Second):
			t.Fatalf("key %q was not handled", data)
		}
	}
}

// dock returns the dock the session holds, read from its frames on the
// owner loop.
func (l *frontendSelectorLoop) dock(t *testing.T) []dockEntry {
	t.Helper()
	var frames []frontend.Frame
	l.onOwner(t, func() { frames = slices.Clone(l.session.frames) })
	return dockTree(t, &fakeFrontendSession{frames: frames})
}

// waitSelector waits until the dock shows a node in the editor's place that
// match accepts, and returns its id and node.
func (l *frontendSelectorLoop) waitSelector(t *testing.T, match func(frontend.Node) bool) (string, frontend.Node) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, entry := range l.dock(t) {
			if strings.HasPrefix(entry.id, "selector.") && match(entry.node) {
				return entry.id, entry.node
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no matching selector in the dock: %#v", l.dock(t))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type frontendSelectResult struct {
	value string
	err   error
}

// selectDialog opens an extension Select dialog and returns its result
// channel and its node once the dock shows it.
func (l *frontendSelectorLoop) selectDialog(t *testing.T) (<-chan frontendSelectResult, string, frontend.Selector) {
	t.Helper()
	result := make(chan frontendSelectResult, 1)
	ui := &ExtUIContext{m: l.m}
	go func() {
		value, err := ui.Select(l.ctx, "Pick one", []string{"alpha", "beta", "gamma"}, extension.ExtensionUIDialogOptions{})
		result <- frontendSelectResult{value, err}
	}()
	id, node := l.waitSelector(t, func(node frontend.Node) bool {
		selector, ok := node.(frontend.Selector)
		return ok && selector.Title == "Pick one"
	})
	return result, id, node.(frontend.Selector)
}

func awaitSelectResult(t *testing.T, result <-chan frontendSelectResult) frontendSelectResult {
	t.Helper()
	select {
	case r := <-result:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the extension call did not return")
		return frontendSelectResult{}
	}
}

// An extension's Select dialog shows as a Selector node in the editor's
// place, and the session's Choose returns the chosen option to the waiting
// extension call; the editor node returns after it.
func TestFrontendSelectorChooseAnswersTheExtension(t *testing.T) {
	l := newFrontendSelectorLoop(t)
	result, id, node := l.selectDialog(t)
	want := []frontend.SelectorItem{{ID: "0", Label: "alpha"}, {ID: "1", Label: "beta"}, {ID: "2", Label: "gamma"}}
	if !slices.Equal(node.Items, want) || node.Selected != "0" {
		t.Fatalf("dialog node = %#v", node)
	}
	l.fe.env.Act(frontend.Action{Node: id, Kind: frontend.Choose, Item: "2"})
	if r := awaitSelectResult(t, result); r.err != nil || r.value != "gamma" {
		t.Fatalf("select = %q, %v", r.value, r.err)
	}
	l.onOwner(t, func() { l.m.tuiInst.Render() })
	if ids := dockIDs(l.dock(t)); slices.ContainsFunc(ids, func(id string) bool { return strings.HasPrefix(id, "selector.") }) || !slices.Contains(ids, "editor") {
		t.Fatalf("dock after the dialog = %q", ids)
	}
}

// The session's Dismiss cancels the dialog.
func TestFrontendSelectorDismissCancelsTheExtension(t *testing.T) {
	l := newFrontendSelectorLoop(t)
	result, id, _ := l.selectDialog(t)
	l.fe.env.Act(frontend.Action{Node: id, Kind: frontend.Dismiss})
	if r := awaitSelectResult(t, result); !errors.Is(r.err, context.Canceled) || r.value != "" {
		t.Fatalf("select = %q, %v; want cancelled", r.value, r.err)
	}
}

// Keys still drive the dialog while a session draws it natively, and the
// node follows them.
func TestFrontendSelectorKeysStillWork(t *testing.T) {
	l := newFrontendSelectorLoop(t)
	result, id, _ := l.selectDialog(t)
	l.key(t, "\x1b[B")
	if _, node := l.waitSelector(t, func(node frontend.Node) bool { return node.(frontend.Selector).Selected == "1" }); node == nil {
		t.Fatal("no node after the move")
	}
	if got, _ := l.waitSelector(t, func(frontend.Node) bool { return true }); got != id {
		t.Fatalf("a move replaced the node: %s, was %s", got, id)
	}
	l.key(t, "\r")
	if r := awaitSelectResult(t, result); r.err != nil || r.value != "beta" {
		t.Fatalf("select = %q, %v", r.value, r.err)
	}
}

// While a built-in selector's modal loop has the keys, a sequence the
// session claims is consumed by it and never reaches the selector; an action
// the session takes in response drives the selector. The settings frame
// shows as one Settings node without its border lines.
func TestFrontendSelectorModalLoopGivesTheSessionItsInput(t *testing.T) {
	l := newFrontendSelectorLoop(t)
	finished := false
	list := tui.NewSettingsList([]tui.SettingItem{
		{ID: "a", Label: "Alpha", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "b", Label: "Beta", CurrentValue: "x", Values: []string{"x", "y"}},
	}, 10, tui.GetSettingsListTheme(), func(string, string) {}, func() { finished = true }, tui.SettingsListOptions{EnableSearch: true})
	closed := make(chan struct{})
	if err := l.m.postToMain(l.ctx, func() {
		defer close(closed)
		l.m.runEditorSlotComponent(settingsFrame(list), list.HandleInput, func() bool { return finished })
	}); err != nil {
		t.Fatal(err)
	}
	id, _ := l.waitSelector(t, func(node frontend.Node) bool { _, ok := node.(frontend.Settings); return ok })
	var settings int
	var lines []string
	for _, entry := range l.dock(t) {
		switch node := entry.node.(type) {
		case frontend.Settings:
			settings++
		case frontend.Lines:
			lines = append(lines, node.Lines...)
		}
	}
	if settings != 1 || strings.Contains(strings.Join(lines, "\n"), "─") {
		t.Fatalf("dock = %#v", l.dock(t))
	}

	l.key(t, tspEvent)
	var inputs []string
	var node frontend.Node
	l.onOwner(t, func() {
		inputs = slices.Clone(l.session.inputs)
		node, _ = list.NativeNode()
	})
	if !slices.Equal(inputs, []string{tspEvent}) {
		t.Fatalf("session inputs = %q", inputs)
	}
	if got := node.(frontend.Settings); got.Query != "" || got.Selected != "a" || got.Items[0].Value != "on" || list.Cancelled() {
		t.Fatalf("the selector took the session's input: %#v", got)
	}

	l.onOwner(t, func() {
		l.session.onInput = func(string) { l.fe.env.Act(frontend.Action{Node: id, Kind: frontend.SetValue, Item: "b", Value: "y"}) }
	})
	l.key(t, tspEvent)
	l.waitSelector(t, func(node frontend.Node) bool {
		settings, ok := node.(frontend.Settings)
		return ok && settings.Selected == "b" && settings.Items[1].Value == "y"
	})
	l.onOwner(t, func() {
		l.session.onInput = func(string) { l.fe.env.Act(frontend.Action{Node: id, Kind: frontend.Dismiss}) }
	})
	l.key(t, tspEvent)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the dismiss did not close the settings")
	}
}

func dockIDs(dock []dockEntry) []string {
	ids := make([]string, len(dock))
	for i, entry := range dock {
		ids[i] = entry.id
	}
	return ids
}
