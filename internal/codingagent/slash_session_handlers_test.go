package codingagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	iexport "github.com/MichaelKinsy/PiG/internal/codingagent/export"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

type exportTestComponent struct{ lines []string }

func (c exportTestComponent) Render(width int) []string { return c.lines }
func (exportTestComponent) Invalidate()                 {}

// fakeSlashRunner captures Append output so handlers can be tested
// without running the full TUI.
func newFakeSlashCtx() (*SlashContext, *strings.Builder) {
	var out strings.Builder
	sc := &SlashContext{
		Append: func(s string) {
			out.WriteString(s)
			out.WriteByte('\n')
		},
	}
	return sc, &out
}

func TestDebugHandler_WritesLogAndConfirms(t *testing.T) {
	sc, out := newFakeSlashCtx()
	called := false
	sc.WriteDebugLog = func() (string, error) { called = true; return "/tmp/pig-debug.log", nil }
	if err := debugHandler(sc); err != nil {
		t.Fatalf("debugHandler: %v", err)
	}
	if !called {
		t.Fatal("debugHandler did not call WriteDebugLog")
	}
	got := out.String()
	if !strings.Contains(got, "Debug log written") || !strings.Contains(got, "/tmp/pig-debug.log") {
		t.Errorf("confirmation missing path/label; got %q", got)
	}
}

func TestDebugHandler_Unavailable(t *testing.T) {
	sc, out := newFakeSlashCtx() // WriteDebugLog nil
	if err := debugHandler(sc); err != nil {
		t.Fatalf("debugHandler: %v", err)
	}
	if !strings.Contains(out.String(), "Debug unavailable") {
		t.Errorf("expected unavailable message; got %q", out.String())
	}
}

// /debug is dispatchable but hidden from /help and autocomplete, matching
// upstream (absent from the canonical slash-commands.js completion list).
func TestSlashRegistry_DebugHiddenFromCompletions(t *testing.T) {
	r := NewSlashRegistry()
	if _, ok := r.Resolve("debug"); !ok {
		t.Fatal("/debug must resolve (be dispatchable)")
	}
	for _, c := range r.All() {
		if c.Name == "debug" {
			t.Fatal("/debug must be hidden from All() (help/autocomplete)")
		}
	}
}

// TestDebugHandler_WritesLogAndConfirms covers the handler contract with an
// injected writer; TestInteractiveMode_WriteDebugLog drives the real
// production method end to end.
func TestSlashFork_NoArgPrintsUsage(t *testing.T) {
	sc, out := newFakeSlashCtx()
	called := false
	sc.ForkToNewSession = func(string) error { called = true; return nil }
	sc.Args = ""
	if err := forkHandler(sc); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Errorf("ForkToNewSession should not be called with empty args")
	}
	if !strings.Contains(out.String(), "Usage: /fork") {
		t.Errorf("usage missing: %q", out.String())
	}
}

func TestSlashFork_CallsForkToNewSession(t *testing.T) {
	sc, out := newFakeSlashCtx()
	var got string
	sc.ForkToNewSession = func(id string) error { got = id; return nil }
	sc.Args = "abc123"
	if err := forkHandler(sc); err != nil {
		t.Fatal(err)
	}
	if got != "abc123" {
		t.Errorf("ForkToNewSession called with %q", got)
	}
	if !strings.Contains(out.String(), "Forked to new session") {
		t.Errorf("confirmation message: %q", out.String())
	}
}

func TestSlashClone_CallsCloneCurrent(t *testing.T) {
	sc, out := newFakeSlashCtx()
	called := false
	sc.CloneCurrent = func() (string, error) {
		called = true
		return "/tmp/cloned.jsonl", nil
	}
	if err := cloneHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Errorf("CloneCurrent not called")
	}
	if out.String() != "Cloned to new session\n" {
		t.Errorf("clone status: %q", out.String())
	}
}

// Ports packages/coding-agent/test/interactive-mode-clone-command.test.ts:23,51.
func TestCloneCommandUpstream(t *testing.T) {
	for _, tc := range []struct {
		name       string
		leaf       *string
		wantCalls  int
		wantEditor []string
		wantStatus string
	}{
		{"clones the current leaf into a new session", new("leaf-123"), 1, []string{""}, "Cloned to new session"},
		{"shows a status message when there is nothing to clone", nil, 0, nil, "Nothing to clone yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, clears := 0, 0
			var editor, status, unexpected []string
			session := &Session{leafID: tc.leaf}
			sc := &SlashContext{
				CurrentSession: func() *Session { return session },
				CloneCurrent:   func() (string, error) { calls++; return "/new/session.jsonl", nil },
				SetEditorText:  func(text string) { editor = append(editor, text) },
				ShowStatus:     func(text string) { status = append(status, text) },
				Append:         func(text string) { unexpected = append(unexpected, text) },
				Clear:          func() { clears++ },
			}
			if err := cloneHandler(sc); err != nil {
				t.Fatal(err)
			}
			if calls != tc.wantCalls || clears != 0 || !slices.Equal(editor, tc.wantEditor) || !slices.Equal(status, []string{tc.wantStatus}) || len(unexpected) != 0 {
				t.Fatalf("clone=%d clears=%d editor=%q status=%q unexpected=%q", calls, clears, editor, status, unexpected)
			}
		})
	}
}

func TestSlashName_RequiresArg(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return &Session{} }
	sc.SetSessionName = func(string) error { return nil }
	if err := nameHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Usage: /name") {
		t.Errorf("usage line missing: %q", out.String())
	}
}

func TestSlashName_EmptyArgsShowsCurrentName(t *testing.T) {
	// bare `/name` with a name already set surfaces it dim-styled.
	// Mirrors upstream interactive-mode.ts:4737-4748.
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return &Session{} }
	sc.GetSessionName = func() string { return "my project" }
	sc.SetSessionName = func(string) error {
		t.Fatalf("SetSessionName should not be called with empty args")
		return nil
	}
	if err := nameHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Session name: my project") {
		t.Errorf("expected current-name display, got %q", out.String())
	}
	if strings.Contains(out.String(), "Usage:") {
		t.Errorf("usage hint should be suppressed when name is set: %q", out.String())
	}
}

func TestSlashName_AppendsName(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return &Session{} }
	var got string
	sc.SetSessionName = func(s string) error { got = s; return nil }
	sc.Args = "  my session  "
	if err := nameHandler(sc); err != nil {
		t.Fatal(err)
	}
	if got != "my session" {
		t.Errorf("got=%q want trimmed", got)
	}
	// Match upstream verbatim (interactive-mode.ts:4754).
	if !strings.Contains(out.String(), "Session name set: my session") {
		t.Errorf("confirmation missing: %q", out.String())
	}
}

func TestSlashReloadExplainIncludesDiagnostics(t *testing.T) {
	sc, out := newFakeSlashCtx()
	reloaded := false
	sc.Args = "--explain"
	sc.Reload = func() error { reloaded = true; return nil }
	sc.ReloadDiagnostics = func() ReloadDiag {
		return ReloadDiag{ContextFiles: 1, Skills: 2, Prompts: 3, Extensions: 4, Themes: 5}
	}
	if err := reloadHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !reloaded {
		t.Fatal("Reload was not called")
	}
	s := out.String()
	for _, want := range []string{"Reload explanation:", "context files: 1", "skills: 2", "prompts: 3", "extensions: 4", "themes: 5", "each extension reloads on its own"} {
		if !strings.Contains(s, want) {
			t.Fatalf("output missing %q:\n%s", want, s)
		}
	}
}

