package codingagent

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestUserMessageSelectorItemsExtractsUserMessagesOldestFirst(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-uniq", "")
	id1, _ := sess.AppendMessage(mkUserMsg("first user msg"))
	_, _ = sess.AppendMessage(mkAssistantMsg("assistant reply 1"))
	id3, _ := sess.AppendMessage(mkUserMsg("second user msg"))
	_, _ = sess.AppendMessage(mkAssistantMsg("assistant reply 2"))

	ids, labels := userMessageSelectorItems(sess)
	if len(ids) != 2 {
		t.Fatalf("expected 2 user messages, got %d", len(ids))
	}
	if ids[0] != id1 || ids[1] != id3 {
		t.Errorf("ids order wrong: got %v want [id1=%s id3=%s]", ids, id1, id3)
	}
	if labels[0] != "first user msg" {
		t.Errorf("labels[0] = %q, want %q", labels[0], "first user msg")
	}
	if labels[1] != "second user msg" {
		t.Errorf("labels[1] = %q, want %q", labels[1], "second user msg")
	}
}

func TestUserMessageSelectorItemsEmptySession(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-empty-msg", "")
	ids, labels := userMessageSelectorItems(sess)
	if len(ids) != 0 || len(labels) != 0 {
		t.Errorf("expected empty slices, got %d ids / %d labels", len(ids), len(labels))
	}
}

func TestTreeNodeAdapterLabelsAndChildren(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-adapter", "")
	id1, _ := sess.AppendMessage(mkUserMsg("hello"))
	_ = sess.Fork(id1)
	_, _ = sess.AppendMessage(mkUserMsg("branch-a-msg"))

	root := sess.Tree()
	if root == nil || len(root.Children) == 0 {
		t.Fatal("expected non-empty tree")
	}
	// 3.1d-e: label format is upstream-style "user: <preview>" -
	// no id prefix, no timestamp, no " · " separator. Connector
	// glyphs come from tui.TreeSelect.flatten, not NodeLabel.
	a := &treeNodeAdapter{n: root, f: newTreeRowFormatter(sess)}
	kids := a.NodeChildren()
	if len(kids) != 1 {
		t.Fatalf("expected 1 root child, got %d", len(kids))
	}
	first := kids[0]
	if first.NodeID() != id1 {
		t.Errorf("NodeID=%q want %q", first.NodeID(), id1)
	}
	lbl := first.NodeLabel()
	wantLbl := fg(tui.ActiveTheme().Accent, "user: ") + "hello"
	if lbl != wantLbl {
		t.Errorf("label = %q, want %q", lbl, wantLbl)
	}
	// One child of the first user message (the branch reply).
	subKids := first.NodeChildren()
	if len(subKids) != 1 {
		t.Errorf("expected 1 child, got %d", len(subKids))
	}
	wantChild := fg(tui.ActiveTheme().Accent, "user: ") + "branch-a-msg"
	if subKids[0].NodeLabel() != wantChild {
		t.Errorf("child label = %q, want %q", subKids[0].NodeLabel(), wantChild)
	}
}

func TestSlashHandlerForkPrefersInteractivePicker(t *testing.T) {
	sc, out := newFakeSlashCtx()
	pickerCalled := false
	sc.PickUserMessage = func() (string, bool) {
		pickerCalled = true
		return "picked-id", true
	}
	forkCalledWith := ""
	sc.ForkToNewSession = func(id string) error {
		forkCalledWith = id
		return nil
	}
	sc.Args = "" // bare /fork: should trigger picker, not usage hint.
	if err := forkHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !pickerCalled {
		t.Errorf("picker should be invoked for bare /fork")
	}
	if forkCalledWith != "picked-id" {
		t.Errorf("ForkToNewSession called with %q want picked-id", forkCalledWith)
	}
	if !strings.Contains(out.String(), "Forked") {
		t.Errorf("missing confirmation: %q", out.String())
	}
}

func TestSlashHandlerForkPickerCancelMessage(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.PickUserMessage = func() (string, bool) { return "", false }
	called := false
	sc.ForkToNewSession = func(string) error { called = true; return nil }
	if err := forkHandler(sc); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Errorf("ForkToNewSession should not run on cancel")
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("missing cancel message: %q", out.String())
	}
}

