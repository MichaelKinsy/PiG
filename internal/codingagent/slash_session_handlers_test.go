package codingagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	iexport "github.com/MichaelKinsy/PiG/internal/codingagent/export"
	"github.com/MichaelKinsy/PiG/tui"
)

type exportTestComponent struct{ lines []string }

func (c exportTestComponent) Render(width int) []string { return c.lines }

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
	if !strings.Contains(out.String(), "/tmp/cloned.jsonl") {
		t.Errorf("path missing from output: %q", out.String())
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
	sc.Reload = func() { reloaded = true }
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
	sc.Reload = func() {}
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
	_ = sess.Fork(id1)
	_, _ = sess.AppendMessage(mkUserMsg("branch-a"))
	_ = sess.Fork(id1)
	_, _ = sess.AppendMessage(mkUserMsg("branch-b"))

	out := renderTreeASCII(sess.Tree())
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

func TestSettingsItems_UpstreamCoreRosterPresentAndOrdered(t *testing.T) {
	items := settingsItems()
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.id)
	}

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
		t.Fatalf("settingsItems prefix = %v, want prefix %v", ids, wantPrefix)
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
		"theme",
	} {
		if !slices.Contains(ids, id) {
			t.Fatalf("settingsItems missing %q in %v", id, ids)
		}
	}

	wantSuffix := []string{"warnings", "model-thinking", "tui-mode", "fullscreen-exit-output", "fullscreen-scrollbar", "fullscreen-copy-on-select", "theme"}
	if len(ids) < len(wantSuffix) || !slices.Equal(ids[len(ids)-len(wantSuffix):], wantSuffix) {
		t.Fatalf("settingsItems suffix = %v, want %v", ids, wantSuffix)
	}
}

func TestSettingsItems_UpstreamDefaults(t *testing.T) {
	lookup := map[string]settingItem{}
	for _, item := range settingsItems() {
		lookup[item.id] = item
	}
	s := Settings{}

	if got := lookup["mermaid-rendering"].get(s); got != "streaming" {
		t.Fatalf("mermaid-rendering default = %q, want streaming", got)
	}
	if got := lookup["tui-mode"].get(s); got != "regular" {
		t.Fatalf("tui-mode default = %q, want regular", got)
	}
	if got := lookup["fullscreen-exit-output"].get(s); got != "transcript" {
		t.Fatalf("fullscreen-exit-output default = %q, want transcript", got)
	}
	if got := lookup["fullscreen-scrollbar"].get(s); got != "auto" {
		t.Fatalf("fullscreen-scrollbar default = %q, want auto", got)
	}
	if got := lookup["fullscreen-copy-on-select"].get(s); got != "true" {
		t.Fatalf("fullscreen-copy-on-select default = %q, want true", got)
	}
	if got := lookup["transport"].get(s); got != "auto" {
		t.Fatalf("transport default = %q, want auto", got)
	}
	if got := lookup["auto-resize-images"].get(s); got != "true" {
		t.Fatalf("auto-resize-images default = %q, want true", got)
	}
	if got := lookup["show-hardware-cursor"].get(s); got != "false" {
		t.Fatalf("show-hardware-cursor default = %q, want false", got)
	}
	if got := lookup["editor-padding"].get(s); got != "0" {
		t.Fatalf("editor-padding default = %q, want 0", got)
	}
	if got := lookup["output-padding"].get(s); got != "1" {
		t.Fatalf("output-padding default = %q, want 1", got)
	}
	if got := lookup["autocomplete-max-visible"].get(s); got != "5" {
		t.Fatalf("autocomplete-max-visible default = %q, want 5", got)
	}
	if got := lookup["clear-on-shrink"].get(s); got != "false" {
		t.Fatalf("clear-on-shrink default = %q, want false", got)
	}
	if got := lookup["terminal-progress"].get(s); got != "false" {
		t.Fatalf("terminal-progress default = %q, want false", got)
	}
	if got := lookup["install-telemetry"].get(s); got != "true" {
		t.Fatalf("install-telemetry default = %q, want true", got)
	}
}

func TestSettingsItems_TransportIncludesWebsocketCached(t *testing.T) {
	lookup := map[string]settingItem{}
	for _, item := range settingsItems() {
		lookup[item.id] = item
	}
	got := lookup["transport"].values
	want := []string{"sse", "websocket", "websocket-cached", "auto"}
	if !slices.Equal(got, want) {
		t.Fatalf("transport values = %v, want %v", got, want)
	}
}