// TestSlashReloadExplainRendersCellPlacement verifies that /reload --explain
// renders the placement decisions surfaced via ReloadDiag.Cells: strategy,
// members, cache state, build duration, quarantine status, and reasons.
func TestSlashReloadExplainRendersCellPlacement(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.Args = "--explain"
	sc.Reload = func() error { return nil }
	sc.ReloadDiagnostics = func() ReloadDiag {
		return ReloadDiag{
			ContextFiles:   0,
			Skills:         0,
			Prompts:        0,
			Extensions:     3,
			Themes:         0,
			ReloadDuration: 250 * time.Millisecond,
			Cells: []ReloadCellDiag{
				{
					Key:        "isolated:ghost",
					Strategy:   "isolated",
					Extensions: []string{"ghost"},
					BinaryPath: "/tmp/ghost",
					Reason:     "isolated subprocess (source/command)",
				},
				{
					Key:           "packed-go:abcd",
					Strategy:      "packed-go",
					Language:      "go",
					Extensions:    []string{"context-info", "subagent"},
					Hash:          "abcdef1234567890aaaaaaaaaaaa",
					BinaryPath:    "/tmp/runner",
					Cached:        false,
					BuildDuration: 800 * time.Millisecond,
					Reason:        "go factory packed (shared-ok)",
				},
				{
					Key:         "quarantined:dead",
					Strategy:    "isolated",
					Quarantined: true,
					Reason:      "fissioned (quarantined): boom",
				},
			},
		}
	}
	if err := reloadHandler(sc); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	want := []string{
		"placement:",
		"isolated/",
		"ghost",
		"packed-go/",
		"context-info+subagent",
		"cold build 800ms",
		"go factory packed",
		"quarantined\u2192fissioned",
		"subprocess reload wall: 250ms",
	}
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Fatalf("output missing %q:\n%s", w, s)
		}
	}
}

func TestSlashResume_ListsSessions(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.ListSessions = func() ([]SessionInfo, error) {
		return []SessionInfo{
			{ID: "sess-aaa", MessageCount: 3, FirstMessage: "hello world"},
			{ID: "sess-bbb", MessageCount: 1, Name: "named one"},
		}, nil
	}
	if err := resumeHandler(sc); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "sess-aaa") || !strings.Contains(s, "sess-bbb") {
		t.Errorf("ids missing: %q", s)
	}
	if !strings.Contains(s, "hello world") {
		t.Errorf("first message preview missing")
	}
	if !strings.Contains(s, "named one") {
		t.Errorf("user-set name missing")
	}
	if !strings.Contains(s, "--session <id>") {
		t.Errorf("relaunch hint missing")
	}
}

func TestSlashResume_EmptyDir(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.ListSessions = func() ([]SessionInfo, error) { return nil, nil }
	if err := resumeHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No sessions") {
		t.Errorf("empty-dir message missing: %q", out.String())
	}
}

func TestSlashTree_RendersAndIncludesIDs(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-tree-render", "")
	id1, _ := sess.AppendMessage(mkUserMsg("first"))
	_ = sess.Branch(id1)
	_, _ = sess.AppendMessage(mkUserMsg("branch-a"))
	_ = sess.Branch(id1)
	_, _ = sess.AppendMessage(mkUserMsg("branch-b"))

	out := renderTreeASCII(sess.treeRoot())
	if !strings.Contains(out, "first") {
		t.Errorf("root entry missing from tree: %s", out)
	}
	if !strings.Contains(out, "branch-a") || !strings.Contains(out, "branch-b") {
		t.Errorf("branches missing: %s", out)
	}
	// Tree connectors present.
	if !strings.Contains(out, "├─") && !strings.Contains(out, "└─") {
		t.Errorf("expected branch connectors in tree: %s", out)
	}
}

func TestSlashTree_DispatchesViaHandler(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.RenderTree = func() string { return "├─ abc · 12:00:00 · user · hello\n" }
	if err := treeHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "abc") {
		t.Errorf("tree text missing: %q", out.String())
	}
	if !strings.Contains(out.String(), "```") {
		t.Errorf("expected code-fence wrapping for monospace tree: %q", out.String())
	}
}

// settingsRowsFor returns the selector's rows for the settings sm holds, by id and in order.
func settingsRowsFor(t *testing.T, sm *SettingsManager) (map[string]tui.SettingItem, []string) {
	t.Helper()
	config, err := settingsConfig(&SlashContext{SettingsManager: sm})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]tui.SettingItem{}
	var ids []string
	for _, item := range NewSettingsSelectorComponent(config, SettingsCallbacks{}).GetSettingsList().Items() {
		rows[item.ID] = item
		ids = append(ids, item.ID)
	}
	return rows, ids
}