func TestSlashHandlerResumePrefersInteractivePicker(t *testing.T) {
	sc, out := newFakeSlashCtx()
	pickerCalled := false
	sc.PickSession = func() (string, bool) {
		pickerCalled = true
		return "/tmp/sess.jsonl", true
	}
	loadCalledWith := ""
	sc.LoadSessionPath = func(p string) error {
		loadCalledWith = p
		return nil
	}
	if err := resumeHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !pickerCalled {
		t.Errorf("PickSession should be invoked")
	}
	if loadCalledWith != "/tmp/sess.jsonl" {
		t.Errorf("LoadSessionPath called with %q", loadCalledWith)
	}
	if !strings.Contains(out.String(), "Resumed") {
		t.Errorf("missing confirmation: %q", out.String())
	}
}

func TestSlashHandlerTreePrefersInteractivePicker(t *testing.T) {
	// Non-leaf navigation case: picked id != current leaf, so we fork +
	// emit the upstream literal status "Navigated to selected point"
	// (interactive-mode.ts:4083-4090). No "Forked at": that was a
	// 3.1d-b overstep, reverted in 3.1d-b'.
	sc, out := newFakeSlashCtx()
	sc.PickTreeEntry = func(string) (string, bool) { return "tree-pick-id", true }
	forkedWith := ""
	sc.ForkAtEntry = func(id string) error { forkedWith = id; return nil }
	if err := treeHandler(sc); err != nil {
		t.Fatal(err)
	}
	if forkedWith != "tree-pick-id" {
		t.Errorf("/tree pick should fork at selected id; got %q", forkedWith)
	}
	if !strings.Contains(out.String(), "Navigated to selected point") {
		t.Errorf("expected 'Navigated to selected point' confirmation: %q", out.String())
	}
}