func TestSettingsItems_HTTPIdleTimeoutChoicesAndDefaults(t *testing.T) {
	lookup := map[string]settingItem{}
	for _, item := range settingsItems() {
		lookup[item.id] = item
	}

	got := lookup["http-idle-timeout"].values
	want := []string{"30 sec", "1 min", "2 min", "5 min", "disabled"}
	if !slices.Equal(got, want) {
		t.Fatalf("http-idle-timeout values = %v, want %v", got, want)
	}
	if got := lookup["http-idle-timeout"].get(Settings{}); got != "5 min" {
		t.Fatalf("http-idle-timeout default = %q, want 5 min", got)
	}
}

func TestSettingsItems_HTTPIdleTimeoutApplyPersistsTimeoutMs(t *testing.T) {
	lookup := map[string]settingItem{}
	for _, item := range settingsItems() {
		lookup[item.id] = item
	}

	var s Settings
	lookup["http-idle-timeout"].apply(&s, "disabled")
	if s.HTTPIdleTimeoutMs == nil || *s.HTTPIdleTimeoutMs != 0 {
		t.Fatalf("HTTPIdleTimeoutMs after disabled = %v, want 0", s.HTTPIdleTimeoutMs)
	}
	lookup["http-idle-timeout"].apply(&s, "2 min")
	if s.HTTPIdleTimeoutMs == nil || *s.HTTPIdleTimeoutMs != 120_000 {
		t.Fatalf("HTTPIdleTimeoutMs after 2 min = %v, want 120000", s.HTTPIdleTimeoutMs)
	}
}

func TestSettingsItems_FollowUpModeDescriptionUsesResolvedKeyDisplayText(t *testing.T) {
	lookup := map[string]settingItem{}
	for _, item := range settingsItems() {
		lookup[item.id] = item
	}
	got := lookup["follow-up-mode"].desc
	wantPrefix := tui.KeyDisplayText("alt+enter") + " queues follow-up messages until agent stops."
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

	tuiSetCapsForTest(false)
	visible := settingsItemsVisible()
	for _, item := range visible {
		if item.id == "show-images" || item.id == "image-width-cells" {
			t.Fatalf("expected image items to be gated out when terminal lacks images, got %q", item.id)
		}
	}

	tuiSetCapsForTest(true)
	visible = settingsItemsVisible()
	var seenShow, seenWidth bool
	for _, item := range visible {
		if item.id == "show-images" {
			seenShow = true
		}
		if item.id == "image-width-cells" {
			seenWidth = true
		}
	}
	if !seenShow || !seenWidth {
		t.Fatalf("expected show-images + image-width-cells when terminal supports images")
	}
}