func TestSettingsItems_UpstreamCoreRosterPresentAndOrdered(t *testing.T) {
	prev := tuiCapabilitiesForTest()
	defer prev.restore()
	tuiSetCapsForTest(true)
	_, ids := settingsRowsFor(t, NewSettingsManager(t.TempDir(), t.TempDir()))

	wantPrefix := []string{
		"autocompact",
		"show-images",
		"image-width-cells",
		"auto-resize-images",
		"block-images",
		"skill-commands",
		"show-hardware-cursor",
		"editor-padding",
		"output-padding",
		"autocomplete-max-visible",
		"clear-on-shrink",
		"terminal-progress",
		"steering-mode",
		"follow-up-mode",
		"transport",
		"http-idle-timeout",
	}
	if len(ids) < len(wantPrefix) || !slices.Equal(ids[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("settings rows prefix = %v, want prefix %v", ids, wantPrefix)
	}

	for _, id := range []string{
		"hide-thinking",
		"mermaid-rendering",
		"collapse-changelog",
		"quiet-startup",
		"install-telemetry",
		"double-escape-action",
		"tree-filter-mode",
		"warnings",
		"model-thinking",
		"tui-mode",
		"fullscreen-exit-output",
		"fullscreen-scrollbar",
		"fullscreen-copy-on-select",
		"fullscreen-wheel-scroll-lines",
		"theme",
	} {
		if !slices.Contains(ids, id) {
			t.Fatalf("settings rows missing %q in %v", id, ids)
		}
	}

	wantSuffix := []string{"warnings", "model-thinking", "tui-mode", "fullscreen-exit-output", "fullscreen-scrollbar", "fullscreen-copy-on-select", "fullscreen-wheel-scroll-lines", "theme"}
	if len(ids) < len(wantSuffix) || !slices.Equal(ids[len(ids)-len(wantSuffix):], wantSuffix) {
		t.Fatalf("settings rows suffix = %v, want %v", ids, wantSuffix)
	}
}

func TestSettingsItems_UpstreamDefaults(t *testing.T) {
	lookup, _ := settingsRowsFor(t, NewSettingsManager(t.TempDir(), t.TempDir()))
	for id, want := range map[string]string{
		"mermaid-rendering":         "streaming",
		"tui-mode":                  "fullscreen",
		"fullscreen-exit-output":    "transcript",
		"fullscreen-scrollbar":      "auto",
		"fullscreen-copy-on-select": "true",
		"transport":                 "auto",
		"auto-resize-images":        "true",
		"show-hardware-cursor":      "false",
		"editor-padding":            "0",
		"output-padding":            "1",
		"autocomplete-max-visible":  "5",
		"clear-on-shrink":           "false",
		"terminal-progress":         "false",
		"install-telemetry":         "true",
	} {
		if got := lookup[id].CurrentValue; got != want {
			t.Fatalf("%s default = %q, want %q", id, got, want)
		}
	}
}

func TestSettingsItems_TransportIncludesWebsocketCached(t *testing.T) {
	lookup, _ := settingsRowsFor(t, NewSettingsManager(t.TempDir(), t.TempDir()))
	got := lookup["transport"].Values
	want := []string{"sse", "websocket", "websocket-cached", "auto"}
	if !slices.Equal(got, want) {
		t.Fatalf("transport values = %v, want %v", got, want)
	}
}

func TestSettingsItems_HTTPIdleTimeoutChoicesAndDefaults(t *testing.T) {
	lookup, _ := settingsRowsFor(t, NewSettingsManager(t.TempDir(), t.TempDir()))

	got := lookup["http-idle-timeout"].Values
	want := []string{"30 sec", "1 min", "2 min", "5 min", "disabled"}
	if !slices.Equal(got, want) {
		t.Fatalf("http-idle-timeout values = %v, want %v", got, want)
	}
	if got := lookup["http-idle-timeout"].CurrentValue; got != "5 min" {
		t.Fatalf("http-idle-timeout default = %q, want 5 min", got)
	}
}

// settings-selector.ts:onChange http-idle-timeout: a cycled label reaches the callback as its timeout in milliseconds.
// Pi: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:113 (SettingsCallbacks.onHttpIdleTimeoutMsChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:72 (SettingsConfig.httpIdleTimeoutMs).
func TestSettingsItems_HTTPIdleTimeoutChangeReportsTimeoutMs(t *testing.T) {
	var got []int
	list := NewSettingsSelectorComponent(SettingsConfig{HttpIdleTimeoutMs: 300_000}, SettingsCallbacks{OnHttpIdleTimeoutMsChange: func(timeoutMs int) { got = append(got, timeoutMs) }}).GetSettingsList()
	list.SelectItem("http-idle-timeout")
	list.HandleInput("\r")
	list.HandleInput("\r")
	if want := []int{0, 30_000}; !slices.Equal(got, want) {
		t.Fatalf("timeouts = %v, want %v", got, want)
	}
}

func TestSettingsItems_FollowUpModeDescriptionUsesResolvedKeyDisplayText(t *testing.T) {
	_ = DefaultKeybindingsManager()
	got := settingsSelectorRow(t, SettingsConfig{}, "follow-up-mode").Description
	wantPrefix := tui.ActionKeyDisplayText("app.message.followUp") + " queues follow-up messages until agent stops."
	if !strings.Contains(got, wantPrefix) {
		t.Fatalf("follow-up-mode description = %q, want prefix %q", got, wantPrefix)
	}
}
func TestSettingsWarningsRoundTrip(t *testing.T) {
	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	if got := sm.GetWarnings(); !got.AnthropicExtraUsage {
		t.Fatalf("default warnings = %+v, want anthropicExtraUsage true", got)
	}
	if err := sm.SetWarnings(WarningSettings{AnthropicExtraUsage: false}); err != nil {
		t.Fatal(err)
	}
	got := sm.GetWarnings()
	if got.AnthropicExtraUsage {
		t.Fatalf("warnings after SetWarnings = %+v, want anthropicExtraUsage false", got)
	}
}

func TestAnthropicExtraUsageWarningEnabled(t *testing.T) {
	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	if !anthropicExtraUsageWarningEnabled(sm) {
		t.Fatal("warning gate should default true")
	}
	if err := sm.SetWarnings(WarningSettings{AnthropicExtraUsage: false}); err != nil {
		t.Fatal(err)
	}
	if anthropicExtraUsageWarningEnabled(sm) {
		t.Fatal("warning gate should be false after disabling warning")
	}
}

func TestSettingsItems_ImageCapabilityGating(t *testing.T) {
	// Force capabilities so the test is deterministic regardless of terminal.
	prev := tuiCapabilitiesForTest()
	defer prev.restore()
	sm := NewSettingsManager(t.TempDir(), t.TempDir())

	tuiSetCapsForTest(false)
	rows, _ := settingsRowsFor(t, sm)
	for _, id := range []string{"show-images", "image-width-cells"} {
		if _, ok := rows[id]; ok {
			t.Fatalf("expected image items to be gated out when terminal lacks images, got %q", id)
		}
	}

	tuiSetCapsForTest(true)
	rows, _ = settingsRowsFor(t, sm)
	for _, id := range []string{"show-images", "image-width-cells"} {
		if _, ok := rows[id]; !ok {
			t.Fatalf("expected %s when terminal supports images", id)
		}
	}
}

func TestSettingsItems_TerminalProgressAlwaysVisible(t *testing.T) {
	prev := tuiCapabilitiesForTest()
	defer prev.restore()
	sm := NewSettingsManager(t.TempDir(), t.TempDir())

	// Upstream settings-selector.ts:438 inserts terminal-progress
	// unconditionally; the OSC 9;4 capability check only affects
	// rendering, not menu visibility. Pi v0.71.0 confirms the toggle
	// always appears in the settings selector even when the terminal
	// does not support OSC 9;4.
	for _, images := range []bool{false, true} {
		tuiSetCapsForTest(images)
		if rows, _ := settingsRowsFor(t, sm); rows["terminal-progress"].ID == "" {
			t.Fatalf("expected terminal-progress with images=%v", images)
		}
	}
}

func TestSetSessionNamePersistsToDisk(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-named", "")
	_, _ = sess.AppendMessage(mkUserMsg("hi"))

	// Mimic the closure in buildSlashContext.
	id, _ := generateEntryID()
	parent := sess.GetLeafID()
	entry := SessionInfoEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "session_info",
			ID:        id,
			ParentID:  parent,
			Timestamp: "2026-04-29T19:00:00Z",
		},
		Name: "my project",
	}
	if err := sess.AppendEntry(entry); err != nil {
		t.Fatal(err)
	}
	flushSession(t, sess)

	// Reload and verify the name surfaces in SessionInfo.
	sm2 := NewSessionManagerWithDir(sm.cwd, sm.sessionDir)
	infos, err := sm2.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "my project" {
		t.Errorf("name not persisted: %#v", infos)
	}
}

// ─── 3.2h: /compact handler tests ────────────────────────────────────────────