func TestSlashHandlerTreeLeafIsNoOp(t *testing.T) {
	// Leaf case: picked id == current leaf id → no fork, status is
	// "Already at this point" (interactive-mode.ts:4083-4090).
	sc, out := newFakeSlashCtx()
	sess := NewSession("sess-leaf-test", "")
	leafID, err := sess.AppendMessage(mkUserMsg("hello"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	sc.CurrentSession = func() *Session { return sess }
	sc.PickTreeEntry = func(string) (string, bool) { return leafID, true }
	forkCalled := false
	sc.ForkAtEntry = func(id string) error { forkCalled = true; return nil }
	if err := treeHandler(sc); err != nil {
		t.Fatal(err)
	}
	if forkCalled {
		t.Errorf("ForkAtEntry should NOT be called when picking current leaf")
	}
	if !strings.Contains(out.String(), "Already at this point") {
		t.Errorf("expected 'Already at this point' confirmation: %q", out.String())
	}
}

// c: when ShowStatus is wired, treeHandler should prefer
// the status-line flash over chat-history Append for navigation
// feedback (mirrors upstream `showStatus` calls at
// interactive-mode.ts:4083 / :4163). Asserts the status text reaches
// the flash sink and does NOT pollute the chat output.
func TestSlashHandlerTreePrefersFlashOverAppend(t *testing.T) {
	sc, out := newFakeSlashCtx()
	flashed := ""
	sc.ShowStatus = func(msg string) { flashed = msg }
	sess := NewSession("sess-flash", "")
	leafID, err := sess.AppendMessage(mkUserMsg("hi"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	sc.CurrentSession = func() *Session { return sess }
	sc.PickTreeEntry = func(string) (string, bool) { return leafID, true }
	sc.ForkAtEntry = func(string) error { t.Fatal("fork should not run on leaf"); return nil }
	if err := treeHandler(sc); err != nil {
		t.Fatal(err)
	}
	if flashed != "Already at this point" {
		t.Errorf("flash got %q want 'Already at this point'", flashed)
	}
	if strings.Contains(out.String(), "Already at this point") {
		t.Errorf("status text leaked into chat: %q", out.String())
	}
}

// c: empty session → upstream flashes "No entries in session"
// instead of opening an empty picker (interactive-mode.ts:4068-4072).
func TestSlashHandlerTreeEmptySessionFlashesStatus(t *testing.T) {
	sc, out := newFakeSlashCtx()
	flashed := ""
	sc.ShowStatus = func(msg string) { flashed = msg }
	sess := NewSession("sess-empty", "") // no entries
	sc.CurrentSession = func() *Session { return sess }
	pickerCalled := false
	sc.PickTreeEntry = func(string) (string, bool) { pickerCalled = true; return "", false }
	if err := treeHandler(sc); err != nil {
		t.Fatal(err)
	}
	if pickerCalled {
		t.Errorf("PickTreeEntry should not be invoked when session is empty")
	}
	if flashed != "No entries in session" {
		t.Errorf("flash got %q want 'No entries in session'", flashed)
	}
	if out.String() != "" {
		t.Errorf("chat should be untouched on empty-session flash: %q", out.String())
	}
}

// c: picker cancel is silent (mirrors upstream onCancel at
// interactive-mode.ts:4181-4184: just `done()` + `requestRender()`,
// no message). The previous "Tree closed." chat append was a pig
// addition, dropped here.
func TestSlashHandlerTreeCancelIsSilent(t *testing.T) {
	sc, out := newFakeSlashCtx()
	flashed := ""
	sc.ShowStatus = func(msg string) { flashed = msg }
	sess := NewSession("sess-cancel", "")
	if _, err := sess.AppendMessage(mkUserMsg("hi")); err != nil {
		t.Fatalf("append: %v", err)
	}
	sc.CurrentSession = func() *Session { return sess }
	sc.PickTreeEntry = func(string) (string, bool) { return "", false } // user cancelled
	if err := treeHandler(sc); err != nil {
		t.Fatal(err)
	}
	if flashed != "" {
		t.Errorf("cancel should not flash: %q", flashed)
	}
	if out.String() != "" {
		t.Errorf("cancel should be silent in chat: %q", out.String())
	}
}

// j: tool-call-only assistant messages are hidden from the
// /tree picker (mirrors upstream tree-selector.ts:287-296). The
// suppression happens at the treeNodeAdapter.NodeChildren layer:
// suppressed nodes are skipped and their visible children are pulled
// up to the suppressed node's slot. The current leaf is exempt so the
// active position is always visible.
func TestTreeNodeAdapterSuppressesToolCallOnlyAssistant(t *testing.T) {
	sess := NewSession("sess-suppress", "")

	// 1. user
	if _, err := sess.AppendMessage(mkUserMsg("read AGENTS.md")); err != nil {
		t.Fatalf("append user: %v", err)
	}
	// 2. assistant with ONLY a tool_use block (no text) → SUPPRESS
	asstToolID, err := sess.AppendMessage(agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role: "assistant",
			Content: []ai.AssistantContentBlock{
				ai.ToolCall{ID: "tu-1", Name: "read", Arguments: ai.JsonObject{"path": "AGENTS.md"}},
			},
			Timestamp: time.Now().UnixMilli(),
		},
	})
	if err != nil {
		t.Fatalf("append asst-tool-only: %v", err)
	}
	// 3. tool result
	if _, err := sess.AppendMessage(agent.AgentMessage{
		ToolResult: &agent.ToolResultMessage{
			Role: agent.RoleToolResult, ToolCallID: "tu-1", ToolName: "read",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file body"}},
		},
	}); err != nil {
		t.Fatalf("append tr: %v", err)
	}
	// 4. assistant with text → KEEP
	if _, err := sess.AppendMessage(mkAssistantMsg("found it")); err != nil {
		t.Fatalf("append asst-text: %v", err)
	}

	root := sess.Tree()
	if root == nil {
		t.Fatal("nil tree")
	}
	a := &treeNodeAdapter{n: root, f: newTreeRowFormatter(sess)}

	// Walk the visible tree depth-first and collect labels.
	var labels []string
	var walk func(node *treeNodeAdapter)
	walk = func(node *treeNodeAdapter) {
		for _, c := range node.NodeChildren() {
			ad, ok := c.(*treeNodeAdapter)
			if !ok {
				continue
			}
			labels = append(labels, ad.NodeLabel())
			walk(ad)
		}
	}
	walk(a)

	// Expect 3 visible rows: user, [read:...], assistant: found it.
	// The asst-tool-only row is suppressed; its tool_result child is
	// pulled up so its grandchild (the asst-text) is reachable.
	if len(labels) != 3 {
		t.Fatalf("visible rows = %d %v, want 3 (user, [read:AGENTS.md], assistant: found it)", len(labels), labels)
	}
	if !strings.HasPrefix(stripANSI(labels[0]), "user: ") {
		t.Errorf("row 0 = %q want 'user: ...'", labels[0])
	}
	if !strings.HasPrefix(stripANSI(labels[1]), "[read: ") {
		t.Errorf("row 1 = %q want '[read: ...]' (tool_result, parent asst-tool-only suppressed)", labels[1])
	}
	if stripANSI(labels[2]) != "assistant: found it" {
		t.Errorf("row 2 = %q want 'assistant: found it'", labels[2])
	}
	for _, l := range labels {
		if strings.Contains(stripANSI(l), "(no content)") {
			t.Errorf("found (no content) row %q: should be suppressed", l)
		}
	}

	// Now point the leaf AT the asst-tool-only entry. It MUST become
	// visible (escape clause for current leaf).
	if err := sess.SetLeafID(&asstToolID); err != nil {
		t.Fatalf("SetLeafID: %v", err)
	}
	a2 := &treeNodeAdapter{n: sess.Tree(), f: newTreeRowFormatter(sess)}
	var labels2 []string
	var walk2 func(node *treeNodeAdapter)
	walk2 = func(node *treeNodeAdapter) {
		for _, c := range node.NodeChildren() {
			ad, ok := c.(*treeNodeAdapter)
			if !ok {
				continue
			}
			labels2 = append(labels2, ad.NodeLabel())
			walk2(ad)
		}
	}
	walk2(a2)
	foundNoContent := false
	for _, l := range labels2 {
		if strings.Contains(stripANSI(l), "(no content)") {
			foundNoContent = true
			break
		}
	}
	if !foundNoContent {
		t.Errorf("leaf-pointed-at-asst-tool-only: expected an 'assistant: (no content)' row to remain visible (current-leaf escape); got %v", labels2)
	}
}

// Pi 0.87.1 themeItems marks the current theme with a "✓ " prefix column
// (settings/04-settings-theme-submenu probe).
func TestThemeSubmenuMarksCurrentTheme(t *testing.T) {
	sel := tui.NewSelectSubmenu("Theme", "Select a theme, or choose Automatic to follow terminal appearance.",
		themeSelectItemsWithAutomatic([]string{"dark", "light"}, "dark"), "dark")
	var rows []string
	for _, line := range sel.Render(100) {
		plain := strings.TrimRight(stripANSI(line), " ")
		if strings.HasPrefix(plain, "    ") || strings.HasPrefix(plain, "→") {
			rows = append(rows, plain)
		}
	}
	want := []string{
		"    Automatic  Use separate themes for light and dark terminal appearance",
		"→ ✓ dark",
		"    light",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("theme rows =\n%s\nwant\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	light := themeSelectItems([]string{"dark", "light"}, "light")
	if light[0].Label != "  dark" || light[1].Label != "✓ light" {
		t.Fatalf("light theme items = %+v", light)
	}
}

// SettingsSelectorComponent frames the settings list and its submenus with
// DynamicBorders (settings-selector.ts).
func TestSettingsFrameDrawsBordersAroundContent(t *testing.T) {
	sl := tui.NewSettingsList([]tui.SettingItem{{ID: "a", Label: "Auto-compact", CurrentValue: "true", Values: []string{"true", "false"}}})
	lines := settingsFrame(sl).Render(40)
	border := strings.Repeat("─", 40)
	if stripANSI(lines[0]) != border || stripANSI(lines[len(lines)-1]) != border {
		t.Fatalf("frame rows = %q", lines)
	}
	if got := strings.TrimRight(stripANSI(lines[1]), " "); got != ">" {
		t.Fatalf("first framed row = %q, want the search input", got)
	}
}