func TestSettingsItems_TerminalProgressAlwaysVisible(t *testing.T) {
	prev := tuiCapabilitiesForTest()
	defer prev.restore()

	// Upstream settings-selector.ts:438 inserts terminal-progress
	// unconditionally; the OSC 9;4 capability check only affects
	// rendering, not menu visibility. Pi v0.71.0 confirms the toggle
	// always appears in the settings selector even when the terminal
	// does not support OSC 9;4.
	tui.SetCapabilities(tui.TerminalCapabilities{Images: ""})
	visible := settingsItemsVisible()
	seen := false
	for _, item := range visible {
		if item.id == "terminal-progress" {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatalf("expected terminal-progress to be visible even without OSC 9;4 support")
	}

	tui.SetCapabilities(tui.TerminalCapabilities{Images: ""})
	visible = settingsItemsVisible()
	seen = false
	for _, item := range visible {
		if item.id == "terminal-progress" {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatalf("expected terminal-progress when OSC 9;4 is supported")
	}
}

func TestSetSessionNamePersistsToDisk(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-named", "")
	_, _ = sess.AppendMessage(mkUserMsg("hi"))

	// Mimic the closure in buildSlashContext.
	id, _ := generateEntryID()
	parent := sess.LeafID()
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

// TestCompactHandlerGuard verifies that /compact with < 2 session messages
// calls flashOrAppend and does NOT invoke CompactSession.
func TestCompactHandlerGuard(t *testing.T) {
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
	if compactCalled {
		t.Error("CompactSession should NOT be called when msgCount < 2")
	}
	if got := strings.TrimSpace(out.String()); got != "Warning: Nothing to compact (no messages yet)" {
		t.Errorf("guard message = %q", got)
	}
}

func TestCompactHandlerGuardUsesWarningSurface(t *testing.T) {
	sc, _ := newFakeSlashCtx()
	sc.SessionInfo = func() (string, string, int) { return "sess-1", "/tmp", 1 }
	var warning string
	sc.ShowWarning = func(message string) { warning = message }

	if err := compactHandler(sc); err != nil {
		t.Fatal(err)
	}
	if warning != "Nothing to compact (no messages yet)" {
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
		{name: "below threshold (1 message) does not compact", args: "", msgCount: 1, wantCompact: false},
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

func TestCycleValue(t *testing.T) {
	cases := []struct {
		name    string
		current string
		values  []string
		want    string
	}{
		{"first to second", "true", []string{"true", "false"}, "false"},
		{"second wraps to first", "false", []string{"true", "false"}, "true"},
		{"middle of 4", "low", []string{"off", "low", "medium", "high"}, "medium"},
		{"last wraps", "high", []string{"off", "low", "medium", "high"}, "off"},
		{"unknown defaults to first", "unknown", []string{"a", "b"}, "a"},
		{"empty values returns current", "x", nil, "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cycleValue(tc.current, tc.values)
			if got != tc.want {
				t.Errorf("cycleValue(%q, %v) = %q, want %q", tc.current, tc.values, got, tc.want)
			}
		})
	}
}

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

func TestSettingsHandlerTUI_HTTPIdleTimeoutPersistsTimeoutMsAndStatusLabel(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)
	var out []string
	var appliedID, appliedValue string
	showCount := 0

	sc := &SlashContext{
		Append:          func(s string) { out = append(out, s) },
		SettingsManager: sm,
		ShowSettingsList: func(items []tui.SettingItem) (string, string, bool) {
			showCount++
			if showCount == 1 {
				return "http-idle-timeout", "disabled", true
			}
			return "", "", false
		},
		OnSettingApplied: func(id, value string) {
			appliedID, appliedValue = id, value
		},
	}

	if err := settingsHandlerTUI(sc); err != nil {
		t.Fatal(err)
	}
	if got := mustTimeout(t)(sm.GetHttpIdleTimeoutMs()); got != 0 {
		t.Fatalf("GetHttpIdleTimeoutMs() = %d, want 0", got)
	}
	if appliedID != "http-idle-timeout" || appliedValue != "0" {
		t.Fatalf("OnSettingApplied = (%q,%q), want (http-idle-timeout,0)", appliedID, appliedValue)
	}
	if len(out) == 0 || !strings.Contains(out[0], "HTTP idle timeout: disabled") {
		t.Fatalf("status output = %v, want HTTP idle timeout: disabled", out)
	}
}

func TestSettingsHandlerTUI_ThemeSubmenuPersistsSelectedTheme(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSettingsManager(tmp, tmp)
	var out []string
	var appliedID, appliedValue string
	showCount := 0

	sc := &SlashContext{
		Append:          func(s string) { out = append(out, s) },
		SettingsManager: sm,
		ShowSettingsList: func(items []tui.SettingItem) (string, string, bool) {
			showCount++
			if showCount == 1 {
				return "theme", "dark", true
			}
			return "", "", false
		},
		ShowThemeSelector: func(currentTheme string) (string, bool) {
			if currentTheme != tui.ActiveTheme().Name {
				t.Fatalf("currentTheme = %q, want active theme %q", currentTheme, tui.ActiveTheme().Name)
			}
			return "light", true
		},
		OnSettingApplied: func(id, value string) {
			appliedID, appliedValue = id, value
		},
	}

	if err := settingsHandlerTUI(sc); err != nil {
		t.Fatal(err)
	}
	if sm.Get().Theme != "light" {
		t.Fatalf("Theme = %q, want light", sm.Get().Theme)
	}
	if appliedID != "theme" || appliedValue != "light" {
		t.Fatalf("OnSettingApplied = (%q,%q), want (theme,light)", appliedID, appliedValue)
	}
	if len(out) == 0 || !strings.Contains(out[0], "Theme: light") {
		t.Fatalf("status output = %v, want Theme: light", out)
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
	wantLevels := levelsForModel(fixtureModel.ToModel())
	if len(wantLevels) == 0 {
		t.Fatal("test fixture model has no supported thinking levels")
	}

	var menu *SteppedSubmenu
	var returnedToModelStep bool
	var gotTitle, gotDesc, gotCurrent string
	var gotOptions []tui.SelectItem

	sc := &SlashContext{
		Append:          func(string) {},
		SettingsManager: sm,
		ShowSettingsList: func(items []tui.SettingItem) (string, string, bool) {
			list := tui.NewSettingsList(items)
			list.HandleInput("Default thinking level per model")
			list.HandleInput("\r")
			if menu == nil {
				t.Fatal("model-thinking row did not open its submenu")
			}
			list.HandleInput("\r")
			step := menu.steps[menu.stepIndex]
			gotTitle, gotDesc = step.Title(menu.context), step.Description(menu.context)
			gotCurrent, gotOptions = step.Preselect(menu.context), step.Options(menu.context)
			for range len(wantLevels) - 1 {
				list.HandleInput("\x1b[B")
			}
			list.HandleInput("\r")
			returnedToModelStep = menu.stepIndex == 0
			list.HandleInput("\x1b")
			if got := strings.Join(list.Render(100), "\n"); !strings.Contains(got, "1 configured") || !strings.Contains(got, "> Default thinking level per model") {
				t.Fatalf("summary/filter after returning: %s", got)
			}
			return "", "", false
		},
		ModelThinkingSubmenu: func(_ string, done func(*string)) tui.Component {
			model, ok := ai.LookupModelExact(spec)
			if !ok {
				t.Fatal("missing fixture model")
			}
			menu = newModelThinkingSubmenu(sm.Get(), []*ai.Model{model.ToModel()}, spec, func(_ *ai.Model, level string) {
				if err := sm.SetModelThinkingLevel(model.Provider, model.ID, level); err != nil {
					t.Fatal(err)
				}
			}, func() { value := "1 configured"; done(&value) })
			return menu
		},
	}

	if err := settingsHandlerTUI(sc); err != nil {
		t.Fatal(err)
	}
	if got := sm.Get().ModelThinkingLevels["amazon-bedrock/anthropic.claude-fable-5"]; got != wantLevels[len(wantLevels)-1] {
		t.Fatalf("ModelThinkingLevels override = %q, want %q", got, wantLevels[len(wantLevels)-1])
	}
	if gotCurrent != "" {
		t.Fatalf("current level before setting = %q, want empty", gotCurrent)
	}
	if wantTitle := "Thinking Level for anthropic.claude-fable-5 [amazon-bedrock]"; gotTitle != wantTitle {
		t.Fatalf("submenu title = %q, want %q", gotTitle, wantTitle)
	}
	if gotDesc != "Select default thinking level for this model" {
		t.Fatalf("submenu description = %q", gotDesc)
	}
	if len(gotOptions) != len(wantLevels) {
		t.Fatalf("thinking options = %#v, want levels %v (no clear-override row without an existing override)", gotOptions, wantLevels)
	}
	for i, level := range wantLevels {
		if gotOptions[i].Value != level {
			t.Fatalf("option[%d] = %q, want %q", i, gotOptions[i].Value, level)
		}
	}
	if !returnedToModelStep {
		t.Fatal("submenu did not loop to the model step after saving")
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
	levels := levelsForModel(fixtureModel.ToModel())
	if err := sm.SetModelThinkingLevel("amazon-bedrock", "anthropic.claude-fable-5", levels[0]); err != nil {
		t.Fatal(err)
	}

	var menu *SteppedSubmenu
	var gotOptions []tui.SelectItem

	sc := &SlashContext{
		Append:          func(string) {},
		SettingsManager: sm,
		ShowSettingsList: func(items []tui.SettingItem) (string, string, bool) {
			list := tui.NewSettingsList(items)
			list.HandleInput("Default thinking level per model")
			list.HandleInput("\r")
			if menu == nil {
				t.Fatal("model-thinking row did not open its submenu")
			}
			list.HandleInput("\r")
			gotOptions = menu.steps[menu.stepIndex].Options(menu.context)
			list.HandleInput("\x1b[A")
			list.HandleInput("\r")
			list.HandleInput("\x1b")
			return "", "", false
		},
		ModelThinkingSubmenu: func(_ string, done func(*string)) tui.Component {
			model, ok := ai.LookupModelExact(spec)
			if !ok {
				t.Fatal("missing fixture model")
			}
			menu = newModelThinkingSubmenu(sm.Get(), []*ai.Model{model.ToModel()}, spec, func(_ *ai.Model, level string) {
				if level != modelThinkingClearOverrideValue {
					t.Fatalf("selected %s, want clear override", level)
				}
				if err := sm.RemoveModelThinkingLevel(model.Provider, model.ID); err != nil {
					t.Fatal(err)
				}
			}, func() { value := "none"; done(&value) })
			return menu
		},
	}

	if err := settingsHandlerTUI(sc); err != nil {
		t.Fatal(err)
	}
	if _, ok := sm.Get().ModelThinkingLevels["amazon-bedrock/anthropic.claude-fable-5"]; ok {
		t.Fatalf("ModelThinkingLevels override not cleared: %v", sm.Get().ModelThinkingLevels)
	}
	last := gotOptions[len(gotOptions)-1]
	if last.Value != modelThinkingClearOverrideValue || !strings.Contains(last.Description, "Revert to global default (medium)") {
		t.Fatalf("clear-override row = %#v, want value %q and default-medium description", last, modelThinkingClearOverrideValue)
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
	sc.RegisteredTools = func() []extension.RegisteredTool {
		return []extension.RegisteredTool{{
			Definition: extension.ToolDefinition{
				Name: "custom-tool",
				RenderCall: func(args json.RawMessage, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
					return exportTestComponent{lines: []string{"\x1b[31mCALL\x1b[0m"}}
				},
				RenderResult: func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
					toolResult := result.(agent.AgentToolResult)
					if options.Expanded {
						return exportTestComponent{lines: []string{"\x1b[32mRESULT: " + toolResult.Content + "\x1b[0m"}}
					}
					return exportTestComponent{lines: []string{"preview"}}
				},
			},
		}}
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

func TestImportHandlerNoArgs(t *testing.T) {
	var out []string
	sc := &SlashContext{
		Args:   "",
		Append: func(s string) { out = append(out, s) },
	}
	_ = importHandler(sc)
	if len(out) == 0 || !strings.Contains(out[0], "Usage: /import") {
		t.Errorf("expected usage hint, got %v", out)
	}
}

func TestImportHandlerNotJsonl(t *testing.T) {
	var out []string
	sc := &SlashContext{
		Args:   "session.txt",
		Append: func(s string) { out = append(out, s) },
	}
	_ = importHandler(sc)
	if len(out) == 0 || !strings.Contains(out[0], "Only .jsonl") {
		t.Errorf("expected .jsonl error, got %v", out)
	}
}

func TestImportHandlerFileNotFound(t *testing.T) {
	var out []string
	sc := &SlashContext{
		Args:           "/nonexistent/path.jsonl",
		Append:         func(s string) { out = append(out, s) },
		CurrentSession: func() *Session { return nil },
	}
	_ = importHandler(sc)
	if len(out) == 0 || !strings.Contains(out[0], "File not found") {
		t.Errorf("expected file not found, got %v", out)
	}
}

func TestImportHandlerSuccess(t *testing.T) {
	// Create a temp JSONL file with a minimal session.
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "test-session.jsonl")
	header := `{"type":"session_start","id":"s1","cwd":"/tmp","model":"test"}` + "\n"
	if err := os.WriteFile(srcFile, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existingSession := filepath.Join(sessionDir, "current.jsonl")
	if err := os.WriteFile(existingSession, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build a minimal Session so CurrentSession and LoadSessionPath work.
	sess := &Session{path: existingSession, header: SessionHeader{CWD: tmp}}
	var loadedPath string
	var out []string
	cleared := false

	sc := &SlashContext{
		Args:           srcFile,
		Append:         func(s string) { out = append(out, s) },
		Clear:          func() { cleared = true },
		CurrentSession: func() *Session { return sess },
		LoadSessionPath: func(p string) error {
			loadedPath = p
			return nil
		},
		ShowStatus: func(msg string) {
			out = append(out, msg)
		},
	}
	if err := importHandler(sc); err != nil {
		t.Fatal(err)
	}

	// Should have copied to session dir and loaded.
	expectedDst := filepath.Join(sessionDir, "test-session.jsonl")
	if loadedPath != expectedDst {
		t.Errorf("loaded path = %q, want %q", loadedPath, expectedDst)
	}
	if !cleared {
		t.Error("expected Clear() to be called")
	}
	// Flash message should reference original arg.
	found := false
	for _, s := range out {
		if strings.Contains(s, "imported from") {
			found = true
		}
	}
	if !found {
		t.Errorf("no import confirmation in output: %v", out)
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
	items := settingsItems()
	index := slices.IndexFunc(items, func(item settingItem) bool { return item.id == "cache-warming-mode" })
	if index <= 0 || items[index-1].id != "http-idle-timeout" || items[index+1].id != "hide-thinking" {
		t.Fatalf("cache-warming-mode at %d is not between http-idle-timeout and hide-thinking", index)
	}
	row := items[index]
	if row.label != "Cache warming" || row.desc != "off; streaming while the agent runs; idle also between runs while continuation stays profitable" {
		t.Fatalf("row = %q / %q", row.label, row.desc)
	}
	if want := []string{"off", "streaming", "idle"}; !slices.Equal(row.values, want) {
		t.Fatalf("values = %v, want %v", row.values, want)
	}
	if got := row.get(Settings{}); got != "streaming" {
		t.Fatalf("default = %q, want streaming", got)
	}
	var s Settings
	row.apply(&s, "off")
	if s.CacheWarming != "off" || row.get(s) != "off" {
		t.Fatalf("apply off: CacheWarming = %q, get = %q", s.CacheWarming, row.get(s))
	}
}