// Pi 0.87.1 interactive-mode.ts:6822-6829 delegates empty Sessions to compact; the Session emits the failure event.
func TestCompactHandlerDelegatesEmptySession(t *testing.T) {
	sc, out := newFakeSlashCtx()
	compactCalled := false
	sc.CompactSession = func(instructions string) error {
		compactCalled = true
		return nil
	}
	// Wire SessionInfo to return msgCount=1 (below the guard threshold).
	sc.SessionInfo = func() (id, dir string, msgCount int) {
		return "sess-1", "/tmp", 1
	}
	sc.Args = ""

	if err := compactHandler(sc); err != nil {
		t.Fatalf("compactHandler: %v", err)
	}
	if !compactCalled {
		t.Error("CompactSession must be called for an empty Session")
	}
	if got := out.String(); got != "" {
		t.Errorf("handler must leave error display to Session events: %q", got)
	}
}

func TestCompactHandlerDoesNotInventEmptyWarning(t *testing.T) {
	sc, _ := newFakeSlashCtx()
	sc.SessionInfo = func() (string, string, int) { return "sess-1", "/tmp", 1 }
	var warning string
	sc.ShowWarning = func(message string) { warning = message }

	if err := compactHandler(sc); err != nil {
		t.Fatal(err)
	}
	if warning != "" {
		t.Fatalf("warning = %q", warning)
	}
}

// TestCompactHandlerInvokes verifies that /compact with ≥ 2 messages calls
// CompactSession with the correct customInstructions.
func TestCompactHandlerInvokes(t *testing.T) {
	cases := []struct {
		name        string
		args        string
		wantInstr   string
		msgCount    int
		wantCompact bool
	}{
		{name: "no custom instructions", args: "", wantInstr: "", msgCount: 3, wantCompact: true},
		{name: "with custom instructions", args: "custom focus here", wantInstr: "custom focus here", msgCount: 3, wantCompact: true},
		{name: "exactly 2 messages: at threshold", args: "", wantInstr: "", msgCount: 2, wantCompact: true},
		{name: "one message delegates to Session", args: "", msgCount: 1, wantCompact: true},
		// Regression: the guard must count message-type *entries* (upstream
		// getEntries().filter(type===message)), not the live agent context.
		// A long, already-compacted session whose live context is small must
		// still compact.
		{name: "long session compacts", args: "", msgCount: 50, wantCompact: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, out := newFakeSlashCtx()
			sess := NewSession("sess-1", "/tmp")
			for range tc.msgCount {
				if _, err := sess.AppendMessage(mkUserMsg("m")); err != nil {
					t.Fatalf("AppendMessage: %v", err)
				}
			}
			sc.CurrentSession = func() *Session { return sess }
			// SessionInfo reports a deliberately tiny live-context count to prove
			// the guard no longer keys off it.
			sc.SessionInfo = func() (id, dir string, msgCount int) { return "sess-1", "/tmp", 1 }

			var gotInstr string
			compactCalled := false
			sc.CompactSession = func(instructions string) error {
				compactCalled = true
				gotInstr = instructions
				return nil
			}
			sc.Args = tc.args

			if err := compactHandler(sc); err != nil {
				t.Fatalf("compactHandler: %v", err)
			}
			if compactCalled != tc.wantCompact {
				t.Fatalf("CompactSession called = %v, want %v", compactCalled, tc.wantCompact)
			}
			if tc.wantCompact && gotInstr != tc.wantInstr {
				t.Errorf("customInstructions = %q; want %q", gotInstr, tc.wantInstr)
			}
			if !tc.wantCompact && !strings.Contains(out.String(), "Nothing to compact") {
				t.Errorf("expected 'Nothing to compact' flash, got %q", out.String())
			}
		})
	}
}

// ─── /settings tests () ─────────────────────────────────────────────

func TestSettingsHandlerReadOnly(t *testing.T) {
	// Without ShowExtensionSelector, handler should fall back to read-only dump.
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)

	var output string
	sc := &SlashContext{
		Append:          func(s string) { output += s },
		SettingsManager: sm,
	}
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Auto-compact") {
		t.Errorf("output missing 'Auto-compact': %q", output)
	}
	if !strings.Contains(output, "read-only") {
		t.Errorf("output missing 'read-only' note: %q", output)
	}
}

// settings-selector.ts onHttpIdleTimeoutMsChange (interactive-mode.ts:4797-4801): the label stays on the row, the timeout in milliseconds is saved and applied, and a status line reports it.
func TestSettingsHandlerTUI_HTTPIdleTimeoutPersistsTimeoutMsAndKeepsLabel(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)
	var out []string
	var appliedID, appliedValue, shown string

	sc := &SlashContext{
		Append:          func(s string) { out = append(out, s) },
		SettingsManager: sm,
		OnSettingApplied: func(id, value string) {
			appliedID, appliedValue = id, value
		},
	}
	useSettingsSelector(sc, func(selector *SettingsSelectorComponent) {
		shown = cycleSettingsRow(t, selector.GetSettingsList(), "http-idle-timeout", "disabled")
	})

	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if got := mustTimeout(t)(sm.GetHttpIdleTimeoutMs()); got != 0 {
		t.Fatalf("GetHttpIdleTimeoutMs() = %d, want 0", got)
	}
	if appliedID != "http-idle-timeout" || appliedValue != "0" {
		t.Fatalf("OnSettingApplied = (%q,%q), want (http-idle-timeout,0)", appliedID, appliedValue)
	}
	if shown != "disabled" || !slices.Equal(out, []string{"HTTP idle timeout: disabled"}) {
		t.Fatalf("row shows %q and printed %v, want disabled and the status", shown, out)
	}
}

// A failed save reports the failure and shows the saved value on the row again, as the caller loop did before the selector took its callbacks.
func TestSettingsHandlerTUI_FailedSaveRestoresTheRowValue(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)
	// A directory where settings.json belongs makes every save fail.
	if err := os.Mkdir(filepath.Join(tmp, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out []string
	sc := &SlashContext{
		Append:           func(s string) { out = append(out, s) },
		SettingsManager:  sm,
		OnSettingApplied: func(id, value string) { t.Errorf("a failed save applied %s=%s", id, value) },
	}
	var shown string
	useSettingsSelector(sc, func(selector *SettingsSelectorComponent) {
		list := selector.GetSettingsList()
		list.SelectItem("block-images")
		list.HandleInput("\r")
		shown = settingsRowValue(t, list, "block-images")
	})
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	// The manager keeps the value in memory when the write fails (TestSettingsUpdateKeepsMemoryWhenTheWriteFailsUpstream), so the row shows the manager's value.
	if want := boolSettingValue(sm.GetBlockImages()); shown != want || len(out) != 1 || !strings.HasPrefix(out[0], "Failed to save settings: ") {
		t.Fatalf("row shows %q and printed %v, want the manager's %q and one failure", shown, out, want)
	}
}

func TestSettingsHandlerTUI_ThemeSubmenuPersistsSelectedTheme(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)
	var out []string
	var appliedID, appliedValue, shown string

	sc := &SlashContext{
		Append:          func(s string) { out = append(out, s) },
		SettingsManager: sm,
		OnSettingApplied: func(id, value string) {
			appliedID, appliedValue = id, value
		},
	}
	useSettingsSelector(sc, func(selector *SettingsSelectorComponent) {
		list := selector.GetSettingsList()
		list.SelectItem("theme")
		list.HandleInput("\r")
		// The single-mode menu lists the system theme, automatic, then the other themes; walk to "light" and choose it.
		for range 8 {
			if strings.Contains(stripANSITest(strings.Join(list.Render(100), "\n")), "→   light") {
				break
			}
			list.HandleInput("\x1b[B")
		}
		list.HandleInput("\r")
		shown = settingsRowValue(t, list, "theme")
	})

	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if sm.Get().Theme != "light" {
		t.Fatalf("Theme = %q, want light", sm.Get().Theme)
	}
	if appliedID != "theme" || appliedValue != "light" {
		t.Fatalf("OnSettingApplied = (%q,%q), want (theme,light)", appliedID, appliedValue)
	}
	if shown != "light" || len(out) != 0 {
		t.Fatalf("row shows %q and printed %v, want light and nothing", shown, out)
	}
}

