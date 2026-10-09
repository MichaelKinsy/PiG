package codingagent

import (
	"context"
	"fmt"
	"io"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type treeCaseEntry struct {
	id, parent, kind, text string
	extra                  map[string]any
}

func treeUser(id, parent, text string) treeCaseEntry {
	return treeCaseEntry{id: id, parent: parent, kind: "user", text: text}
}
func treeAssistant(id, parent, text string) treeCaseEntry {
	return treeCaseEntry{id: id, parent: parent, kind: "assistant", text: text}
}

func upstreamTreeSession(t *testing.T, leaf string, entries ...treeCaseEntry) *Session {
	t.Helper()
	session := NewSession("tree", t.TempDir())
	for _, e := range entries {
		var parent any
		if e.parent != "" {
			parent = e.parent
		}
		row := map[string]any{"type": e.kind, "id": e.id, "parentId": parent, "timestamp": "2026-03-28T14:32:00Z"}
		switch e.kind {
		case "user":
			row["type"] = "message"
			row["message"] = map[string]any{"role": "user", "content": e.text, "timestamp": 1}
		case "assistant", "tool-only":
			content := []any{map[string]any{"type": "text", "text": e.text}}
			stop := "stop"
			if e.kind == "tool-only" {
				content = []any{map[string]any{"type": "toolCall", "id": "tc-" + e.id, "name": "read", "arguments": map[string]any{"path": "test.ts"}}}
				stop = "toolUse"
			}
			row["type"] = "message"
			row["message"] = map[string]any{"role": "assistant", "content": content, "api": "anthropic-messages", "provider": "anthropic", "model": "claude-sonnet-4", "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": stop, "timestamp": 1}
		}
		maps.Copy(row, e.extra)
		if err := session.AppendEntry(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.SetLeafID(&leaf); err != nil {
		t.Fatal(err)
	}
	return session
}

func upstreamTree(t *testing.T, leaf string, entries ...treeCaseEntry) *tui.TreeSelectorComponent {
	t.Helper()
	session := upstreamTreeSession(t, leaf, entries...)
	return upstreamTreeFromRoot(session, session.treeRoot(), leaf)
}

func upstreamTreeFromRoot(session *Session, root *SessionTreeNode, leaf string) *tui.TreeSelectorComponent {
	selector := tui.NewTreeSelectorComponent("Session tree", &treeNodeAdapter{n: root, f: newTreeRowFormatter(session)})
	selector.MaxVisibleLines = tui.TreeVisibleLines(24)
	selector.SetInitialCursor(leaf, "")
	return selector
}

func selectedTreeID(selector *tui.TreeSelectorComponent) string {
	// The current native selector has no highlighted-node accessor. Read its entry identity without confirming, copying its atomic state, or adding a test-only production API.
	value := reflect.ValueOf(selector).Elem()
	rows := value.FieldByName("rows")
	cursor := int(value.FieldByName("cursor").Int())
	if cursor < 0 || cursor >= rows.Len() {
		return ""
	}
	return rows.Index(cursor).FieldByName("id").String()
}
func assertTreeSelection(t *testing.T, selector *tui.TreeSelectorComponent, want string) {
	t.Helper()
	if got := selectedTreeID(selector); got != want {
		t.Fatalf("selected = %q, want %q\n%s", got, want, strings.Join(selector.Render(200), "\n"))
	}
}

func simpleBranchEntries() []treeCaseEntry {
	return []treeCaseEntry{treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"), treeUser("user-2", "asst-1", "active branch"), treeAssistant("asst-2", "user-2", "response"), treeUser("user-3", "asst-1", "sibling branch")}
}
func branchingTreeEntries() []treeCaseEntry {
	return []treeCaseEntry{
		treeUser("user-1", "", "first message"), treeAssistant("asst-1", "user-1", "response 1"), treeUser("user-2", "asst-1", "second message"), treeAssistant("asst-2", "user-2", "response 2"),
		treeUser("user-3a", "asst-2", "branch A start"), treeAssistant("asst-3a", "user-3a", "branch A response"), treeUser("user-4a", "asst-3a", "branch A deep"), treeAssistant("asst-4a", "user-4a", "branch A leaf"),
		treeUser("user-3b", "asst-2", "branch B start"), treeAssistant("asst-3b", "user-3b", "branch B response"), treeUser("user-4b", "asst-3b", "branch B deep"),
	}
}

func TestTreeSelectorUpstream(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	DefaultKeybindingsManager().syncToTUI()
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:130
	t.Run("focuses nearest visible ancestor when currentLeafId is a model_change with sibling branch", func(t *testing.T) {
		selector := upstreamTree(t, "model-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"), treeUser("user-2", "asst-1", "active branch"), treeCaseEntry{id: "model-1", parent: "user-2", kind: "model_change", extra: map[string]any{"provider": "anthropic", "modelId": "claude-sonnet-4"}}, treeUser("user-3", "asst-1", "sibling branch"))
		assertTreeSelection(t, selector, "user-2")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:159
	t.Run("hides context edits by default and labels them in all mode", func(t *testing.T) {
		selector := upstreamTree(t, "edit-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"), treeCaseEntry{id: "edit-1", parent: "asst-1", kind: "context_edit", extra: map[string]any{"targetId": "asst-1", "replacement": nil}})
		assertTreeSelection(t, selector, "asst-1")
		// Pi passes initialFilterMode "all" through the constructor (tree-selector.test.ts:182-193).
		session := upstreamTreeSession(t, "edit-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"), treeCaseEntry{id: "edit-1", parent: "asst-1", kind: "context_edit", extra: map[string]any{"targetId": "asst-1", "replacement": nil}})
		selector = tui.NewTreeSelectWithInitialFilter("Session tree", &treeNodeAdapter{n: session.treeRoot(), f: newTreeRowFormatter(session)}, "all")
		selector.MaxVisibleLines = tui.TreeVisibleLines(24)
		selector.SetInitialCursor("edit-1", "")
		if got := stripANSITest(strings.Join(selector.Render(200), "\n")); !strings.Contains(got, "[context omit: asst-1]") {
			t.Fatalf("context edit label missing: %q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:196
	t.Run("focuses nearest visible ancestor when currentLeafId is a thinking_level_change entry", func(t *testing.T) {
		selector := upstreamTree(t, "thinking-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"), treeUser("user-2", "asst-1", "active branch"), treeCaseEntry{id: "thinking-1", parent: "user-2", kind: "thinking_level_change", extra: map[string]any{"thinkingLevel": "high"}}, treeUser("user-3", "asst-1", "sibling branch"))
		assertTreeSelection(t, selector, "user-2")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:227
	t.Run("switches to nearest visible user message when changing to user-only filter", func(t *testing.T) {
		selector := upstreamTree(t, "asst-2", simpleBranchEntries()...)
		assertTreeSelection(t, selector, "asst-2")
		selector.HandleInput("\x15")
		assertTreeSelection(t, selector, "user-2")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:256
	t.Run("returns to nearest visible ancestor when switching back to default filter", func(t *testing.T) {
		selector := upstreamTree(t, "asst-2", simpleBranchEntries()...)
		assertTreeSelection(t, selector, "asst-2")
		selector.HandleInput("\x15")
		assertTreeSelection(t, selector, "user-2")
		selector.HandleInput("\x04")
		assertTreeSelection(t, selector, "user-2")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:290
	t.Run("renders semantic help rows without truncating narrow terminal controls", func(t *testing.T) {
		selector := upstreamTree(t, "asst-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"))
		lines := selector.Render(30)
		plain := stripANSITest(strings.Join(lines, "\n"))
		for _, text := range []string{"branch", "copy", "filters", "cycle", "label time"} {
			if !strings.Contains(plain, text) {
				t.Fatalf("missing %q: %q", text, plain)
			}
		}
		if strings.Contains(plain, "...") {
			t.Fatalf("truncated controls: %q", plain)
		}
		for _, line := range lines {
			if widthx.VisibleWidth(line) > 30 {
				t.Fatalf("over-width help: %q", line)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:314
	t.Run("copies the full selected message with ctrl+x", testTreeCopyUsesProductionPickerAndClipboardRoute)
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:336
	t.Run("toggles label timestamps for labeled nodes", func(t *testing.T) {
		session := upstreamTreeSession(t, "asst-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"))
		root := session.treeRoot()
		root.Children[0].Label = "checkpoint"
		root.Children[0].LabelTimestamp = time.Date(2026, 3, 28, 14, 32, 0, 0, time.Local).UTC().Format(time.RFC3339)
		selector := upstreamTreeFromRoot(session, root, "asst-1")
		render := strings.Join(selector.Render(200), "\n")
		if !strings.Contains(render, "[checkpoint]") || strings.Contains(render, "3/28 14:32") || strings.Contains(render, "[+label time]") {
			t.Fatalf("initial label: %q", render)
		}
		selector.HandleInput("T")
		render = strings.Join(selector.Render(200), "\n")
		if !strings.Contains(render, "3/28 14:32") || !strings.Contains(render, "[+label time]") {
			t.Fatalf("timestamp label: %q", render)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:366
	t.Run("preserves selection when switching to empty labeled filter and back", func(t *testing.T) {
		selector := upstreamTree(t, "asst-2", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"), treeUser("user-2", "asst-1", "bye"), treeAssistant("asst-2", "user-2", "goodbye"))
		assertTreeSelection(t, selector, "asst-2")
		selector.HandleInput("\x0c")
		assertTreeSelection(t, selector, "")
		selector.HandleInput("\x04")
		assertTreeSelection(t, selector, "asst-2")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:400
	t.Run("preserves selection through multiple empty filter switches", func(t *testing.T) {
		selector := upstreamTree(t, "asst-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "hi"))
		assertTreeSelection(t, selector, "asst-1")
		for _, step := range []struct{ key, want string }{{"\x0c", ""}, {"\x0c", "asst-1"}, {"\x0c", ""}, {"\x04", "asst-1"}} {
			selector.HandleInput(step.key)
			assertTreeSelection(t, selector, step.want)
		}
	})
	const up, down, left, right, altLeft, altRight = "\x1b[A", "\x1b[B", "\x1b[1;5D", "\x1b[1;5C", "\x1b[1;3D", "\x1b[1;3C"
	for _, tc := range []struct {
		name  string
		steps []struct{ key, want string }
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:476
		{"ctrl+right unfolds a folded node, then does segment jump when unfolded", []struct{ key, want string }{{left, "user-3a"}, {left, "user-3a"}, {down, "user-3b"}, {up, "user-3a"}, {right, "user-3a"}, {down, "asst-3a"}, {left, "user-3a"}, {right, "asst-4a"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:512
		{"alt+left/right are aliases for fold and unfold navigation", []struct{ key, want string }{{altLeft, "user-3a"}, {altLeft, "user-3a"}, {altRight, "user-3a"}, {altRight, "asst-4a"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:536
		{"folding root hides entire subtree, nested fold preserved on unfold", []struct{ key, want string }{{left, "user-3a"}, {left, "user-3a"}, {left, "user-1"}, {left, "user-1"}, {down, "user-1"}, {right, "user-1"}, {right, "user-3a"}, {down, "user-3b"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := upstreamTree(t, "asst-4a", branchingTreeEntries()...)
			for _, step := range tc.steps {
				selector.HandleInput(step.key)
				assertTreeSelection(t, selector, step.want)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:572
	t.Run("fold and navigate on non-active branch", func(t *testing.T) {
		selector := upstreamTree(t, "asst-4a", branchingTreeEntries()...)
		findTreeByDown(t, selector, "user-3b")
		for _, step := range []struct{ key, want string }{{right, "user-4b"}, {left, "user-3b"}, {left, "user-3b"}, {left, "user-1"}} {
			selector.HandleInput(step.key)
			assertTreeSelection(t, selector, step.want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:607
	t.Run("fold and navigate with multiple roots", func(t *testing.T) {
		selector := upstreamTree(t, "asst-1", treeUser("user-1", "", "first root"), treeAssistant("asst-1", "user-1", "response 1"), treeUser("user-2", "", "second root"), treeAssistant("asst-2", "user-2", "response 2"))
		assertTreeSelection(t, selector, "asst-1")
		for _, step := range []struct{ key, want string }{{left, "user-1"}, {left, "user-1"}, {down, "user-2"}, {right, "asst-2"}, {left, "user-2"}, {left, "user-2"}, {left, "user-2"}} {
			selector.HandleInput(step.key)
			assertTreeSelection(t, selector, step.want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:648
	t.Run("folding root hides descendants even when intermediate nodes are filtered out", func(t *testing.T) {
		selector := upstreamTree(t, "asst-2", treeUser("user-1", "", "hello"), treeCaseEntry{id: "tool-asst-1", parent: "user-1", kind: "tool-only"}, treeUser("user-2", "tool-asst-1", "follow up"), treeAssistant("asst-2", "user-2", "response"))
		for _, key := range []string{left, left, down} {
			selector.HandleInput(key)
			assertTreeSelection(t, selector, "user-1")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:676
	t.Run("search resets fold state", func(t *testing.T) {
		selector := upstreamTree(t, "asst-4a", branchingTreeEntries()...)
		selector.HandleInput(left)
		selector.HandleInput(left)
		selector.HandleInput(down)
		assertTreeSelection(t, selector, "user-3b")
		selector.HandleInput("b")
		selector.HandleInput("\x1b")
		findTreeByDown(t, selector, "user-3a")
		selector.HandleInput(down)
		assertTreeSelection(t, selector, "asst-3a")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tree-selector.test.ts:709
	t.Run("filter mode change resets fold state", func(t *testing.T) {
		selector := upstreamTree(t, "asst-4a", branchingTreeEntries()...)
		selector.HandleInput(left)
		selector.HandleInput(left)
		selector.HandleInput("\x15")
		selector.HandleInput("\x04")
		findTreeByDown(t, selector, "user-3a")
		selector.HandleInput(down)
		assertTreeSelection(t, selector, "asst-3a")
	})
}

func TestTreeActiveBranchIsPrioritizedAndMarked(t *testing.T) {
	selector := upstreamTree(t, "user-4b", branchingTreeEntries()...)
	lines := selector.Render(200)
	branchA, branchB := -1, -1
	for index, line := range lines {
		plain := stripANSITest(line)
		if strings.Contains(plain, "branch A start") {
			branchA = index
			if strings.Contains(plain, "•") {
				t.Error("inactive branch has an active-path marker")
			}
		}
		if strings.Contains(plain, "branch B start") {
			branchB = index
			if !strings.Contains(plain, "•") {
				t.Error("active branch lacks its path marker")
			}
		}
	}
	if branchA < 0 || branchB < 0 || branchB >= branchA {
		t.Fatalf("active branch must render first: A=%d B=%d", branchA, branchB)
	}
}

func testTreeCopyUsesProductionPickerAndClipboardRoute(t *testing.T) {
	t.Helper()
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	DefaultKeybindingsManager().syncToTUI()
	message := strings.Repeat("long message ", 30) + "\nsecond line"
	session := upstreamTreeSession(t, "asst-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", message))
	renderer := tui.NewWithOutput(io.Discard, 120, 40)
	renderer.SetRenderDispatcher(func(func()) {})
	t.Cleanup(renderer.CancelPendingRender)
	var copied string
	releaseCopy := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCopy) }) }
	m := &InteractiveMode{
		opts:    InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: session}},
		tuiInst: renderer, chatContainer: tui.NewContainer(), backgroundCtx: t.Context(),
		uiTaskCh: make(chan func(), 1), modalInputCh: make(chan []byte, 2),
		copyClipboard: func(text string) error { copied = text; <-releaseCopy; return nil },
	}
	t.Cleanup(func() { release(); m.backgroundTasks.Wait() })
	m.modalInputCh <- []byte("\x18")
	m.modalInputCh <- []byte("\x1b")
	if _, selected := m.buildSlashContext(t.Context()).PickTreeEntry(""); selected {
		t.Fatal("copy then cancel selected a branch")
	}
	if m.chatContainer.ChildCount() != 0 {
		t.Fatal("copy completion changed the UI before clipboard completion")
	}
	release()
	m.backgroundTasks.Wait()
	if copied != message {
		t.Fatalf("production picker copied %q, want the full message", copied)
	}
	select {
	case complete := <-m.uiTaskCh:
		complete()
	default:
		t.Fatal("copy completion did not return to the UI loop")
	}
	if got := stripANSITest(strings.Join(m.chatContainer.Render(120), "\n")); !strings.Contains(got, "Copied selected message to clipboard") {
		t.Fatalf("copy confirmation: %q", got)
	}
}

type treeCaptureRenderer struct {
	*tui.TuiMainScreen
	capture func()
}

func (r *treeCaptureRenderer) Render()          { r.capture() }
func (r *treeCaptureRenderer) ForceFullRender() { r.capture() }

func TestTreeCopyCompletionPaintsBeforeDialogCloses(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	DefaultKeybindingsManager().syncToTUI()
	for _, editorSlot := range []bool{false, true} {
		t.Run(fmt.Sprint(editorSlot), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			session := upstreamTreeSession(t, "asst-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "copy text"))
			input := make(chan []byte, 2)
			input <- []byte("\x18")
			m := &InteractiveMode{opts: InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: session}}, chatContainer: tui.NewContainer(), editorContainer: tui.NewContainer(), editor: tui.NewEditor(), runCtx: ctx, backgroundCtx: ctx, uiTaskCh: make(chan func(), 1), modalInputCh: input, copyClipboard: func(string) error { return nil }}
			if editorSlot {
				m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
			}
			painted := false
			base := tui.NewWithOutput(io.Discard, 120, 40)
			base.SetRenderDispatcher(func(func()) {})
			t.Cleanup(base.CancelPendingRender)
			m.tuiInst = &treeCaptureRenderer{TuiMainScreen: base, capture: func() {
				if !painted && strings.Contains(stripANSITest(strings.Join(m.chatContainer.Render(120), "\n")), "Copied selected message to clipboard") {
					painted = true
					input <- []byte("\x1b")
				}
			}}
			finished := make(chan struct{})
			go func() { defer close(finished); m.buildSlashContext(ctx).PickTreeEntry("") }()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				input <- []byte("\x1b")
				<-finished
				cancel()
				m.backgroundTasks.Wait()
				t.Fatal("clipboard completion was stranded behind the open tree dialog")
			}
			m.backgroundTasks.Wait()
			if !painted {
				t.Fatal("dialog closed before copy confirmation painted")
			}
		})
	}
}

func findTreeByDown(t *testing.T, selector *tui.TreeSelectorComponent, want string) {
	t.Helper()
	for range 20 {
		selector.HandleInput("\x1b[B")
		if selectedTreeID(selector) == want {
			return
		}
	}
	assertTreeSelection(t, selector, want)
}

// Pi's /tree passes settingsManager.getTreeFilterMode() as the selector's initialFilterMode (interactive-mode.ts:5403, :5531). The production selector must open in that mode, with the initial selection falling back to the nearest visible ancestor.
func TestTreeSelectorOpensInConfiguredFilterMode(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	DefaultKeybindingsManager().syncToTUI()
	for _, tc := range []struct {
		mode, wantSelected string
		hidden, visible    string
	}{
		{"", "asst-2", "", "response"},
		{"user-only", "user-2", "response", "active branch"},
		{"labeled-only", "", "active branch", ""},
	} {
		t.Run("treeFilterMode="+tc.mode, func(t *testing.T) {
			session := upstreamTreeSession(t, "asst-2", simpleBranchEntries()...)
			m := &InteractiveMode{
				opts:    InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: session}, Settings: Settings{TreeFilterMode: tc.mode}},
				tuiInst: tui.NewWithOutput(io.Discard, 120, 40),
			}
			selector := m.newSessionTreeSelect(session.treeRoot(), "")
			assertTreeSelection(t, selector, tc.wantSelected)
			rendered := stripANSITest(strings.Join(selector.Render(200), "\n"))
			if tc.hidden != "" && strings.Contains(rendered, tc.hidden) {
				t.Fatalf("%q mode shows %q:\n%s", tc.mode, tc.hidden, rendered)
			}
			if tc.visible != "" && !strings.Contains(rendered, tc.visible) {
				t.Fatalf("%q mode hides %q:\n%s", tc.mode, tc.visible, rendered)
			}
		})
	}
}

// The production /tree path (PickTreeEntry) must open in the configured treeFilterMode: with "user-only" the assistant leaf is hidden, so Enter confirms the nearest visible ancestor (interactive-mode.ts:5403, :5531).
func TestPickTreeEntryUsesConfiguredFilterMode(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	DefaultKeybindingsManager().syncToTUI()
	for _, tc := range []struct{ mode, want string }{{"", "asst-2"}, {"user-only", "user-2"}} {
		t.Run("treeFilterMode="+tc.mode, func(t *testing.T) {
			session := upstreamTreeSession(t, "asst-2", simpleBranchEntries()...)
			renderer := tui.NewWithOutput(io.Discard, 120, 40)
			renderer.SetRenderDispatcher(func(func()) {})
			t.Cleanup(renderer.CancelPendingRender)
			m := &InteractiveMode{
				opts: InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: session},
					SettingsManager: &SettingsManager{merged: Settings{TreeFilterMode: tc.mode}}},
				tuiInst: renderer, chatContainer: tui.NewContainer(), backgroundCtx: t.Context(),
				uiTaskCh: make(chan func(), 1), modalInputCh: make(chan []byte, 1),
			}
			m.modalInputCh <- []byte("\r")
			if id, ok := m.buildSlashContext(t.Context()).PickTreeEntry(""); !ok || id != tc.want {
				t.Fatalf("PickTreeEntry = %q, %v; want %q", id, ok, tc.want)
			}
		})
	}
}