// TestSettingsHandlerTUI_ModelThinkingSubmenuSetsPerModelOverride mirrors
// upstream settings-selector.ts's "model-thinking" SteppedSubmenu: picking a
// model, then a level for it, persists a per-model override (not the global
// default), and the "N configured" summary reflects it on the next render.
func TestSettingsHandlerTUI_ModelThinkingSubmenuSetsPerModelOverride(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)
	const spec = "amazon-bedrock/anthropic.claude-fable-5"
	fixtureModel, ok := ai.LookupModelExact(spec)
	if !ok {
		t.Fatal("missing fixture model")
	}
	model := fixtureModel.ToModel()
	wantLevels := levelsForModel(model)
	if len(wantLevels) == 0 {
		t.Fatal("test fixture model has no supported thinking levels")
	}

	var applied []string
	sc := &SlashContext{
		Append:          func(string) {},
		SettingsManager: sm,
		SettingsModels:  func() ([]*ai.Model, *ai.Model) { return []*ai.Model{model}, model },
		ApplyModelThinkingLevel: func(provider, modelID, level string) {
			applied = append(applied, provider+"/"+modelID+"="+level)
		},
	}
	useSettingsSelector(sc, func(selector *SettingsSelectorComponent) {
		list := selector.GetSettingsList()
		list.HandleInput("Default thinking level per model")
		list.HandleInput("\r")
		list.HandleInput("\r")
		text := stripANSITest(strings.Join(list.Render(100), "\n"))
		if !strings.Contains(text, "Thinking Level for anthropic.claude-fable-5 [amazon-bedrock]") || !strings.Contains(text, "Select default thinking level for this model") {
			t.Fatalf("level step = %s", text)
		}
		if strings.Contains(text, "(clear override)") || strings.Contains(text, "✓") {
			t.Fatalf("level step without an override shows a clear row or a check: %s", text)
		}
		for range len(wantLevels) - 1 {
			list.HandleInput("\x1b[B")
		}
		list.HandleInput("\r")
		if got := stripANSITest(strings.Join(list.Render(100), "\n")); !strings.Contains(got, "Per-Model Thinking Level") {
			t.Fatalf("submenu did not loop to the model step after saving: %s", got)
		}
		list.HandleInput("\x1b")
		if got := strings.Join(list.Render(100), "\n"); !strings.Contains(got, "1 configured") || !strings.Contains(got, "> Default thinking level per model") {
			t.Fatalf("summary/filter after returning: %s", got)
		}
	})

	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if got := sm.Get().ModelThinkingLevels[spec]; got != wantLevels[len(wantLevels)-1] {
		t.Fatalf("ModelThinkingLevels override = %q, want %q", got, wantLevels[len(wantLevels)-1])
	}
	if want := []string{spec + "=" + wantLevels[len(wantLevels)-1]}; !slices.Equal(applied, want) {
		t.Fatalf("session applications = %v, want %v", applied, want)
	}
}

// TestSettingsHandlerTUI_ModelThinkingSubmenuClearsOverride mirrors upstream's
// CLEAR_OVERRIDE_VALUE row, shown only once a model has an override, which
// reverts to the global default.
func TestSettingsHandlerTUI_ModelThinkingSubmenuClearsOverride(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)
	const spec = "amazon-bedrock/anthropic.claude-fable-5"
	fixtureModel, ok := ai.LookupModelExact(spec)
	if !ok {
		t.Fatal("missing fixture model")
	}
	model := fixtureModel.ToModel()
	levels := levelsForModel(model)
	if err := sm.SetModelThinkingLevel("amazon-bedrock", "anthropic.claude-fable-5", ai.ThinkingLevel(levels[0])); err != nil {
		t.Fatal(err)
	}

	var applied []string
	sc := &SlashContext{
		Append:          func(string) {},
		SettingsManager: sm,
		SettingsModels:  func() ([]*ai.Model, *ai.Model) { return []*ai.Model{model}, model },
		ApplyModelThinkingLevel: func(provider, modelID, level string) {
			applied = append(applied, provider+"/"+modelID+"="+level)
		},
	}
	var levelStep string
	useSettingsSelector(sc, func(selector *SettingsSelectorComponent) {
		list := selector.GetSettingsList()
		list.HandleInput("Default thinking level per model")
		list.HandleInput("\r")
		list.HandleInput("\r")
		levelStep = stripANSITest(strings.Join(list.Render(100), "\n"))
		list.HandleInput("\x1b[A")
		list.HandleInput("\r")
		list.HandleInput("\x1b")
	})

	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if _, ok := sm.Get().ModelThinkingLevels[spec]; ok {
		t.Fatalf("ModelThinkingLevels override not cleared: %v", sm.Get().ModelThinkingLevels)
	}
	if !strings.Contains(levelStep, "(clear override)") || !strings.Contains(levelStep, "Revert to global default (medium)") {
		t.Fatalf("clear-override row missing from the level step: %s", levelStep)
	}
	if want := []string{spec + "="}; !slices.Equal(applied, want) {
		t.Fatalf("session applications = %v, want %v", applied, want)
	}
}

func TestExportHandler_PrerendersCustomToolHTML(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	cwd, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	jsonl := `{"type":"session","id":"sess-1","cwd":` + string(cwd) + `,"timestamp":"2026-05-14T12:00:00Z"}
{"type":"message","id":"a1","timestamp":"2026-05-14T12:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"custom-tool","arguments":{"path":"README.md"}}]}}
{"type":"message","id":"r1","timestamp":"2026-05-14T12:00:02Z","message":{"role":"toolResult","toolCallId":"call-1","toolName":"custom-tool","content":[{"type":"text","text":"done"}],"isError":false}}
`
	if err := os.WriteFile(sessionPath, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "out.html")
	session := &Session{header: SessionHeader{CWD: dir}, path: sessionPath}
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.Args = outPath
	sc.ToolRenderers = func() func(string) *extension.ToolRenderers {
		return ExportToolRenderers(inproc.NewRunner([]extension.Extension{{Name: "test-ext", Tools: map[string]extension.RegisteredTool{"custom-tool": {
			Definition: extension.ToolDefinition{
				Name: "custom-tool",
				RenderCall: func(args json.RawMessage, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
					return exportTestComponent{lines: []string{"\x1b[31mCALL\x1b[0m"}}
				},
				RenderResult: func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
					toolResult := result
					if options.Expanded {
						return exportTestComponent{lines: []string{"\x1b[32mRESULT: " + toolResult.Text() + "\x1b[0m"}}
					}
					return exportTestComponent{lines: []string{"preview"}}
				},
			},
		}}}}, dir))
	}
	if err := exportHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), outPath) {
		t.Fatalf("export confirmation missing output path: %q", out.String())
	}
	htmlBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlBytes)
	re := regexp.MustCompile(`<script id="session-data" type="application/json">([^<]+)</script>`)
	m := re.FindStringSubmatch(html)
	if len(m) != 2 {
		t.Fatal("session-data script not found")
	}
	payload, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatal(err)
	}
	var sd iexport.SessionData
	if err := json.Unmarshal(payload, &sd); err != nil {
		t.Fatal(err)
	}
	if sd.RenderedTools == nil || sd.RenderedTools["call-1"] == nil {
		t.Fatalf("RenderedTools missing custom tool html: %#v", sd.RenderedTools)
	}
	if got, _ := sd.RenderedTools["call-1"]["callHtml"].(string); !strings.Contains(got, "CALL") {
		t.Fatalf("callHtml = %q, want CALL", got)
	}
	if got, _ := sd.RenderedTools["call-1"]["resultHtmlExpanded"].(string); !strings.Contains(got, "RESULT: done") {
		t.Fatalf("resultHtmlExpanded = %q, want RESULT: done", got)
	}
}

func TestSettingsHandlerNilManager(t *testing.T) {
	var output string
	sc := &SlashContext{
		Append: func(s string) { output += s },
	}
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "unavailable") {
		t.Errorf("expected 'unavailable' message, got %q", output)
	}
}

func TestUpdateGlobalPersists(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)

	// Update a setting.
	err := sm.UpdateGlobal(func(s *Settings) {
		if s.Compaction == nil {
			s.Compaction = &CompactionSettingsJSON{}
		}
		b := false
		s.Compaction.Enabled = &b
	})
	if err != nil {
		t.Fatal(err)
	}

	// Reload from disk and verify.
	sm2 := NewSettingsManager(tmp, tmp)
	if sm2.Get().Compaction == nil || sm2.Get().Compaction.Enabled == nil || *sm2.Get().Compaction.Enabled != false {
		t.Errorf("Compaction.Enabled should be false after UpdateGlobal, got %+v", sm2.Get().Compaction)
	}
}

// importCommandFixture records what upstream's handleImportCommand test
// context records (interactive-mode-import-command.test.ts): the confirm
// prompts, the importFromJsonl calls, the status lines and the fatal path.
type importCommandFixture struct {
	prompts   []string
	answers   []string
	imports   [][2]string
	results   []error
	cancelled bool
	status    []string
	fatal     []string
}

func (f *importCommandFixture) context(args string) *SlashContext {
	return &SlashContext{
		Args:   args,
		Append: func(s string) { f.status = append(f.status, "append:"+s) },
		ShowExtensionSelector: func(title string, options []string, _ string) (string, bool) {
			f.prompts = append(f.prompts, title+" "+strings.Join(options, "/"))
			if len(f.answers) == 0 {
				return "", false
			}
			answer := f.answers[0]
			f.answers = f.answers[1:]
			return answer, true
		},
		ImportSession: func(inputPath, cwdOverride string) (bool, error) {
			f.imports = append(f.imports, [2]string{inputPath, cwdOverride})
			var err error
			if len(f.results) > 0 {
				err = f.results[0]
				f.results = f.results[1:]
			}
			return f.cancelled, err
		},
		ShowStatus: func(message string) { f.status = append(f.status, message) },
		FatalRuntimeError: func(prefix string, err error) error {
			f.fatal = append(f.fatal, prefix)
			return ErrInteractiveCrashed
		},
	}
}

// interactive-mode-import-command.test.ts "strips quotes from /import path
// arguments" and "preserves apostrophes in unquoted /import path arguments".
func TestImportPathArgumentMatchesUpstream(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{`"path/to/session.jsonl"`, "path/to/session.jsonl"},
		{`"path with spaces/session.jsonl"`, "path with spaces/session.jsonl"},
		{"john's/session.jsonl", "john's/session.jsonl"},
		{"/tmp/session.jsonl", "/tmp/session.jsonl"},
	} {
		if got := pathCommandArgument(tc.args); got != tc.want {
			t.Errorf("pathCommandArgument(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// interactive-mode-import-command.test.ts "enforces command token
// boundaries": /important and /exporter are not /import and /export.
func TestImportCommandTokenBoundaries(t *testing.T) {
	registry := NewSlashRegistry()
	for line, want := range map[string]string{"/important /tmp/session.jsonl": "", "/exporter out.html": "", "/import /tmp/session.jsonl": "import"} {
		name, _ := parseSlashLine(line)
		got, ok := registry.Resolve(name)
		if !ok {
			got = ""
		}
		if got != want {
			t.Errorf("%q resolves to %q, want %q", line, got, want)
		}
	}
}

// interactive-mode-import-command.test.ts "passes unquoted path to
// runtimeHost.importFromJsonl" and its apostrophe variant: confirm with Pi's
// title and message, import the parsed path, then report it.
func TestImportHandlerConfirmsAndImportsParsedPath(t *testing.T) {
	for args, want := range map[string]string{`"path/to/session.jsonl"`: "path/to/session.jsonl", "john's/session.jsonl": "john's/session.jsonl"} {
		f := &importCommandFixture{answers: []string{"Yes"}}
		if err := importHandler(f.context(args)); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		if wantPrompt := "Import session\nReplace current session with " + want + "? Yes/No"; !slices.Equal(f.prompts, []string{wantPrompt}) {
			t.Fatalf("%s: prompts = %q", args, f.prompts)
		}
		if !slices.Equal(f.imports, [][2]string{{want, ""}}) {
			t.Fatalf("%s: imports = %q", args, f.imports)
		}
		if !slices.Equal(f.status, []string{"Session imported from: " + want}) || f.fatal != nil {
			t.Fatalf("%s: status = %q, fatal = %q", args, f.status, f.fatal)
		}
	}
}

// interactive-mode-import-command.test.ts "shows a non-fatal error when
// /import path does not exist".
func TestImportHandlerMissingFileIsNotFatal(t *testing.T) {
	f := &importCommandFixture{answers: []string{"Yes"}, results: []error{&SessionImportFileNotFoundError{FilePath: "/tmp/missing-session.jsonl"}}}
	err := importHandler(f.context("/tmp/missing-session.jsonl"))
	if err == nil || err.Error() != "Failed to import session: File not found: /tmp/missing-session.jsonl" {
		t.Fatalf("err = %v", err)
	}
	if f.status != nil || f.fatal != nil {
		t.Fatalf("status = %q, fatal = %q", f.status, f.fatal)
	}
}

// Upstream handleImportCommand reports usage through showError and stops at a
// declined confirmation or a session_before_switch cancel.
func TestImportHandlerUsageAndCancellation(t *testing.T) {
	f := &importCommandFixture{}
	if err := importHandler(f.context("   ")); err == nil || err.Error() != "Usage: /import <path.jsonl>" {
		t.Fatalf("usage err = %v", err)
	}
	for name, fx := range map[string]*importCommandFixture{
		"declined":      {answers: []string{"No"}},
		"escaped":       {},
		"before_switch": {answers: []string{"Yes"}, cancelled: true},
		"cwd prompt no": {answers: []string{"Yes", "No"}, results: []error{&MissingSessionCwdError{Issue: SessionCwdIssue{SessionCwd: "/gone", FallbackCwd: "/here"}}}},
	} {
		if err := importHandler(fx.context("session.jsonl")); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.Equal(fx.status, []string{"Import cancelled"}) || fx.fatal != nil {
			t.Fatalf("%s: status = %q, fatal = %q", name, fx.status, fx.fatal)
		}
	}
}

// Upstream promptForMissingSessionCwd offers the current cwd and retries
// importFromJsonl with it.
func TestImportHandlerRetriesWithOfferedCwd(t *testing.T) {
	issue := SessionCwdIssue{SessionFile: "/sessions/moved.jsonl", SessionCwd: "/gone", FallbackCwd: "/here"}
	f := &importCommandFixture{answers: []string{"Yes", "Yes"}, results: []error{&MissingSessionCwdError{Issue: issue}, nil}}
	if err := importHandler(f.context("moved.jsonl")); err != nil {
		t.Fatal(err)
	}
	if want := "Session cwd not found\n" + FormatMissingSessionCwdPrompt(issue) + " Yes/No"; len(f.prompts) != 2 || f.prompts[1] != want {
		t.Fatalf("prompts = %q", f.prompts)
	}
	if !slices.Equal(f.imports, [][2]string{{"moved.jsonl", ""}, {"moved.jsonl", "/here"}}) {
		t.Fatalf("imports = %q", f.imports)
	}
	if !slices.Equal(f.status, []string{"Session imported from: moved.jsonl"}) {
		t.Fatalf("status = %q", f.status)
	}
}

// Navigating the tree must deliver messages that were queued during a
// compaction, mirroring upstream's flushCompactionQueue after "Navigated to
// selected point" (interactive-mode.ts:1847, 5021). Slash commands run during
// compaction, so a user can queue a message, open /tree, and navigate; without
// this the queue sat until some later event happened to drain it.
func TestTreeNavigationFlushesTheCompactionQueue(t *testing.T) {
	var flushed int
	sc := &SlashContext{
		PickTreeEntry:        func(string) (string, bool) { return "entry-1", true },
		ForkAtEntry:          func(string) error { return nil },
		FlushCompactionQueue: func() { flushed++ },
		Append:               func(string) {},
	}
	if err := treeHandlerWithInitial(sc, ""); err != nil {
		t.Fatalf("treeHandlerWithInitial: %v", err)
	}
	if flushed != 1 {
		t.Errorf("compaction queue flushed %d times after navigation, want 1", flushed)
	}
}

// Upstream showTreeSelector opens the selector without emitting
// session_before_tree; the event fires inside AgentSession.navigateTree once an
// entry is chosen, so a cancelling extension cancels the navigation, not the
// selector.
func TestTreeSelectorDoesNotEmitSessionBeforeTree(t *testing.T) {
	var beforeTreeCalls, pickerCalls int
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		EventSessionBeforeTree: {func(...any) (any, error) {
			beforeTreeCalls++
			return extension.SessionBeforeTreeResult{Cancel: true}, nil
		}},
	}}
	var status []string
	sc := &SlashContext{
		ExtRunner:     inproc.NewRunner([]extension.Extension{ext}, t.TempDir()),
		PickTreeEntry: func(string) (string, bool) { pickerCalls++; return "entry-1", true },
		NavigateTreeFull: func(context.Context, string, bool, string) (NavigateTreeResult, error) {
			return NavigateTreeResult{Cancelled: true}, nil
		},
		ShowExtensionSelector: func(string, []string, string) (string, bool) { return "No summary", true },
		ShowStatus:            func(message string) { status = append(status, message) },
		Append:                func(string) {},
	}
	if err := treeHandlerWithInitial(sc, ""); err != nil {
		t.Fatalf("treeHandlerWithInitial: %v", err)
	}
	if pickerCalls != 1 {
		t.Fatalf("tree selector opened %d times, want 1", pickerCalls)
	}
	if beforeTreeCalls != 0 {
		t.Fatalf("slash /tree emitted session_before_tree %d times, want 0", beforeTreeCalls)
	}
	if !slices.Equal(status, []string{"Navigation cancelled"}) {
		t.Fatalf("status = %v, want [Navigation cancelled]", status)
	}
}

// A host that supplies no flush hook must not panic.
func TestTreeNavigationWithoutFlushHookIsSafe(t *testing.T) {
	sc := &SlashContext{
		PickTreeEntry: func(string) (string, bool) { return "entry-1", true },
		ForkAtEntry:   func(string) error { return nil },
		Append:        func(string) {},
	}
	if err := treeHandlerWithInitial(sc, ""); err != nil {
		t.Fatalf("treeHandlerWithInitial: %v", err)
	}
}

func TestShareHandlerUploadsJSONLArtifact(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"type":"session","version":3,"id":"test","cwd":"/tmp","timestamp":"2026-05-10T12:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := &Session{path: sessionPath, header: SessionHeader{CWD: dir}}
	var output []string
	sc := &SlashContext{
		CurrentSession: func() *Session { return session },
		ShareState:     func() ShareState { return ShareState{SystemPrompt: "system"} },
		ShareSession: func(got *Session, state ShareState, showStatus func(string)) (string, error) {
			if got != session || state.SystemPrompt != "system" {
				t.Fatalf("share args = %p %+v", got, state)
			}
			showStatus(sharePrivacyNotice)
			return "Share URL: https://pi-in-go.dev/session/p_test", nil
		},
		Append:     func(message string) { output = append(output, message) },
		ShowStatus: func(message string) { output = append(output, message) },
	}
	if err := shareHandler(sc); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(output, "\n")
	if !strings.Contains(joined, sharePrivacyNotice) || !strings.Contains(joined, "https://pi-in-go.dev/session/p_test") {
		t.Fatalf("share output = %q", joined)
	}
}

// The cache-warming row sits between http-idle-timeout and hide-thinking, as
// in settings-selector.ts, defaults to Pi's "streaming", and writes the global
// cacheWarming key.
func TestSettingsItems_CacheWarmingRow(t *testing.T) {
	rows, ids := settingsRowsFor(t, NewSettingsManager(t.TempDir(), t.TempDir()))
	index := slices.Index(ids, "cache-warming-mode")
	if index <= 0 || ids[index-1] != "http-idle-timeout" || ids[index+1] != "hide-thinking" {
		t.Fatalf("cache-warming-mode at %d is not between http-idle-timeout and hide-thinking", index)
	}
	row := rows["cache-warming-mode"]
	if row.Label != "Cache warming" || row.Description != "off; streaming while the agent runs; idle also between runs while continuation stays profitable" {
		t.Fatalf("row = %q / %q", row.Label, row.Description)
	}
	if want := []string{"off", "streaming", "idle"}; !slices.Equal(row.Values, want) {
		t.Fatalf("values = %v, want %v", row.Values, want)
	}
	if row.CurrentValue != "streaming" {
		t.Fatalf("default = %q, want streaming", row.CurrentValue)
	}
	// Cycling streaming -> idle -> off reaches the callback, and the saved value is the global cacheWarming key.
	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	var modes []CacheWarmingMode
	sc := &SlashContext{SettingsManager: sm, Append: func(string) {}, ShowStatus: func(string) {}}
	useSettingsSelector(sc, func(selector *SettingsSelectorComponent) {
		list := selector.GetSettingsList()
		list.SelectItem("cache-warming-mode")
		list.HandleInput("\r")
		modes = append(modes, sm.GetCacheWarmingMode())
		list.HandleInput("\r")
		modes = append(modes, sm.GetCacheWarmingMode())
	})
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if want := []CacheWarmingMode{"idle", "off"}; !slices.Equal(modes, want) {
		t.Fatalf("saved modes = %v, want %v", modes, want)
	}
	if sm.GetGlobalSettings().CacheWarming != "off" {
		t.Fatalf("CacheWarming = %q, want off", sm.GetGlobalSettings().CacheWarming)
	}
}

// Upstream handleNameCommand (interactive-mode.ts:6642-6652) reads the name back
// from the session manager, which folds line breaks to spaces: a changed name
// is announced as a warning, and "Session name set" shows the stored name.
func TestSlashName_WarnsWhenTheStoredNameIsNormalized(t *testing.T) {
	for _, tc := range []struct {
		name, args, stored string
		warning            string
	}{
		{"unchanged", "my project", "my project", ""},
		{"line break", "a\nb", "a b", `Warning: Session name was normalized from "a\nb" to "a b"`},
		{"carriage returns", "a\r\n\r\nb", "a b", `Warning: Session name was normalized from "a\r\n\r\nb" to "a b"`},
		{"html characters stay literal", "x\n<&>", "x <&>", `Warning: Session name was normalized from "x\n<&>" to "x <&>"`},
		// JSON.stringify leaves U+2028 literal and escapes other control characters (node: JSON.stringify("a\u2028b\nc")).
		{"line separator stays literal", "a\u2028b\nc", "a\u2028b c", "Warning: Session name was normalized from \"a\u2028b\\nc\" to \"a\u2028b c\""},
		{"control characters escape", "a\x01\tb\nc", "a\x01\tb c", `Warning: Session name was normalized from "a\u0001\tb\nc" to "a\u0001\tb c"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, out := newFakeSlashCtx()
			sc.CurrentSession = func() *Session { return &Session{} }
			sc.Args = tc.args
			var stored, titled string
			sc.SetSessionName = func(string) error { stored = tc.stored; return nil }
			sc.GetSessionName = func() string { return stored }
			sc.OnNameChange = func(name string) { titled = name }
			if err := nameHandler(sc); err != nil {
				t.Fatal(err)
			}
			text := stripANSITest(out.String())
			if got := strings.Contains(text, tc.warning); tc.warning != "" && !got {
				t.Fatalf("missing %q in %q", tc.warning, text)
			}
			if tc.warning == "" && strings.Contains(text, "normalized") {
				t.Fatalf("unexpected warning in %q", text)
			}
			if !strings.Contains(text, "Session name set: "+tc.stored) || titled != tc.stored {
				t.Fatalf("set line/title = %q / %q, want stored name %q", text, titled, tc.stored)
			}
		})
	}
}

// /export <file>.jsonl writes through the session's exportToJsonl (interactive-mode.ts handleExportCommand calls
// this.session.exportToJsonl(outputPath)), so the slash context's ExportToJsonl receives the path and its result is shown.
func TestExportJsonlGoesThroughTheSessionExport(t *testing.T) {
	session := &Session{header: SessionHeader{CWD: t.TempDir()}}
	var got []string
	var output []string
	sc := &SlashContext{
		Args:           "out.jsonl",
		CurrentSession: func() *Session { return session },
		ExportToJsonl: func(outputPath string) (string, error) {
			got = append(got, outputPath)
			return "/exported/out.jsonl", nil
		},
		Append:     func(message string) { output = append(output, message) },
		ShowStatus: func(message string) { output = append(output, message) },
	}
	if err := exportHandler(sc); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "out.jsonl" {
		t.Fatalf("ExportToJsonl calls = %q, want one call with out.jsonl", got)
	}
	if joined := strings.Join(output, "\n"); !strings.Contains(joined, "Session exported to: /exported/out.jsonl") {
		t.Fatalf("status = %q", joined)
	}
}

// The interactive slash context routes ExportToJsonl to the SessionHandle when one is bound.
func TestInteractiveExportJsonlUsesTheSessionHandle(t *testing.T) {
	handle := &exportRecordingHandle{recordingCompactHandle: &recordingCompactHandle{}}
	m := &InteractiveMode{}
	m.opts.SessionHandle = handle
	sc := m.buildSlashContext(t.Context())
	if path, err := sc.ExportToJsonl("x.jsonl"); err != nil || path != "handle:x.jsonl" {
		t.Fatalf("ExportToJsonl = %q, %v; want the handle's result", path, err)
	}
}

type exportRecordingHandle struct{ *recordingCompactHandle }

func (h *exportRecordingHandle) ExportToJsonl(outputPath string) (string, error) {
	return "handle:" + outputPath, nil
}

// interactive-mode.ts:5590-5680: with branchSummary.skipPrompt the "Summarize branch?" selector never opens, and navigation with no
// summary then reports its result as after a chosen "No summary": "Navigation cancelled" for a cancelled navigation, "Navigated to
// selected point" on success (with the editor text of a user entry), and an error as an error rather than a status line.
func TestTreeNavigationSkippingThePromptReportsEveryResult(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     NavigateTreeResult
		err        error
		wantStatus string
		wantErr    string
		wantEditor string
	}{
		{name: "success", result: NavigateTreeResult{EditorText: "prefill"}, wantStatus: "Navigated to selected point", wantEditor: "prefill"},
		{name: "cancelled by a hook", result: NavigateTreeResult{Cancelled: true}, wantStatus: "Navigation cancelled"},
		{name: "failure", err: errors.New("boom"), wantErr: "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, _ := newFakeSlashCtx()
			sc.SettingsManager = &SettingsManager{merged: Settings{BranchSummary: &BranchSummaryConfig{SkipPrompt: true}}}
			sc.ShowExtensionSelector = func(string, []string, string) (string, bool) {
				t.Fatal("the summarize prompt opened despite skipPrompt")
				return "", false
			}
			var summarize []bool
			sc.NavigateTreeFull = func(_ context.Context, id string, wantsSummary bool, instructions string) (NavigateTreeResult, error) {
				summarize = append(summarize, wantsSummary)
				if id != "target" || instructions != "" {
					t.Errorf("navigate(%q, %v, %q)", id, wantsSummary, instructions)
				}
				return tc.result, tc.err
			}
			var status, editor string
			sc.ShowStatus = func(msg string) { status = msg }
			sc.SetEditorText = func(text string) { editor = text }
			err := treeNavigateWithSummarize(sc, "target")
			if (err == nil) != (tc.wantErr == "") || (err != nil && err.Error() != tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			if status != tc.wantStatus || editor != tc.wantEditor || len(summarize) != 1 || summarize[0] {
				t.Fatalf("status %q editor %q summarize %v, want %q %q [false]", status, editor, summarize, tc.wantStatus, tc.wantEditor)
			}
		})
	}
}
